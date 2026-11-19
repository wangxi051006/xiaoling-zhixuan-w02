//go:build integration

package schedule

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func slotFixture(t *testing.T) (*pgxpool.Pool, context.Context, time.Time) {
	return slotFixtureWithIdentity(t, false)
}

func slotFixtureWithIdentity(t *testing.T, withIdentity bool) (*pgxpool.Pool, context.Context, time.Time) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("isolated TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var suffix [12]byte
	rand.Read(suffix[:])
	schema := pgx.Identifier{"p03_" + hex.EncodeToString(suffix[:])}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	files := fstest.MapFS{}
	names := []string{"000_001_base.sql", "030_001_slots.sql"}
	if withIdentity {
		names = append(names, "010_001_identity.sql", "060_001_account_identity_email.sql")
	}
	for _, name := range names {
		data, err := migrations.SQL.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	for range 2 {
		if err := platform.Migrate(ctx, pool, files); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Station');
	INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity) VALUES
	 ('early','s','2026-11-18T03:00:00Z','2026-11-18T03:15:00Z','2026-11-18T02:45:00Z',1),
	 ('late','s','2026-11-18T03:15:00Z','2026-11-18T03:30:00Z','2026-11-18T03:00:00Z',1);`); err != nil {
		t.Fatal(err)
	}
	return pool, ctx, mustTime(t, "2026-11-18T02:40:00Z")
}

func TestLastSlotSharedByASAPAndScheduled(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	svc := Store{}
	const asap = contract.DeliveryModeAsap
	const scheduled = contract.DeliveryModeScheduled
	reserve := func(slot, order string, mode contract.DeliveryMode, now time.Time) error {
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return svc.ReserveSlot(ctx, tx, slot, order, mode, now)
		})
	}
	release := func(order string, when time.Time) error {
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return svc.ReleaseSlot(ctx, tx, order, when)
		})
	}
	for _, request := range []struct {
		mode contract.DeliveryMode
		id   string
	}{{asap, ""}, {scheduled, "early"}} {
		if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			q, err := svc.SelectSlot(ctx, tx, "s", request.mode, request.id, now)
			if err != nil || q.ID != "early" || q.StationID != "s" || q.Version != 1 {
				t.Fatalf("quote %+v err=%v", q, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		order string
		mode  contract.DeliveryMode
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i, mode := range []contract.DeliveryMode{asap, scheduled} {
		order := []string{"asap-order", "scheduled-order"}[i]
		go func() {
			<-start
			results <- result{order, mode, reserve("early", order, mode, now)}
		}()
	}
	close(start)
	winner, loser := <-results, <-results
	if winner.err != nil {
		winner, loser = loser, winner
	}
	if winner.err != nil || !errors.Is(loser.err, ErrSlotFull) {
		t.Fatalf("want only one reservation, got %+v and %+v", winner, loser)
	}
	if err := reserve("early", winner.order, winner.mode, now); err != nil {
		t.Fatalf("exact reserve replay: %v", err)
	}
	if err := reserve("late", winner.order, winner.mode, now); !errors.Is(err, ErrSlotConflict) {
		t.Fatalf("moved a reserved order: %v", err)
	}
	assertSlotUsed(t, pool, ctx, "early", 1)
	assertSlotUsed(t, pool, ctx, "late", 0)
	if err := release(winner.order, now); err != nil {
		t.Fatal(err)
	}
	if err := reserve("early", loser.order, loser.mode, now); err != nil {
		t.Fatal(err)
	}
	if err := release(winner.order, now); err != nil {
		t.Fatal(err)
	}
	if err := reserve("early", winner.order, winner.mode, now); !errors.Is(err, ErrSlotConflict) {
		t.Fatalf("reserved a released order again: %v", err)
	}
	assertSlotUsed(t, pool, ctx, "early", 1)
	if err := release(loser.order, mustTime(t, "2026-11-18T03:15:00Z")); !errors.Is(err, ErrSlotExpired) {
		t.Fatalf("released an ended slot: %v", err)
	}
	assertSlotUsed(t, pool, ctx, "early", 1)
}

func TestSlotReserveRollbackExpiryAndClosedWindow(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	svc := Store{}
	abort := errors.New("rollback fixture")
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if err := svc.ReserveSlot(ctx, tx, "early", "rolled-back", contract.DeliveryModeScheduled, now); err != nil {
			return err
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatalf("failed to roll back reservation: %v", err)
	}
	assertSlotUsed(t, pool, ctx, "early", 0)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM slot_reservations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("reservation did not roll back: %d, %v", count, err)
	}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return svc.ReserveSlot(ctx, tx, "early", "expired", contract.DeliveryModeScheduled, mustTime(t, "2026-11-18T02:45:00Z"))
	}); !errors.Is(err, ErrSlotExpired) {
		t.Fatalf("expected original slot to expire even with another available: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_slots SET accepting=false`); err != nil {
		t.Fatal(err)
	}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		_, err := svc.SelectSlot(ctx, tx, "s", contract.DeliveryModeAsap, "", now)
		return err
	}); !errors.Is(err, ErrSlotExpired) {
		t.Fatalf("ASAP selected a closed window: %v", err)
	}
}

func assertSlotUsed(t *testing.T, pool *pgxpool.Pool, ctx context.Context, slot string, want int64) {
	t.Helper()
	var used int64
	if err := pool.QueryRow(ctx, `SELECT used FROM delivery_slots WHERE id=$1`, slot).Scan(&used); err != nil || used != want {
		t.Fatalf("slot %s used=%d, want=%d: %v", slot, used, want, err)
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestSlotHTTPContract(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	mux := http.NewServeMux()
	h := HTTP{Pool: pool, Clock: fixedClock{now}, Authorize: func(w http.ResponseWriter, r *http.Request, entry identity.Entry, mfa bool, roles ...contract.Role) (identity.Session, bool) {
		want := contract.RoleOperator
		if r.URL.Path == "/api/v1/delivery/slots" {
			want = contract.RoleCustomer
			if entry != identity.UserEntry {
				t.Fatalf("customer entry = %q", entry)
			}
		} else if entry != identity.AdminEntry {
			t.Fatalf("operator entry = %q", entry)
		}
		if mfa || len(roles) != 1 || roles[0] != want {
			t.Fatalf("unexpected auth requirements: %v %v", roles, mfa)
		}
		return identity.Session{AccountID: "actor", StationID: "s"}, true
	}}
	h.Register(mux)
	call := func(method, path, body, key string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s = %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	page := call("GET", "/delivery/slots?date=2026-11-18&limit=1", "", "", 200)
	var decoded struct {
		Items []map[string]any `json:"items"`
		Next  string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &decoded); err != nil || decoded.Next == "" || len(decoded.Items) != 1 || decoded.Items[0]["id"] != "early" || len(decoded.Items[0]) != 5 {
		t.Fatalf("customer page leaked fields or was malformed: %s", page.Body)
	}
	second := call("GET", "/delivery/slots?date=2026-11-18&limit=1&cursor="+decoded.Next, "", "", 200)
	if !strings.Contains(second.Body.String(), `"id":"late"`) || !strings.Contains(second.Body.String(), `"next_cursor":null`) {
		t.Fatalf("invalid second page: %s", second.Body)
	}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return (Store{}).ReserveSlot(ctx, tx, "early", "order", contract.DeliveryModeAsap, now)
	}); err != nil {
		t.Fatal(err)
	}
	call("PUT", "/admin/slots/early", `{"expected_version":1,"capacity":0,"accepting":false}`, "", 409)
	call("PUT", "/admin/slots/early", `{"expected_version":1,"capacity":2,"accepting":false,"starts_at":"2026-11-18T03:00:00Z"}`, "", 400)
	closed := call("PUT", "/admin/slots/early", `{"expected_version":1,"capacity":1,"accepting":false}`, "", 200)
	if !strings.Contains(closed.Body.String(), `"version":2`) || !strings.Contains(closed.Body.String(), `"used":1`) {
		t.Fatalf("closure changed reservation: %s", closed.Body)
	}
	call("PUT", "/admin/slots/early", `{"expected_version":1,"capacity":2,"accepting":true}`, "", 409)
	call("GET", "/admin/slots/early", "", "", 200)
	call("GET", "/admin/slots?date=2026-11-18", "", "", 200)
	call("GET", "/delivery/slots?date=2026-02-30", "", "", 400)
	body := `{"local_date":"2026-11-19","opens_at":"23:30","closes_at":"00:30","lead_minutes":15,"capacity":6}`
	firstBatch := call("POST", "/admin/slots/batches", body, "batch", 201)
	replay := call("POST", "/admin/slots/batches", body, "batch", 201)
	if !bytes.Equal(firstBatch.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("batch replay changed original result: %s / %s", firstBatch.Body, replay.Body)
	}
	call("POST", "/admin/slots/batches", strings.Replace(body, `"capacity":6`, `"capacity":5`, 1), "batch", 409)
	call("POST", "/admin/slots/batches", body, "new-key", 201)
	overlap := strings.Replace(body, `"opens_at":"23:30"`, `"opens_at":"23:35"`, 1)
	overlap = strings.Replace(overlap, `"closes_at":"00:30"`, `"closes_at":"00:35"`, 1)
	call("POST", "/admin/slots/batches", overlap, "overlap", 409)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_slots`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("batch did not contain exactly four new windows: %d %v", count, err)
	}
}
