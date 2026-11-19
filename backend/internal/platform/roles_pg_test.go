//go:build integration

package platform

import (
	"os"
	"strings"
	"testing"

	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
)

func TestRuntimeRoleCannotChangeAuditOrSchema(t *testing.T) {
	pool, ctx := isolatedPool(t)
	if err := Migrate(ctx, pool, migrations.SQL); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	name := schema + "_runtime"
	role := pgx.Identifier{name}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT"); err != nil {
		t.Fatal("isolated DB fixture user must permit CREATE ROLE:", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role); err != nil {
			t.Error(err)
		}
	})
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('isolated','Fixture')`); err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile("../../../docs/engineering/p00-runtime-grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	grants := strings.NewReplacer("@schema@", pgx.Identifier{schema}.Sanitize(), "@runtime@", role).Replace(string(template))
	if _, err := pool.Exec(ctx, grants); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sql     string
		allowed bool
	}{
		{`SELECT id FROM stations`, true},
		{`INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id) VALUES('isolated','actor','fixture','object','req')`, true},
		{`UPDATE audit_events SET action = 'changed'`, false},
		{`DELETE FROM audit_events`, false},
		{`TRUNCATE audit_events`, false},
		{`DELETE FROM schema_migrations`, false},
		{`CREATE TABLE forbidden(id int)`, false},
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, tc.sql)
		_ = tx.Rollback(ctx)
		if (err == nil) != tc.allowed {
			t.Fatalf("unexpected permission for %s: %v", tc.sql, err)
		}
	}
}
