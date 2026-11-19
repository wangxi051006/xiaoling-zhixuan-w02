package identity

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

// RiderAvailability is the owning-domain read used while claiming a task.
func (s *Directory) RiderAvailability(ctx context.Context, tx pgx.Tx, actorID, stationID string) (bool, error) {
	if tx == nil {
		return false, ErrRiderInput
	}
	if err := s.AssertRider(ctx, tx, actorID, stationID); err != nil {
		return false, err
	}
	var available bool
	err := tx.QueryRow(ctx, `SELECT available FROM rider_profiles WHERE account_id=$1`, actorID).Scan(&available)
	return available, err
}

// SetRiderAvailability uses the fulfillment caller's transaction. Going offline
// does not revoke a session or prevent completion of an already assigned task.
func (s *Directory) SetRiderAvailability(ctx context.Context, tx pgx.Tx, actorID, stationID string, available bool) error {
	if tx == nil {
		return ErrRiderInput
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT true FROM accounts a JOIN role_bindings b ON b.account_id=a.id AND b.role='RIDER'
 WHERE a.id=$1 AND a.station_id=$2 AND a.enabled FOR SHARE OF a,b`, actorID, stationID).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrObjectNotFound
	}
	if err != nil {
		return err
	}
	// Avoid a shared profile lock before the update: concurrent writers would
	// both need to upgrade it. Account/role locks still serialize with disabling.
	result, err := tx.Exec(ctx, `UPDATE rider_profiles SET available=$2,version=version+1 WHERE account_id=$1 AND enabled`, actorID, available)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrObjectNotFound
	}
	return nil
}
