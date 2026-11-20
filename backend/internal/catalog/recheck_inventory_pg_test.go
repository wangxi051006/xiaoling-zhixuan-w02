//go:build integration

package catalog

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCatalogPromotionClockBoundariesAndVersion(t *testing.T) {
	pool, ctx := stockFixture(t)
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	clock := &catalogClock{at}
	s := Store{Clock: clock}
	run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	if err := run(func(tx pgx.Tx) error {
		p, err := s.UpdatePromotion(ctx, tx, "s", "p", 1, &Promotion{StartsAt: at, EndsAt: at.Add(time.Hour), Tiers: []Tier{{2, 1500}, {1, 500}}})
		if err == nil && (p.Version != 2 || p.Promotion == nil) {
			t.Fatal("promotion did not increment version")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		unit int64
	}{{"before", at.Add(-time.Nanosecond), 110}, {"start", at, 105}, {"inside", at.Add(time.Hour - time.Nanosecond), 105}, {"end", at.Add(time.Hour), 110}} {
		t.Run(tc.name, func(t *testing.T) {
			clock.at = tc.at
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			q, err := s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 1}}, tc.at)
			if err != nil || q.TotalFen != tc.unit {
				t.Fatalf("clock price: %d %v", q.TotalFen, err)
			}
			if err = s.Reserve(ctx, tx, "clock-"+tc.name, q); err != nil {
				t.Fatal(err)
			}
		})
	}
	clock.at = at.Add(time.Hour)
	var old contract.PricedLines
	if err := run(func(tx pgx.Tx) (err error) {
		old, err = s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 1}}, at.Add(-time.Nanosecond))
		return
	}); err != nil {
		t.Fatal(err)
	}
	clock.at = at
	if err := run(func(tx pgx.Tx) error { return s.Reserve(ctx, tx, "started", old) }); !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("natural promotion start accepted old quote: %v", err)
	}
	if err := run(func(tx pgx.Tx) error { _, err := s.UpdatePromotion(ctx, tx, "s", "p", 2, nil); return err }); err != nil {
		t.Fatal(err)
	}
	if err := run(func(tx pgx.Tx) error { return s.Reserve(ctx, tx, "modified", old) }); !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("promotion version accepted old quote: %v", err)
	}
	assertStock(t, pool, ctx, 3, 0, 0)
}

func TestCatalogPriceRejectsDuplicateInactiveAndAggregateOverflow(t *testing.T) {
	pool, ctx := stockFixture(t)
	s := Store{}
	for _, lines := range [][]contract.OrderLineInput{nil, {{SKUID: "single", Qty: 0}}, {{SKUID: "single", Qty: -1}}, {{SKUID: "single", Qty: 1000}}, {{SKUID: "single", Qty: 1}, {SKUID: "single", Qty: 1}}} {
		err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { _, err := s.Price(ctx, tx, lines, time.Now()); return err })
		if !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid pricing request not rejected")
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE skus SET active=false WHERE id='single'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 1}}, time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("inactive SKU priced")
	}
	if _, err = tx.Exec(ctx, `UPDATE skus SET active=true,unit_price_fen=9007199254740991`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 1}, {SKUID: "triple", Qty: 1}}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatal("aggregate amount overflow accepted")
	}
}

func TestCatalogMultiItemMidOperationFailureRollsBackEverything(t *testing.T) {
	for _, op := range []string{"Reserve", "Consume", "Release", "Restock", "RestockCanceled"} {
		t.Run(op, func(t *testing.T) {
			pool, ctx := stockFixture(t)
			s := Store{}
			run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
			if _, err := pool.Exec(ctx, `INSERT INTO products(id,station_id,name,on_sale) VALUES('p2','s','Second',true);INSERT INTO inventory(id,product_id,on_hand) VALUES('j','p2',3);INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen) VALUES('other','p2','One',1,100)`); err != nil {
				t.Fatal(err)
			}
			reserve := func(tx pgx.Tx) error {
				q, err := s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 1}, {SKUID: "other", Qty: 1}}, time.Now())
				if err != nil {
					return err
				}
				return s.Reserve(ctx, tx, "fault", q)
			}
			if op != "Reserve" {
				if err := run(reserve); err != nil {
					t.Fatal(err)
				}
			}
			if op == "Restock" {
				if err := run(func(tx pgx.Tx) error { return s.Consume(ctx, tx, "fault") }); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := func() string {
				t.Helper()
				var state string
				err := pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM inventory i),(SELECT jsonb_agg(to_jsonb(r) ORDER BY inventory_id) FROM stock_reservations r),(SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM inventory_movements m))::text`).Scan(&state)
				if err != nil {
					t.Fatal(err)
				}
				return state
			}
			before := snapshot()
			if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_second_stock() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture_second_movement'; END $$;CREATE TRIGGER fail_second_stock BEFORE INSERT ON inventory_movements FOR EACH ROW WHEN (NEW.inventory_id='j') EXECUTE FUNCTION fail_second_stock()`); err != nil {
				t.Fatal(err)
			}
			err := run(func(tx pgx.Tx) error {
				switch op {
				case "Reserve":
					return reserve(tx)
				case "Consume":
					return s.Consume(ctx, tx, "fault")
				case "Release":
					return s.Release(ctx, tx, "fault")
				case "Restock":
					return s.Restock(ctx, tx, "fault", []contract.RestockLine{{InventoryID: "i", SellableUnits: 1}, {InventoryID: "j", SellableUnits: 1}})
				default:
					return s.RestockCanceled(ctx, tx, "fault", []contract.CanceledStockLine{{InventoryID: "i", DamagedUnits: 1}})
				}
			})
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "P0001" {
				t.Fatalf("did not hit actual second-item fault: %v", err)
			}
			if after := snapshot(); after != before {
				t.Fatal("partial inventory/reservation/movement changes survived rollback")
			}
		})
	}
}

func TestCatalogDatabaseConstraintsAndAppendOnlyNegativeCases(t *testing.T) {
	pool, ctx := stockFixture(t)
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		q, err := stockQuote(ctx, tx, "single")
		if err != nil {
			return err
		}
		return (Store{}).Reserve(ctx, tx, "constraints", q)
	}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE inventory SET reserved=-1`, `UPDATE inventory SET reserved=on_hand+1`, `UPDATE inventory SET on_hand=-1`, `UPDATE inventory SET version=0`, `UPDATE stock_reservations SET units=0`, `UPDATE stock_reservations SET state='RETURNED',final_units=units+1`, `UPDATE stock_reservations SET request_digest=decode('00','hex')`} {
		_, err := pool.Exec(ctx, sql)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
			t.Fatalf("database accepted invalid state or wrong failure: %s %v", sql, err)
		}
	}
	for _, sql := range []string{`UPDATE inventory_movements SET hand_delta=0`, `DELETE FROM inventory_movements`, `TRUNCATE inventory_movements`} {
		_, err := pool.Exec(ctx, sql)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatalf("append-only statement not rejected: %s %v", sql, err)
		}
	}
	assertStock(t, pool, ctx, 3, 1, 1)
}

func TestCatalogLifecycleDetailsAndExactReplay(t *testing.T) {
	for _, mode := range []string{"release", "sorted", "return"} {
		t.Run(mode, func(t *testing.T) {
			pool, ctx := stockFixture(t)
			s := Store{}
			run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
			if err := run(func(tx pgx.Tx) error {
				q, err := stockQuote(ctx, tx, "triple")
				if err != nil {
					return err
				}
				return s.Reserve(ctx, tx, "life", q)
			}); err != nil {
				t.Fatal(err)
			}
			assertStock(t, pool, ctx, 3, 3, 1)
			if err := run(func(tx pgx.Tx) error { return s.Restock(ctx, tx, "life", nil) }); !errors.Is(err, ErrStockState) {
				t.Fatal("return without pickup accepted")
			}
			if mode == "return" {
				if err := run(func(tx pgx.Tx) error { return s.Consume(ctx, tx, "life") }); err != nil {
					t.Fatal(err)
				}
				assertStock(t, pool, ctx, 0, 0, 2)
			}
			if _, err := pool.Exec(ctx, `UPDATE products SET on_sale=false,version=version+1 WHERE id='p'`); err != nil {
				t.Fatal(err)
			}
			valid := func(tx pgx.Tx) error {
				switch mode {
				case "release":
					return s.Release(ctx, tx, "life")
				case "sorted":
					return s.RestockCanceled(ctx, tx, "life", []contract.CanceledStockLine{{InventoryID: "i", DamagedUnits: 1}})
				default:
					return s.Restock(ctx, tx, "life", []contract.RestockLine{{InventoryID: "i", SellableUnits: 2}})
				}
			}
			if mode != "release" {
				for _, bad := range []string{"negative", "zero", "duplicate", "unknown", "above"} {
					err := run(func(tx pgx.Tx) error {
						n := int32(1)
						id := "i"
						switch bad {
						case "negative":
							n = -1
						case "zero":
							n = 0
						case "unknown":
							id = "missing"
						case "above":
							n = 4
						}
						if mode == "sorted" {
							lines := []contract.CanceledStockLine{{InventoryID: id, DamagedUnits: n}}
							if bad == "duplicate" {
								lines = append(lines, lines[0])
							}
							return s.RestockCanceled(ctx, tx, "life", lines)
						}
						lines := []contract.RestockLine{{InventoryID: id, SellableUnits: n}}
						if bad == "duplicate" {
							lines = append(lines, lines[0])
						}
						return s.Restock(ctx, tx, "life", lines)
					})
					if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrStockState) {
						t.Fatalf("invalid inspection accepted: %s %v", bad, err)
					}
				}
			}
			for range 2 {
				if err := run(valid); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "release":
				assertStock(t, pool, ctx, 3, 0, 2)
			case "sorted":
				assertStock(t, pool, ctx, 2, 0, 2)
			default:
				assertStock(t, pool, ctx, 2, 0, 3)
			}
			if mode != "release" {
				err := run(func(tx pgx.Tx) error {
					if mode == "sorted" {
						return s.RestockCanceled(ctx, tx, "life", nil)
					}
					return s.Restock(ctx, tx, "life", nil)
				})
				if !errors.Is(err, ErrStockState) {
					t.Fatal("different inspection replay accepted")
				}
			}
		})
	}
}

func TestCatalogSamePhysicalAggregationAndConcurrentLastUnit(t *testing.T) {
	t.Run("mixed specifications aggregate", func(t *testing.T) {
		pool, ctx := stockFixture(t)
		s := Store{}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		q, err := s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 1}, {SKUID: "triple", Qty: 1}}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Reserve(ctx, tx, "mixed", q); !errors.Is(err, ErrStock) {
			t.Fatal("mixed spec bypassed stock aggregate")
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE inventory SET on_hand=4`); err != nil {
			t.Fatal(err)
		}
		if err = platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			if err = s.Reserve(ctx, tx, "mixed", q); err != nil {
				return err
			}
			q.Lines[0], q.Lines[1] = q.Lines[1], q.Lines[0]
			return s.Reserve(ctx, tx, "mixed", q)
		}); err != nil {
			t.Fatal(err)
		}
		assertStock(t, pool, ctx, 4, 4, 1)
	})
	t.Run("last physical unit barrier", func(t *testing.T) {
		pool, ctx := stockFixture(t)
		if _, err := pool.Exec(ctx, `UPDATE inventory SET on_hand=1`); err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{}, 2)
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, id := range []string{"a", "b"} {
			go func() {
				results <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
					q, err := stockQuote(ctx, tx, "single")
					ready <- struct{}{}
					select {
					case <-start:
					case <-ctx.Done():
						return ctx.Err()
					}
					if err != nil {
						return err
					}
					return (Store{}).Reserve(ctx, tx, id, q)
				})
			}()
		}
		for range 2 {
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("transactions did not reach barrier")
			}
		}
		close(start)
		a, b := <-results, <-results
		if !((a == nil && errors.Is(b, ErrStock)) || (b == nil && errors.Is(a, ErrStock))) {
			t.Fatalf("last unit: %v / %v", a, b)
		}
		assertStock(t, pool, ctx, 1, 1, 1)
	})
}

func TestCatalogMigrationsStandaloneRepeatAndUniqueVersions(t *testing.T) {
	pool, ctx := stockFixture(t)
	files := fstest.MapFS{}
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range names {
		version := name[:7]
		if seen[version] {
			t.Fatal("duplicate global migration version")
		}
		seen[version] = true
		if strings.HasPrefix(name, "000_") || strings.HasPrefix(name, "020_") || name == "070_001_product_image_url.sql" {
			data, err := fs.ReadFile(migrations.SQL, name)
			if err != nil {
				t.Fatal(err)
			}
			files[name] = &fstest.MapFile{Data: data}
		}
	}
	snapshot := func() string {
		t.Helper()
		var state string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m),(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM inventory i),(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p),(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM skus s))::text`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	for range 2 {
		if err := platform.Migrate(ctx, pool, files); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot() != before {
		t.Fatal("repeat changed historical digest/time or existing business data")
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version NOT LIKE '000_%' AND version NOT LIKE '020_%' AND version<>'070_001'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("core fixture used another domain's migration")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('accounts','sessions','delivery_slots','wallets','orders')`).Scan(&n); err != nil || n != 0 {
		t.Fatal("core fixture contains private tables from another package")
	}
}

func TestCatalogMigrationUpgradePreservesLegacyFulfillment(t *testing.T) {
	pool, ctx := stockFixtureVersion(t, false, true)
	if _, err := pool.Exec(ctx, `INSERT INTO stock_reservations(order_id,inventory_id,units) VALUES('legacy','i',3);UPDATE inventory SET reserved=3,version=2;INSERT INTO inventory_movements(inventory_id,order_id,kind,hand_delta,reserved_delta,inventory_version) VALUES('i','legacy','RESERVED',0,3,2)`); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "000_") || strings.HasPrefix(name, "020_") {
			data, err := fs.ReadFile(migrations.SQL, name)
			if err != nil {
				t.Fatal(err)
			}
			files[name] = &fstest.MapFile{Data: data}
		}
	}
	var before string
	if err = pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = platform.Migrate(ctx, pool, files); err != nil {
		t.Fatal(err)
	}
	var old string
	if err = pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m WHERE version<>'020_002'`).Scan(&old); err != nil || old != before {
		t.Fatal("upgrade changed original migration history")
	}
	assertStock(t, pool, ctx, 3, 3, 1)
	run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	if err = run(func(tx pgx.Tx) error {
		q, err := stockQuote(ctx, tx, "triple")
		if err != nil {
			return err
		}
		return (Store{}).Reserve(ctx, tx, "legacy", q)
	}); !errors.Is(err, ErrStockState) {
		t.Fatal("legacy request without snapshot falsely claimed successful replay")
	}
	for range 2 {
		if err = run(func(tx pgx.Tx) error { return (Store{}).Consume(ctx, tx, "legacy") }); err != nil {
			t.Fatal(err)
		}
	}
	assertStock(t, pool, ctx, 0, 0, 2)
}

func TestReleaseAndCanceledRestockReadPendingCallerReservation(t *testing.T) {
	for _, mode := range []string{"release", "inspect"} {
		t.Run(mode, func(t *testing.T) {
			pool, ctx := stockFixture(t)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			s := Store{}
			q, err := stockQuote(ctx, tx, "triple")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Reserve(ctx, tx, "pending", q); err != nil {
				t.Fatal(err)
			}
			if mode == "release" {
				err = s.Release(ctx, tx, "pending")
			} else {
				err = s.RestockCanceled(ctx, tx, "pending", []contract.CanceledStockLine{{InventoryID: "i", DamagedUnits: 1}})
			}
			if err != nil {
				t.Fatal(err)
			}
			var h, r, m int
			if err = tx.QueryRow(ctx, `SELECT on_hand,reserved,(SELECT count(*) FROM inventory_movements) FROM inventory WHERE id='i'`).Scan(&h, &r, &m); err != nil || r != 0 || m != 2 {
				t.Fatal("pending caller changes not visible")
			}
			if mode == "release" && h != 3 || mode == "inspect" && h != 2 {
				t.Fatal("incorrect pending on-hand")
			}
			assertStock(t, pool, ctx, 3, 0, 0)
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			assertStock(t, pool, ctx, 3, 0, 0)
		})
	}
}

func TestCatalogEmptyInspectionMeansZeroDamageOrZeroSellable(t *testing.T) {
	for _, mode := range []string{"sorted", "return"} {
		t.Run(mode, func(t *testing.T) {
			pool, ctx := stockFixture(t)
			s := Store{}
			run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
			if err := run(func(tx pgx.Tx) error {
				q, err := stockQuote(ctx, tx, "triple")
				if err != nil {
					return err
				}
				return s.Reserve(ctx, tx, "empty", q)
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "return" {
				if err := run(func(tx pgx.Tx) error { return s.Consume(ctx, tx, "empty") }); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := run(func(tx pgx.Tx) error {
					if mode == "sorted" {
						return s.RestockCanceled(ctx, tx, "empty", nil)
					}
					return s.Restock(ctx, tx, "empty", nil)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "sorted" {
				assertStock(t, pool, ctx, 3, 0, 2)
			} else {
				assertStock(t, pool, ctx, 0, 0, 3)
			}
		})
	}
}
