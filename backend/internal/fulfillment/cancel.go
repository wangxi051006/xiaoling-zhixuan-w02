package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"context"
	"github.com/jackc/pgx/v5"
	"regexp"
)

var reasonPattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

type Reason struct {
	Code string `json:"reason_code"`
}
type Damage struct {
	InventoryID string `json:"inventory_id"`
	Units       int32  `json:"damaged_units"`
}
type Sellable struct {
	InventoryID string `json:"inventory_id"`
	Units       int32  `json:"sellable_units"`
}
type RestockInput struct {
	Code    string   `json:"reason_code"`
	Damaged []Damage `json:"damaged"`
}
type InspectInput struct {
	Code     string     `json:"reason_code"`
	Sellable []Sellable `json:"sellable"`
}

func (s *Service) cancel(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow, reason string, customer bool) error {
	if !reasonPattern.MatchString(reason) {
		return ErrInvalid
	}
	if customer && o.ActorID != actor.AccountID {
		return ErrNotFound
	}
	now := s.Clock.Now().UTC()
	if customer && !selfCancelable(o.Mode, o.Status, o.Stock, o.Slot.DispatchAt, now) {
		return ErrInvalidState
	}
	switch o.Status {
	case contract.OrderStatusScheduled, contract.OrderStatusPreparing, contract.OrderStatusReadyForClaim, contract.OrderStatusAwaitingPickup:
	default:
		return ErrInvalidState
	}
	// Slot must precede inventory and settlement; sorted capacity is intentionally retained.
	stock := contract.StockStatusRestockPending
	if o.Stock == contract.StockStatusUnsorted {
		if now.Before(o.Slot.EndsAt) {
			if err := s.Schedule.ReleaseSlot(ctx, tx, o.ID, now); err != nil {
				return err
			}
		}
		if err := s.Catalog.Release(ctx, tx, o.ID); err != nil {
			return err
		}
		stock = contract.StockStatusRestocked
	} else if o.Stock != contract.StockStatusSorted {
		return ErrInvalidState
	}
	if err := s.Settlement.CancelSettlement(ctx, tx, o.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE delivery_tasks SET state='CLOSED',finished_at=$2 WHERE order_id=$1 AND state IN ('OPEN','ASSIGNED')`, o.ID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='CANCELED',stock_status=$2 WHERE id=$1`, o.ID, stock); err != nil {
		return err
	}
	if err := s.event(ctx, tx, actor, o.ID, "CANCELED", reason); err != nil {
		return err
	}
	return s.notify(ctx, tx, "ORDER_CANCELED", o)
}
func (s *Service) restock(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow, in RestockInput) error {
	if !reasonPattern.MatchString(in.Code) || in.Damaged == nil || len(in.Damaged) > 50 {
		return ErrInvalid
	}
	if o.Status != contract.OrderStatusCanceled || o.Stock != contract.StockStatusRestockPending {
		return ErrInvalidState
	}
	lines := make([]contract.CanceledStockLine, 0, len(in.Damaged))
	seen := map[string]bool{}
	for _, l := range in.Damaged {
		if !validID(l.InventoryID) || l.Units < 1 || l.Units > 99999 || seen[l.InventoryID] {
			return ErrInvalid
		}
		seen[l.InventoryID] = true
		lines = append(lines, contract.CanceledStockLine{InventoryID: l.InventoryID, DamagedUnits: l.Units})
	}
	if err := s.Catalog.RestockCanceled(ctx, tx, o.ID, lines); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET stock_status='RESTOCKED' WHERE id=$1`, o.ID); err != nil {
		return err
	}
	return s.event(ctx, tx, actor, o.ID, "RESTOCKED", in.Code)
}
func (s *Service) inspect(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow, in InspectInput) error {
	if !reasonPattern.MatchString(in.Code) || in.Sellable == nil || len(in.Sellable) > 50 {
		return ErrInvalid
	}
	if o.Status != contract.OrderStatusReturning {
		return ErrInvalidState
	}
	lines := make([]contract.RestockLine, 0, len(in.Sellable))
	seen := map[string]bool{}
	for _, l := range in.Sellable {
		if !validID(l.InventoryID) || l.Units < 1 || l.Units > 99999 || seen[l.InventoryID] {
			return ErrInvalid
		}
		seen[l.InventoryID] = true
		lines = append(lines, contract.RestockLine{InventoryID: l.InventoryID, SellableUnits: l.Units})
	}
	if err := s.Catalog.Restock(ctx, tx, o.ID, lines); err != nil {
		return err
	}
	if err := s.Settlement.FinalizeReturnSettlement(ctx, tx, o.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE delivery_tasks SET state='CLOSED',finished_at=$2 WHERE order_id=$1 AND state='RETURNING'`, o.ID, s.Clock.Now().UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='RETURNED',stock_status='RESTOCKED' WHERE id=$1`, o.ID); err != nil {
		return err
	}
	return s.event(ctx, tx, actor, o.ID, "RETURNED", in.Code)
}
