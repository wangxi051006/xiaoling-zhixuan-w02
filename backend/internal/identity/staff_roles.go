package identity

import (
	"context"
	"errors"
	"net/http"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

// ChangeStaffRole is a privileged domain command, not a public HTTP endpoint.
func (s *Sessions) ChangeStaffRole(ctx context.Context, actor Session, target string, role contract.Role, grant bool, reason, requestID string) error {
	now := s.clock.Now().UTC()
	if actor.Entry != AdminEntry || actor.AccountID == "" || actor.AccountID == target || actor.StationID == "" ||
		actor.MFAAt.IsZero() || actor.MFAAt.After(now) || now.Sub(actor.MFAAt) >= recentMFAWindow {
		return AccessError(http.StatusForbidden)
	}
	if target == "" || len(target) > 128 || (!roleForEntry(AdminEntry, role) && role != contract.RoleRider) || !reasonCodePattern.MatchString(reason) || requestID == "" {
		return ErrRiderInput
	}
	return platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM accounts WHERE station_id=$1 AND id IN ($2,$3) AND account_type='STAFF' ORDER BY id FOR UPDATE`, actor.StationID, actor.AccountID, target)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT true FROM accounts a JOIN role_bindings b ON b.account_id=a.id AND b.role='SECURITY_ADMIN'
			JOIN sessions s ON s.account_id=a.id AND s.entry='admin'
			WHERE a.id=$1 AND a.station_id=$2 AND a.enabled AND a.account_type='STAFF' AND s.csrf_token=$3
			AND s.expires_at>$4 AND s.mfa_at<=$4 AND s.mfa_at>$5 FOR SHARE OF b,s`, actor.AccountID, actor.StationID,
			actor.CSRFToken, now, now.Add(-recentMFAWindow)).Scan(&allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			return AccessError(http.StatusForbidden)
		}
		if err != nil {
			return err
		}
		if len(ids) != 2 {
			return ErrObjectNotFound
		}

		statement, action := `DELETE FROM role_bindings WHERE account_id=$1 AND role=$2`, "staff.role_revoke."
		if grant {
			statement, action = `INSERT INTO role_bindings(account_id,role) VALUES($1,$2) ON CONFLICT DO NOTHING`, "staff.role_grant."
		}
		tag, err := tx.Exec(ctx, statement, target, role)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE account_id=$1`, target); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id,reason_code) VALUES($1,$2,$3,$4,$5,$6)`,
			actor.StationID, actor.AccountID, action+string(role), target, requestID, reason)
		return err
	})
}
