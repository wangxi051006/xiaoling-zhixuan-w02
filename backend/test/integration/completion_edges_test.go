//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
)

func TestP05CODRefusalAllDamagedReturnKeepsSettlementIndependent(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "COD")
	task := f.pickup(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/return", `{"reason_code":"REFUSED"}`, 200)
	secondID := f.order("buyer", "ASAP", "COD")
	if f.n(`SELECT count(*) FROM cod_receipts`) != 0 || f.n(`SELECT count(*) FROM rider_earnings`) != 0 || f.n(`SELECT count(*) FROM settlements WHERE state='COD_UNCOLLECTED'`) != 2 {
		t.Fatal("refusal fabricated cash, income or early reversal")
	}
	f.call("op", "POST", "/admin/orders/"+id+"/return-inspect", `{"reason_code":"ALL_DAMAGED","sellable":[]}`, 200)
	if f.n(`SELECT on_hand FROM inventory`) != 99 || f.n(`SELECT reserved FROM inventory`) != 1 ||
		f.n(`SELECT count(*) FROM settlements WHERE order_id=$1 AND state='VOID'`, id) != 1 ||
		f.n(`SELECT count(*) FROM settlements WHERE order_id=$1 AND state='COD_UNCOLLECTED'`, secondID) != 1 {
		t.Fatal("return inspection did not close only the returned order's stock and settlement")
	}
}
func TestP05SuccessfulOwnerAdminReadsAndPreHandoffRefundCancellation(t *testing.T) {
	for _, method := range []string{"COD", "BALANCE"} {
		t.Run(method, func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "ASAP", method)
			f.order("other", "SCHEDULED", "BALANCE")
			own := f.call("buyer", "GET", "/orders?limit=1", "", 200)
			if len(own["items"].([]any)) != 1 {
				t.Fatal(own)
			}
			f.call("buyer", "GET", "/orders/"+id, "", 200)
			admin := f.call("op", "GET", "/admin/orders?limit=1", "", 200)
			if admin["next_cursor"] == nil {
				t.Fatal("admin pagination missing")
			}
			f.call("op", "GET", "/admin/orders/"+id, "", 200)
			refund := f.call("op", "POST", "/admin/orders/"+id+"/refund", `{"reason_code":"BEFORE_HANDOFF"}`, 201)
			if refund["order_id"] != id || f.n(`SELECT count(*) FROM orders WHERE id=$1 AND status='CANCELED'`, id) != 1 || f.n(`SELECT reserved FROM inventory`) != 1 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 0 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000 {
				t.Fatal("pre-handoff refund bypassed complete cancellation")
			}
			for _, path := range []string{"/orders?unexpected=x", "/orders?limit=1&limit=2", "/orders/" + id + "?cursor=x"} {
				f.call("buyer", "GET", path, "", 400)
			}
			w := f.request("buyer", "POST", "/orders", `{"quote_id":"x"}`, "")
			if w.Code != 400 {
				t.Fatal("missing idempotency accepted")
			}
			w = f.request("buyer", "POST", "/orders", `{"quote_id":"`+strings.Repeat("x", 33000)+`"}`, "body-limit")
			if w.Code != 413 {
				t.Fatal("large body accepted", w.Code)
			}
			q := f.quote("buyer", "ASAP", "BALANCE")
			w = f.sourceRequest("buyer", "untrusted-source", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), "invalid-source")
			if w.Code != 400 {
				t.Fatal("invalid source accepted", w.Code)
			}
		})
	}
}
