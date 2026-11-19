//go:build integration

package schedule

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
)

func TestRecheckHTTPWithRealIdentity(t *testing.T) {
	pool, ctx, now := slotFixtureWithIdentity(t, true)
	policy, err := identity.NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	clock := &fixedClock{now}
	sessions := identity.NewSessions(pool, clock)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	auth := identity.NewIdentityHTTP(policy, sessions, identity.NewDirectory(pool), nil, nil, logger)
	h := HTTP{Pool: pool, Clock: clock, Authorize: auth.Authenticated}
	mux := http.NewServeMux()
	h.Register(mux)
	handler := platform.NewHandler(mux, nil, logger, 0)
	type actor struct {
		token   string
		session identity.Session
		host    string
	}
	actors := map[string]actor{}
	for i, tc := range []struct {
		name  string
		role  contract.Role
		entry identity.Entry
		host  string
	}{
		{"customer", contract.RoleCustomer, identity.UserEntry, "user.example.test"},
		{"rider", contract.RoleRider, identity.RiderEntry, "rider.example.test"},
		{"operator", contract.RoleOperator, identity.AdminEntry, "admin.example.test"},
		{"finance", contract.RoleFinance, identity.AdminEntry, "admin.example.test"},
		{"security", contract.RoleSecurityAdmin, identity.AdminEntry, "admin.example.test"},
	} {
		if tc.entry == identity.UserEntry {
			// The phone is synthetic and is never emitted to test output.
			_, err = pool.Exec(ctx, `INSERT INTO accounts(id,station_id,phone) VALUES($1,'s',$2)`, tc.name, fmt.Sprintf("+1"+"555"+"000%04d", i))
		} else {
			_, err = pool.Exec(ctx, `INSERT INTO accounts(id,station_id,issuer,subject) VALUES($1,'s','https://idp.example.test',$1)`, tc.name)
		}
		if err != nil {
			t.Fatal("synthetic account setup failed")
		}
		if _, err = pool.Exec(ctx, `INSERT INTO role_bindings(account_id,role) VALUES($1,$2)`, tc.name, tc.role); err != nil {
			t.Fatal(err)
		}
		if tc.entry == identity.RiderEntry {
			if _, err = pool.Exec(ctx, `INSERT INTO rider_profiles(account_id) VALUES($1)`, tc.name); err != nil {
				t.Fatal(err)
			}
		}
		mfa := now
		if tc.entry == identity.UserEntry {
			mfa = time.Time{}
		}
		token, session, err := sessions.Issue(ctx, tc.name, tc.entry, mfa)
		if err != nil {
			t.Fatal("real session issue failed")
		}
		actors[tc.name] = actor{token, session, tc.host}
	}
	call := func(name, method, path, body, host, origin, csrf string, want int) {
		t.Helper()
		a := actors[name]
		if host == "" {
			host = "admin.example.test"
		}
		r := httptest.NewRequest(method, "https://"+host+"/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "real-identity-batch")
		if a.token != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: a.token})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		// None of these client assertions may grant privileges or select a site.
		r.Header.Set("X-Role", "OPERATOR")
		r.Header.Set("X-Station-ID", "other")
		r.Header.Set("X-Forwarded-Host", "admin.example.test")
		r.Header.Set("Forwarded", "host=admin.example.test;proto=https")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s: status=%d want=%d", name, method, path, w.Code, want)
		}
		if want >= 400 {
			assertSlotError(t, w)
		}
	}
	update := `{"expected_version":1,"capacity":2,"accepting":true}`
	batch := `{"local_date":"2026-11-19","opens_at":"08:00","closes_at":"09:00","lead_minutes":15,"capacity":2}`
	adminPaths := []struct{ method, path, body string }{{"GET", "/admin/slots?date=2026-11-18", ""}, {"GET", "/admin/slots/early", ""}, {"PUT", "/admin/slots/early", update}, {"POST", "/admin/slots/batches", batch}}
	for _, name := range []string{"", "customer", "rider", "finance", "security"} {
		for _, route := range adminPaths {
			want := 401
			if name == "finance" || name == "security" {
				want = 403
			}
			call(name, route.method, route.path, route.body, "admin.example.test", "https://admin.example.test", actors[name].session.CSRFToken, want)
		}
	}
	for _, name := range []string{"", "operator", "rider", "finance", "security"} {
		call(name, "GET", "/delivery/slots", "", "user.example.test", "", "", 401)
	}
	call("customer", "GET", "/delivery/slots?date=2026-11-18", "", "user.example.test", "", "", 200)
	call("operator", "GET", "/admin/slots?date=2026-11-18", "", "admin.example.test", "", "", 200)
	call("operator", "GET", "/admin/slots/early", "", "admin.example.test", "", "", 200)
	call("operator", "GET", "/admin/slots/early", "", "user.example.test", "", "", 404)
	for _, route := range adminPaths[2:] {
		for _, bad := range []struct{ origin, csrf string }{{"", actors["operator"].session.CSRFToken}, {"null", actors["operator"].session.CSRFToken}, {"https://evil.example.test", actors["operator"].session.CSRFToken}, {"https://admin.example.test", ""}, {"https://admin.example.test", "wrong"}} {
			call("operator", route.method, route.path, route.body, "admin.example.test", bad.origin, bad.csrf, 403)
		}
	}
	// Ordinary P03 routes explicitly do not require recent MFA. A real staff
	// session was issued after MFA; ordinary updates remain valid after 16 minutes.
	clock.now = now.Add(16 * time.Minute)
	call("operator", "PUT", "/admin/slots/early", update, "admin.example.test", "https://admin.example.test", actors["operator"].session.CSRFToken, 200)
	call("operator", "POST", "/admin/slots/batches", batch, "admin.example.test", "https://admin.example.test", actors["operator"].session.CSRFToken, 201)
	if scalar(t, pool, ctx, `SELECT count(*) FROM audit_events WHERE station_id<>'s' OR actor_id<>'operator' OR request_id=''`) != 0 {
		t.Fatal("client-controlled audit identity")
	}
	// Production is single-site. Only this disposable fixture removes its
	// singleton index to exercise the defensive cross-site predicates.
	if _, err := pool.Exec(ctx, `DROP INDEX stations_singleton; INSERT INTO stations(id,name) VALUES('other','Other'); UPDATE accounts SET station_id='other' WHERE id='operator'`); err != nil {
		t.Fatal(err)
	}
	call("operator", "GET", "/admin/slots/early", "", "admin.example.test", "", "", 404)
	call("operator", "PUT", "/admin/slots/early", `{"expected_version":2,"capacity":4,"accepting":true}`, "admin.example.test", "https://admin.example.test", actors["operator"].session.CSRFToken, 404)
	r := httptest.NewRequest("GET", "https://admin.example.test/api/v1/admin/slots?date=2026-11-18", nil)
	r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: actors["operator"].token})
	r.Header.Set("X-Station-ID", "s")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatal("cross-site list leaked windows")
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='operator'`); err != nil {
		t.Fatal(err)
	}
	call("operator", "GET", "/admin/slots/early", "", "admin.example.test", "", "", 401)
	if scalar(t, pool, ctx, `SELECT count(*) FROM schema_migrations WHERE version NOT LIKE '000_%' AND version NOT LIKE '010_%' AND version NOT LIKE '030_%' AND version NOT LIKE '060_%'`) != 0 {
		t.Fatal("identity fixture uses another package")
	}
	for _, a := range actors {
		for _, secret := range []string{a.token, a.session.CSRFToken} {
			if secret != "" && strings.Contains(logs.String(), secret) {
				t.Fatal("authentication material leaked into log")
			}
		}
	}
	for _, private := range []string{"Cookie", "X-CSRF-Token", "phone", "room", "Origin", "Forwarded", "?date="} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("private request data logged")
		}
	}
}
