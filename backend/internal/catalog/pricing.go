package catalog

import (
	"errors"
	"time"

	"campus.local/xiaoling/internal/contract"
)

const maxFen int64 = 1<<53 - 1

var (
	ErrInvalid      = errors.New("invalid catalog input")
	ErrUnavailable  = errors.New("SKU unavailable")
	ErrPriceChanged = errors.New("price version changed")
	ErrStock        = errors.New("insufficient available stock")
	ErrStockState   = errors.New("stock operation conflicts with prior operation")
)

type Tier struct {
	MinQty      int32 `json:"min_qty"`
	DiscountBPS int32 `json:"discount_bps"`
}

type Promotion struct {
	ID       string    `json:"-"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Tiers    []Tier    `json:"tiers"`
}

type pricingSKU struct {
	productID, inventoryID, name, label string
	version, price                      int64
	units                               int32
	onSale                              bool
	promotion                           *Promotion
}

func priceLine(input contract.OrderLineInput, sku pricingSKU, at time.Time) (contract.PricedLine, error) {
	if !sku.onSale {
		return contract.PricedLine{}, ErrUnavailable
	}
	if input.SKUID == "" || input.Qty < 1 || input.Qty > 999 || (sku.units != 1 && sku.units != 3) || sku.price < 0 || sku.price > maxFen {
		return contract.PricedLine{}, ErrInvalid
	}
	var threshold, discount int32
	var promoID string
	if p := sku.promotion; p != nil && !at.Before(p.StartsAt) && at.Before(p.EndsAt) {
		for _, tier := range p.Tiers {
			if tier.MinQty < 1 || tier.MinQty > 999 || tier.DiscountBPS < 0 || tier.DiscountBPS > 10000 {
				return contract.PricedLine{}, ErrInvalid
			}
			if tier.MinQty <= input.Qty && tier.MinQty > threshold {
				threshold, discount, promoID = tier.MinQty, tier.DiscountBPS, p.ID
			}
		}
	}
	// Split the multiplication so even the maximum API amount fits in int64.
	rate := int64(10000 - discount)
	unit := sku.price/10000*rate + (sku.price%10000*rate+5000)/10000
	if unit > maxFen/int64(input.Qty) {
		return contract.PricedLine{}, ErrInvalid
	}
	return contract.PricedLine{
		OrderLineInput: input, ProductID: sku.productID, InventoryID: sku.inventoryID,
		Name: sku.name, Spec: sku.label, PromotionID: promoID, UnitsPerPack: sku.units,
		DiscountBPS: discount, PriceVersion: sku.version, UnitPriceFen: unit,
		LineTotalFen: unit * int64(input.Qty),
	}, nil
}
