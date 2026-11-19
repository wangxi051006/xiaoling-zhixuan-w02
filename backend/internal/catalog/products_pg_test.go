//go:build integration

package catalog

import (
	"errors"
	"testing"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

func TestAdjustmentRespectsReservationAndRecordsActor(t *testing.T) {
	pool, ctx := stockFixture(t)
	s := Store{}
	run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	err := run(func(tx pgx.Tx) error {
		quote, err := stockQuote(ctx, tx, "triple")
		if err != nil {
			return err
		}
		return s.Reserve(ctx, tx, "order", quote)
	})
	if err != nil {
		t.Fatal(err)
	}
	adjust := func(station string, version, delta int64) error {
		return run(func(tx pgx.Tx) error {
			_, err := s.Adjust(ctx, tx, station, "operator", Adjustment{"i", delta, version, "COUNT"})
			return err
		})
	}
	if err := adjust("other", 2, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("station scope: %v", err)
	}
	if err := adjust("s", 1, 1); !errors.Is(err, ErrVersion) {
		t.Fatalf("stale version: %v", err)
	}
	if err := adjust("s", 2, -1); !errors.Is(err, ErrStock) {
		t.Fatalf("reduce below reserved: %v", err)
	}
	assertStock(t, pool, ctx, 3, 3, 1)
	if err := adjust("s", 2, 2); err != nil {
		t.Fatal(err)
	}
	assertStock(t, pool, ctx, 5, 3, 2)
	var actor, reason string
	var delta, version int64
	err = pool.QueryRow(ctx, `SELECT actor_id,reason_code,hand_delta,inventory_version FROM inventory_movements WHERE kind='ADJUSTED'`).Scan(&actor, &reason, &delta, &version)
	if err != nil || actor != "operator" || reason != "COUNT" || delta != 2 || version != 3 {
		t.Fatalf("unexpected adjustment ledger: %s %s %d %d %v", actor, reason, delta, version, err)
	}
}

func TestProductChangeInvalidatesPriorQuote(t *testing.T) {
	pool, ctx := stockFixture(t)
	s := Store{}
	run := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	var quote contract.PricedLines
	if err := run(func(tx pgx.Tx) (err error) { quote, err = stockQuote(ctx, tx, "triple"); return }); err != nil {
		t.Fatal(err)
	}
	err := run(func(tx pgx.Tx) error {
		item, err := s.UpdateProduct(ctx, tx, "s", "p", ProductUpdate{ExpectedVersion: 1, SKUs: &[]SKU{{ID: "single", Label: "Single", UnitsPerPack: 1, UnitPriceFen: 110}, {ID: "triple", Label: "Triple", UnitsPerPack: 3, UnitPriceFen: 320}}})
		if err == nil && (item.Version != 2 || len(item.SKUs) != 2 || item.SKUs[1].UnitPriceFen != 320) {
			t.Fatalf("updated product: %+v", item)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(func(tx pgx.Tx) error { return s.Reserve(ctx, tx, "old-quote", quote) }); !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("old quote: %v", err)
	}
	assertStock(t, pool, ctx, 3, 0, 0)
}
