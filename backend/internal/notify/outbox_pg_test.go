//go:build integration

package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type recordingSender struct {
	messages []Message
	fail     bool
}

func (s *recordingSender) Send(_ context.Context, m Message) error {
	if s.fail {
		return errors.New("smtp connection refused")
	}
	s.messages = append(s.messages, m)
	return nil
}

func notifyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("an isolated TEST_DATABASE_URL is required")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	t.Cleanup(admin.Close)
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	schema := pgx.Identifier{"a01_notify_" + hex.EncodeToString(nonce[:])}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	files := fstest.MapFS{}
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "000_") && !strings.HasPrefix(name, "010_") && !strings.HasPrefix(name, "060_") {
			continue
		}
		raw, err := fs.ReadFile(migrations.SQL, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: raw}
	}
	if err := platform.Migrate(t.Context(), pool, files); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestOutboxDedupesRetriesAndRecordsFinalFailure(t *testing.T) {
	pool := notifyPool(t)
	ctx := t.Context()
	clock := &testClock{time.Unix(1780000000, 0)}
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Synthetic')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts(id,station_id,phone,email,email_verified_at) VALUES ('c','s','+15550000001','student@example.test',$1)`, clock.now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_bindings(account_id,role) VALUES ('c','CUSTOMER')`); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{fail: true}
	outbox := &Outbox{Pool: pool, Clock: clock, Sender: sender, Enabled: true}

	// The enqueue runs in the caller's transaction and dedupes per event+recipient.
	for i := 0; i < 2; i++ {
		if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return outbox.NotifyOrder(ctx, tx, "ORDER_PLACED", "s", "order-1", "c")
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return outbox.NotifyOrder(ctx, tx, "ORDER_CANCELED", "s", "order-1", "c")
	}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("expected one row per event kind, saw %d", rows)
	}

	// A disabled outbox never writes and never sends.
	inert := &Outbox{Pool: pool, Clock: clock, Sender: sender}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return inert.NotifyOrder(ctx, tx, "DELIVERY_INCIDENT", "s", "order-1", "c")
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("a disabled outbox wrote a row: %d", rows)
	}

	// Failed deliveries back off and stop after the attempt budget.
	for attempt := 1; attempt <= maxEmailAttempts; attempt++ {
		if _, err := outbox.RunDue(ctx, 10); err == nil {
			t.Fatalf("attempt %d did not report the delivery failure", attempt)
		}
		var state string
		var attempts int
		if err := pool.QueryRow(ctx, `SELECT CASE WHEN failed_at IS NOT NULL THEN 'FAILED' WHEN sent_at IS NOT NULL THEN 'SENT' ELSE 'PENDING' END, attempts
			FROM email_outbox WHERE kind = 'ORDER_PLACED'`).Scan(&state, &attempts); err != nil {
			t.Fatal(err)
		}
		if attempts != attempt {
			t.Fatalf("attempt %d recorded %d", attempt, attempts)
		}
		if attempt < maxEmailAttempts && state != "PENDING" {
			t.Fatalf("attempt %d left state %s", attempt, state)
		}
		if attempt == maxEmailAttempts && state != "FAILED" {
			t.Fatalf("final attempt left state %s", state)
		}
		clock.now = clock.now.Add(10 * time.Minute)
	}
	var lastError string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM email_outbox WHERE kind = 'ORDER_PLACED'`).Scan(&lastError); err != nil || lastError == "" {
		t.Fatalf("final failure was not recorded: %q %v", lastError, err)
	}

	// Both events were claimed in the same windows and each exhausted its own
	// budget, so nothing is silently dropped.
	var failed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE failed_at IS NOT NULL`).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 2 {
		t.Fatalf("expected both events to end failed, saw %d", failed)
	}

	// A later event with a working transport is delivered exactly once.
	sender.fail = false
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return outbox.NotifyOrder(ctx, tx, "DELIVERY_INCIDENT", "s", "order-1", "c")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := outbox.RunDue(ctx, 10); err != nil {
		t.Fatalf("delivery failed: %v", err)
	}
	var sent int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE kind = 'DELIVERY_INCIDENT' AND sent_at IS NOT NULL`).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent != 1 || len(sender.messages) != 1 {
		t.Fatalf("queued incident was not sent once: rows=%d messages=%d", sent, len(sender.messages))
	}
	if strings.Contains(sender.messages[0].Text, "宿舍") || strings.Contains(sender.messages[0].Text, "+1555") {
		t.Fatal("order mail leaked address or phone data")
	}
	if sender.messages[0].To != "student@example.test" {
		t.Fatalf("mail sent to the wrong mailbox: %s", sender.messages[0].To)
	}
}
