package contract

import "time"

type AddressSnapshot struct {
	AddressID, StationID, BuildingID, BuildingName string
	Room, ContactName, ContactPhone                string
	Version                                        int64
}

type OrderLineInput struct {
	SKUID string `json:"sku_id"`
	Qty   int32  `json:"qty"`
}

type PricedLine struct {
	OrderLineInput
	ProductID, InventoryID, Name, Spec, PromotionID string
	UnitsPerPack, DiscountBPS                       int32
	PriceVersion, UnitPriceFen, LineTotalFen        int64
}

type PricedLines struct {
	Lines                                 []PricedLine
	SubtotalFen, DeliveryFeeFen, TotalFen int64
}

type RestockLine struct {
	InventoryID   string
	SellableUnits int32
}

// CanceledStockLine describes damage found in goods that have not been picked up.
// RestockCanceled releases the whole reservation and decrements on-hand by damage only.
type CanceledStockLine struct {
	InventoryID  string
	DamagedUnits int32
}

type SlotSnapshot struct {
	ID, StationID                string
	StartsAt, EndsAt, DispatchAt time.Time
	Version                      int64
}

type OrderMoney struct {
	OrderID, ActorID, StationID string
	PaymentMethod               PaymentMethod
	TotalFen                    int64
}

type RefundRequest struct {
	OrderID, OperatorID, ReasonCode string
}

type RefundApproval struct {
	OrderID, RefundID, FinanceID, ReasonCode string
}

type CODPayout struct {
	OrderID, RefundID, FinanceID, ReceiptReference string
}
