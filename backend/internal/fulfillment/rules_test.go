package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/settlement"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestQuoteNormalizesAndBoundsTotalItems(t *testing.T) {
	got, err := normalizeLines([]contract.OrderLineInput{{SKUID: "b", Qty: 2}, {SKUID: "a", Qty: 1}, {SKUID: "b", Qty: 3}})
	want := []contract.OrderLineInput{{SKUID: "a", Qty: 1}, {SKUID: "b", Qty: 5}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("normalization got=%v err=%v", got, err)
	}
	for _, in := range [][]contract.OrderLineInput{nil, {{SKUID: "a", Qty: 0}}, {{SKUID: "a", Qty: 999}, {SKUID: "b", Qty: 1}}, {{SKUID: "", Qty: 1}}, {{SKUID: "a", Qty: 500}, {SKUID: "a", Qty: 500}}} {
		if _, err := normalizeLines(in); err == nil {
			t.Errorf("accepted unsafe quote %v", in)
		}
	}
}

func TestUnknownOrMissingPaymentIsMalformedAndWechatIsDisabled(t *testing.T) {
	s := Service{AcceptNewOrders: true, Policy: settlement.PaymentPolicy{CODEnabled: true, BalanceEnabled: true}}
	for _, method := range []contract.PaymentMethod{"", "FAKE", "WECHAT"} {
		_, err := s.Quote(context.Background(), nil, identity.Session{}, QuoteInput{Lines: []contract.OrderLineInput{{SKUID: "sku", Qty: 1}}, AddressID: "address", Mode: contract.DeliveryModeAsap, Method: method})
		want := ErrInvalid
		if method == "WECHAT" {
			want = settlement.ErrPaymentMethodDisabled
		}
		if !errors.Is(err, want) {
			t.Errorf("method %q error=%v want %v", method, err, want)
		}
	}
}
func TestSelfCancellationExactCutoff(t *testing.T) {
	d := time.Date(2026, 11, 19, 4, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		mode, status, stock string
		now                 time.Time
		want                bool
	}{
		{"SCHEDULED", "SCHEDULED", "UNSORTED", d.Add(-time.Nanosecond), true},
		{"SCHEDULED", "SCHEDULED", "UNSORTED", d, false},
		{"SCHEDULED", "PREPARING", "UNSORTED", d, false},
		{"ASAP", "PREPARING", "UNSORTED", d, true},
		{"ASAP", "PREPARING", "SORTED", d, false},
		{"ASAP", "READY_FOR_CLAIM", "SORTED", d, false},
	} {
		if got := selfCancelable(contract.DeliveryMode(tc.mode), contract.OrderStatus(tc.status), contract.StockStatus(tc.stock), d, tc.now); got != tc.want {
			t.Errorf("%+v got=%v", tc, got)
		}
	}
}
func TestExactJSONRejectsAmbiguousBodies(t *testing.T) {
	for _, v := range []string{`{"sorted":true,"sorted":true}`, `{"sorted":null}`, `{"sorted":true,"extra":1}`, `{"Sorted":true}`, `{"sorted":true} {}`, `null`, `[]`} {
		var in struct {
			Sorted bool `json:"sorted"`
		}
		if exactJSON([]byte(v), &in) == nil {
			t.Errorf("accepted %s", v)
		}
	}
	var in struct {
		Sorted bool `json:"sorted"`
	}
	if err := exactJSON([]byte(`{"sorted":true}`), &in); err != nil || !in.Sorted {
		t.Fatal(err)
	}
}
