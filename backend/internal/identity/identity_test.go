package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fixtureOTP struct {
	saved    Challenge
	lastCode string
	failSend bool
}

func (f *fixtureOTP) Begin(_ context.Context, c Challenge, _ time.Time) error {
	f.saved = c
	return nil
}
func (f *fixtureOTP) MarkSent(_ context.Context, id string) error {
	if id != f.saved.ID {
		return errors.New("unexpected id")
	}
	f.saved.Sent = true
	return nil
}
func (f *fixtureOTP) Verify(_ context.Context, id string, now time.Time, max int, compare func([32]byte) bool) (string, error) {
	c := &f.saved
	if c.ID != id || !c.Sent || c.Consumed || !now.Before(c.ExpiresAt) || c.Attempts >= max {
		return "", ErrInvalidOTP
	}
	c.Attempts++
	if !compare(c.Digest) {
		return "", ErrInvalidOTP
	}
	c.Consumed = true
	return c.Phone, nil
}
func (f *fixtureOTP) Send(_ context.Context, _, code string) error {
	f.lastCode = code
	if f.failSend {
		return errors.New("provider credential must not escape")
	}
	return nil
}

func TestOTPIsOneTimeAndNotStoredInClear(t *testing.T) {
	f := &fixtureOTP{}
	c := &fakeClock{time.Unix(100, 0)}
	s, err := NewOTP(f, f, c, []byte("one-of-your-local-test-fixture-keys"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Request(t.Context(), "+8613800000000", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.lastCode) != 6 || f.saved.Digest == ([32]byte{}) || r.ID == "" || !r.ExpiresAt.After(c.Now()) {
		t.Fatalf("invalid challenge metadata")
	}
	phone, err := s.Verify(t.Context(), r.ID, f.lastCode)
	if err != nil || phone != "+8613800000000" {
		t.Fatalf("valid code failed: %v", err)
	}
	if _, err := s.Verify(t.Context(), r.ID, f.lastCode); !errors.Is(err, ErrInvalidOTP) {
		t.Fatal("replayed OTP accepted")
	}
}

func TestOTPAttemptExpiryAndDeliveryFailure(t *testing.T) {
	for _, scenario := range []string{"attempts", "expiry", "send failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := &fixtureOTP{failSend: scenario == "send failure"}
			c := &fakeClock{time.Unix(100, 0)}
			s, err := NewOTP(f, f, c, []byte("one-of-your-local-test-fixture-keys"))
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Request(t.Context(), "+8613800000000", "127.0.0.1")
			if scenario == "send failure" {
				if !errors.Is(err, ErrOTPSend) || f.saved.Sent {
					t.Fatal("send failure did not fail closed")
				}
				r.ID = f.saved.ID
			} else if err != nil {
				t.Fatal(err)
			}
			if scenario == "expiry" {
				c.now = r.ExpiresAt
			}
			if scenario == "attempts" {
				wrong := "000000"
				if wrong == f.lastCode {
					wrong = "000001"
				}
				for range 5 {
					if _, err := s.Verify(t.Context(), r.ID, wrong); !errors.Is(err, ErrInvalidOTP) {
						t.Fatal("wrong OTP accepted")
					}
				}
			}
			if _, err := s.Verify(t.Context(), r.ID, f.lastCode); !errors.Is(err, ErrInvalidOTP) {
				t.Fatalf("invalid challenge accepted: %v", err)
			}
		})
	}
}
