//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestP05RealHTTPDeliveredRefundApproveAndCODPayout(t *testing.T) {
	for _, method := range []string{"COD", "BALANCE"} {
		t.Run(method, func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "ASAP", method)
			task := f.pickup(id)
			f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
			f.service.AcceptNewOrders = false
			f.service.AllowDispatch = false
			f.service.Policy.CODEnabled = false
			f.service.Policy.BalanceEnabled = false
			refund := f.call("op", "POST", "/admin/orders/"+id+"/refund", `{"reason_code":"AFTERSALE"}`, 201)
			refundID := refund["id"].(string)
			f.exec(`INSERT INTO role_bindings(account_id,role) VALUES('op','FINANCE')`)
			f.issue("op")
			f.call("op", "POST", "/admin/refunds/"+refundID+"/approve", `{"reason_code":"SELF_REVIEW"}`, 409)
			f.call("fin", "POST", "/admin/refunds/"+refundID+"/approve", `{"reason_code":"APPROVED"}`, 200)
			if method == "COD" {
				if f.n(`SELECT count(*) FROM settlements WHERE state='REFUND_PENDING'`) != 1 || f.n(`SELECT count(*) FROM cod_refund_payouts`) != 0 {
					t.Fatal("approval fabricated payout")
				}
				f.call("op", "POST", "/admin/refunds/"+refundID+"/record-cod-payout", `{"receipt_reference":"synthetic-payout"}`, 409)
				f.call("fin2", "POST", "/admin/refunds/"+refundID+"/record-cod-payout", `{"receipt_reference":"synthetic-payout"}`, 200)
				f.call("fin2", "POST", "/admin/refunds/"+refundID+"/record-cod-payout", `{"receipt_reference":"synthetic-payout"}`, 200)
				if f.n(`SELECT count(*) FROM cod_refund_payouts`) != 1 {
					t.Fatal("duplicate payout")
				}
			} else if f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000 || f.n(`SELECT count(*) FROM wallet_entries WHERE kind='REFUND'`) != 1 {
				t.Fatal("balance refund missing")
			}
			if f.n(`SELECT count(*) FROM orders WHERE status='DELIVERED'`) != 1 || f.n(`SELECT count(*) FROM rider_earnings`) != 1 || f.n(`SELECT count(*) FROM settlements WHERE state='REFUNDED'`) != 1 {
				t.Fatal("refund rewrote fulfillment/income")
			}
			dashboard := f.call("fin", "GET", "/admin/dashboard", "", 200)
			if dashboard["net_collected_fen"] != float64(0) {
				t.Fatal("refund dashboard", dashboard)
			}
		})
	}
}
func TestP05RealHTTPRefundFailuresRollbackAndEligibility(t *testing.T) {
	for _, method := range []string{"COD", "BALANCE"} {
		t.Run(method, func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "ASAP", method)
			task := f.pickup(id)
			f.call("op", "POST", "/admin/orders/"+id+"/refund", `{"reason_code":"BEFORE_DELIVERY"}`, 409)
			f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
			refund := f.call("op", "POST", "/admin/orders/"+id+"/refund", `{"reason_code":"AFTERSALE"}`, 201)
			refundID := refund["id"].(string)
			f.exec(`CREATE FUNCTION p05_refund_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private refund audit fault'; END $$;CREATE TRIGGER p05_refund_fault BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION p05_refund_fault()`)
			f.call("fin", "POST", "/admin/refunds/"+refundID+"/approve", `{"reason_code":"APPROVED"}`, 500)
			if f.n(`SELECT count(*) FROM refunds WHERE finance_id IS NOT NULL`) != 0 || f.n(`SELECT count(*) FROM wallet_entries WHERE kind='REFUND'`) != 0 {
				t.Fatal("approval partial commit")
			}
			f.exec(`DROP TRIGGER p05_refund_fault ON audit_events`)
			f.call("fin", "POST", "/admin/refunds/"+refundID+"/approve", `{"reason_code":"APPROVED"}`, 200)
			if method == "COD" {
				f.exec(`CREATE TRIGGER p05_refund_fault BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION p05_refund_fault()`)
				f.call("fin2", "POST", "/admin/refunds/"+refundID+"/record-cod-payout", `{"receipt_reference":"cash-proof"}`, 500)
				if f.n(`SELECT count(*) FROM cod_refund_payouts`) != 0 || f.n(`SELECT count(*) FROM settlements WHERE state='REFUND_PENDING'`) != 1 {
					t.Fatal("payout partial commit")
				}
				f.exec(`DROP TRIGGER p05_refund_fault ON audit_events`)
				f.call("fin2", "POST", "/admin/refunds/"+refundID+"/record-cod-payout", `{"receipt_reference":"cash-proof"}`, 200)
			}
		})
	}
}
func TestP05UrgeThrottleNotificationOwnerAndHistoryPrivacy(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "COD")
	task := f.dispatch(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	f.call("other", "POST", "/orders/"+id+"/urge", "", 404)
	f.call("buyer", "POST", "/orders/"+id+"/urge", "", 202)
	f.call("buyer", "POST", "/orders/"+id+"/urge", "", 429)
	if _, e := f.service.RunDue(f.ctx, 100); e != nil {
		t.Fatal(e)
	}
	messages := f.call("r1", "GET", "/rider/messages", "", 200)
	items := messages["items"].([]any)
	if len(items) != 1 {
		t.Fatal(messages)
	}
	messageID := items[0].(map[string]any)["id"].(string)
	body := fmt.Sprintf(`{"ids":[%q]}`, messageID)
	f.call("r2", "POST", "/rider/messages/read", body, 404)
	f.call("r1", "POST", "/rider/messages/read", body, 200)
	if f.n(`SELECT count(*) FROM notifications WHERE recipient_id='r1' AND read_at IS NOT NULL`) != 1 {
		t.Fatal("notification ownership/read")
	}
	f.clock.Set(f.clock.Now().Add(time.Minute))
	f.call("buyer", "POST", "/orders/"+id+"/urge", "", 202)
	f.call("r1", "POST", "/rider/tasks/"+task+"/pickup", "", 200)
	f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
	f.call("buyer", "POST", "/orders/"+id+"/urge", "", 409)
	history := f.call("r1", "GET", "/rider/history", "", 200)
	wire, _ := json.Marshal(history)
	if strings.Contains(string(wire), "private-room") || strings.Contains(string(wire), "138000") || len(history["items"].([]any)) != 1 {
		t.Fatal("history contact projection")
	}
	other := f.call("r2", "GET", "/rider/history", "", 200)
	if len(other["items"].([]any)) != 0 {
		t.Fatal("foreign history")
	}
	f.call("other", "GET", "/rider/tasks/"+task, "", 404)
}
func TestP05ScheduledCustomerCutoffAndPausedHistoricalCancel(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprint(late), func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "SCHEDULED", "BALANCE")
			cutoff := f.clock.Now().Add(45 * time.Minute)
			if late {
				f.clock.Set(cutoff)
			} else {
				f.clock.Set(cutoff.Add(-time.Microsecond))
			}
			f.service.AcceptNewOrders = false
			f.service.AllowDispatch = false
			want := 200
			if late {
				want = 409
			}
			f.call("buyer", "POST", "/orders/"+id+"/cancel", `{"reason_code":"CUSTOMER_CANCEL"}`, want)
			if !late && (f.n(`SELECT used FROM delivery_slots WHERE id='later'`) != 0 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000) {
				t.Fatal("scheduled cancellation settlement/capacity")
			}
		})
	}
}
