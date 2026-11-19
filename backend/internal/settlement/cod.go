package settlement

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

var (
	ErrPaymentMethodDisabled = errors.New("payment method disabled")
	ErrInvalid               = errors.New("invalid settlement request")
	ErrInvalidState          = errors.New("invalid settlement state")
	ErrNotFound              = errors.New("settlement not found")
)

const maxMoneyFen = 1<<53 - 1

type Store struct {
	Policy PaymentPolicy
}

type settlementRow struct {
	contract.OrderMoney
	state contract.SettlementStatus
}

func validID(id string) bool {
	return utf8.ValidString(id) && utf8.RuneCountInString(id) <= 128 && strings.TrimSpace(id) != "" && !strings.ContainsRune(id, 0)
}

func lockSettlement(ctx context.Context, tx pgx.Tx, id string) (settlementRow, error) {
	r := settlementRow{OrderMoney: contract.OrderMoney{OrderID: id}}
	if tx == nil {
		return r, errNotReady
	}
	err := tx.QueryRow(ctx, `SELECT actor_id,station_id,method,amount_fen,state FROM settlements WHERE order_id=$1 FOR UPDATE`, id).
		Scan(&r.ActorID, &r.StationID, &r.PaymentMethod, &r.TotalFen, &r.state)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

func (s Store) CreateSettlement(ctx context.Context, tx pgx.Tx, money contract.OrderMoney) error {
	if tx == nil {
		return errNotReady
	}
	if err := s.Policy.Check(money.PaymentMethod); err != nil {
		return err
	}
	if !validID(money.OrderID) || !validID(money.ActorID) || !validID(money.StationID) || money.TotalFen < 0 || money.TotalFen > maxMoneyFen {
		return ErrInvalid
	}
	initial := contract.SettlementStatusCodUncollected
	if money.PaymentMethod == contract.PaymentMethodBalance {
		initial = contract.SettlementStatusBalancePaid
	}
	insert, err := tx.Exec(ctx, `INSERT INTO settlements(order_id,actor_id,station_id,method,amount_fen,state)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, money.OrderID, money.ActorID, money.StationID,
		money.PaymentMethod, money.TotalFen, initial)
	if err != nil {
		return err
	}
	if insert.RowsAffected() == 1 {
		if money.PaymentMethod == contract.PaymentMethodBalance {
			return changeWallet(ctx, tx, money, "DEBIT", -money.TotalFen)
		}
		return nil
	}
	r, err := lockSettlement(ctx, tx, money.OrderID)
	if err != nil {
		return err
	}
	if r.OrderMoney != money || r.state == contract.SettlementStatusVoid || r.state == contract.SettlementStatusRefunded || r.state == contract.SettlementStatusRefundPending {
		return ErrInvalidState
	}
	return nil
}

// The caller must hold the order lock and validate the rider's assignment before collecting.
func (s Store) RecordCODCollection(ctx context.Context, tx pgx.Tx, orderID, riderID string) error {
	if !validID(orderID) || !validID(riderID) {
		return ErrInvalid
	}
	r, err := lockSettlement(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if r.state == contract.SettlementStatusCodCollected {
		var existingRider string
		if err := tx.QueryRow(ctx, `SELECT rider_id FROM cod_receipts WHERE order_id=$1`, orderID).Scan(&existingRider); err != nil {
			return err
		}
		if existingRider == riderID {
			return nil
		}
		return ErrInvalidState
	}
	if r.state != contract.SettlementStatusCodUncollected {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cod_receipts(order_id,rider_id,amount_fen) VALUES ($1,$2,$3)`, orderID, riderID, r.TotalFen); err != nil {
		return err
	}
	return updateState(ctx, tx, orderID, contract.SettlementStatusCodCollected)
}

func updateState(ctx context.Context, tx pgx.Tx, orderID string, state contract.SettlementStatus) error {
	_, err := tx.Exec(ctx, `UPDATE settlements SET state=$2 WHERE order_id=$1`, orderID, state)
	return err
}

type PaymentPolicy struct {
	CODEnabled     bool
	BalanceEnabled bool
}

type PaymentOption struct {
	Code    contract.PaymentMethod `json:"code"`
	Enabled bool                   `json:"enabled"`
	Label   string                 `json:"label"`
}

func (p PaymentPolicy) Check(method contract.PaymentMethod) error {
	if (method == contract.PaymentMethodCod && p.CODEnabled) || (method == contract.PaymentMethodBalance && p.BalanceEnabled) {
		return nil
	}
	return ErrPaymentMethodDisabled
}

func (p PaymentPolicy) Methods() []PaymentOption {
	options := []PaymentOption{
		{Code: contract.PaymentMethodCod, Label: "货到付款"},
		{Code: contract.PaymentMethodBalance, Label: "余额支付"},
		{Code: contract.PaymentMethodWechat, Label: "微信支付（待接入）"},
	}
	for i := range options {
		options[i].Enabled = p.Check(options[i].Code) == nil
	}
	return options
}
