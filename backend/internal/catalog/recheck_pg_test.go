//go:build integration

package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

func TestReserveRejectsNaturallyExpiredPromotion(t *testing.T) {
	pool, ctx := stockFixture(t)
	s := Store{}
	now := time.Now().UTC().Truncate(time.Second)
	run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	var q contract.PricedLines
	if err := run(func(tx pgx.Tx) error {
		_, err := s.UpdatePromotion(ctx, tx, "s", "p", 1, &Promotion{StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), Tiers: []Tier{{1, 5000}}})
		if err != nil {
			return err
		}
		q, err = s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 1}}, now.Add(-90*time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := run(func(tx pgx.Tx) error { return s.Reserve(ctx, tx, "expired", q) }); !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("expired promotion was accepted: %v", err)
	}
	assertStock(t, pool, ctx, 3, 0, 0)
}

func TestReserveRejectsChangedSnapshotAndSameUnitsDifferentSKUReplay(t *testing.T) {
	for _, field := range []string{"unit_price", "total", "discount", "promotion", "name", "spec", "subtotal", "fee", "grand_total"} {
		t.Run(field, func(t *testing.T) {
			pool, ctx := stockFixture(t)
			var q contract.PricedLines
			err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) (err error) { q, err = stockQuote(ctx, tx, "single"); return })
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "unit_price":
				q.Lines[0].UnitPriceFen++
			case "total":
				q.Lines[0].LineTotalFen++
			case "discount":
				q.Lines[0].DiscountBPS = 10001
			case "promotion":
				q.Lines[0].PromotionID = "untrusted"
			case "name":
				q.Lines[0].Name = "changed"
			case "spec":
				q.Lines[0].Spec = "changed"
			case "subtotal":
				q.SubtotalFen++
			case "fee":
				q.DeliveryFeeFen++
			case "grand_total":
				q.TotalFen++
			}
			err = platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return (Store{}).Reserve(ctx, tx, "changed", q) })
			if !errors.Is(err, ErrPriceChanged) && !errors.Is(err, ErrInvalid) {
				t.Fatalf("changed %s accepted: %v", field, err)
			}
			assertStock(t, pool, ctx, 3, 0, 0)
		})
	}
	t.Run("different sku same physical count", func(t *testing.T) {
		pool, ctx := stockFixture(t)
		s := Store{}
		run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
		if err := run(func(tx pgx.Tx) error {
			q, err := stockQuote(ctx, tx, "triple")
			if err != nil {
				return err
			}
			return s.Reserve(ctx, tx, "same-key", q)
		}); err != nil {
			t.Fatal(err)
		}
		err := run(func(tx pgx.Tx) error {
			q, err := s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: "single", Qty: 3}}, time.Now())
			if err != nil {
				return err
			}
			return s.Reserve(ctx, tx, "same-key", q)
		})
		if !errors.Is(err, ErrStockState) {
			t.Fatalf("different request treated as successful replay: %v", err)
		}
		assertStock(t, pool, ctx, 3, 3, 1)
	})
}

func TestCatalogCallerTransactionVisibilityAndRollback(t *testing.T) {
	pool, ctx := stockFixture(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	s := Store{}
	p, err := s.CreateProduct(ctx, tx, "s", ProductInput{Name: "Uncommitted", OnSale: true, SKUs: []SKUInput{{"Single", 1, 7}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Adjust(ctx, tx, "s", "operator", Adjustment{p.Inventory.ID, 2, 1, "COUNT"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Price(ctx, tx, []contract.OrderLineInput{{SKUID: p.SKUs[0].ID, Qty: 2}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if q.TotalFen != 14 {
		t.Fatal("uncommitted price not visible")
	}
	if err = s.Reserve(ctx, tx, "uncommitted", q); err != nil {
		t.Fatal(err)
	}
	if err = s.Consume(ctx, tx, "uncommitted"); err != nil {
		t.Fatal(err)
	}
	if err = s.Restock(ctx, tx, "uncommitted", []contract.RestockLine{{InventoryID: p.Inventory.ID, SellableUnits: 1}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE id=$1`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("caller data escaped transaction")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM products WHERE id=$1)+(SELECT count(*) FROM stock_reservations)+(SELECT count(*) FROM inventory_movements)`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("caller rollback left data: %d %v", n, err)
	}
}
