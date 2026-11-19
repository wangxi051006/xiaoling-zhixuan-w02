package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AccountEmail reports the verified mailbox of one account, if any.
func (s *Directory) AccountEmail(ctx context.Context, accountID string) (*string, error) {
	var email *string
	err := s.pool.QueryRow(ctx, `SELECT email FROM accounts WHERE id = $1`, accountID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrObjectNotFound
	}
	return email, err
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// RegisterEmail creates the only CUSTOMER row for a freshly verified address and
// issues its user session inside the caller's transaction.
func (s *Sessions) RegisterEmail(ctx context.Context, tx pgx.Tx, v VerifiedEmail) (string, Session, error) {
	if v.Purpose != EmailPurposeRegister || v.AccountID != "" {
		return "", Session{}, ErrEmailInput
	}
	email, err := normalizeEmail(v.Email)
	if err != nil {
		return "", Session{}, ErrEmailInput
	}
	var station string
	if err := tx.QueryRow(ctx, `SELECT id FROM stations`).Scan(&station); err != nil {
		return "", Session{}, err
	}
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE email = $1)`, email).Scan(&owned); err != nil {
		return "", Session{}, err
	}
	if owned {
		return "", Session{}, ErrEmailAlreadyOwned
	}
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	id := hex.EncodeToString(raw[:])
	now := s.clock.Now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO accounts (id, station_id, email, email_verified_at, enabled)
		VALUES ($1,$2,$3,$4,true)`, id, station, email, now)
	if uniqueViolation(err) {
		return "", Session{}, ErrEmailAlreadyOwned
	}
	if err != nil {
		return "", Session{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO role_bindings (account_id, role) VALUES ($1,'CUSTOMER')`, id); err != nil {
		return "", Session{}, err
	}
	return s.issueAt(ctx, tx, id, "", "", "", UserEntry, time.Time{})
}

// LoginEmail signs an existing verified mailbox in. A code is only issued for an
// owned address, so a miss here is a generic rejection.
func (s *Sessions) LoginEmail(ctx context.Context, tx pgx.Tx, v VerifiedEmail) (string, Session, error) {
	if v.Purpose != EmailPurposeLogin {
		return "", Session{}, ErrEmailInput
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE email = $1 AND account_type = 'CUSTOMER' AND enabled`, v.Email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, ErrInvalidEmailCode
	}
	if err != nil {
		return "", Session{}, err
	}
	return s.issueAt(ctx, tx, id, "", "", "", UserEntry, time.Time{})
}

// BindEmail attaches a verified address to the signed-in customer's own account.
func (s *Sessions) BindEmail(ctx context.Context, tx pgx.Tx, current Session, v VerifiedEmail) error {
	if v.Purpose != EmailPurposeBind || v.AccountID == "" || v.AccountID != current.AccountID || current.Entry != UserEntry || current.AccountID == "" {
		return ErrEmailInput
	}
	email, err := normalizeEmail(v.Email)
	if err != nil {
		return ErrEmailInput
	}
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE email = $1 AND id <> $2)`, email, current.AccountID).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return ErrEmailAlreadyOwned
	}
	tag, err := tx.Exec(ctx, `UPDATE accounts SET email = $2, email_verified_at = $3
		WHERE id = $1 AND account_type = 'CUSTOMER' AND email IS NULL`, current.AccountID, email, s.clock.Now().UTC())
	if uniqueViolation(err) {
		return ErrEmailAlreadyOwned
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrEmailAlreadyOwned
	}
	return nil
}
