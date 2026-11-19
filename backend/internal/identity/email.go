package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	emailTTL            = 10 * time.Minute
	emailMaxAttempts    = 5
	emailResendInterval = 60 * time.Second
	emailPerHour        = 5
	emailSourcePerHour  = 20
	emailMinKeyBytes    = 32
	maxEmailLength      = 254
	maxEmailLocalLength = 64
)

var (
	ErrEmailInput         = errors.New("invalid email verification request")
	ErrInvalidEmailCode   = errors.New("invalid or expired email verification code")
	ErrEmailSend          = errors.New("could not deliver verification email")
	ErrEmailAlreadyOwned  = errors.New("email is already verified by another account")
	ErrRegistrationClosed = errors.New("email registration is closed")
	emailPattern          = regexp.MustCompile(`^[^@[:space:]]{1,64}@[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?([.][a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// EmailPurpose keeps register, login and bind challenges in one table while
// making a code issued for one purpose unusable for another.
type EmailPurpose string

const (
	EmailPurposeRegister = EmailPurpose("REGISTER")
	EmailPurposeLogin    = EmailPurpose("LOGIN")
	EmailPurposeBind     = EmailPurpose("BIND")
)

func (p EmailPurpose) valid() bool {
	return p == EmailPurposeRegister || p == EmailPurposeLogin || p == EmailPurposeBind
}

// EmailChallenge is the server-side state of one verification code. Only a
// keyed digest is stored; the plain code never reaches the database.
type EmailChallenge struct {
	ID, Email     string
	Purpose       EmailPurpose
	AccountID     string
	Digest        [32]byte
	EmailKey      [32]byte
	SourceKey     [32]byte
	ExpiresAt     time.Time
	Attempts      int
	Sent, Consumd bool
	Superseded    bool
}

type EmailChallengeReceipt struct {
	ID          string    `json:"challenge_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	ResendAfter int       `json:"resend_after_seconds"`
}

// VerifiedEmail is emitted only after a code matched and the challenge row is
// locked inside the caller's transaction.
type VerifiedEmail struct {
	Purpose   EmailPurpose
	Email     string
	AccountID string
}

// EmailVerificationSender delivers a one-time code to a dedicated mailbox.
type EmailVerificationSender interface {
	SendVerification(ctx context.Context, to, code, purpose string) error
}

// EmailCodes issues and redeems short-lived email verification codes.
type EmailCodes struct {
	pool   *pgxpool.Pool
	sender EmailVerificationSender
	clock  contract.Clock
	key    []byte
	// AllowRegistration keeps self-service sign-up switchable without closing
	// email login or phone login for existing customers.
	AllowRegistration bool
}

func NewEmailCodes(pool *pgxpool.Pool, sender EmailVerificationSender, clock contract.Clock, key []byte) (*EmailCodes, error) {
	if pool == nil || sender == nil || clock == nil || len(key) < emailMinKeyBytes {
		return nil, errors.New("email codes require a store, sender, clock and at least 32 signing key bytes")
	}
	return &EmailCodes{pool: pool, sender: sender, clock: clock, key: append([]byte(nil), key...)}, nil
}

// keyedDigest is the shared server-keyed MAC used by phone and email codes.
func keyedDigest(key []byte, domain string, fields ...string) (digest [32]byte) {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(domain))
	for _, field := range fields {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(field))
	}
	copy(digest[:], h.Sum(nil))
	return digest
}

// normalizeEmail trims surrounding space and lower-cases the whole address so
// one mailbox always maps to one row. '+' tags and dots are preserved.
func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxEmailLength || !utf8.ValidString(email) {
		return "", ErrEmailInput
	}
	at := strings.LastIndex(email, "@")
	if at < 1 || at != strings.Index(email, "@") || at > maxEmailLocalLength || !emailPattern.MatchString(email) {
		return "", ErrEmailInput
	}
	return email, nil
}

func sourceKeyOf(source string) (string, error) {
	addr, err := netip.ParseAddr(source)
	if err != nil || addr.Zone() != "" {
		return "", ErrEmailInput
	}
	ip := addr.Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String(), nil
	}
	return ip.String(), nil
}

// Request begins a challenge. The HTTP response is identical whether or not the
// address already belongs to an account: an undeliverable purpose still gets a
// challenge row, but it stays unsent and can therefore never verify.
func (s *EmailCodes) Request(ctx context.Context, purpose EmailPurpose, email, accountID, source string) (EmailChallengeReceipt, error) {
	if !purpose.valid() || (purpose == EmailPurposeBind) != (accountID != "") {
		return EmailChallengeReceipt{}, ErrEmailInput
	}
	if purpose == EmailPurposeRegister && !s.AllowRegistration {
		return EmailChallengeReceipt{}, ErrRegistrationClosed
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	sourceIP, err := sourceKeyOf(source)
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	id := hex.EncodeToString(nonce[:])
	number, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return EmailChallengeReceipt{}, errors.New("could not generate email verification code")
	}
	code := fmt.Sprintf("%06d", number.Int64())
	now := s.clock.Now().UTC()
	challenge := EmailChallenge{
		ID: id, Email: normalized, Purpose: purpose, AccountID: accountID,
		ExpiresAt: now.Add(emailTTL),
		Digest:    keyedDigest(s.key, "email-code", id, code),
		EmailKey:  keyedDigest(s.key, "email", normalized),
		SourceKey: keyedDigest(s.key, "source", sourceIP),
	}
	deliver, err := s.begin(ctx, challenge, now)
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	receipt := EmailChallengeReceipt{ID: id, ExpiresAt: challenge.ExpiresAt, ResendAfter: int(emailResendInterval / time.Second)}
	if !deliver {
		return receipt, nil
	}
	if err := s.sender.SendVerification(ctx, normalized, code, string(purpose)); err != nil {
		return EmailChallengeReceipt{}, ErrEmailSend
	}
	if err := s.markSent(ctx, id); err != nil {
		return EmailChallengeReceipt{}, ErrEmailSend
	}
	return receipt, nil
}

// begin enforces email and source windows, retires the previous live code for
// the same purpose and reports whether the code may actually be delivered.
func (s *EmailCodes) begin(ctx context.Context, c EmailChallenge, now time.Time) (bool, error) {
	deliver := false
	err := platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		// Every sender takes locks in email, then source order regardless of outcome.
		const lock = `SELECT pg_advisory_xact_lock($1, hashtext($2))`
		if _, err := tx.Exec(ctx, lock, int32(10201), hex.EncodeToString(c.EmailKey[:])); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, lock, int32(10202), hex.EncodeToString(c.SourceKey[:])); err != nil {
			return err
		}
		var recent, hour, source int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE issued_at > $3), COUNT(*) FROM email_challenges
			WHERE email_key = $1 AND purpose = $2 AND issued_at > $4`,
			c.EmailKey[:], string(c.Purpose), now.Add(-emailResendInterval), now.Add(-time.Hour)).Scan(&recent, &hour); err != nil {
			return err
		}
		if recent != 0 || hour >= emailPerHour {
			return ErrRateLimit
		}
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM email_challenges WHERE source_key = $1 AND issued_at > $2`,
			c.SourceKey[:], now.Add(-time.Hour)).Scan(&source); err != nil {
			return err
		}
		if source >= emailSourcePerHour {
			return ErrRateLimit
		}
		var owned, active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE email = $1)`, c.Email).Scan(&owned); err != nil {
			return err
		}
		switch c.Purpose {
		case EmailPurposeRegister:
			deliver = !owned
		case EmailPurposeLogin:
			deliver = owned
		default:
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id = $1 AND enabled AND email IS NULL)`,
				c.AccountID).Scan(&active); err != nil {
				return err
			}
			deliver = active && !owned
		}
		if _, err := tx.Exec(ctx, `UPDATE email_challenges SET superseded = true
			WHERE email_key = $1 AND purpose = $2 AND sent AND NOT consumed AND NOT superseded`, c.EmailKey[:], string(c.Purpose)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO email_challenges
			(id, purpose, email, email_key, source_key, account_id, code_digest, issued_at, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			c.ID, string(c.Purpose), c.Email, c.EmailKey[:], c.SourceKey[:], nullIfEmpty(c.AccountID), c.Digest[:], now, c.ExpiresAt)
		return err
	})
	return deliver, err
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *EmailCodes) markSent(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE email_challenges SET sent = true WHERE id = $1 AND NOT sent AND NOT superseded AND NOT consumed`, id)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrInvalidEmailCode
	}
	return err
}

// Complete locks the challenge, checks the code and runs apply in the same
// transaction, so a registration cannot exist without consuming its code.
func (s *EmailCodes) Complete(ctx context.Context, id, code string, apply func(context.Context, pgx.Tx, VerifiedEmail) error) (VerifiedEmail, error) {
	if !idPattern.MatchString(id) || apply == nil {
		return VerifiedEmail{}, ErrInvalidEmailCode
	}
	expected := keyedDigest(s.key, "email-code", id, code)
	validForm := codePattern.MatchString(code)
	var out VerifiedEmail
	var invalid bool
	err := platform.InTransaction(ctx, s.pool, func(tx pgx.Tx) error {
		var purpose, email string
		var accountID *string
		var raw []byte
		var expires time.Time
		var attempts int
		var sent, consumed, superseded bool
		err := tx.QueryRow(ctx, `SELECT purpose,email,account_id,code_digest,expires_at,attempts,sent,consumed,superseded
			FROM email_challenges WHERE id = $1 FOR UPDATE`, id).
			Scan(&purpose, &email, &accountID, &raw, &expires, &attempts, &sent, &consumed, &superseded)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidEmailCode
		}
		if err != nil {
			return err
		}
		now := s.clock.Now().UTC()
		if !sent || consumed || superseded || !now.Before(expires) || attempts >= emailMaxAttempts || len(raw) != sha256Size {
			return ErrInvalidEmailCode
		}
		if !validForm || !hmac.Equal(expected[:], raw) {
			// The failed attempt must survive the rejection, so commit the
			// increment and report the error after the transaction.
			invalid = true
			_, err := tx.Exec(ctx, `UPDATE email_challenges SET attempts = attempts + 1 WHERE id = $1`, id)
			return err
		}
		if EmailPurpose(purpose) == EmailPurposeRegister && !s.AllowRegistration {
			return ErrRegistrationClosed
		}
		v := VerifiedEmail{Purpose: EmailPurpose(purpose), Email: email}
		if accountID != nil {
			v.AccountID = *accountID
		}
		if err := apply(ctx, tx, v); err != nil {
			return err
		}
		out = v
		_, err = tx.Exec(ctx, `UPDATE email_challenges SET consumed = true WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return VerifiedEmail{}, err
	}
	if invalid {
		return VerifiedEmail{}, ErrInvalidEmailCode
	}
	return out, nil
}
