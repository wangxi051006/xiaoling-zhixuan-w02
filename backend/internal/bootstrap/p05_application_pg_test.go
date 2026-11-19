//go:build integration

package bootstrap

import (
	"campus.local/xiaoling/internal/catalog"
	"campus.local/xiaoling/internal/fulfillment"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/schedule"
	"campus.local/xiaoling/internal/settlement"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestP05ApplicationUsesRealIdentityAndRefundOrchestration(t *testing.T) {
	p, ctx := g1Pool(t)
	clock := &g1Clock{time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)}
	if _, err := p.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');
 INSERT INTO accounts(id,station_id,phone)VALUES('c','s','+15550000001');
 INSERT INTO accounts(id,station_id,issuer,subject)VALUES('f','s','https://idp.example.test','finance');
 INSERT INTO role_bindings(account_id,role)VALUES('c','CUSTOMER'),('f','FINANCE')`); err != nil {
		t.Fatal(err)
	}
	d := identity.NewDirectory(p)
	policy := settlement.PaymentPolicy{CODEnabled: true}
	orders := &fulfillment.Service{Pool: p, Clock: clock, Identity: d, Catalog: catalog.Store{Clock: clock}, Schedule: schedule.Store{}, Settlement: settlement.Store{Policy: policy}, RiderState: d, Policy: policy, AcceptNewOrders: false, AllowDispatch: false}
	h, err := NewApplication(ApplicationOptions{Pool: p, Clock: clock, UserOrigin: "https://user.example.test", RiderOrigin: "https://rider.example.test", AdminOrigin: "https://admin.example.test", Payments: policy, Orders: orders})
	if err != nil {
		t.Fatal(err)
	}
	sessions := identity.NewSessions(p, clock)
	customer, _, err := sessions.Issue(ctx, "c", identity.UserEntry, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	finance, fin, err := sessions.Issue(ctx, "f", identity.AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, host, body, token, csrf string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "https://"+host+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "p05-app-key")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		}
		if csrf != "" {
			r.Header.Set("Origin", "https://"+host)
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
	}
	call("GET", "/api/v1/orders", "user.example.test", "", customer, "", 200)
	call("GET", "/api/v1/orders", "user.example.test", "", "", "", 401)
	call("GET", "/api/v1/orders", "admin.example.test", "", customer, "", 404)
	call("POST", "/api/v1/admin/refunds/missing/approve", "admin.example.test", `{"reason_code":"APPROVE"}`, finance, "", 403)
	call("POST", "/api/v1/admin/refunds/missing/approve", "admin.example.test", `{"reason_code":"APPROVE"}`, finance, fin.CSRFToken, 404)
	clock.now = clock.now.Add(15 * time.Minute)
	call("POST", "/api/v1/admin/refunds/missing/approve", "admin.example.test", `{"reason_code":"APPROVE"}`, finance, fin.CSRFToken, 403)
	if _, err = p.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='c'`); err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/v1/orders", "user.example.test", "", customer, "", 401)
}
