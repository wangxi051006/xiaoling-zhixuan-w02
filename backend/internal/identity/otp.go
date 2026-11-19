package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"regexp"
	"time"

	"campus.local/xiaoling/internal/contract"
)

const otpTTL = 5 * time.Minute
const otpMaxAttempts = 5

var (
	ErrInvalidOTP = errors.New("invalid or expired verification code")
	ErrOTPSend    = errors.New("could not deliver verification code")
	ErrRateLimit  = errors.New("too many verification requests")
	ErrOTPInput   = errors.New("invalid verification request")
	phonePattern  = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	codePattern   = regexp.MustCompile(`^[0-9]{6}$`)
	idPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type Challenge struct {
	ID, Phone      string
	Digest         [32]byte
	PhoneKey       [32]byte
	SourceKey      [32]byte
	ExpiresAt      time.Time
	Attempts       int
	Sent, Consumed bool
}

type ChallengeReceipt struct {
	ID        string    `json:"challenge_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OTPStore interface {
	// Begin enforces phone and source limits atomically, including undelivered attempts.
	Begin(ctx context.Context, challenge Challenge, now time.Time) error
	MarkSent(ctx context.Context, id string) error
	// Verify persists failures, and compares and consumes successes under the same lock.
	Verify(ctx context.Context, id string, now time.Time, maxAttempts int, compare func([32]byte) bool) (phone string, err error)
}

type SMSSender interface {
	Send(ctx context.Context, phone, code string) error
}

type OTP struct {
	store  OTPStore
	sender SMSSender
	clock  contract.Clock
	key    []byte
}

func NewOTP(store OTPStore, sender SMSSender, clock contract.Clock, key []byte) (*OTP, error) {
	if store == nil || sender == nil || clock == nil || len(key) < 32 {
		return nil, errors.New("OTP requires a store, sender, clock and at least 32 signing key bytes")
	}
	return &OTP{store: store, sender: sender, clock: clock, key: append([]byte(nil), key...)}, nil
}

func (s *OTP) Request(ctx context.Context, phone, source string) (ChallengeReceipt, error) {
	phone = normalizePhone(phone)
	addr, err := netip.ParseAddr(source)
	if err != nil || addr.Zone() != "" || !phonePattern.MatchString(phone) {
		return ChallengeReceipt{}, ErrOTPInput
	}
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	id := hex.EncodeToString(nonce[:])
	codeNumber, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return ChallengeReceipt{}, errors.New("could not generate OTP")
	}
	code := fmt.Sprintf("%06d", codeNumber.Int64())
	now := s.clock.Now().UTC()
	sourceIP := addr.Unmap()
	sourceKey := sourceIP.String()
	if sourceIP.Is6() {
		sourceKey = netip.PrefixFrom(sourceIP, 64).Masked().String()
	}
	challenge := Challenge{
		ID: id, Phone: phone, ExpiresAt: now.Add(otpTTL),
		Digest: s.mac("code", id, code), PhoneKey: s.mac("phone", phone), SourceKey: s.mac("source", sourceKey),
	}
	if err := s.store.Begin(ctx, challenge, now); err != nil {
		return ChallengeReceipt{}, err
	}
	if err := s.sender.Send(ctx, phone, code); err != nil {
		return ChallengeReceipt{}, ErrOTPSend
	}
	if err := s.store.MarkSent(ctx, id); err != nil {
		return ChallengeReceipt{}, err
	}
	return ChallengeReceipt{ID: id, ExpiresAt: challenge.ExpiresAt}, nil
}

func (s *OTP) Verify(ctx context.Context, id, code string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", ErrInvalidOTP
	}
	expected := s.mac("code", id, code)
	validForm := codePattern.MatchString(code)
	return s.store.Verify(ctx, id, s.clock.Now().UTC(), otpMaxAttempts, func(digest [32]byte) bool {
		matches := hmac.Equal(expected[:], digest[:])
		return validForm && matches
	})
}

func (s *OTP) mac(domain string, fields ...string) (digest [32]byte) {
	return keyedDigest(s.key, domain, fields...)
}
