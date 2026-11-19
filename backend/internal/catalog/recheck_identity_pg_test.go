//go:build integration

package catalog

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
)

type catalogClock struct{ at time.Time }

func (c *catalogClock) Now() time.Time { return c.at }

func TestCatalogWithRealIdentityHTTPRoleSiteOriginAndCSRF(t *testing.T) {
	pool, ctx := stockFixtureWithIdentity(t, true)
	_, err := pool.Exec(ctx, `DROP INDEX stations_singleton; INSERT INTO stations(id,name) VALUES('other','Other');
 INSERT INTO accounts(id,station_id,issuer,subject) VALUES('operator','s','https://idp.test','op'),('finance','s','https://idp.test','fin'),('security','s','https://idp.test','sec'),('rider','s','https://idp.test','r'),('outside','other','https://idp.test','outside');
 INSERT INTO role_bindings(account_id,role) VALUES('operator','OPERATOR'),('finance','FINANCE'),('security','SECURITY_ADMIN'),('rider','RIDER'),('outside','OPERATOR');
 INSERT INTO rider_profiles(account_id) VALUES('rider');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO accounts(id,station_id,phone) VALUES('customer','s',$1)`, fmt.Sprintf("+1%d", 2025550100)); err != nil {
		t.Fatal("seed customer failed")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO role_bindings(account_id,role) VALUES('customer','CUSTOMER')`); err != nil {
		t.Fatal(err)
	}
	clock := &catalogClock{time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
	sessions := identity.NewSessions(pool, clock)
	policy, err := identity.NewEntryPolicy("https://user.test", "https://rider.test", "https://admin.test")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	auth := identity.NewIdentityHTTP(policy, sessions, identity.NewDirectory(pool), nil, nil, logger)
	mux := http.NewServeMux()
	(&HTTP{Pool: pool, Authorize: auth.Authenticated}).Register(mux)
	handler := platform.NewHandler(mux, nil, logger, 0)
	type credentials struct{ token, csrf string }
	creds := map[string]credentials{}
	for _, id := range []string{"operator", "finance", "security", "rider", "outside", "customer"} {
		entry := identity.AdminEntry
		if id == "rider" {
			entry = identity.RiderEntry
		}
		if id == "customer" {
			entry = identity.UserEntry
		}
		token, session, e := sessions.Issue(ctx, id, entry, clock.Now())
		if e != nil {
			t.Fatal("issue fixture session failed")
		}
		creds[id] = credentials{token, session.CSRFToken}
	}
	call := func(id, method, path, origin, csrf, host string, status int) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"expected_version":1,"name":"Updated"}`
		if method == "PUT" {
			body = `{"expected_version":1,"promotion":null}`
		}
		if method == "POST" && strings.Contains(path, "adjustments") {
			body = `{"inventory_id":"i","delta":1,"expected_version":1,"reason_code":"COUNT"}`
		}
		if method == "POST" && path == "/admin/products" {
			body = `{"name":"Created","description":"","on_sale":true,"reorder_point":1,"skus":[{"label":"one","units_per_pack":1,"unit_price_fen":9}]}`
		}
		if method == "GET" {
			body = ""
		}
		r := httptest.NewRequest(method, host+"/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "identity-"+id+path)
		// These attacker-controlled claims must never grant authority.
		r.Header.Set("X-Role", "OPERATOR")
		r.Header.Set("X-Station-ID", "s")
		r.Header.Set("Forwarded", "host=admin.test;proto=https")
		r.Header.Set("X-Forwarded-Host", "admin.test")
		r.Header.Set("X-Forwarded-Proto", "https")
		if id != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: creds[id].token})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf == "valid" {
			r.Header.Set("X-CSRF-Token", creds[id].csrf)
		} else if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s actor=%s: want %d got %d", method, path, id, status, w.Code)
		}
		return w
	}
	paths := []struct{ method, path string }{{"POST", "/admin/products"}, {"PATCH", "/admin/products/p"}, {"PUT", "/admin/products/p/promotion"}, {"POST", "/admin/inventory/adjustments"}}
	for _, id := range []string{"", "customer", "rider", "finance", "security"} {
		for _, p := range paths {
			want := 403
			if id == "" || id == "customer" || id == "rider" {
				want = 401
			}
			call(id, p.method, p.path, "https://admin.test", "valid", "https://admin.test", want)
		}
	}
	call("operator", "GET", "/admin/products", "", "", "https://admin.test", 200)
	call("operator", "PATCH", "/admin/products/p", "", "valid", "https://admin.test", 403)
	call("operator", "PATCH", "/admin/products/p", "null", "valid", "https://admin.test", 403)
	call("operator", "PATCH", "/admin/products/p", "https://user.test", "valid", "https://admin.test", 403)
	call("operator", "PATCH", "/admin/products/p", "https://admin.test", "wrong", "https://admin.test", 403)
	call("operator", "PATCH", "/admin/products/p", "https://admin.test", "", "https://admin.test", 403)
	call("operator", "PATCH", "/admin/products/p", "https://admin.test", "valid", "https://rider.test", 404)
	for _, p := range paths[1:] {
		call("outside", p.method, p.path, "https://admin.test", "valid", "https://admin.test", 404)
	}
	if w := call("outside", "GET", "/admin/products", "", "", "https://admin.test", 200); strings.TrimSpace(w.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatal("cross-site list exposed products")
	}
	call("operator", "PATCH", "/admin/products/p", "https://admin.test", "valid", "https://admin.test", 200)
	// P02 frozen routes explicitly do not require recent MFA. The session was
	// issued with MFA; a 16 minute old MFA still permits this ordinary config write.
	clock.at = clock.at.Add(16 * time.Minute)
	call("operator", "POST", "/admin/products", "https://admin.test", "valid", "https://admin.test", 201)
	if _, err = pool.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='operator'`); err != nil {
		t.Fatal(err)
	}
	call("operator", "GET", "/admin/products", "", "", "https://admin.test", 401)
	for _, c := range creds {
		if strings.Contains(logs.String(), c.token) || strings.Contains(logs.String(), c.csrf) {
			t.Fatal("authentication material leaked into logs")
		}
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_id='outside'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("cross-site write reached persistence")
	}
}
