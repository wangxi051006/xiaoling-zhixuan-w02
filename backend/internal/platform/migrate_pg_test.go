//go:build integration

package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func isolatedPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	if dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("integration database must not be an application or deployment migration database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	t.Cleanup(admin.Close)
	var suffix [8]byte
	_, _ = rand.Read(suffix[:])
	name := "p00_test_" + hex.EncodeToString(suffix[:])
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal("create isolated test schema:", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error("drop owned test schema:", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = ident
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func TestMigrationReplayAndAuditAppendOnly(t *testing.T) {
	pool, ctx := isolatedPool(t)
	base, err := migrations.SQL.ReadFile("000_001_base.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := fstest.MapFS{"000_001_base.sql": {Data: base}}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Go(func() { results <- Migrate(ctx, pool, source) })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"stations", "idempotency_keys", "audit_events", "outbox_jobs"} {
		var found bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&found); err != nil || !found {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration count %d: %v", count, err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO stations (id, name) VALUES ('fixture','Test station'); INSERT INTO audit_events (station_id,actor_id,action,object_id,request_id) VALUES ('fixture','actor','fixture','object','request')"); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"UPDATE audit_events SET action = 'modified'", "DELETE FROM audit_events", "TRUNCATE audit_events"} {
		if _, err := pool.Exec(ctx, command); err == nil {
			t.Fatalf("audit mutation succeeded: %s", command)
		}
	}
}

func TestMigrationAtomicAndDigestChecked(t *testing.T) {
	pool, ctx := isolatedPool(t)
	name := "000_001_fixture.sql"
	files := fstest.MapFS{name: &fstest.MapFile{Data: []byte("CREATE TABLE half_done (id int); SELECT absent FROM absent_table;")}}
	if err := Migrate(ctx, pool, files); err == nil {
		t.Fatal("invalid migration passed")
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('half_done') IS NOT NULL OR to_regclass('schema_migrations') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("migration left partial DDL: %v, %v", exists, err)
	}
	files[name].Data = []byte("CREATE TABLE full_done (id int);")
	if err := Migrate(ctx, pool, files); err != nil {
		t.Fatal(err)
	}
	files[name].Data = []byte(fmt.Sprint(string(files[name].Data), " -- altered"))
	if err := Migrate(ctx, pool, files); err == nil {
		t.Fatal("changed applied migration was accepted")
	}
}
