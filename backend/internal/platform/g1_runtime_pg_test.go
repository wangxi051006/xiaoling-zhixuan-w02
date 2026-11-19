//go:build integration

package platform

import (
	"campus.local/xiaoling/migrations"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"
)

// This is a local least-privilege capability proof, not a production grant
// template or an assertion that deployed IAM has been reviewed.
func TestG1RuntimeDomainCapabilitiesAndDDLIsolation(t *testing.T) {
	p, ctx := isolatedPool(t)
	// Historical W1 fixture; P05 tests full schema with real orders.
	w1 := fstest.MapFS{}
	names, readErr := fs.Glob(migrations.SQL, "*.sql")
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, name := range names {
		if name < "050_" {
			b, _ := migrations.SQL.ReadFile(name)
			w1[name] = &fstest.MapFile{Data: b}
		}
	}
	if err := Migrate(ctx, p, w1); err != nil {
		t.Fatal(err)
	}
	var schema string
	p.QueryRow(ctx, "SELECT current_schema()").Scan(&schema)
	name := schema + "_g1_app"
	role := pgx.Identifier{name}.Sanitize()
	if _, err := p.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := p.Exec(c, "DROP OWNED BY "+role+";DROP ROLE "+role); err != nil {
			t.Error(err)
		}
	})
	_, err := p.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');INSERT INTO accounts(id,station_id,phone)VALUES('a','s','+15550000001');INSERT INTO products(id,station_id,name,on_sale)VALUES('p','s','Synthetic',true);INSERT INTO inventory(id,product_id,on_hand)VALUES('i','p',3);INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity)VALUES('slot','s','2026-11-19T04:00Z','2026-11-19T04:15Z','2026-11-19T03:45Z',3);INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)VALUES('s','a',1000,1000)`)
	if err != nil {
		t.Fatal(err)
	}
	grants := []string{"REVOKE CREATE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " FROM PUBLIC", "GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role, "GRANT SELECT ON stations,accounts,products,inventory,delivery_slots,wallets,settlements TO " + role, "GRANT INSERT ON settlements,stock_reservations,slot_reservations,wallet_entries,audit_events,outbox_jobs TO " + role, "GRANT UPDATE(reserved,version) ON inventory TO " + role, "GRANT UPDATE(used) ON delivery_slots TO " + role, "GRANT UPDATE(balance_fen) ON wallets TO " + role, "GRANT USAGE ON SEQUENCE wallet_entries_id_seq,audit_events_id_seq,outbox_jobs_id_seq TO " + role}
	for _, s := range grants {
		if _, err = p.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		sql     string
		allowed bool
	}{
		{`SELECT a.id,p.id FROM accounts a JOIN products p ON a.station_id=p.station_id`, true},
		{`UPDATE inventory SET reserved=reserved+1,version=version+1 WHERE id='i'`, true},
		{`UPDATE delivery_slots SET used=used+1 WHERE id='slot'`, true},
		{`UPDATE wallets SET balance_fen=900 WHERE actor_id='a';INSERT INTO settlements(order_id,actor_id,station_id,method,amount_fen,state)VALUES('o','a','s','BALANCE',100,'BALANCE_PAID');INSERT INTO wallet_entries(station_id,actor_id,order_id,kind,delta_fen)VALUES('s','a','o','DEBIT',-100)`, true},
		{`INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id)VALUES('s','a','g1','o','request')`, true},
		{`CREATE TABLE forbidden(id int)`, false}, {`UPDATE schema_migrations SET name='forged'`, false}, {`UPDATE audit_events SET action='forged'`, false}, {`DELETE FROM wallet_entries`, false}, {`TRUNCATE wallet_entries`, false},
	} {
		tx, e := p.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, "SET LOCAL ROLE "+role); e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(ctx, tc.sql)
		tx.Rollback(ctx)
		if (e == nil) != tc.allowed {
			t.Fatalf("runtime capability allowed=%v got %v", tc.allowed, e)
		}
		if !tc.allowed {
			var pg *pgconn.PgError
			if !errors.As(e, &pg) || pg.Code != "42501" {
				t.Fatalf("expected privilege SQLSTATE 42501: %v", e)
			}
		}
	}
}
