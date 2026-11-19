package settlement

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

var (
	_          contract.SettlementPort = Store{}
	reasonCode                         = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)
)

type refundRow struct {
	id, source, operator, requestedReason, finance, approvalReason string
}

// The caller holds the settlement lock. Every mutation follows order -> settlement -> wallet.
func getRefund(ctx context.Context, tx pgx.Tx, orderID string) (r refundRow, err error) {
	err = tx.QueryRow(ctx, `SELECT id,source,coalesce(operator_id,''),coalesce(request_reason,''),
		coalesce(finance_id,''),coalesce(approval_reason,'') FROM refunds WHERE order_id=$1`, orderID).
		Scan(&r.id, &r.source, &r.operator, &r.requestedReason, &r.finance, &r.approvalReason)
	return r, err
}

func (s Store) CancelSettlement(ctx context.Context, tx pgx.Tx, orderID string) error {
	return reverseUnfulfilled(ctx, tx, orderID)
}

func (s Store) FinalizeReturnSettlement(ctx context.Context, tx pgx.Tx, orderID string) error {
	return reverseUnfulfilled(ctx, tx, orderID)
}

// Called after the order service validates a cancellation or accepted return under its order lock.
// Historical reversals do not consult the current payment switches.
func reverseUnfulfilled(ctx context.Context, tx pgx.Tx, orderID string) error {
	if !validID(orderID) {
		return ErrInvalid
	}
	r, err := lockSettlement(ctx, tx, orderID)
	if err != nil {
		return err
	}
	existing, err := getRefund(ctx, tx, orderID)
	if err == nil {
		if existing.source == "UNFULFILLED" && (r.state == contract.SettlementStatusVoid || r.state == contract.SettlementStatusRefunded) {
			return nil
		}
		return ErrInvalidState
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if r.state != contract.SettlementStatusCodUncollected && r.state != contract.SettlementStatusBalancePaid {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `INSERT INTO refunds(order_id,source) VALUES ($1,'UNFULFILLED')`, orderID); err != nil {
		return err
	}
	if r.state == contract.SettlementStatusCodUncollected {
		return updateState(ctx, tx, orderID, contract.SettlementStatusVoid)
	}
	return refundWallet(ctx, tx, r.OrderMoney)
}

func refundWallet(ctx context.Context, tx pgx.Tx, money contract.OrderMoney) error {
	if err := changeWallet(ctx, tx, money, "REFUND", money.TotalFen); err != nil {
		return err
	}
	return updateState(ctx, tx, money.OrderID, contract.SettlementStatusRefunded)
}

// Role, station, and order eligibility checks belong to the caller holding the order lock.
func (s Store) RequestRefund(ctx context.Context, tx pgx.Tx, in contract.RefundRequest) error {
	if !validID(in.OrderID) || !validID(in.OperatorID) || !reasonCode.MatchString(in.ReasonCode) {
		return ErrInvalid
	}
	settle, err := lockSettlement(ctx, tx, in.OrderID)
	if err != nil {
		return err
	}
	r, err := getRefund(ctx, tx, in.OrderID)
	if err == nil {
		if r.source == "MANUAL" && r.operator == in.OperatorID && r.requestedReason == in.ReasonCode {
			return nil
		}
		return ErrInvalidState
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if settle.state != contract.SettlementStatusCodCollected && settle.state != contract.SettlementStatusBalancePaid {
		return ErrInvalidState
	}
	_, err = tx.Exec(ctx, `INSERT INTO refunds(order_id,source,operator_id,request_reason) VALUES ($1,'MANUAL',$2,$3)`,
		in.OrderID, in.OperatorID, in.ReasonCode)
	return err
}

func (s Store) ApproveRefund(ctx context.Context, tx pgx.Tx, in contract.RefundApproval) error {
	if !validID(in.OrderID) || !validID(in.RefundID) || !validID(in.FinanceID) || !reasonCode.MatchString(in.ReasonCode) {
		return ErrInvalid
	}
	settle, err := lockSettlement(ctx, tx, in.OrderID)
	if err != nil {
		return err
	}
	r, err := getRefund(ctx, tx, in.OrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.id != in.RefundID || r.source != "MANUAL" || r.operator == in.FinanceID {
		return ErrInvalidState
	}
	if r.finance != "" {
		if r.finance == in.FinanceID && r.approvalReason == in.ReasonCode {
			return nil
		}
		return ErrInvalidState
	}
	if settle.state != contract.SettlementStatusCodCollected && settle.state != contract.SettlementStatusBalancePaid {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `UPDATE refunds SET finance_id=$2,approval_reason=$3,approved_at=now() WHERE id=$1`, r.id, in.FinanceID, in.ReasonCode); err != nil {
		return err
	}
	if settle.PaymentMethod == contract.PaymentMethodBalance {
		return refundWallet(ctx, tx, settle.OrderMoney)
	}
	return updateState(ctx, tx, in.OrderID, contract.SettlementStatusRefundPending)
}

func (s Store) RecordCODPayout(ctx context.Context, tx pgx.Tx, in contract.CODPayout) error {
	if !validID(in.OrderID) || !validID(in.RefundID) || !validID(in.FinanceID) || !validID(in.ReceiptReference) || strings.TrimSpace(in.ReceiptReference) != in.ReceiptReference {
		return ErrInvalid
	}
	settle, err := lockSettlement(ctx, tx, in.OrderID)
	if err != nil {
		return err
	}
	r, err := getRefund(ctx, tx, in.OrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.id != in.RefundID || r.source != "MANUAL" || r.finance == "" || r.operator == in.FinanceID || settle.PaymentMethod != contract.PaymentMethodCod {
		return ErrInvalidState
	}
	if settle.state == contract.SettlementStatusRefunded {
		var finance, reference string
		if err := tx.QueryRow(ctx, `SELECT finance_id,receipt_reference FROM cod_refund_payouts WHERE refund_id=$1`, r.id).Scan(&finance, &reference); err != nil {
			return err
		}
		if finance == in.FinanceID && reference == in.ReceiptReference {
			return nil
		}
		return ErrInvalidState
	}
	if settle.state != contract.SettlementStatusRefundPending {
		return ErrInvalidState
	}
	insert, err := tx.Exec(ctx, `INSERT INTO cod_refund_payouts(refund_id,finance_id,receipt_reference)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, r.id, in.FinanceID, in.ReceiptReference)
	if err != nil {
		return err
	}
	if insert.RowsAffected() != 1 {
		return ErrInvalidState
	}
	return updateState(ctx, tx, in.OrderID, contract.SettlementStatusRefunded)
}
