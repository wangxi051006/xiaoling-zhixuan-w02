//go:build integration

package settlement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rcHTTPSetup(t *testing.T, kind string) (*pgxpool.Pool, context.Context, *HTTP, string, string, int, string) {
	t.Helper()
	p, ctx := settlementFixture(t)
	h := &HTTP{Pool: p, Clock: &recheckClock{time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC)}, Authorize: rcAuth, Refunds: &recheckRefundCommands{}}
	if kind == "approve" {
		rcWallet(t, p, ctx, 100)
		if err := rcCreate(ctx, p, "a", contract.PaymentMethodBalance, 100); err != nil {
			t.Fatal(err)
		}
		a := rcRequest(t, p, ctx, "a")
		return p, ctx, h, "/admin/refunds/" + a.RefundID + "/approve", `{"reason_code":"APPROVED"}`, 200, "settlements"
	}
	rcCollect(t, p, ctx, "a", 100)
	var date string
	if err := p.QueryRow(ctx, `SELECT (collected_at AT TIME ZONE 'Asia/Shanghai')::date::text FROM cod_receipts`).Scan(&date); err != nil {
		t.Fatal(err)
	}
	if kind == "payout" {
		a := rcRequest(t, p, ctx, "a")
		if err := rcDo(ctx, p, func(tx pgx.Tx) error { return (Store{}).ApproveRefund(ctx, tx, a) }); err != nil {
			t.Fatal(err)
		}
		return p, ctx, h, "/admin/refunds/" + a.RefundID + "/record-cod-payout", `{"receipt_reference":"synthetic-proof"}`, 200, "settlements"
	}
	rcCollect(t, p, ctx, "b", 200)
	if kind == "batch" {
		return p, ctx, h, "/admin/cod/batches", fmt.Sprintf(`{"rider_id":"rider","local_date":%q,"receipt_ids":["a","b"]}`, date), 201, "cod_receipts"
	}
	var b CODBatch
	if err := rcDo(ctx, p, func(tx pgx.Tx) error {
		var err error
		b, err = (Store{}).CreateCODBatch(ctx, tx, "s", "fin-a", "rider", date, []string{"a", "b"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return p, ctx, h, "/admin/cod/batches/" + b.ID + "/verify", `{"actual_fen":299,"reason_code":"COUNTED"}`, 200, "cod_batches"
}

func rcBusinessFingerprint(t *testing.T, p *pgxpool.Pool, ctx context.Context) string {
	t.Helper()
	var parts []string
	for _, table := range []string{"settlements", "wallets", "wallet_entries", "cod_receipts", "cod_batches", "cod_batch_receipts", "cod_batch_verifications", "refunds", "cod_refund_payouts", "audit_events", "idempotency_keys"} {
		var v string
		if err := p.QueryRow(ctx, `SELECT coalesce(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS v FROM `+table+` t) s`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, "\n")
}

func rcLogicalJSON(t *testing.T, body string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRecheckHTTPWriteFaultsAndReplay(t *testing.T) {
	for _, kind := range []string{"batch", "verify", "approve", "payout"} {
		for _, fault := range []string{"audit", "idempotency", "business"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				p, ctx, h, path, body, status, _ := rcHTTPSetup(t, kind)
				table, event, when := "audit_events", "INSERT", "true"
				if fault == "idempotency" {
					table, event = "idempotency_keys", "UPDATE"
				}
				if fault == "business" {
					switch kind {
					case "batch":
						table, when = "cod_batch_receipts", "NEW.receipt_id='b'"
					case "verify":
						table = "cod_batch_verifications"
					case "approve", "payout":
						h.Refunds.(*recheckRefundCommands).failAfter = true
					}
				}
				before := rcBusinessFingerprint(t, p, ctx)
				if fault != "business" || kind == "batch" || kind == "verify" {
					rcFault(t, p, ctx, table, event, when)
				}
				handler := rcHTTP(t, h)
				rcCall(t, handler, "POST", path, body, "atomic-key", 500)
				if got := rcBusinessFingerprint(t, p, ctx); got != before {
					t.Fatal("HTTP fault partially committed money/audit/idempotency")
				}
				if fault == "business" && (kind == "approve" || kind == "payout") {
					adapter := h.Refunds.(*recheckRefundCommands)
					if adapter.seen == nil {
						t.Fatal("refund adapter not called with transaction")
					}
					adapter.failAfter = false
				} else {
					rcClearFault(t, p, ctx, table)
				}
				first := rcCall(t, handler, "POST", path, body, "atomic-key", status)
				again := rcCall(t, handler, "POST", path, body, "atomic-key", status)
				if rcLogicalJSON(t, first.Body.String()) != rcLogicalJSON(t, again.Body.String()) {
					t.Fatal("same key changed logical response")
				}
				var changed string
				switch kind {
				case "batch":
					changed = strings.Replace(body, "rider\"", "other\"", 1)
				case "verify":
					changed = strings.Replace(body, "299", "298", 1)
				case "approve":
					changed = `{"reason_code":"OTHER"}`
				case "payout":
					changed = `{"receipt_reference":"other-proof"}`
				}
				w := rcCall(t, handler, "POST", path, changed, "atomic-key", 409)
				if !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
					t.Fatal("wrong replay error")
				}
				if rcScalar(t, p, ctx, `SELECT count(*) FROM audit_events`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM idempotency_keys`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM audit_events WHERE actor_id<>'finance' OR station_id<>'s' OR request_id=''`) != 0 {
					t.Fatal("audit/key identity or replay duplicated")
				}
			})
		}
	}
}

func TestRecheckHTTPConcurrentIdempotency(t *testing.T) {
	for _, kind := range []string{"batch", "verify", "approve", "payout"} {
		t.Run(kind, func(t *testing.T) {
			p, ctx, h, path, body, status, lockTable := rcHTTPSetup(t, kind)
			cfg := p.Config()
			name := "http_" + strings.Trim(cfg.ConnConfig.RuntimeParams["search_path"], `"`)
			cfg.ConnConfig.RuntimeParams["application_name"] = name
			contenders, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer contenders.Close()
			h.Pool = contenders
			handler := rcHTTP(t, h)
			gate, err := p.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err := gate.Exec(ctx, `SELECT 1 FROM `+lockTable+` FOR UPDATE`); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			out := make(chan *httptest.ResponseRecorder, 2)
			for range 2 {
				go func() {
					<-start
					r := httptest.NewRequest("POST", "/api/v1"+path, strings.NewReader(body))
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("Idempotency-Key", "concurrent-key")
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					out <- w
				}()
			}
			close(start)
			for rcScalar(t, p, ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'`, name) != 2 {
				select {
				case w := <-out:
					t.Fatalf("HTTP contender escaped barrier: %d", w.Code)
				case <-ctx.Done():
					t.Fatal("HTTP barrier timeout")
				default:
					runtime.Gosched()
				}
			}
			if err := gate.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			a, b := <-out, <-out
			if a.Code != status || b.Code != status || rcLogicalJSON(t, a.Body.String()) != rcLogicalJSON(t, b.Body.String()) {
				t.Fatalf("concurrent replay status %d/%d want=%d", a.Code, b.Code, status)
			}
			if rcScalar(t, p, ctx, `SELECT count(*) FROM idempotency_keys`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM audit_events`) != 1 {
				t.Fatal("concurrent replay duplicate")
			}
		})
	}
}

func TestRecheckHTTPPaginationAndFrozenInputs(t *testing.T) {
	p, ctx := settlementFixture(t)
	clock := &recheckClock{time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)}
	h := HTTP{Pool: p, Clock: clock, Authorize: rcAuth}
	handler := rcHTTP(t, &h)
	if _, err := p.Exec(ctx, `INSERT INTO settlements(order_id,actor_id,station_id,method,amount_fen,state) SELECT 'receipt-'||lpad(n::text,3,'0'),'buyer','s','COD',1,'COD_COLLECTED' FROM generate_series(1,95)n;INSERT INTO cod_receipts(order_id,rider_id,amount_fen,collected_at) SELECT order_id,'rider',1,'2026-12-31T16:00:00Z' FROM settlements;INSERT INTO refunds(order_id,source,operator_id,request_reason)SELECT order_id,'MANUAL','ops','R' FROM settlements`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/cod/receipts", "/admin/refunds"} {
		w := rcCall(t, handler, "GET", path, "", "", 200)
		var first map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &first) != nil || len(first) != 2 {
			t.Fatal("list envelope")
		}
		var list []map[string]any
		_ = json.Unmarshal(first["items"], &list)
		if len(list) != 20 {
			t.Fatal("default page size")
		}
		wantFields := 6
		if path == "/admin/cod/receipts" {
			wantFields = 6
		}
		if len(list[0]) != wantFields {
			t.Fatal("list public whitelist")
		}
		cursor := ""
		seen := map[string]bool{}
		for {
			query := "?limit=17"
			if cursor != "" {
				query += "&cursor=" + url.QueryEscape(cursor)
			}
			w := rcCall(t, handler, "GET", path+query, "", "", 200)
			var page struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				Next *string `json:"next_cursor"`
			}
			if json.Unmarshal(w.Body.Bytes(), &page) != nil {
				t.Fatal("invalid page")
			}
			for _, row := range page.Items {
				if seen[row.ID] {
					t.Fatal("pagination duplicate")
				}
				seen[row.ID] = true
			}
			if page.Next == nil {
				break
			}
			if len(*page.Next) > 128 {
				t.Fatal("cursor over frozen bound")
			}
			cursor = *page.Next
		}
		if len(seen) != 95 {
			t.Fatalf("pagination lost rows: %d", len(seen))
		}
		rcCall(t, handler, "GET", path+"?limit=100", "", "", 200)
		for _, q := range []string{"?limit=0", "?limit=101", "?limit=1&limit=2", "?cursor=", "?cursor=" + strings.Repeat("a", 129), "?unknown=1"} {
			rcCall(t, handler, "GET", path+q, "", "", 400)
		}
	}
	w := rcCall(t, handler, "GET", "/admin/cod/receipts?date=2026-12-31", "", "", 200)
	if strings.TrimSpace(w.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatal("Shanghai new year default date wrong")
	}
	for _, date := range []string{"0000-01-01", "2026-02-29", "2026-13-01", "2026-1-01"} {
		rcCall(t, handler, "GET", "/admin/cod/receipts?date="+date, "", "", 400)
	}
	for _, path := range []string{"/payment-methods", "/me/wallet"} {
		rcCall(t, handler, "GET", path+"?actor_id=other", "", "", 400)
	}
	for _, kind := range []string{"batch", "verify", "approve", "payout"} {
		p2, _, configured, path, valid, _, _ := rcHTTPSetup(t, kind)
		_ = p2
		handler := rcHTTP(t, configured)
		var object map[string]json.RawMessage
		_ = json.Unmarshal([]byte(valid), &object)
		for field, value := range object {
			for _, mut := range []string{"missing", "null", "case", "duplicate", "nested"} {
				parts := []string{}
				for k, v := range object {
					if k == field {
						switch mut {
						case "missing":
							continue
						case "null":
							v = json.RawMessage(`null`)
						case "case":
							k = strings.ToUpper(k)
						case "duplicate":
							parts = append(parts, fmt.Sprintf("%q:%s", k, v))
						case "nested":
							v = json.RawMessage(`{}`)
						}
					}
					parts = append(parts, fmt.Sprintf("%q:%s", k, v))
				}
				bad := "{" + strings.Join(parts, ",") + "}"
				_ = value
				rcCall(t, handler, "POST", path, bad, "invalid-key", 400)
			}
		}
		rcCall(t, handler, "POST", path, valid, "", 400)
		rcCall(t, handler, "POST", path, valid, strings.Repeat("k", 129), 400)
		rcCall(t, handler, "POST", path, strings.Repeat(" ", 32769), "large-key", 413)
	}
}

func TestRecheckHTTPMissingDependenciesAllRoutes(t *testing.T) {
	p, _ := settlementFixture(t)
	clock := &recheckClock{time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)}
	for _, missing := range []string{"pool", "clock", "authorize"} {
		h := HTTP{Pool: p, Clock: clock, Authorize: rcAuth}
		switch missing {
		case "pool":
			h.Pool = nil
		case "clock":
			h.Clock = nil
		case "authorize":
			h.Authorize = nil
		}
		handler := rcHTTP(t, &h)
		for _, route := range []struct{ method, path string }{{"GET", "/payment-methods"}, {"GET", "/me/wallet"}, {"GET", "/admin/cod/receipts"}, {"POST", "/admin/cod/batches"}, {"POST", "/admin/cod/batches/id/verify"}, {"GET", "/admin/refunds"}, {"POST", "/admin/refunds/id/approve"}, {"POST", "/admin/refunds/id/record-cod-payout"}} {
			rcCall(t, handler, route.method, route.path, `{}`, "key", 503)
		}
	}
	h := HTTP{Pool: p, Clock: clock, Authorize: rcAuth}
	handler := rcHTTP(t, &h)
	for _, path := range []string{"/admin/refunds/id/approve", "/admin/refunds/id/record-cod-payout"} {
		rcCall(t, handler, "POST", path, `{}`, "key", 503)
	}
}

func TestRecheckHTTPAmountAndCommandBounds(t *testing.T) {
	p, ctx, h, path, _, _, _ := rcHTTPSetup(t, "verify")
	handler := rcHTTP(t, h)
	for _, amount := range []string{"-1", "9007199254740992", "9223372036854775808", "1.1", "1e2", `"1"`} {
		body := fmt.Sprintf(`{"actual_fen":%s,"reason_code":"COUNTED"}`, amount)
		rcCall(t, handler, "POST", path, body, "bounds", 400)
	}
	for _, body := range []string{`{"actual_fen":1,"reason_code":"lower"}`, `{"actual_fen":1,"reason_code":""}`, `{"actual_fen":1,"reason_code":"` + strings.Repeat("R", 65) + `"}`, `{"actual_fen":1,"reason_code":"R","expected_version":1}`} {
		rcCall(t, handler, "POST", path, body, "bounds", 400)
	}
	// The frozen CODVerify schema has no expected_version. Its immutable one-shot
	// report, database unique key and Idempotency-Key govern concurrent verification.
	rcCall(t, handler, "POST", path, `{"actual_fen":9007199254740991,"reason_code":"`+strings.Repeat("R", 64)+`"}`, strings.Repeat("界", 128), 200)
	if rcScalar(t, p, ctx, `SELECT actual_fen FROM cod_batch_verifications`) != maxMoneyFen {
		t.Fatal("lost integer precision")
	}
	_, _, batch, bpath, valid, _, _ := rcHTTPSetup(t, "batch")
	handler = rcHTTP(t, batch)
	for _, ids := range []string{`[]`, `["a","a"]`, `[null]`, `[1]`, `["` + strings.Repeat("a", 129) + `"]`} {
		var obj map[string]json.RawMessage
		_ = json.Unmarshal([]byte(valid), &obj)
		obj["receipt_ids"] = json.RawMessage(ids)
		data, _ := json.Marshal(obj)
		rcCall(t, handler, "POST", bpath, string(data), "ids", 400)
	}
}
