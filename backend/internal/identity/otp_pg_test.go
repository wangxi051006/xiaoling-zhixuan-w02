//go:build integration

package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func identityMigrations(t *testing.T) fs.FS {
	t.Helper()
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := fstest.MapFS{}
	for _, name := range names {
		if !strings.HasPrefix(name, "000_") && !strings.HasPrefix(name, "010_") && !strings.HasPrefix(name, "060_") {
			continue
		}
		raw, err := fs.ReadFile(migrations.SQL, name)
		if err != nil {
			t.Fatal(err)
		}
		source[name] = &fstest.MapFile{Data: raw}
	}
	return source
}

func identityPool(t *testing.T) *pgxpool.Pool {
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
	schema := pgx.Identifier{"p01_test_" + hex.EncodeToString(nonce[:])}.Sanitize()
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
	if err := platform.Migrate(t.Context(), pool, identityMigrations(t)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPostgresOTPThrottlesAndConsumesExactlyOnce(t *testing.T) {
	pool := identityPool(t)
	sender := &fixtureOTP{}
	clock := &fakeClock{time.Unix(1780000000, 0)}
	service, err := NewOTP(NewPostgresOTPStore(pool), sender, clock, []byte("one-of-your-local-test-fixture-keys"))
	if err != nil {
		t.Fatal(err)
	}
	const phone = "+8613800000000"
	challenge, err := service.Request(t.Context(), phone, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(t.Context(), phone, "127.0.0.2"); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("phone cooldown: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			got, err := service.Verify(t.Context(), challenge.ID, sender.lastCode)
			if err == nil && got != phone {
				err = errors.New("wrong verified phone")
			}
			results <- err
		})
	}
	close(start)
	group.Wait()
	close(results)
	passed := 0
	for err := range results {
		if err == nil {
			passed++
			continue
		}
		if !errors.Is(err, ErrInvalidOTP) {
			t.Fatal(err)
		}
	}
	if passed != 1 {
		t.Fatalf("expected one successful verification, got %d", passed)
	}
}
