//go:build integration

package settlement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/jackc/pgx/v5"
)

// This is only a P04 transaction adapter fixture. It does not implement or
// certify P05 order eligibility, fulfillment locks, inventory, or delivery.
type recheckRefundCommands struct {
	seen      pgx.Tx
	failAfter bool
}

func (f *recheckRefundCommands) order(ctx context.Context, tx pgx.Tx, s identity.Session, id string) (string, error) {
	f.seen = tx
	var order string
	err := tx.QueryRow(ctx, `SELECT r.order_id FROM refunds r JOIN settlements s USING(order_id) WHERE r.id=$1 AND s.station_id=$2`, id, s.StationID).Scan(&order)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return order, err
}
func (f *recheckRefundCommands) ApproveRefund(ctx context.Context, tx pgx.Tx, s identity.Session, id, reason string) error {
	order, err := f.order(ctx, tx, s, id)
	if err != nil {
		return err
	}
	if err := (Store{}).ApproveRefund(ctx, tx, contract.RefundApproval{OrderID: order, RefundID: id, FinanceID: s.AccountID, ReasonCode: reason}); err != nil {
		return err
	}
	if f.failAfter {
		return errors.New("synthetic adapter failure")
	}
	return nil
}
func (f *recheckRefundCommands) RecordCODPayout(ctx context.Context, tx pgx.Tx, s identity.Session, id, ref string) error {
	order, err := f.order(ctx, tx, s, id)
	if err != nil {
		return err
	}
	if err := (Store{}).RecordCODPayout(ctx, tx, contract.CODPayout{OrderID: order, RefundID: id, FinanceID: s.AccountID, ReceiptReference: ref}); err != nil {
		return err
	}
	if f.failAfter {
		return errors.New("synthetic adapter failure")
	}
	return nil
}

func TestRecheckEightRoutesRealIdentityAndMFA(t *testing.T) {
	p, ctx := settlementFixtureWithIdentity(t, true)
	now := time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC)
	clock := &recheckClock{now}
	policy, err := identity.NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	sessions := identity.NewSessions(p, clock)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	auth := identity.NewIdentityHTTP(policy, sessions, identity.NewDirectory(p), nil, nil, logger)
	h := HTTP{Pool: p, Clock: clock, Authorize: auth.Authenticated, Refunds: &recheckRefundCommands{}}
	m := http.NewServeMux()
	h.Register(m)
	handler := platform.NewHandler(m, nil, logger, 0)
	type actor struct {
		token   string
		session identity.Session
	}
	actors := map[string]actor{}
	for i, tc := range []struct {
		name  string
		role  contract.Role
		entry identity.Entry
	}{{"customer", contract.RoleCustomer, identity.UserEntry}, {"rider", contract.RoleRider, identity.RiderEntry}, {"operator", contract.RoleOperator, identity.AdminEntry}, {"finance", contract.RoleFinance, identity.AdminEntry}, {"finance-b", contract.RoleFinance, identity.AdminEntry}, {"security", contract.RoleSecurityAdmin, identity.AdminEntry}} {
		if tc.entry == identity.UserEntry {
			_, err = p.Exec(ctx, `INSERT INTO accounts(id,station_id,phone)VALUES($1,'s',$2)`, tc.name, fmt.Sprintf("+1"+"555000%04d", i))
		} else {
			_, err = p.Exec(ctx, `INSERT INTO accounts(id,station_id,issuer,subject)VALUES($1,'s','https://idp.example.test',$1)`, tc.name)
		}
		if err != nil {
			t.Fatal("synthetic account setup failed")
		}
		if _, err = p.Exec(ctx, `INSERT INTO role_bindings(account_id,role)VALUES($1,$2)`, tc.name, tc.role); err != nil {
			t.Fatal(err)
		}
		if tc.entry == identity.RiderEntry {
			if _, err = p.Exec(ctx, `INSERT INTO rider_profiles(account_id)VALUES($1)`, tc.name); err != nil {
				t.Fatal(err)
			}
		}
		mfa := now
		if tc.entry == identity.UserEntry {
			mfa = time.Time{}
		}
		token, s, err := sessions.Issue(ctx, tc.name, tc.entry, mfa)
		if err != nil {
			t.Fatal("real session issue failed")
		}
		actors[tc.name] = actor{token, s}
	}
	if _, err := p.Exec(ctx, `INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)VALUES('s','customer',123,123),('s','other',999,999)`); err != nil {
		t.Fatal(err)
	}
	routes := []struct {
		method, path, body string
		allowed            int
	}{
		{"GET", "/payment-methods", "", 200}, {"GET", "/me/wallet", "", 200}, {"GET", "/admin/cod/receipts", "", 200},
		{"POST", "/admin/cod/batches", `{"rider_id":"rider","local_date":"2026-11-19","receipt_ids":["missing"]}`, 409},
		{"POST", "/admin/cod/batches/missing/verify", `{"actual_fen":1,"reason_code":"COUNTED"}`, 404},
		{"GET", "/admin/refunds", "", 200}, {"POST", "/admin/refunds/missing/approve", `{"reason_code":"APPROVED"}`, 404},
		{"POST", "/admin/refunds/missing/record-cod-payout", `{"receipt_reference":"synthetic-proof"}`, 404},
	}
	call := func(name, method, path, body, host, origin, csrf string, want int) *httptest.ResponseRecorder {
		t.Helper()
		a := actors[name]
		r := httptest.NewRequest(method, "https://"+host+"/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "real-identity-key")
		if a.token != "" {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: a.token})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		r.Header.Set("X-Role", "FINANCE")
		r.Header.Set("X-Station-ID", "other")
		r.Header.Set("X-MFA-At", clock.now.Format(time.RFC3339))
		r.Header.Set("X-Forwarded-Host", "admin.example.test")
		r.Header.Set("Forwarded", "host=admin.example.test;proto=https")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("real identity %s %s %s status=%d want=%d body=%s", name, method, path, w.Code, want, w.Body.String())
		}
		if want >= 400 {
			var e map[string]any
			if json.Unmarshal(w.Body.Bytes(), &e) != nil || len(e) != 3 || e["request_id"] == "" {
				t.Fatal("identity error envelope")
			}
		}
		return w
	}
	for i, route := range routes {
		host := "admin.example.test"
		if i < 2 {
			host = "user.example.test"
		}
		for _, name := range []string{"", "customer", "rider", "operator", "finance", "security"} {
			want := 401
			if i < 2 {
				if name == "customer" {
					want = 200
				}
			} else if name == "operator" || name == "finance" || name == "security" {
				want = 403
				if name == "finance" || i == 5 && name == "operator" {
					want = route.allowed
				}
			}
			w := call(name, route.method, route.path, route.body, host, "https://"+host, actors[name].session.CSRFToken, want)
			if i == 1 && name == "customer" && strings.TrimSpace(w.Body.String()) != `{"balance_fen":123,"currency":"CNY"}` {
				t.Fatal("wallet did not use session owner")
			}
		}
	}
	for _, i := range []int{3, 4, 6, 7} {
		route := routes[i]
		for _, bad := range []struct{ origin, csrf string }{{"", actors["finance"].session.CSRFToken}, {"null", actors["finance"].session.CSRFToken}, {"https://evil.example.test", actors["finance"].session.CSRFToken}, {"https://admin.example.test", ""}, {"https://admin.example.test", "wrong"}} {
			call("finance", route.method, route.path, route.body, "admin.example.test", bad.origin, bad.csrf, 403)
		}
	}
	for _, elapsed := range []time.Duration{15*time.Minute - time.Microsecond, 15 * time.Minute, 16 * time.Minute} {
		clock.now = now.Add(elapsed)
		for _, i := range []int{3, 4, 6, 7} {
			route := routes[i]
			want := 403
			if elapsed < 15*time.Minute {
				want = route.allowed
			}
			call("finance", route.method, route.path, route.body, "admin.example.test", "https://admin.example.test", actors["finance"].session.CSRFToken, want)
		}
		for _, i := range []int{2, 5} {
			route := routes[i]
			call("finance", route.method, route.path, route.body, "admin.example.test", "", "", 200)
		}
	}
	clock.now = now
	// P01 already prevents staff sessions without MFA in SQL. First prove that
	// constraint, then remove it only in this disposable adversarial fixture to
	// verify P04's real authorization adapter also rejects a legacy malformed row.
	if _, err := p.Exec(ctx, `UPDATE sessions SET mfa_at=NULL WHERE account_id='finance'`); err == nil {
		t.Fatal("staff session accepted absent MFA")
	}
	if _, err := p.Exec(ctx, `ALTER TABLE sessions DROP CONSTRAINT sessions_check`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE sessions SET mfa_at=NULL WHERE account_id='finance'`); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{3, 4, 6, 7} {
		route := routes[i]
		call("finance", route.method, route.path, route.body, "admin.example.test", "https://admin.example.test", actors["finance"].session.CSRFToken, 403)
	}
	if _, err := p.Exec(ctx, `UPDATE sessions SET mfa_at=$1 WHERE account_id='finance'`, now); err != nil {
		t.Fatal(err)
	}
	call("finance", "GET", "/admin/refunds", "", "user.example.test", "", "", 404)
	call("finance", "GET", "/admin/refunds", "", "evil.example.test", "", "", 404)
	// Only this disposable test removes the singleton constraint to exercise
	// defensive station filtering. Production remains a single station.
	rcCollect(t, p, ctx, "cod", 100)
	a := rcRequest(t, p, ctx, "cod")
	if _, err := p.Exec(ctx, `DROP INDEX stations_singleton;INSERT INTO stations(id,name)VALUES('other','Other');UPDATE accounts SET station_id='other' WHERE id IN('finance','customer')`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/refunds", "/admin/cod/receipts"} {
		w := call("finance", "GET", path, "", "admin.example.test", "", "", 200)
		if strings.TrimSpace(w.Body.String()) != `{"items":[],"next_cursor":null}` {
			t.Fatal("cross-station list leaked")
		}
	}
	call("finance", "POST", "/admin/refunds/"+a.RefundID+"/approve", `{"reason_code":"APPROVED"}`, "admin.example.test", "https://admin.example.test", actors["finance"].session.CSRFToken, 404)
	w := call("customer", "GET", "/me/wallet", "", "user.example.test", "", "", 200)
	if strings.TrimSpace(w.Body.String()) != `{"balance_fen":0,"currency":"CNY"}` {
		t.Fatal("cross-station wallet leak")
	}
	if _, err := p.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='finance'`); err != nil {
		t.Fatal(err)
	}
	call("finance", "GET", "/admin/refunds", "", "admin.example.test", "", "", 401)
	clock.now = now.Add(8 * time.Hour)
	call("finance-b", "GET", "/admin/refunds", "", "admin.example.test", "", "", 401)
	for _, a := range actors {
		for _, secret := range []string{a.token, a.session.CSRFToken} {
			if secret != "" && strings.Contains(logs.String(), secret) {
				t.Fatal("authentication material logged")
			}
		}
	}
	for _, private := range []string{"Cookie", "X-CSRF-Token", "phone", "room", "Origin", "Forwarded", "?"} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("private request data logged")
		}
	}
	if rcScalar(t, p, ctx, `SELECT count(*) FROM schema_migrations WHERE version NOT LIKE '000_%' AND version NOT LIKE '010_%' AND version NOT LIKE '040_%' AND version NOT LIKE '060_%'`) != 0 {
		t.Fatal("identity fixture uses other packages")
	}
	t.Log("8 routes: real sessions, role/entry/station isolation, Origin/CSRF, recent MFA at 15m-1us / 15m /16m /NULL, disabled and expired sessions; no secrets logged")
}
