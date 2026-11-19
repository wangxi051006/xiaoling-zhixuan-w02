package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"

	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

var (
	ErrProvisionInput     = errors.New("invalid rider provisioning request")
	ErrStaffIdentityBound = errors.New("staff identity is already bound")
)

// RiderProvision is the trusted identity an administrator has independently
// verified with the staff IdP. Nothing else may authorize a rider.
type RiderProvision struct {
	Issuer      string `json:"issuer"`
	Subject     string `json:"subject"`
	DisplayName string `json:"display_name"`
	ReasonCode  string `json:"reason_code"`
}

func (p RiderProvision) validated() (RiderProvision, error) {
	issuer, issuerOK := trimText(p.Issuer, 255)
	subject, subjectOK := trimText(p.Subject, 255)
	name, nameOK := trimText(p.DisplayName, 60)
	if !issuerOK || !subjectOK || !nameOK || !reasonCodePattern.MatchString(p.ReasonCode) {
		return RiderProvision{}, ErrProvisionInput
	}
	p.Issuer, p.Subject, p.DisplayName = issuer, subject, name
	return p, nil
}

// ProvisionRider is a privileged domain command, not a public endpoint. Only a
// SECURITY_ADMIN acting through the admin entry with recent MFA may call it. The
// staff account, the single RIDER grant, the disabled rider profile and the audit
// row commit together; a rider starts unable to take work.
func (s *Sessions) ProvisionRider(ctx context.Context, actor Session, in RiderProvision, requestID string) (Rider, error) {
	now := s.clock.Now().UTC()
	if actor.Entry != AdminEntry || actor.AccountID == "" || actor.StationID == "" ||
		actor.MFAAt.IsZero() || actor.MFAAt.After(now) || now.Sub(actor.MFAAt) >= recentMFAWindow {
		return Rider{}, AccessError(http.StatusForbidden)
	}
	in, err := in.validated()
	if err != nil {
		return Rider{}, err
	}
	if requestID == "" {
		return Rider{}, ErrProvisionInput
	}
	var out Rider
	err = platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT true FROM accounts a
			JOIN role_bindings b ON b.account_id = a.id AND b.role = 'SECURITY_ADMIN'
			JOIN sessions s ON s.account_id = a.id AND s.entry = 'admin'
			WHERE a.id = $1 AND a.station_id = $2 AND a.enabled AND a.account_type = 'STAFF'
			AND s.csrf_token = $3 AND s.expires_at > $4 AND s.mfa_at <= $4 AND s.mfa_at > $5
			FOR SHARE OF b,s`, actor.AccountID, actor.StationID, actor.CSRFToken, now, now.Add(-recentMFAWindow)).Scan(&allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			return AccessError(http.StatusForbidden)
		}
		if err != nil {
			return err
		}
		var raw [16]byte
		_, _ = rand.Read(raw[:])
		id := hex.EncodeToString(raw[:])
		tag, err := tx.Exec(ctx, `INSERT INTO accounts (id, station_id, issuer, subject, display_name, enabled)
			VALUES ($1,$2,$3,$4,$5,true) ON CONFLICT (issuer, subject) DO NOTHING`, id, actor.StationID, in.Issuer, in.Subject, in.DisplayName)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrStaffIdentityBound
		}
		if _, err := tx.Exec(ctx, `INSERT INTO role_bindings (account_id, role) VALUES ($1,'RIDER')`, id); err != nil {
			return err
		}
		// A new rider cannot take work until an operator enables the profile.
		if _, err := tx.Exec(ctx, `INSERT INTO rider_profiles (account_id, enabled, available) VALUES ($1,false,false)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (station_id, actor_id, action, object_id, request_id, reason_code)
			VALUES ($1,$2,'rider.provision',$3,$4,$5)`, actor.StationID, actor.AccountID, id, requestID, in.ReasonCode); err != nil {
			return err
		}
		out, err = scanRider(tx.QueryRow(ctx, riderSelect+` AND a.id = $2`, actor.StationID, id))
		return err
	})
	if err != nil {
		return Rider{}, err
	}
	return out, nil
}
