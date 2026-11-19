package settlement

import (
	"context"
	"errors"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

var ErrInsufficientBalance = errors.New("insufficient balance")

// The caller has already locked or inserted the settlement. The enclosing transaction
// must roll back on any error, including errors returned by the order service afterward.
func changeWallet(ctx context.Context, tx pgx.Tx, money contract.OrderMoney, kind string, delta int64) error {
	var balance int64
	err := tx.QueryRow(ctx, `SELECT balance_fen FROM wallets WHERE station_id=$1 AND actor_id=$2 FOR UPDATE`, money.StationID, money.ActorID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) && kind == "DEBIT" {
		return ErrInsufficientBalance
	}
	if err != nil {
		return err
	}
	if delta < -balance {
		return ErrInsufficientBalance
	}
	if delta > maxMoneyFen-balance {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wallet_entries(station_id,actor_id,order_id,kind,delta_fen)
		VALUES ($1,$2,$3,$4,$5)`, money.StationID, money.ActorID, money.OrderID, kind, delta); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE wallets SET balance_fen=$3 WHERE station_id=$1 AND actor_id=$2`, money.StationID, money.ActorID, balance+delta)
	return err
}
