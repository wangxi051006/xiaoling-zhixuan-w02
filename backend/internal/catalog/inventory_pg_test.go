//go:build integration

package catalog

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func stockFixture(t *testing.T) (*pgxpool.Pool, context.Context) {
	return stockFixtureWithIdentity(t, false)
}

// Core catalog fixtures use 000+020 only; identity HTTP fixtures opt into 010.
func stockFixtureWithIdentity(t *testing.T, withIdentity bool) (*pgxpool.Pool, context.Context) {
	return stockFixtureVersion(t, withIdentity, false)
}

func stockFixtureVersion(t *testing.T, withIdentity, legacy bool) (*pgxpool.Pool, context.Context) {
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
	schema := pgx.Identifier{"p02_" + randomID()}.Sanitize()
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
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if legacy && name == "020_002_reservation_digest.sql" {
			continue
		}
		if !strings.HasPrefix(name, "000_") && !strings.HasPrefix(name, "020_") && !(withIdentity && strings.HasPrefix(name, "010_")) && !(withIdentity && strings.HasPrefix(name, "060_")) {
			continue
		}
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
	INSERT INTO products(id,station_id,name,on_sale) VALUES ('p','s','Snack',true);
	INSERT INTO inventory(id,product_id,on_hand) VALUES ('i','p',3);
	INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen) VALUES ('single','p','Single',1,110),('triple','p','Triple',3,300);`); err != nil {
		t.Fatal(err)
	}
	return pool, ctx
}

func stockQuote(ctx context.Context, tx pgx.Tx, sku string) (contract.PricedLines, error) {
	return (Store{}).Price(ctx, tx, []contract.OrderLineInput{{SKUID: sku, Qty: 1}}, time.Now())
}

func assertStock(t *testing.T, pool *pgxpool.Pool, ctx context.Context, hand, reserved, moves int) {
	t.Helper()
	var h, r, m int
	err := pool.QueryRow(ctx, `SELECT on_hand,reserved,(SELECT count(*) FROM inventory_movements) FROM inventory WHERE id='i'`).Scan(&h, &r, &m)
	if err != nil || h != hand || r != reserved || m != moves {
		t.Fatalf("stock=(%d,%d,%d) want (%d,%d,%d): %v", h, r, m, hand, reserved, moves, err)
	}
}

func TestSharedInventoryRace(t *testing.T) {
	pool, ctx := stockFixture(t)
	svc := Store{}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	results := make(chan error, 2)
	for _, sku := range []string{"single", "triple"} {
		go func() {
			results <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				quote, err := stockQuote(ctx, tx, sku)
				ready <- struct{}{}
				select {
				case <-start:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err != nil {
					return err
				}
				return svc.Reserve(ctx, tx, sku, quote)
			})
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("transactions did not reach stock barrier")
		}
	}
	close(start)
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, ErrStock)) || (b == nil && errors.Is(a, ErrStock))) {
		t.Fatalf("want one success and one stock conflict, got %v / %v", a, b)
	}
	var reserved int
	if err := pool.QueryRow(ctx, `SELECT CASE order_id WHEN 'triple' THEN 3 ELSE 1 END FROM stock_reservations`).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	assertStock(t, pool, ctx, 3, reserved, 1)
}

func TestPickupAndCancellationExcludeEachOther(t *testing.T) {
	pool, ctx := stockFixture(t)
	svc := Store{}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		q, err := stockQuote(ctx, tx, "triple")
		if err != nil {
			return err
		}
		return svc.Reserve(ctx, tx, "order", q)
	}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	pickup, cancel := make(chan error, 1), make(chan error, 1)
	for _, op := range []struct {
		fn     func(context.Context, pgx.Tx, string) error
		result chan error
	}{{svc.Consume, pickup}, {svc.Release, cancel}} {
		go func() {
			op.result <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				ready <- struct{}{}
				select {
				case <-start:
				case <-ctx.Done():
					return ctx.Err()
				}
				return op.fn(ctx, tx, "order")
			})
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("transactions did not reach lifecycle barrier")
		}
	}
	close(start)
	a, b := <-pickup, <-cancel
	switch {
	case a == nil && errors.Is(b, ErrStockState):
		assertStock(t, pool, ctx, 0, 0, 2)
	case b == nil && errors.Is(a, ErrStockState):
		assertStock(t, pool, ctx, 3, 0, 2)
	default:
		t.Fatalf("pickup=%v cancellation=%v", a, b)
	}
}

func TestReservationsLockMultipleItemsInOneOrder(t *testing.T) {
	pool, ctx := stockFixture(t)
	_, err := pool.Exec(ctx, `INSERT INTO products(id,station_id,name,on_sale) VALUES('p2','s','Other',true);
		INSERT INTO inventory(id,product_id,on_hand) VALUES('j','p2',3);
		INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen) VALUES('other','p2','Other',1,100);`)
	if err != nil {
		t.Fatal(err)
	}
	svc := Store{}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	results := make(chan error, 2)
	for _, ids := range [][2]string{{"single", "other"}, {"other", "single"}} {
		go func() {
			results <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				q, err := svc.Price(ctx, tx, []contract.OrderLineInput{{SKUID: ids[0], Qty: 1}, {SKUID: ids[1], Qty: 1}}, time.Now())
				ready <- struct{}{}
				select {
				case <-start:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err != nil {
					return err
				}
				return svc.Reserve(ctx, tx, ids[0], q)
			})
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("transactions did not reach multi-item barrier")
		}
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count, moves int
	if err := pool.QueryRow(ctx, `SELECT count(*),(SELECT count(*) FROM inventory_movements) FROM inventory WHERE on_hand=3 AND reserved=2`).Scan(&count, &moves); err != nil || count != 2 || moves != 4 {
		t.Fatalf("inventory=%d movements=%d error=%v", count, moves, err)
	}
}

func TestStockRollbackAndReplay(t *testing.T) {
	pool, ctx := stockFixture(t)
	svc := Store{}
	run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	reserve := func(tx pgx.Tx) error {
		q, err := stockQuote(ctx, tx, "triple")
		if err != nil {
			return err
		}
		return svc.Reserve(ctx, tx, "order", q)
	}
	failure := errors.New("abort order transaction")
	if err := run(func(tx pgx.Tx) error {
		if err := reserve(tx); err != nil {
			return err
		}
		return failure
	}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	assertStock(t, pool, ctx, 3, 0, 0)
	for range 2 {
		if err := run(reserve); err != nil {
			t.Fatal(err)
		}
	}
	assertStock(t, pool, ctx, 3, 3, 1)
	if err := run(func(tx pgx.Tx) error {
		q, err := stockQuote(ctx, tx, "single")
		if err != nil {
			return err
		}
		return svc.Reserve(ctx, tx, "order", q)
	}); !errors.Is(err, ErrStockState) {
		t.Fatalf("changed replay: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET on_sale=false,version=version+1 WHERE id='p'`); err != nil {
		t.Fatal(err)
	}
	if err := run(func(tx pgx.Tx) error { _, err := stockQuote(ctx, tx, "triple"); return err }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("off sale: %v", err)
	}
	for range 2 {
		if err := run(func(tx pgx.Tx) error { return svc.Consume(ctx, tx, "order") }); err != nil {
			t.Fatal(err)
		}
	}
	assertStock(t, pool, ctx, 0, 0, 2)
	if err := run(func(tx pgx.Tx) error { return svc.Release(ctx, tx, "order") }); !errors.Is(err, ErrStockState) {
		t.Fatalf("release after pickup: %v", err)
	}
	if err := run(func(tx pgx.Tx) error {
		return svc.Restock(ctx, tx, "order", []contract.RestockLine{{InventoryID: "i", SellableUnits: 4}})
	}); !errors.Is(err, ErrStockState) {
		t.Fatalf("return beyond quantity: %v", err)
	}
	for range 2 {
		if err := run(func(tx pgx.Tx) error {
			return svc.Restock(ctx, tx, "order", []contract.RestockLine{{InventoryID: "i", SellableUnits: 2}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertStock(t, pool, ctx, 2, 0, 3)
}

func TestCanceledSortedStockAndStaleQuote(t *testing.T) {
	pool, ctx := stockFixture(t)
	svc := Store{}
	run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	var q contract.PricedLines
	if err := run(func(tx pgx.Tx) (err error) { q, err = stockQuote(ctx, tx, "triple"); return }); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE products SET version=version+1 WHERE id='p'`); err != nil {
		t.Fatal(err)
	}
	if err := run(func(tx pgx.Tx) error { return svc.Reserve(ctx, tx, "order", q) }); !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("stale quote: %v", err)
	}
	assertStock(t, pool, ctx, 3, 0, 0)
	if err := run(func(tx pgx.Tx) error {
		q, err := stockQuote(ctx, tx, "triple")
		if err != nil {
			return err
		}
		return svc.Reserve(ctx, tx, "order", q)
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := run(func(tx pgx.Tx) error {
			return svc.RestockCanceled(ctx, tx, "order", []contract.CanceledStockLine{{InventoryID: "i", DamagedUnits: 1}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertStock(t, pool, ctx, 2, 0, 2)
	if _, err := pool.Exec(ctx, `DELETE FROM inventory_movements`); err == nil {
		t.Fatal("ledger allowed deletion")
	}
	if _, err := pool.Exec(ctx, `UPDATE inventory SET reserved=on_hand+1`); err == nil {
		t.Fatal("stock invariant unenforced")
	}
}
