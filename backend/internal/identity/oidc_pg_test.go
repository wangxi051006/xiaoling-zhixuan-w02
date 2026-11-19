//go:build integration

package identity

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"campus.local/xiaoling/internal/platform"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestOIDCVerifiesTokenAndConsumesBoundState(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	clock := &fakeClock{time.Unix(1780000000, 0)}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwk := jose.JSONWebKey{Key: key.Public(), KeyID: "fixture", Algorithm: "RS256", Use: "sig"}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "fixture"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	type reply struct{ raw, verifier, redirect string }
	var response atomic.Pointer[reply]
	var exchanges atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
		case "/token":
			exchanges.Add(1)
			p := response.Load()
			id, secret, basic := r.BasicAuth()
			if p == nil || !basic || id != "staff" || secret != "fixture" || r.FormValue("code") != "fixture-code" || r.FormValue("code_verifier") != p.verifier || r.FormValue("redirect_uri") != p.redirect {
				http.Error(w, "exchange denied", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture", "token_type": "Bearer", "id_token": p.raw})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	staff, err := NewStaffOIDC(StaffOIDCConfig{Issuer: srv.URL, AuthURL: srv.URL + "/auth", TokenURL: srv.URL + "/token", JWKSURL: srv.URL + "/keys", ClientID: "staff", ClientSecret: "fixture", MFAContext: "test-mfa"}, policy, clock, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Fixture');`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts(id,station_id,issuer,subject) VALUES ('staff-id','s',$1,'sub')`, srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_bindings(account_id,role) VALUES ('staff-id','FINANCE')`); err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(pool, clock)
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	wrongSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: wrongKey}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, issuer, audience, subject, acr string
		invalidSignature                     bool
	}{
		{"bad issuer", "https://other.example.test", "staff", "sub", "test-mfa", false},
		{"bad audience", srv.URL, "other", "sub", "test-mfa", false},
		{"bad signature", srv.URL, "staff", "sub", "test-mfa", true},
		{"no binding", srv.URL, "staff", "unbound", "test-mfa", false},
		{"no mfa", srv.URL, "staff", "sub", "", false},
		{"expired token", srv.URL, "staff", "sub", "test-mfa", false},
		{"nonce mismatch", srv.URL, "staff", "sub", "test-mfa", false},
		{"disabled account", srv.URL, "staff", "sub", "test-mfa", false},
		{"PKCE rejected", srv.URL, "staff", "sub", "test-mfa", false},
		{"redirect rejected", srv.URL, "staff", "sub", "test-mfa", false},
		{"valid", srv.URL, "staff", "sub", "test-mfa", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "disabled account" {
				if _, err := pool.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='staff-id'`); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := pool.Exec(ctx, `UPDATE accounts SET enabled=true WHERE id='staff-id'`); err != nil {
						t.Error(err)
					}
				}()
			}
			start, browser, err := staff.Begin(ctx, sessions, AdminEntry)
			if err != nil {
				t.Fatal(err)
			}
			issuerSigner := signer
			if tc.invalidSignature {
				issuerSigner = wrongSigner
			}
			nonce, expiry := start.Nonce, clock.Now().Add(time.Minute)
			if tc.name == "nonce mismatch" {
				nonce = secretToken()
			}
			if tc.name == "expired token" {
				expiry = clock.Now().Add(-time.Second)
			}
			raw, err := jwt.Signed(issuerSigner).Claims(jwt.Claims{Issuer: tc.issuer, Subject: tc.subject, Audience: jwt.Audience{tc.audience}, IssuedAt: jwt.NewNumericDate(clock.Now()), Expiry: jwt.NewNumericDate(expiry)}).Claims(map[string]any{"nonce": nonce, "acr": tc.acr, "auth_time": clock.Now().Unix(), "role": "SECURITY_ADMIN"}).Serialize()
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(start.AuthorizationURL)
			if err != nil {
				t.Fatal(err)
			}
			challenge := sha256.Sum256([]byte(start.Verifier))
			if parsed.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) {
				t.Fatal("authorization request has incorrect S256 challenge")
			}
			expectedVerifier, expectedRedirect := start.Verifier, "https://admin.example.test/api/v1/auth/staff/callback"
			if tc.name == "PKCE rejected" {
				expectedVerifier = "different-verifier"
			}
			if tc.name == "redirect rejected" {
				expectedRedirect = "https://other.example.test/api/v1/auth/staff/callback"
			}
			response.Store(&reply{raw, expectedVerifier, expectedRedirect})
			count := exchanges.Load()
			if _, _, err := staff.Complete(ctx, sessions, AdminEntry, secretToken(), browser, "fixture-code"); !errors.Is(err, ErrStaffLogin) {
				t.Fatal("unknown state accepted")
			}
			if _, _, err := staff.Complete(ctx, sessions, RiderEntry, start.State, browser, "fixture-code"); !errors.Is(err, ErrStaffLogin) {
				t.Fatalf("cross entry: %v", err)
			}
			if _, _, err := staff.Complete(ctx, sessions, AdminEntry, start.State, secretToken(), "fixture-code"); !errors.Is(err, ErrStaffLogin) {
				t.Fatalf("cross browser: %v", err)
			}
			if exchanges.Load() != count {
				t.Fatal("unbound flow reached IdP")
			}
			token, session, err := staff.Complete(ctx, sessions, AdminEntry, start.State, browser, "fixture-code")
			if tc.name == "valid" {
				if err != nil || token == "" || session.AccountID != "staff-id" || len(session.Roles) != 1 || session.Roles[0] != "FINANCE" {
					t.Fatalf("bound staff not mapped locally: %v", err)
				}
			} else if !errors.Is(err, ErrStaffLogin) || token != "" {
				t.Fatalf("invalid response accepted: %v", err)
			}
			if _, _, err := staff.Complete(ctx, sessions, AdminEntry, start.State, browser, "fixture-code"); !errors.Is(err, ErrStaffLogin) || exchanges.Load() != count+1 {
				t.Fatalf("callback state reused: %v", err)
			}
		})
	}
	t.Run("expired state", func(t *testing.T) {
		flow, browser, err := staff.Begin(ctx, sessions, AdminEntry)
		if err != nil {
			t.Fatal(err)
		}
		count := exchanges.Load()
		clock.now = clock.Now().Add(5 * time.Minute)
		defer func() { clock.now = clock.Now().Add(-5 * time.Minute) }()
		if _, _, err := staff.Complete(ctx, sessions, AdminEntry, flow.State, browser, "fixture-code"); !errors.Is(err, ErrStaffLogin) || exchanges.Load() != count {
			t.Fatal("expired state reached IdP")
		}
	})
	t.Run("HTTP callback fixed redirect and private logs", func(t *testing.T) {
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		mux := http.NewServeMux()
		NewIdentityHTTP(policy, sessions, NewDirectory(pool), nil, staff, logger).Register(mux)
		handler := platform.NewHandler(mux, nil, logger, 4096)
		request := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
			t.Helper()
			r := httptest.NewRequest("GET", "https://admin.example.test/api/v1/auth/staff/"+path, nil)
			for _, c := range cookies {
				r.AddCookie(c)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		if w := request("start?redirect_uri=https%3A%2F%2Fother.example.test"); w.Code != 400 || w.Header().Get("Location") != "" {
			t.Fatal("client selected callback")
		}
		start := request("start")
		if start.Code != 302 || len(start.Result().Cookies()) != 1 {
			t.Fatal("staff HTTP start failed")
		}
		browser := start.Result().Cookies()[0]
		if browser.Name != staffBrowserCookie || browser.Domain != "" || browser.Path != "/" || !browser.Secure || !browser.HttpOnly || browser.SameSite != http.SameSiteLaxMode || browser.MaxAge != 300 {
			t.Fatal("staff browser cookie flags invalid")
		}
		authorization, err := url.Parse(start.Header().Get("Location"))
		if err != nil {
			t.Fatal("authorization location invalid")
		}
		state := authorization.Query().Get("state")
		stateHash := sha256.Sum256([]byte(state))
		var nonce, verifier string
		if err := pool.QueryRow(ctx, `SELECT nonce,verifier FROM staff_login_flows WHERE state_digest=$1`, stateHash[:]).Scan(&nonce, &verifier); err != nil {
			t.Fatal(err)
		}
		raw, err := jwt.Signed(signer).Claims(jwt.Claims{Issuer: srv.URL, Subject: "sub", Audience: jwt.Audience{"staff"}, Expiry: jwt.NewNumericDate(clock.Now().Add(time.Minute))}).Claims(map[string]any{"nonce": nonce, "acr": "test-mfa", "auth_time": clock.Now().Unix(), "role": "SECURITY_ADMIN"}).Serialize()
		if err != nil {
			t.Fatal("signing fixture failed")
		}
		response.Store(&reply{raw, verifier, "https://admin.example.test/api/v1/auth/staff/callback"})
		query := url.Values{"state": {state}, "code": {"fixture-code"}, "iss": {srv.URL}}
		callback := request("callback?"+query.Encode(), browser)
		if callback.Code != 302 || callback.Header().Get("Location") != "https://admin.example.test/" || callback.Header().Get("Referrer-Policy") != "no-referrer" || callback.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("HTTP callback did not use fixed private redirect")
		}
		var token string
		for _, c := range callback.Result().Cookies() {
			if c.Name == SessionCookieName {
				token = c.Value
			}
		}
		session, err := sessions.Lookup(ctx, token, AdminEntry)
		if err != nil || len(session.Roles) != 1 || session.Roles[0] != "FINANCE" {
			t.Fatal("HTTP callback trusted asserted role")
		}
		if w := request("callback?"+query.Encode(), browser); w.Code != 401 {
			t.Fatal("HTTP state replay accepted")
		}
		for _, private := range []string{state, browser.Value, nonce, verifier, raw, token, session.CSRFToken, "fixture-code", "state=", "code="} {
			if strings.Contains(logs.String(), private) {
				t.Fatal("authentication material leaked into HTTP logs")
			}
		}
	})
}
