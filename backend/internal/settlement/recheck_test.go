package settlement

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
)

func TestRecheckNilCallerTransaction(t *testing.T) {
	s := Store{Policy: PaymentPolicy{CODEnabled: true}}
	for name, call := range map[string]func() error{
		"create": func() error {
			return s.CreateSettlement(context.Background(), nil, contract.OrderMoney{OrderID: "o", ActorID: "a", StationID: "s", PaymentMethod: contract.PaymentMethodCod, TotalFen: 1})
		},
		"collect": func() error { return s.RecordCODCollection(context.Background(), nil, "o", "r") },
		"cancel":  func() error { return s.CancelSettlement(context.Background(), nil, "o") },
		"return":  func() error { return s.FinalizeReturnSettlement(context.Background(), nil, "o") },
		"request": func() error {
			return s.RequestRefund(context.Background(), nil, contract.RefundRequest{OrderID: "o", OperatorID: "ops", ReasonCode: "R"})
		},
		"approve": func() error {
			return s.ApproveRefund(context.Background(), nil, contract.RefundApproval{OrderID: "o", RefundID: "f", FinanceID: "fin", ReasonCode: "R"})
		},
		"payout": func() error {
			return s.RecordCODPayout(context.Background(), nil, contract.CODPayout{OrderID: "o", RefundID: "f", FinanceID: "fin", ReceiptReference: "ref"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() != nil {
					t.Error("nil transaction panicked")
				}
			}()
			if err := call(); err == nil {
				t.Error("nil transaction accepted")
			}
		})
	}
}

func TestRecheckImportExactJSON(t *testing.T) {
	for _, data := range []string{
		`{"STATION_ID":"s","total_fen":1,"entries":[{"actor_id":"a","external_reference":"r","amount_fen":1}]}`,
		`{"station_id":"other","station_id":"s","total_fen":1,"entries":[{"actor_id":"a","external_reference":"r","amount_fen":1}]}`,
		`{"station_id":"s","total_fen":1,"entries":[{"ACTOR_ID":"a","external_reference":"r","amount_fen":1}]}`,
		`{"station_id":"s","total_fen":1,"entries":[{"actor_id":"other","actor_id":"a","external_reference":"r","amount_fen":1}]}`,
	} {
		auth := ImportAuthorization{ApprovalID: "A", ApproverID: "fin", ExecutorID: "exec", SHA256: sha256.Sum256([]byte(data))}
		if _, err := parseBalanceManifest([]byte(data), auth); !errors.Is(err, ErrInvalid) {
			t.Errorf("ambiguous import accepted: %v", err)
		}
	}
}

func TestRecheckCursorContract(t *testing.T) {
	// A valid 128-character order ID must produce a cursor within the frozen
	// maxLength=128, and that cursor must select the exact last returned ID.
	id := strings.Repeat("x", 128)
	p := paginate([]string{id, "y"}, 1, func(s string) string { return s })
	if p.Next == nil || len(*p.Next) > 128 {
		t.Fatal("generated cursor exceeds frozen maximum")
	}
	q, err := readPage("cursor="+url.QueryEscape(*p.Next), false)
	if err != nil || q.cursor != id {
		t.Fatalf("cursor roundtrip failed: %v", err)
	}
	if _, err := readPage("cursor="+strings.Repeat("a", 129), false); err == nil {
		t.Error("oversized cursor accepted")
	}
}

func TestRecheckPaymentErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{{ErrPaymentMethodDisabled, "PAYMENT_METHOD_DISABLED"}, {ErrInsufficientBalance, "INSUFFICIENT_BALANCE"}} {
		mux := httpTestHandler(tc.err)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/test", nil))
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != 409 || body["code"] != tc.code || body["request_id"] == "" || len(body) != 3 {
			t.Errorf("wrong business error: status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

// No database or authorization double is used for this error-mapping boundary.
func httpTestHandler(err error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/test", func(w http.ResponseWriter, r *http.Request) { writeSettlementError(w, r, err) })
	return platform.NewHandler(mux, nil, nil, 0)
}
