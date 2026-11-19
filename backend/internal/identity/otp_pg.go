package identity

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresOTPStore struct{ pool *pgxpool.Pool }

var _ OTPStore = (*PostgresOTPStore)(nil)

func NewPostgresOTPStore(pool *pgxpool.Pool) *PostgresOTPStore {
	return &PostgresOTPStore{pool: pool}
}

func (s *PostgresOTPStore) Begin(ctx context.Context, c Challenge, now time.Time) error {
	return platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		// All senders acquire locks in phone, then source order, independent of lookup outcome.
		const lock = `SELECT pg_advisory_xact_lock($1, hashtext($2))`
		if _, err := tx.Exec(ctx, lock, int32(10101), hex.EncodeToString(c.PhoneKey[:])); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, lock, int32(10102), hex.EncodeToString(c.SourceKey[:])); err != nil {
			return err
		}
		var recent, hour, source int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE issued_at > $2), COUNT(*)
			FROM otp_challenges WHERE phone_key = $1 AND issued_at > $3`, c.PhoneKey[:], now.Add(-time.Minute), now.Add(-time.Hour)).Scan(&recent, &hour); err != nil {
			return err
		}
		if recent != 0 || hour >= 5 {
			return ErrRateLimit
		}
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM otp_challenges WHERE source_key = $1 AND issued_at > $2`, c.SourceKey[:], now.Add(-time.Hour)).Scan(&source); err != nil {
			return err
		}
		if source >= 20 {
			return ErrRateLimit
		}
		_, err := tx.Exec(ctx, `INSERT INTO otp_challenges (id, phone, phone_key, source_key, code_digest, issued_at, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, c.ID, c.Phone, c.PhoneKey[:], c.SourceKey[:], c.Digest[:], now, c.ExpiresAt)
		return err
	})
}

func (s *PostgresOTPStore) MarkSent(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE otp_challenges SET sent = true WHERE id = $1 AND NOT sent`, id)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrInvalidOTP
	}
	return err
}

func (s *PostgresOTPStore) Verify(ctx context.Context, id string, now time.Time, maxAttempts int, compare func([32]byte) bool) (string, error) {
	if maxAttempts < 1 || maxAttempts > otpMaxAttempts || compare == nil {
		return "", ErrInvalidOTP
	}
	var phone string
	var accepted bool
	err := platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		var raw []byte
		var expires time.Time
		var attempts int
		var sent, consumed bool
		err := tx.QueryRow(ctx, `SELECT phone,code_digest,expires_at,attempts,sent,consumed
			FROM otp_challenges WHERE id = $1 FOR UPDATE`, id).Scan(&phone, &raw, &expires, &attempts, &sent, &consumed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !sent || consumed || !now.Before(expires) || attempts >= maxAttempts || len(raw) != sha256Size {
			return nil
		}
		accepted = compare([sha256Size]byte(raw))
		_, err = tx.Exec(ctx, `UPDATE otp_challenges SET attempts = attempts + 1, consumed = $2 WHERE id = $1`, id, accepted)
		return err
	})
	if err != nil {
		return "", err
	}
	if !accepted {
		return "", ErrInvalidOTP
	}
	return phone, nil
}

const sha256Size = 32
