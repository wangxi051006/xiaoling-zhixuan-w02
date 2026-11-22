package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// MerchantID obtains and holds the actor's shop assignment for the transaction.
func MerchantID(ctx context.Context, tx pgx.Tx, actor Session) (string, error) {
	if actor.Entry != MerchantEntry {
		return "", ErrObjectNotFound
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT m.merchant_id FROM merchant_staff m
		JOIN accounts a ON a.id=m.account_id AND a.enabled AND a.account_type='STAFF'
		JOIN role_bindings r ON r.account_id=a.id AND r.role='MERCHANT'
		JOIN sessions s ON s.account_id=a.id AND s.entry='merchant' AND s.csrf_token=$3
		WHERE m.account_id=$1 AND m.station_id=$2 FOR SHARE OF m,a,r,s`,
		actor.AccountID, actor.StationID, actor.CSRFToken).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrObjectNotFound
	}
	return id, err
}
