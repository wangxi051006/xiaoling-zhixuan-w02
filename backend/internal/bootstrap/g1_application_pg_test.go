//go:build integration

package bootstrap

import (
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/internal/settlement"
	"campus.local/xiaoling/migrations"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type g1Clock struct{ now time.Time }

func (c *g1Clock) Now() time.Time { return c.now }

func g1Pool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	if dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("test database must be isolated")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("test database configuration")
	}
	t.Cleanup(admin.Close)
	var b [8]byte
	rand.Read(b[:])
	schema := pgx.Identifier{"g1_" + hex.EncodeToString(b[:])}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(c, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("test database configuration")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err = platform.Migrate(ctx, p, migrations.SQL); err != nil {
		t.Fatal(err)
	}
	return p, ctx
}

func TestG1RealApplicationSessionsAndMissingRefunds(t *testing.T) {
	p, ctx := g1Pool(t)
	clock := &g1Clock{time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)}
	_, err := p.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');
 INSERT INTO accounts(id,station_id,phone)VALUES('customer','s','+15550000001');
 INSERT INTO accounts(id,station_id,issuer,subject)VALUES('operator','s','https://idp.example.test','op'),('finance','s','https://idp.example.test','fin');
 INSERT INTO role_bindings(account_id,role)VALUES('customer','CUSTOMER'),('operator','OPERATOR'),('finance','FINANCE')`)
	if err != nil {
		t.Fatal(err)
	}
	sessions := identity.NewSessions(p, clock)
	type actor struct {
		token   string
		session identity.Session
	}
	actors := map[string]actor{}
	for _, a := range []string{"customer", "operator", "finance"} {
		entry := identity.AdminEntry
		mfa := clock.now
		if a == "customer" {
			entry = identity.UserEntry
			mfa = time.Time{}
		}
		token, s, e := sessions.Issue(ctx, a, entry, mfa)
		if e != nil {
			t.Fatal(e)
		}
		actors[a] = actor{token, s}
	}
	h, err := NewApplication(ApplicationOptions{Pool: p, Clock: clock, UserOrigin: "https://user.example.test", RiderOrigin: "https://rider.example.test", AdminOrigin: "https://admin.example.test", Payments: settlement.PaymentPolicy{CODEnabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(actor, method, path, body, host string, csrf bool, want int) *httptest.ResponseRecorder {
		t.Helper()
		a := actors[actor]
		r := httptest.NewRequest(method, "https://"+host+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:42000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "g1-key")
		r.Header.Set("X-Role", "FINANCE")
		r.Header.Set("X-Station-ID", "forged")
		r.Header.Set("X-Forwarded-Host", "admin.example.test")
		if a.token != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: a.token})
		}
		if csrf {
			r.Header.Set("Origin", "https://"+host)
			r.Header.Set("X-CSRF-Token", a.session.CSRFToken)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s: got %d want %d: %s", actor, method, path, w.Code, want, w.Body.String())
		}
		if want >= 400 {
			var e map[string]any
			if json.Unmarshal(w.Body.Bytes(), &e) != nil || len(e) != 3 || e["request_id"] == "" {
				t.Fatal("error envelope")
			}
		}
		return w
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		call("", "GET", path, "", "127.0.0.1", false, 200)
	}
	call("", "GET", "/api/v1/catalog/products", "", "user.example.test", false, 200)
	call("customer", "GET", "/api/v1/me", "", "user.example.test", false, 200)
	call("customer", "GET", "/api/v1/me/addresses", "", "user.example.test", false, 200)
	call("customer", "GET", "/api/v1/delivery/slots", "", "user.example.test", false, 200)
	methods := call("customer", "GET", "/api/v1/payment-methods", "", "user.example.test", false, 200)
	if !strings.Contains(methods.Body.String(), `"code":"WECHAT","enabled":false`) {
		t.Fatal("wechat enabled")
	}
	call("customer", "GET", "/api/v1/me/wallet", "", "user.example.test", false, 200)
	call("operator", "GET", "/api/v1/admin/products", "", "admin.example.test", false, 200)
	call("operator", "GET", "/api/v1/admin/slots", "", "admin.example.test", false, 200)
	call("finance", "GET", "/api/v1/admin/cod/receipts", "", "admin.example.test", false, 200)
	call("finance", "GET", "/api/v1/admin/refunds", "", "admin.example.test", false, 200)
	call("operator", "GET", "/api/v1/admin/cod/receipts", "", "admin.example.test", false, 403)
	call("customer", "GET", "/api/v1/payment-methods", "", "admin.example.test", false, 404)
	call("", "GET", "/api/v1/me/wallet", "", "user.example.test", false, 401)
	routes := []struct {
		path, body string
		status     int
	}{{"/admin/cod/batches", `{"rider_id":"r","local_date":"2026-11-19","receipt_ids":["missing"]}`, 409}, {"/admin/cod/batches/missing/verify", `{"actual_fen":0,"reason_code":"COUNT"}`, 404}, {"/admin/refunds/missing/approve", `{"reason_code":"APPROVE"}`, 503}, {"/admin/refunds/missing/record-cod-payout", `{"receipt_reference":"synthetic"}`, 503}}
	for _, r := range routes {
		call("finance", "POST", "/api/v1"+r.path, r.body, "admin.example.test", false, 403)
		call("finance", "POST", "/api/v1"+r.path, r.body, "admin.example.test", true, r.status)
	}
	clock.now = clock.now.Add(15 * time.Minute)
	for _, r := range routes {
		call("finance", "POST", "/api/v1"+r.path, r.body, "admin.example.test", true, 403)
	}
	call("finance", "GET", "/api/v1/admin/refunds", "", "admin.example.test", false, 200)
	call("", "POST", "/api/v1/auth/otp/request", `{"phone":"+15550000002"}`, "user.example.test", true, 503)
	if _, err = p.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='customer'`); err != nil {
		t.Fatal(err)
	}
	call("customer", "GET", "/api/v1/me/wallet", "", "user.example.test", false, 401)
	t.Log(fmt.Sprintf("real sessions; shared clock; four exact MFA boundaries; unavailable suppliers/refund orchestration fail closed"))
}
