//go:build integration

package platform

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
)

// Catches a missing embedded migration, an invalid combined schema, or a replay
// that mutates any full business row, including migration timestamps and digests.
func TestG1CombinedMigrationPrefixUpgradeAndReplay(t *testing.T) {
	p, ctx := isolatedPool(t)
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	disk, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(disk) != len(names) {
		t.Fatal("embedded manifest differs from disk")
	}
	for _, name := range names {
		a, _ := migrations.SQL.ReadFile(name)
		b, e := os.ReadFile("../../migrations/" + name)
		if e != nil || !bytes.Equal(a, b) {
			t.Fatal("embedded migration differs:", name)
		}
	}
	base, _ := migrations.SQL.ReadFile(names[0])
	// G1 proves the W1 boundary. P05 separately tests all real order FKs.
	w1 := fstest.MapFS{}
	var w1Names []string
	for _, name := range names {
		if name < "050_" {
			b, _ := migrations.SQL.ReadFile(name)
			w1[name] = &fstest.MapFile{Data: b}
			w1Names = append(w1Names, name)
		}
	}
	names = w1Names
	prefix := fstest.MapFS{names[0]: {Data: base}}
	if err = Migrate(ctx, p, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id)VALUES('s','a','prefix','s','g1');INSERT INTO outbox_jobs(station_id,kind,dedupe_key,payload,available_at)VALUES('s','fixture','base','{}','2026-11-19T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	oldRows := map[string]string{}
	for _, table := range []string{"stations", "audit_events", "outbox_jobs", "schema_migrations"} {
		query := "SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM " + pgx.Identifier{table}.Sanitize() + " t"
		var value string
		if err = p.QueryRow(ctx, query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		oldRows[table] = value
	}
	if err = Migrate(ctx, p, w1); err != nil {
		t.Fatal(err)
	}
	for table, want := range oldRows {
		query := "SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM " + pgx.Identifier{table}.Sanitize() + " t"
		if table == "schema_migrations" {
			query += " WHERE version='000_001'"
		}
		var got string
		if err = p.QueryRow(ctx, query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatal("prefix full rows changed:", table)
		}
	}
	_, err = p.Exec(ctx, `INSERT INTO accounts(id,station_id,phone)VALUES('a','s','+15550000001');
 INSERT INTO products(id,station_id,name,on_sale)VALUES('p','s','Synthetic',true);
 INSERT INTO inventory(id,product_id,on_hand,reserved)VALUES('i','p',3,1);
 INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen)VALUES('sku','p','single',1,100);
 INSERT INTO stock_reservations(order_id,inventory_id,units)VALUES('o','i',1);
 INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity,used)VALUES('slot','s','2026-11-19T04:00Z','2026-11-19T04:15Z','2026-11-19T03:45Z',3,1);
 INSERT INTO slot_reservations(order_id,slot_id,mode,reserved_at)VALUES('o','slot','SCHEDULED','2026-11-19T00:00Z');
 INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)VALUES('s','a',1000,900);
 INSERT INTO settlements(order_id,actor_id,station_id,method,amount_fen,state)VALUES('o','a','s','BALANCE',100,'BALANCE_PAID'),('cod','a','s','COD',200,'COD_COLLECTED');
 INSERT INTO wallet_entries(station_id,actor_id,order_id,kind,delta_fen)VALUES('s','a','o','DEBIT',-100);
 INSERT INTO cod_receipts(order_id,rider_id,amount_fen)VALUES('cod','r',200);
 INSERT INTO cod_batches(id,station_id,rider_id,local_date,creator_id,expected_fen)VALUES('batch','s','r','2026-11-19','finance-a',200);
 INSERT INTO cod_batch_receipts(receipt_id,batch_id)VALUES('cod','batch');
 INSERT INTO cod_batch_verifications(batch_id,creator_id,verifier_id,reason_code,expected_fen,actual_fen)VALUES('batch','finance-a','finance-b','COUNT',200,180);
 INSERT INTO refunds(id,order_id,source,operator_id,request_reason)VALUES('refund','cod','MANUAL','operator','TEST');`)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := func() []byte {
		t.Helper()
		rows, e := p.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema() ORDER BY tablename`)
		if e != nil {
			t.Fatal(e)
		}
		tables, e := pgx.CollectRows(rows, pgx.RowTo[string])
		if e != nil {
			t.Fatal(e)
		}
		data := map[string]string{}
		for _, table := range tables {
			var value string
			e = p.QueryRow(ctx, "SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM "+pgx.Identifier{table}.Sanitize()+" t").Scan(&value)
			if e != nil {
				t.Fatal(e)
			}
			data[table] = value
		}
		b, _ := json.Marshal(data)
		return b
	}
	before := fingerprint()
	for range 2 {
		if err = Migrate(ctx, p, w1); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, fingerprint()) {
			t.Fatal("replay changed full rows")
		}
	}
	rows, err := p.Query(ctx, `SELECT name,digest FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var name string
		var digest []byte
		if err = rows.Scan(&name, &digest); err != nil {
			t.Fatal(err)
		}
		if name != names[n] {
			t.Fatal("migration order")
		}
		b, _ := migrations.SQL.ReadFile(name)
		sum := sha256.Sum256(b)
		if !bytes.Equal(digest, sum[:]) {
			t.Fatal("migration digest")
		}
		n++
	}
	rows.Close()
	if n != len(names) {
		t.Fatal("missing migration")
	}
	t.Logf("%d embedded migrations; legal populated 000 prefix upgrade; full-row replay unchanged", n)
}
