package identity

import (
	"net/url"
	"testing"
	"time"
)

func TestStaffOIDCConfigAndPKCE(t *testing.T) {
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := StaffOIDCConfig{
		Issuer: "https://idp.example.test", AuthURL: "https://idp.example.test/authorize",
		TokenURL: "https://idp.example.test/token", JWKSURL: "https://idp.example.test/keys",
		ClientID: "staff", ClientSecret: "fixture", MFAContext: "test-mfa",
	}
	clock := &fakeClock{time.Unix(1000, 0)}
	staff, err := NewStaffOIDC(cfg, policy, clock)
	if err != nil {
		t.Fatal(err)
	}
	start, err := staff.Start(RiderEntry)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	q := destination.Query()
	if q.Get("redirect_uri") != "https://rider.example.test/api/v1/auth/staff/callback" || q.Get("code_challenge_method") != "S256" || q.Get("state") != start.State || q.Get("nonce") != start.Nonce || start.Verifier == q.Get("code_challenge") || q.Has("code_verifier") {
		t.Fatal("staff authorization request lacks bound OAuth parameters")
	}
	if _, err := staff.Start(UserEntry); err == nil {
		t.Fatal("staff login started on customer entry")
	}
	cfg.TokenURL = "http://idp.example.test/token"
	if _, err := NewStaffOIDC(cfg, policy, clock); err == nil {
		t.Fatal("non-TLS token endpoint accepted")
	}
}

func TestStaffClaimsFailClosed(t *testing.T) {
	now := time.Unix(1000, 0)
	valid := staffClaims{Nonce: "a", Subject: "sub", ACR: "mfa", AuthTime: now.Add(-time.Minute).Unix(), AuthorizedParty: "staff"}
	if _, err := validateStaffClaims(valid, "a", "mfa", "staff", 2, now); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []staffClaims{
		{Nonce: "b", Subject: "sub", ACR: "mfa", AuthTime: valid.AuthTime, AuthorizedParty: "staff"},
		{Nonce: "a", Subject: "sub", AuthTime: valid.AuthTime, AuthorizedParty: "staff"},
		{Nonce: "a", Subject: "sub", ACR: "mfa", AuthTime: now.Add(-time.Hour).Unix(), AuthorizedParty: "staff"},
		{Nonce: "a", Subject: "sub", ACR: "mfa", AuthTime: valid.AuthTime},
		{Nonce: "a", Subject: "sub", ACR: "mfa", AuthTime: valid.AuthTime, AuthorizedParty: "other"},
	} {
		if _, err := validateStaffClaims(attempt, "a", "mfa", "staff", 2, now); err == nil {
			t.Fatal("invalid claims passed")
		}
	}
}
