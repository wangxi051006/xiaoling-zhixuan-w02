package contract

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type IdentityPort interface {
	ResolveAddress(ctx context.Context, tx pgx.Tx, actorID, addressID string) (AddressSnapshot, error)
	AssertRider(ctx context.Context, tx pgx.Tx, actorID, stationID string) error
}

type CatalogPort interface {
	Price(ctx context.Context, tx pgx.Tx, lines []OrderLineInput, at time.Time) (PricedLines, error)
	Reserve(ctx context.Context, tx pgx.Tx, orderID string, lines PricedLines) error
	Consume(ctx context.Context, tx pgx.Tx, orderID string) error
	Release(ctx context.Context, tx pgx.Tx, orderID string) error
	Restock(ctx context.Context, tx pgx.Tx, orderID string, lines []RestockLine) error
	RestockCanceled(ctx context.Context, tx pgx.Tx, orderID string, lines []CanceledStockLine) error
}

type SchedulePort interface {
	SelectSlot(ctx context.Context, tx pgx.Tx, stationID string, mode DeliveryMode, requestedSlotID string, now time.Time) (SlotSnapshot, error)
	ReserveSlot(ctx context.Context, tx pgx.Tx, slotID, orderID string, mode DeliveryMode, now time.Time) error
	ReleaseSlot(ctx context.Context, tx pgx.Tx, orderID string, now time.Time) error
}

type SettlementPort interface {
	CreateSettlement(ctx context.Context, tx pgx.Tx, money OrderMoney) error
	RecordCODCollection(ctx context.Context, tx pgx.Tx, orderID, riderID string) error
	CancelSettlement(ctx context.Context, tx pgx.Tx, orderID string) error
	FinalizeReturnSettlement(ctx context.Context, tx pgx.Tx, orderID string) error
	RequestRefund(ctx context.Context, tx pgx.Tx, request RefundRequest) error
	ApproveRefund(ctx context.Context, tx pgx.Tx, approval RefundApproval) error
	RecordCODPayout(ctx context.Context, tx pgx.Tx, payout CODPayout) error
}
