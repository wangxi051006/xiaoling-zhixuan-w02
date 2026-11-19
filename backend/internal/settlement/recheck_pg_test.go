//go:build integration

package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recheckClock struct{ now time.Time }

func (c *recheckClock) Now() time.Time { return c.now }
func rcScalar(t *testing.T, p *pgxpool.Pool, ctx context.Context, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := p.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func rcDo(ctx context.Context, p *pgxpool.Pool, f func(pgx.Tx) error) error {
	return platform.InTransaction(ctx, p, f)
}
func rcCreate(ctx context.Context, p *pgxpool.Pool, id string, method contract.PaymentMethod, amount int64) error {
	return rcDo(ctx, p, func(tx pgx.Tx) error {
		return (Store{Policy: PaymentPolicy{CODEnabled: true, BalanceEnabled: true}}).CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: id, ActorID: "buyer", StationID: "s", PaymentMethod: method, TotalFen: amount})
	})
}
func rcHTTP(t *testing.T, h *HTTP) http.Handler {
	t.Helper()
	m := http.NewServeMux()
	h.Register(m)
	return platform.NewHandler(m, nil, nil, 0)
}
func rcAuth(_ http.ResponseWriter, r *http.Request, _ identity.Entry, _ bool, _ ...contract.Role) (identity.Session, bool) {
	a := r.Header.Get("Test-Actor")
	if a == "" {
		a = "finance"
	}
	s := r.Header.Get("Test-Station")
	if s == "" {
		s = "s"
	}
	return identity.Session{AccountID: a, StationID: s}, true
}
func rcCall(t *testing.T, h http.Handler, method, path, body, key string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	if want >= 400 {
		var e map[string]any
		if json.Unmarshal(w.Body.Bytes(), &e) != nil || len(e) != 3 || e["request_id"] == "" {
			t.Fatal("invalid error envelope")
		}
	}
	return w
}

func TestRecheckHTTPExactJSON(t *testing.T) {
	p, ctx := settlementFixture(t)
	h := HTTP{Pool: p, Authorize: rcAuth}
	m := http.NewServeMux()
	m.HandleFunc("POST /api/v1/shape", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Amount int64  `json:"actual_fen"`
			Reason string `json:"reason_code"`
		}
		h.mutate(w, r, identity.Session{AccountID: "finance", StationID: "s"}, "shape", 200, &in, func(tx pgx.Tx) (commandResult, error) { return commandResult{body: in, objectID: "shape"}, nil })
	})
	handler := platform.NewHandler(m, nil, nil, 0)
	for _, body := range []string{`{"ACTUAL_FEN":1,"reason_code":"R"}`, `{"actual_fen":2,"actual_fen":1,"reason_code":"R"}`, `{"actual_fen":null,"reason_code":"R"}`, `{"actual_fen":1,"reason_code":null}`, `{"reason_code":"R"}`, `{"actual_fen":1}`, `null`, `{"actual_fen":1,"reason_code":"R","extra":1}`, `{"actual_fen":{},"reason_code":"R"}`, `{"actual_fen":1.1,"reason_code":"R"}`, `{"actual_fen":1,"reason_code":"R"} {}`} {
		t.Run(body, func(t *testing.T) { rcCall(t, handler, "POST", "/shape", body, "shape-key", 400) })
	}
	if n := rcScalar(t, p, ctx, `SELECT count(*) FROM idempotency_keys`); n != 0 {
		t.Errorf("invalid input wrote %d keys", n)
	}
}

func TestRecheckBatchRejectsUncollectedReceipt(t *testing.T) {
	p, ctx := settlementFixture(t)
	if err := rcCreate(ctx, p, "o", contract.PaymentMethodCod, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO cod_receipts(order_id,rider_id,amount_fen,collected_at) VALUES('o','rider',1,'2026-11-18T16:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	err := rcDo(ctx, p, func(tx pgx.Tx) error {
		_, err := (Store{}).CreateCODBatch(ctx, tx, "s", "fin", "rider", "2026-11-19", []string{"o"})
		return err
	})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("uncollected receipt entered batch: %v", err)
	}
}

func TestRecheckMissingClock(t *testing.T) {
	p, _ := settlementFixture(t)
	h := HTTP{Pool: p, Authorize: rcAuth}
	rcCall(t, rcHTTP(t, &h), "GET", "/payment-methods", "", "", 503)
}
