package catalog

import (
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
)

func TestPricingSelectsOneTierAndRoundsBeforeMultiplication(t *testing.T) {
	start := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	p := &Promotion{ID: "promo-v2", StartsAt: start, EndsAt: start.Add(time.Hour), Tiers: []Tier{{4, 2500}, {1, 500}, {2, 1500}}}
	for _, tc := range []struct {
		name           string
		at             time.Time
		qty, pack, bps int32
		unit, total    int64
		promo          string
	}{
		{"before start", start.Add(-time.Nanosecond), 2, 1, 0, 110, 220, ""},
		{"start inclusive and 93.5 rounds to 94", start, 2, 3, 1500, 94, 188, "promo-v2"},
		{"highest threshold", start, 4, 1, 2500, 83, 332, "promo-v2"},
		{"end exclusive", p.EndsAt, 4, 1, 0, 110, 440, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pricingSKU{productID: "product", inventoryID: "shared", name: "Snack", label: "Pack", version: 7, units: tc.pack, price: 110, onSale: true, promotion: p}
			got, err := priceLine(contract.OrderLineInput{SKUID: "sku", Qty: tc.qty}, s, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if got.UnitPriceFen != tc.unit || got.LineTotalFen != tc.total || got.DiscountBPS != tc.bps || got.PromotionID != tc.promo || got.PriceVersion != 7 || got.UnitsPerPack != tc.pack || got.InventoryID != "shared" || got.ProductID != "product" {
				t.Fatalf("unexpected price snapshot: %+v", got)
			}
		})
	}
}

func TestPricingRejectsInvalidAmountsAndUnavailableSKUs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		qty, pack int32
		price     int64
		onSale    bool
	}{
		{"zero qty", 0, 1, 1, true},
		{"too many packs", 1000, 1, 1, true},
		{"unsupported spec", 1, 2, 1, true},
		{"negative price", 1, 1, -1, true},
		{"out of contract range", 1, 1, 9007199254740992, true},
		{"line total out of range", 2, 3, 9007199254740991, true},
		{"off sale", 1, 1, 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := priceLine(contract.OrderLineInput{SKUID: "sku", Qty: tc.qty}, pricingSKU{units: tc.pack, price: tc.price, onSale: tc.onSale}, time.Now())
			if err == nil {
				t.Fatal("invalid input or unavailable SKU received a price")
			}
		})
	}
}

func TestMaximumPriceCanBeDiscountedWithoutOverflow(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	s := pricingSKU{units: 1, price: 9007199254740991, onSale: true, promotion: &Promotion{ID: "half", StartsAt: now, EndsAt: now.Add(time.Minute), Tiers: []Tier{{1, 5000}}}}
	got, err := priceLine(contract.OrderLineInput{SKUID: "sku", Qty: 1}, s, now)
	if err != nil || got.UnitPriceFen != 4503599627370496 {
		t.Fatalf("discount at public amount limit: %+v; %v", got, err)
	}
}
