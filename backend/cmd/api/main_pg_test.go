//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type operationalLogChannel chan []byte

func (c operationalLogChannel) Write(p []byte) (int, error) {
	c <- append([]byte(nil), p...)
	return len(p), nil
}

func TestP05MainServesRealSessionOrdersAndShutsDown(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	if dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("test database must be isolated")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("test database configuration")
	}
	t.Cleanup(admin.Close)
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "main_p05_" + hex.EncodeToString(random[:])
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	endpoint, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("test database configuration")
	}
	query := endpoint.Query()
	query.Set("search_path", schema)
	endpoint.RawQuery = query.Encode()
	p, err := pgxpool.New(ctx, endpoint.String())
	if err != nil {
		t.Fatal("test pool configuration")
	}
	t.Cleanup(p.Close)
	if err = platform.Migrate(ctx, p, migrations.SQL); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');INSERT INTO accounts(id,station_id,phone)VALUES('c','s','+15550000001');INSERT INTO role_bindings(account_id,role)VALUES('c','CUSTOMER')`); err != nil {
		t.Fatal(err)
	}
	token, _, err := identity.NewSessions(p, contract.SystemClock{}).Issue(ctx, "c", identity.UserEntry, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"APP_ENV": "test", "HTTP_ADDR": address, "DATABASE_URL": endpoint.String(), "USER_ORIGIN": "https://user.example.test", "RIDER_ORIGIN": "https://rider.example.test", "ADMIN_ORIGIN": "https://admin.example.test", "COD_ENABLED": "true", "BALANCE_ENABLED": "false", "ORDERS_ENABLED": "false", "DISPATCH_ENABLED": "false", "WECHAT_ENABLED": "false"} {
		t.Setenv(key, value)
	}
	for key, value := range map[string]string{
		"STAFF_OIDC_ISSUER": "https://idp.example.test", "STAFF_OIDC_AUTH_URL": "https://idp.example.test/authorize",
		"STAFF_OIDC_TOKEN_URL": "https://idp.example.test/token", "STAFF_OIDC_JWKS_URL": "https://idp.example.test/jwks",
		"STAFF_OIDC_CLIENT_ID": "fixture", "STAFF_OIDC_CLIENT_SECRET": "synthetic", "STAFF_OIDC_MFA_CONTEXT": "fixture-mfa",
		"EMAIL_CODE_KEY": "synthetic-test-only-key-0123456789", "EMAIL_SMTP_ADDR": "smtp.example.test:587",
		"EMAIL_SMTP_FROM": "fixture@example.test", "EMAIL_NOTIFICATIONS_ENABLED": "true",
	} {
		t.Setenv(key, value)
	}
	serviceCtx, stopService := context.WithCancel(ctx)
	done := make(chan error, 1)
	logs := make(operationalLogChannel, 100)
	go func() { done <- run(serviceCtx, slog.New(slog.NewJSONHandler(logs, nil)), nil) }()
	t.Cleanup(func() {
		stopService()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("main or order worker failed to stop before database cleanup")
		}
	})
	client := &http.Client{Timeout: time.Second}
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/readyz", nil)
		response, e := client.Do(request)
		if e == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("main readiness failed")
			}
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("main exited before readiness: %v", err)
		case <-ctx.Done():
			t.Fatal("main readiness timed out")
		default:
		}
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/v1/orders", nil)
	for _, entry := range []string{"admin", "rider"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/v1/auth/staff/start", nil)
		req.Host = entry + ".example.test"
		noRedirect := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		loc, err := res.Location()
		if res.StatusCode != http.StatusFound || err != nil || loc.Host != "idp.example.test" ||
			loc.Query().Get("redirect_uri") != "https://"+req.Host+"/api/v1/auth/staff/callback" || loc.Query().Get("code_challenge_method") != "S256" {
			t.Errorf("%s start did not route to its configured IdP callback: HTTP %d", entry, res.StatusCode)
		}
	}
	if _, err := p.Exec(ctx, `INSERT INTO accounts(id,station_id,issuer,subject)VALUES('sec','s','https://idp.example.test','security');
		INSERT INTO role_bindings(account_id,role)VALUES('sec','SECURITY_ADMIN')`); err != nil {
		t.Fatal(err)
	}
	staffToken, staffSession, err := identity.NewSessions(p, contract.SystemClock{}).Issue(ctx, "sec", identity.AdminEntry, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		issuer string
		status int
	}{
		{"https://other.example.test", http.StatusBadRequest},
		{"https://idp.example.test", http.StatusCreated},
		{"https://idp.example.test", http.StatusConflict},
	} {
		body := fmt.Sprintf(`{"issuer":%q,"subject":"fixture-rider","display_name":"Fixture rider","reason_code":"ONBOARDING"}`, tc.issuer)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/api/v1/admin/riders/provision", strings.NewReader(body))
		req.Host = "admin.example.test"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://admin.example.test")
		req.Header.Set("X-CSRF-Token", staffSession.CSRFToken)
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: staffToken})
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Errorf("rider provision with issuer %q: got %d, want %d", tc.issuer, res.StatusCode, tc.status)
		}
	}
	request.Host = "user.example.test"
	request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "{\"items\":[],\"next_cursor\":null}\n" {
		t.Fatalf("real main order response: status=%d body=%s error=%v", response.StatusCode, body, err)
	}
	// Seed a legal historical order after the list check. Only identifiers may
	// leave the worker through the actual main-wired operational channel.
	if _, err = p.Exec(ctx, `
INSERT INTO accounts(id,station_id,issuer,subject)VALUES('op','s','synthetic','op');
INSERT INTO role_bindings(account_id,role)VALUES('op','OPERATOR');
INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity)
 VALUES('past','s',now()-interval '20 minutes',now()-interval '5 minutes',now()-interval '25 minutes',1);
INSERT INTO quotes(id,actor_id,station_id,mode,method,slot_id,address_snapshot,slot_snapshot,priced_snapshot,valid_until,created_at)
 VALUES('q','c','s','ASAP','COD','past','{"Room":"private-room","ContactPhone":"private-phone"}',
 jsonb_build_object('ID','past','StationID','s','EndsAt',now()-interval '5 minutes'),'{}',now()+interval '5 minutes',now());
INSERT INTO orders(id,quote_id,actor_id,station_id,mode,method,slot_id,address_snapshot,slot_snapshot,total_fen,fee_fen,item_count,status,created_at)
 SELECT 'historic',id,actor_id,station_id,mode,method,slot_id,address_snapshot,slot_snapshot,0,0,1,'PREPARING',now() FROM quotes WHERE id='q';`); err != nil {
		t.Fatal(err)
	}
	alertWait, stopAlertWait := context.WithTimeout(ctx, 5*time.Second)
	defer stopAlertWait()
	for {
		select {
		case raw := <-logs:
			var event map[string]any
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event["msg"] != "order_operational_alert" {
				continue
			}
			var jobID int64
			if err := p.QueryRow(ctx, `SELECT id FROM outbox_jobs WHERE station_id='s' AND kind='OVERDUE_ALERT' AND dedupe_key='historic'`).Scan(&jobID); err != nil {
				t.Fatal(err)
			}
			eventID, validID := event["event_id"].(string)
			if event["kind"] != "OVERDUE_ALERT" || event["order_id"] != "historic" || event["station_id"] != "s" || !validID || eventID != fmt.Sprintf("fulfillment-outbox:%d", jobID) {
				t.Fatalf("wrong operational alert: %s", raw)
			}
			recipients, ok := event["recipient_ids"].([]any)
			if !ok || len(recipients) != 1 || recipients[0] != "op" || len(event) != 8 {
				t.Fatalf("wrong recipients or unexpected payload: %s", raw)
			}
			t.Log("actual main served P05, consumed overdue job and emitted a contact-free operational alert while orders/dispatch paused; shutdown joins worker")
			return
		case <-alertWait.Done():
			t.Fatal("actual main did not deliver overdue operational alert")
		}
	}
}
