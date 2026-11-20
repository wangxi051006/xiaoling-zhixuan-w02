package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const SessionCookieName = "__Host-xiaoling-session"

type Session struct {
	AccountID, StationID string
	Entry                Entry
	Roles                []contract.Role
	CSRFToken            string
	ExpiresAt, MFAAt     time.Time
}

var ErrSession = errors.New("session unavailable")

type Sessions struct {
	pool  *pgxpool.Pool
	clock contract.Clock
}

func NewSessions(pool *pgxpool.Pool, clock contract.Clock) *Sessions {
	return &Sessions{pool: pool, clock: clock}
}

func secretToken() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func entryRoles(entry Entry, raw []string) []contract.Role {
	var roles []contract.Role
	for _, value := range raw {
		r := contract.Role(value)
		if roleForEntry(entry, r) {
			roles = append(roles, r)
		}
	}
	return roles
}

func (s *Sessions) Issue(ctx context.Context, accountID string, entry Entry, mfa time.Time) (string, Session, error) {
	return s.issue(ctx, accountID, "", "", "", entry, mfa)
}

func (s *Sessions) issueCustomer(ctx context.Context, phone string) (string, Session, error) {
	if !phonePattern.MatchString(phone) {
		return "", Session{}, ErrSession
	}
	return s.issue(ctx, "", "", "", phone, UserEntry, time.Time{})
}

func (s *Sessions) issue(ctx context.Context, accountID, issuer, subject, phone string, entry Entry, mfa time.Time) (string, Session, error) {
	var token string
	var session Session
	err := platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		token, session, err = s.issueAt(ctx, tx, accountID, issuer, subject, phone, entry, mfa)
		return err
	})
	if err != nil {
		return "", Session{}, err
	}
	return token, session, nil
}

// issueAt applies the same checks inside an existing transaction, so an account
// created there is signed in without a second, separable commit.
func (s *Sessions) issueAt(ctx context.Context, tx pgx.Tx, accountID, issuer, subject, phone string, entry Entry, mfa time.Time) (string, Session, error) {
	return s.issueAtMode(ctx, tx, accountID, issuer, subject, phone, entry, mfa, false)
}

func (s *Sessions) issueAtMode(ctx context.Context, tx pgx.Tx, accountID, issuer, subject, phone string, entry Entry, mfa time.Time, localTest bool) (string, Session, error) {
	now := s.clock.Now().UTC()
	if entry == UserEntry {
		mfa = time.Time{}
	} else if (entry != AdminEntry && entry != RiderEntry) || mfa.IsZero() || mfa.After(now) || now.Sub(mfa) >= recentMFAWindow {
		return "", Session{}, ErrSession
	}
	token := secretToken()
	digest := sha256.Sum256([]byte(token))
	session := Session{AccountID: accountID, Entry: entry, CSRFToken: secretToken(), ExpiresAt: now.Add(8 * time.Hour), MFAAt: mfa}
	var mfaValue any
	if !mfa.IsZero() {
		mfaValue = mfa
	}
	var customer bool
	var roles []string
	err := tx.QueryRow(ctx, `SELECT id, station_id, account_type = 'CUSTOMER',
		ARRAY(SELECT role FROM role_bindings WHERE account_id=a.id ORDER BY role)
		FROM accounts a WHERE enabled AND (CASE WHEN $4<>'' THEN phone=$4 WHEN $2<>'' THEN issuer=$2 AND subject=$3 ELSE id=$1 END)
		AND ($5<>'rider' OR EXISTS(SELECT 1 FROM rider_profiles WHERE account_id=a.id AND enabled))
		FOR UPDATE`, accountID, issuer, subject, phone, entry).Scan(&session.AccountID, &session.StationID, &customer, &roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, ErrSession
	}
	if err != nil {
		return "", Session{}, err
	}
	if isLocalTestAccount(session.AccountID) != localTest || (localTest && session.AccountID != localTestAccountID(entry)) {
		return "", Session{}, ErrSession
	}
	session.Roles = entryRoles(entry, roles)
	if customer != (entry == UserEntry) || len(session.Roles) == 0 {
		return "", Session{}, ErrSession
	}
	_, err = tx.Exec(ctx, `INSERT INTO sessions(token_digest,account_id,entry,csrf_token,expires_at,mfa_at)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(account_id,entry) DO UPDATE SET
		token_digest=EXCLUDED.token_digest, csrf_token=EXCLUDED.csrf_token,
		expires_at=EXCLUDED.expires_at, mfa_at=EXCLUDED.mfa_at`, digest[:], session.AccountID, entry, session.CSRFToken, session.ExpiresAt, mfaValue)
	if err != nil {
		return "", Session{}, err
	}
	return token, session, nil
}

func (s *Sessions) Lookup(ctx context.Context, token string, entry Entry) (Session, error) {
	return s.lookup(ctx, token, entry, false)
}

func (s *Sessions) lookup(ctx context.Context, token string, entry Entry, allowLocalTest bool) (Session, error) {
	if len(token) != 64 {
		return Session{}, ErrSession
	}
	digest := sha256.Sum256([]byte(token))
	state := Session{Entry: entry}
	var roles []string
	var mfa *time.Time
	err := s.pool.QueryRow(ctx, `SELECT a.id,a.station_id,s.csrf_token,s.expires_at,s.mfa_at,
		ARRAY(SELECT role FROM role_bindings WHERE account_id=a.id ORDER BY role)
		FROM sessions s JOIN accounts a ON a.id=s.account_id
		WHERE s.token_digest=$1 AND s.entry=$2 AND s.expires_at>$3 AND a.enabled
		AND (s.entry<>'rider' OR EXISTS(SELECT 1 FROM rider_profiles WHERE account_id=a.id AND enabled))`, digest[:], entry, s.clock.Now().UTC()).
		Scan(&state.AccountID, &state.StationID, &state.CSRFToken, &state.ExpiresAt, &mfa, &roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSession
	}
	if err != nil {
		return Session{}, err
	}
	if isLocalTestAccount(state.AccountID) && (!allowLocalTest || state.AccountID != localTestAccountID(entry)) {
		return Session{}, ErrSession
	}
	state.Roles = entryRoles(entry, roles)
	if len(state.Roles) == 0 {
		return Session{}, ErrSession
	}
	if mfa != nil {
		state.MFAAt = *mfa
	}
	return state, nil
}

func (s *Sessions) Revoke(ctx context.Context, token string, entry Entry) error {
	if len(token) != 64 {
		return ErrSession
	}
	digest := sha256.Sum256([]byte(token))
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_digest=$1 AND entry=$2`, digest[:], entry)
	return err
}

func SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expiresAt})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}
