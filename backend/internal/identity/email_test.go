package identity

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeEmailKeepsTagsAndDotsButRejectsShape(t *testing.T) {
	valid := map[string]string{
		"  Student.Tag+e2e@Example.COM ": "student.tag+e2e@example.com",
		"A@b.co":                         "a@b.co",
	}
	for in, want := range valid {
		got, err := normalizeEmail(in)
		if err != nil || got != want {
			t.Fatalf("normalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	invalid := []string{
		"", "a@b", "a@@b.com", "a b@c.com", "a@b..com", "@example.com",
		strings.Repeat("a", 65) + "@b.co",
		"a@" + strings.Repeat("b", 250) + ".com",
	}
	for _, in := range invalid {
		if _, err := normalizeEmail(in); !errors.Is(err, ErrEmailInput) {
			t.Fatalf("normalizeEmail(%q) accepted an invalid address", in)
		}
	}
}

func TestKeyedDigestSeparatesDomainsAndInputs(t *testing.T) {
	key := []byte("one-of-your-local-test-fixture-keys")
	code := keyedDigest(key, "email-code", "id-a", "123456")
	if code != keyedDigest(key, "email-code", "id-a", "123456") {
		t.Fatal("digest is not deterministic")
	}
	if code == keyedDigest(key, "phone", "id-a", "123456") || code == keyedDigest(key, "email-code", "id-a", "123457") {
		t.Fatal("digest is not domain or input separated")
	}
	if code == keyedDigest([]byte("another-local-test-fixture-key!!"), "email-code", "id-a", "123456") {
		t.Fatal("digest does not depend on the server key")
	}
}

func TestEmailCodesRejectInvalidPurposeAndClosedRegistration(t *testing.T) {
	service := &EmailCodes{key: []byte("one-of-your-local-test-fixture-keys")}
	if _, err := service.Request(t.Context(), EmailPurpose("RESET"), "student@example.test", "", "127.0.0.1"); !errors.Is(err, ErrEmailInput) {
		t.Fatalf("unknown purpose accepted: %v", err)
	}
	if _, err := service.Request(t.Context(), EmailPurposeBind, "student@example.test", "", "127.0.0.1"); !errors.Is(err, ErrEmailInput) {
		t.Fatalf("bind without an account accepted: %v", err)
	}
	if _, err := service.Request(t.Context(), EmailPurposeLogin, "student@example.test", "account", "127.0.0.1"); !errors.Is(err, ErrEmailInput) {
		t.Fatalf("login with an account accepted: %v", err)
	}
	// Registration stays closed until the switch is on, and that check runs
	// before any database access.
	if _, err := service.Request(t.Context(), EmailPurposeRegister, "student@example.test", "", "127.0.0.1"); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed registration accepted: %v", err)
	}
}

func TestEmailPurposeValues(t *testing.T) {
	for _, purpose := range []EmailPurpose{EmailPurposeRegister, EmailPurposeLogin, EmailPurposeBind} {
		if !purpose.valid() {
			t.Fatalf("%q should be valid", purpose)
		}
	}
	if EmailPurpose("").valid() {
		t.Fatal("empty purpose is valid")
	}
}
