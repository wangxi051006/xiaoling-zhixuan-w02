package identity

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Directory) AssertRider(ctx context.Context, tx pgx.Tx, actorID, stationID string) error {
	var permitted bool
	err := tx.QueryRow(ctx, `SELECT true FROM accounts a JOIN role_bindings b ON b.account_id=a.id AND b.role='RIDER'
		JOIN rider_profiles p ON p.account_id=a.id WHERE a.id=$1 AND a.station_id=$2 AND a.enabled AND p.enabled
		FOR SHARE OF a,b,p`, actorID, stationID).Scan(&permitted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrObjectNotFound
	}
	return err
}

var ErrRiderInput = errors.New("invalid rider update")
var reasonCodePattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

type Rider struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
	Available   bool   `json:"available"`
	Version     int64  `json:"version"`
}

const riderSelect = `SELECT a.id,a.display_name,(a.enabled AND p.enabled),(a.enabled AND p.available),p.version
	FROM rider_profiles p JOIN accounts a ON a.id=p.account_id
	JOIN role_bindings b ON b.account_id=a.id AND b.role='RIDER'
	WHERE a.station_id=$1 AND a.account_type='STAFF'`

func scanRider(row addressScanner) (Rider, error) {
	var rider Rider
	err := row.Scan(&rider.ID, &rider.DisplayName, &rider.Enabled, &rider.Available, &rider.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rider{}, ErrObjectNotFound
	}
	return rider, err
}

func (s *Directory) ListRiders(ctx context.Context, stationID, cursor string, limit int) ([]Rider, error) {
	if limit < 1 || limit > 101 {
		return nil, ErrRiderInput
	}
	rows, err := s.pool.Query(ctx, riderSelect+` AND a.id>$2 ORDER BY a.id LIMIT $3`, stationID, cursor, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Rider, error) { return scanRider(row) })
}

func (s *Directory) GetRider(ctx context.Context, stationID, id string) (Rider, error) {
	return scanRider(s.pool.QueryRow(ctx, riderSelect+` AND a.id=$2`, stationID, id))
}

func (s *Directory) SetRiderEnabled(ctx context.Context, actorID, stationID, id string, enabled bool, version int64, reason, requestID string) (Rider, error) {
	if !reasonCodePattern.MatchString(reason) || version < 1 || version >= maxVersion || requestID == "" {
		return Rider{}, ErrRiderInput
	}
	var out Rider
	err := platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize with session issuance and acquire account locks in a stable order.
		rows, err := tx.Query(ctx, `SELECT id FROM accounts WHERE station_id=$1 AND id IN ($2,$3) ORDER BY id FOR UPDATE`, stationID, actorID, id)
		if err != nil {
			return err
		}
		if _, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return err
		}
		var actorAllowed bool
		err = tx.QueryRow(ctx, `SELECT true FROM role_bindings b JOIN accounts a ON a.id=b.account_id
			WHERE a.id=$1 AND a.station_id=$2 AND a.enabled AND a.account_type='STAFF' AND b.role='OPERATOR' FOR SHARE OF b`, actorID, stationID).Scan(&actorAllowed)
		if errors.Is(err, pgx.ErrNoRows) {
			return AccessError(http.StatusForbidden)
		}
		if err != nil {
			return err
		}
		before, err := scanRider(tx.QueryRow(ctx, riderSelect+` AND a.id=$2 FOR UPDATE OF p FOR SHARE OF b`, stationID, id))
		if err != nil {
			return err
		}
		if before.Version != version {
			return ErrVersionConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE rider_profiles SET enabled=$2,available=available AND $2,version=version+1 WHERE account_id=$1`, id, enabled); err != nil {
			return err
		}
		if !enabled {
			if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE account_id=$1 AND entry='rider'`, id); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id,reason_code)
			VALUES($1,$2,'rider.status',$3,$4,$5)`, stationID, actorID, id, requestID, reason); err != nil {
			return err
		}
		out, err = scanRider(tx.QueryRow(ctx, riderSelect+` AND a.id=$2`, stationID, id))
		return err
	})
	return out, err
}
