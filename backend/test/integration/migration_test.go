//go:build integration

package integration

import (
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestP05FullEmptyAndLegalPrefixUpgradeReplay(t *testing.T) {
	f := setup(t)
	// Replay over actual fulfillment, financial and identity rows, not an empty
	// schema: include reservations, cash, income, manual refund and payout.
	id := f.order("buyer", "ASAP", "COD")
	task := f.pickup(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
	refundID := f.call("op", "POST", "/admin/orders/"+id+"/refund", `{"reason_code":"AFTERSALE"}`, 201)["id"].(string)
	f.call("fin", "POST", "/admin/refunds/"+refundID+"/approve", `{"reason_code":"APPROVED"}`, 200)
	f.call("fin2", "POST", "/admin/refunds/"+refundID+"/record-cod-payout", `{"receipt_reference":"migration-synthetic"}`, 200)
	f.order("other", "SCHEDULED", "BALANCE")
	beforeReplay := tableFingerprints(t, f.ctx, f.pool, false)
	if err := platform.Migrate(f.ctx, f.pool, migrations.SQL); err != nil {
		t.Fatal(err)
	}
	if after := tableFingerprints(t, f.ctx, f.pool, false); !reflect.DeepEqual(beforeReplay, after) {
		t.Fatal("full 050 lifecycle replay mutated existing full rows")
	}
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	schema := "p05_upgrade_" + hex.EncodeToString(raw[:])
	ident := pgx.Identifier{schema}.Sanitize()
	f.exec("CREATE SCHEMA " + ident)
	cfg := f.pool.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close(); f.exec("DROP SCHEMA " + ident + " CASCADE") })
	prefix := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.SQL, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		// The legal prefix predates the fulfillment segment, so every migration
		// that follows it in the chain is left out as well; the full replay below
		// still applies and verifies all of them.
		if strings.HasSuffix(e.Name(), ".sql") && !strings.HasPrefix(e.Name(), "050_") && !strings.HasPrefix(e.Name(), "060_") {
			data, err := migrations.SQL.ReadFile(e.Name())
			if err != nil {
				t.Fatal(err)
			}
			prefix[e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if err = platform.Migrate(f.ctx, p, prefix); err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(f.ctx, `INSERT INTO stations(id,name)VALUES('upgrade','Synthetic legal-prefix upgrade');INSERT INTO accounts(id,station_id,phone)VALUES('customer','upgrade','+15550000111');INSERT INTO role_bindings VALUES('customer','CUSTOMER');INSERT INTO products(id,station_id,name)VALUES('product','upgrade','Retained product');INSERT INTO inventory(id,product_id,on_hand,reserved)VALUES('inventory','product',7,0);INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen)VALUES('sku','product','single',1,0);INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)VALUES('upgrade','customer',0,0);INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity)VALUES('slot','upgrade','2026-11-19T04:00Z','2026-11-19T04:15Z','2026-11-19T03:45Z',1)`)
	if err != nil {
		t.Fatal(err)
	}
	// account_type/email/email_verified_at arrive with a later migration, so the
	// upgrade comparison keeps to the columns the prefix schema actually had.
	addedLater := map[string][]string{"accounts": {"account_type", "email", "email_verified_at"}}
	beforeUpgrade := tableFingerprintsWithout(t, f.ctx, p, true, addedLater)
	for i := 0; i < 2; i++ {
		if err = platform.Migrate(f.ctx, p, migrations.SQL); err != nil {
			t.Fatal(err)
		}
		afterUpgrade := tableFingerprintsWithout(t, f.ctx, p, true, addedLater)
		for table, digest := range beforeUpgrade {
			if afterUpgrade[table] != digest {
				t.Fatalf("legal prefix upgrade/replay mutated all-row fingerprint of %s", table)
			}
		}
	}
	// The full replay must have applied every embedded migration, however many
	// files the build carries.
	buildMigrationCount := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			buildMigrationCount++
		}
	}
	var count, hand int
	if err = p.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM schema_migrations),(SELECT on_hand FROM inventory WHERE id='inventory')`).Scan(&count, &hand); err != nil || count != buildMigrationCount || hand != 7 {
		t.Fatal(count, hand, err)
	}
}

// Hash the canonical JSON of every full row, including bytea digests and time
// fields. The table list is read before querying, so no open row cursor shares
// a connection with subsequent fingerprint queries.
// tableFingerprints digests every row of every table. strip removes columns that a
// later migration adds, so a legal-prefix upgrade is still compared on exactly
// the columns that existed before the upgrade.
func tableFingerprints(t *testing.T, ctx context.Context, p *pgxpool.Pool, excludeMigrations bool) map[string]string {
	return tableFingerprintsWithout(t, ctx, p, excludeMigrations, nil)
}

func tableFingerprintsWithout(t *testing.T, ctx context.Context, p *pgxpool.Pool, excludeMigrations bool, strip map[string][]string) map[string]string {
	t.Helper()
	rows, err := p.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, table := range tables {
		filter := ""
		if excludeMigrations && table == "schema_migrations" {
			filter = " WHERE name NOT LIKE '050_%' AND name NOT LIKE '060_%'"
		}
		projection := "to_jsonb(t)"
		for _, column := range strip[table] {
			projection += " - '" + strings.ReplaceAll(column, "'", "''") + "'"
		}
		projection = "(" + projection + ")"
		var raw string
		err = p.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT `+projection+` AS v FROM `+pgx.Identifier{table}.Sanitize()+` t`+filter+`) rows`).Scan(&raw)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(raw))
		result[table] = hex.EncodeToString(sum[:])
	}
	return result
}
func TestP05CrossDomainForeignKeyNegativeCases(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "COD")
	task := f.dispatch(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	quote := f.quote("buyer", "ASAP", "BALANCE")
	cases := []struct {
		name, sql string
		args      []any
	}{
		{"stock", `INSERT INTO stock_reservations(order_id,inventory_id,units,request_digest) VALUES('missing',$1,1,decode(repeat('00',32),'hex'))`, []any{f.inventory}},
		{"slot", `INSERT INTO slot_reservations(order_id,slot_id,mode,reserved_at) VALUES('missing','asap','ASAP',now())`, nil},
		{"settlement", `INSERT INTO settlements(order_id,actor_id,station_id,method,amount_fen,state) VALUES('missing','buyer','s','COD',100,'COD_UNCOLLECTED')`, nil},
		{"task", `INSERT INTO delivery_tasks(order_id,created_at) VALUES('missing',now())`, nil},
		{"earning", `INSERT INTO rider_earnings(task_id,rider_id,amount_fen,recorded_at) VALUES('missing','r1',0,now())`, nil},
		{"cash-assignment", `INSERT INTO cod_receipts(order_id,rider_id,amount_fen) VALUES($1,'r2',100)`, []any{id}},
		{"quote-owner", `INSERT INTO orders(quote_id,actor_id,station_id,mode,method,slot_id,address_snapshot,slot_snapshot,total_fen,fee_fen,item_count,status,created_at) SELECT id,'other',station_id,mode,method,slot_id,address_snapshot,slot_snapshot,100,0,1,'PREPARING',created_at FROM quotes WHERE id=$1`, []any{quote}},
		{"notification", `INSERT INTO notifications(station_id,recipient_id,kind,task_id,created_at) VALUES('s','missing','ORDER_URGE',$1,now())`, []any{task}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.pool.Exec(f.ctx, tc.sql, tc.args...)
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != "23503" {
				t.Fatalf("expected FK rejection, got %v", err)
			}
		})
	}
	for _, table := range []string{"order_events", "rider_earnings"} {
		_, err := f.pool.Exec(f.ctx, "DELETE FROM "+table)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "42501" {
			t.Fatalf("mutable fulfillment ledger %s: %v", table, err)
		}
	}
}

var _ = context.Background
