//go:build integration

package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
)

func TestIdentityHTTPEntryOwnershipAndRedaction(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	// The disposable test schema needs a second station to exercise isolation.
	if _, err := pool.Exec(ctx, `DROP INDEX stations_singleton;
	INSERT INTO stations(id,name) VALUES ('s','Fixture'),('other','Other');
	INSERT INTO accounts(id,station_id,phone) VALUES ('a','s','+8613800000000'),('b','s','+8613800000001');
	INSERT INTO accounts(id,station_id,issuer,subject,display_name) VALUES
	('op','s','https://idp.example.test','op','Operator'),('fin','s','https://idp.example.test','fin','Finance'),
	('r','s','https://idp.example.test','r','Rider'),('outside','other','https://idp.example.test','out','Outside');
	INSERT INTO role_bindings(account_id,role) VALUES ('a','CUSTOMER'),('b','CUSTOMER'),('op','OPERATOR'),('fin','FINANCE'),('r','RIDER'),('outside','RIDER');
	INSERT INTO rider_profiles(account_id) VALUES ('r'),('outside');
	INSERT INTO service_buildings(id,station_id,name) VALUES ('d','s','Fixture Hall');`); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{time.Unix(1780000000, 0)}
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	sender := &fixtureOTP{}
	otp, err := NewOTP(NewPostgresOTPStore(pool), sender, clock, []byte("one-of-your-local-test-fixture-keys"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(pool, clock)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	mux := http.NewServeMux()
	NewIdentityHTTP(policy, sessions, NewDirectory(pool), otp, nil, logger).Register(mux)
	handler := platform.NewHandler(mux, nil, logger, 4096)
	request := func(method string, entry Entry, path, body, token, csrf string, origin bool, status int) *httptest.ResponseRecorder {
		t.Helper()
		u := policy.origins[entry].origin
		r := httptest.NewRequest(method, u+"/api/v1"+path, strings.NewReader(body))
		if origin {
			r.Header.Set("Origin", u)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: status %d, want %d, body=%s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	request("POST", UserEntry, "/auth/otp/request", `{"phone":"+8613800000000"}`, "", "", false, 403)
	request("POST", AdminEntry, "/auth/otp/request", `{"phone":"+8613800000000"}`, "", "", true, 404)
	otpResponse := request("POST", UserEntry, "/auth/otp/request", `{"phone":"+8613800000000"}`, "", "", true, 202)
	var receipt ChallengeReceipt
	if err := json.Unmarshal(otpResponse.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	login := request("POST", UserEntry, "/auth/otp/verify", `{"challenge_id":"`+receipt.ID+`","code":"`+sender.lastCode+`"}`, "", "", true, 200)
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	validCookie := login.Result().Cookies()[0]
	if !validCookie.Secure || !validCookie.HttpOnly || validCookie.Domain != "" || me.CSRF == "" {
		t.Fatal("invalid auth response")
	}
	request("GET", AdminEntry, "/me", "", validCookie.Value, "", false, 401)
	request("GET", RiderEntry, "/me", "", validCookie.Value, "", false, 401)
	body := `{"building_id":"d","room":"private-room","contact_name":"Private Name","contact_phone":"+8613800000000"}`
	request("POST", UserEntry, "/me/addresses", body, validCookie.Value, "bad", true, 403)
	address := request("POST", UserEntry, "/me/addresses", body, validCookie.Value, me.CSRF, true, 201)
	var row struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(address.Body.Bytes(), &row); err != nil || row.ID == "" {
		t.Fatal("address response invalid")
	}
	b, bSession, err := sessions.Issue(ctx, "b", UserEntry, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	request("PATCH", UserEntry, "/me/addresses/"+row.ID, `{"room":"private-room","expected_version":1}`, b, bSession.CSRFToken, true, 404)
	op, opSession, err := sessions.Issue(ctx, "op", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	fin, finSession, err := sessions.Issue(ctx, "fin", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	rider, _, err := sessions.Issue(ctx, "r", RiderEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	patch := `{"enabled":false,"expected_version":1,"reason_code":"POLICY"}`
	request("PATCH", AdminEntry, "/admin/riders/r", patch, fin, finSession.CSRFToken, true, 403)
	request("PATCH", AdminEntry, "/admin/riders/outside", patch, op, opSession.CSRFToken, true, 404)
	request("PATCH", AdminEntry, "/admin/riders/r", patch, op, opSession.CSRFToken, true, 200)
	request("GET", RiderEntry, "/me", "", rider, "", false, 401)
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id='op' AND object_id='r' AND request_id<>'' AND reason_code='POLICY'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("expected one audited rider update: %v", err)
	}
	for _, value := range []string{validCookie.Value, me.CSRF, receipt.ID, sender.lastCode, "+8613800000000", "private-room", "Private Name", "POLICY"} {
		if strings.Contains(logs.String(), value) {
			t.Fatal("logs contain private value")
		}
	}
}

func TestStaffRoleMutationRequiresSeparateSecurityAdminAndRevokesSession(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Fixture');
	INSERT INTO accounts(id,station_id,issuer,subject) VALUES
	('sec','s','https://idp.example.test','sec'),('op','s','https://idp.example.test','op'),('fin','s','https://idp.example.test','fin');
	INSERT INTO role_bindings(account_id,role) VALUES ('sec','SECURITY_ADMIN'),('op','OPERATOR'),('fin','FINANCE');`); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{time.Unix(1780000000, 0)}
	sessions := NewSessions(pool, clock)
	_, security, err := sessions.Issue(ctx, "sec", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, operator, err := sessions.Issue(ctx, "op", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := sessions.Issue(ctx, "fin", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	call := func(actor Session, target string, grant bool) error {
		return sessions.ChangeStaffRole(ctx, actor, target, contract.RoleFinance, grant, "POLICY", "request-id")
	}
	if err := call(operator, "fin", false); AccessStatus(err) != 403 {
		t.Fatalf("operator changed role: %v", err)
	}
	if err := call(security, "sec", true); AccessStatus(err) != 403 {
		t.Fatalf("self grant allowed: %v", err)
	}
	stale := security
	stale.MFAAt = clock.Now().Add(-recentMFAWindow)
	if err := call(stale, "fin", false); AccessStatus(err) != 403 {
		t.Fatalf("stale MFA accepted: %v", err)
	}
	if err := call(security, "fin", false); err != nil {
		t.Fatal(err)
	}
	if err := call(security, "fin", true); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Lookup(ctx, old, AdminEntry); !errors.Is(err, ErrSession) {
		t.Fatalf("old session resurrected: %v", err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id='sec' AND object_id='fin' AND request_id='request-id'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("expected 2 audited changes: %v", err)
	}
}
