package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/settlement"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

var _ settlement.RefundCommands = (*Service)(nil)

func hasRole(s identity.Session, role contract.Role) bool {
	for _, v := range s.Roles {
		if v == role {
			return true
		}
	}
	return false
}
func (s *Service) ApproveRefund(ctx context.Context, tx pgx.Tx, actor identity.Session, id, reason string) error {
	return s.refundCommand(ctx, tx, actor, id, reason, false)
}
func (s *Service) RecordCODPayout(ctx context.Context, tx pgx.Tx, actor identity.Session, id, reference string) error {
	return s.refundCommand(ctx, tx, actor, id, reference, true)
}
func (s *Service) refundCommand(ctx context.Context, tx pgx.Tx, actor identity.Session, id, value string, payout bool) error {
	if !validID(id) || (!payout && !reasonPattern.MatchString(value)) || (payout && !validID(value)) {
		return settlement.ErrInvalid
	}
	if !hasRole(actor, contract.RoleFinance) || actor.Entry != identity.AdminEntry {
		return settlement.ErrNotFound
	}
	// Read identity without a settlement/refund lock, then lock the owning order and re-read.
	var orderID string
	err := tx.QueryRow(ctx, `SELECT f.order_id FROM refunds f JOIN orders o ON o.id=f.order_id WHERE f.id=$1 AND o.station_id=$2 AND f.source='MANUAL'`, id, actor.StationID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return settlement.ErrNotFound
	}
	if err != nil {
		return err
	}
	o, err := lockOrder(ctx, tx, actor.StationID, orderID)
	if errors.Is(err, ErrNotFound) {
		return settlement.ErrNotFound
	}
	if err != nil {
		return err
	}
	if o.Status != contract.OrderStatusDelivered {
		return settlement.ErrInvalidState
	}
	var rechecked string
	err = tx.QueryRow(ctx, `SELECT order_id FROM refunds WHERE id=$1 AND source='MANUAL'`, id).Scan(&rechecked)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && rechecked != orderID {
		return settlement.ErrNotFound
	}
	if err != nil {
		return err
	}
	if payout {
		return s.Settlement.RecordCODPayout(ctx, tx, contract.CODPayout{OrderID: orderID, RefundID: id, FinanceID: actor.AccountID, ReceiptReference: value})
	}
	return s.Settlement.ApproveRefund(ctx, tx, contract.RefundApproval{OrderID: orderID, RefundID: id, FinanceID: actor.AccountID, ReasonCode: value})
}
func (s *Service) requestRefund(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow, reason string) error {
	if !reasonPattern.MatchString(reason) {
		return ErrInvalid
	}
	if o.Status == contract.OrderStatusDelivered {
		if err := s.Settlement.RequestRefund(ctx, tx, contract.RefundRequest{OrderID: o.ID, OperatorID: actor.AccountID, ReasonCode: reason}); err != nil {
			return err
		}
		return s.event(ctx, tx, actor, o.ID, "REFUND_REQUESTED", reason)
	}
	return s.cancel(ctx, tx, actor, o, reason, false)
}
