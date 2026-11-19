//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestP05CreateEveryPersistenceStageRollsBackAllPorts(t *testing.T) {
	for _, table := range []string{"settlements", "order_events", "outbox_jobs", "audit_events"} {
		t.Run(table, func(t *testing.T) {
			f := setup(t)
			q := f.quote("buyer", "ASAP", "BALANCE")
			f.exec(fmt.Sprintf(`CREATE FUNCTION p05_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private synthetic fault'; END $$;CREATE TRIGGER p05_fault BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION p05_fault()`, table))
			body := fmt.Sprintf(`{"quote_id":%q}`, q)
			w := f.request("buyer", "POST", "/orders", body, "rollback-create")
			if w.Code != 500 {
				t.Fatal(w.Code, w.Body)
			}
			for _, sql := range []string{`SELECT count(*) FROM orders`, `SELECT count(*) FROM stock_reservations`, `SELECT count(*) FROM slot_reservations`, `SELECT count(*) FROM settlements`, `SELECT count(*) FROM wallet_entries`, `SELECT count(*) FROM order_events`, `SELECT count(*) FROM outbox_jobs`, `SELECT count(*) FROM idempotency_keys WHERE key='rollback-create'`, `SELECT reserved FROM inventory`} {
				if f.n(sql) != 0 {
					t.Fatal("residue after failure", sql)
				}
			}
			if f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 0 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000 {
				t.Fatal("port rollback failed")
			}
			f.exec(fmt.Sprintf(`DROP TRIGGER p05_fault ON %s`, table))
			w = f.request("buyer", "POST", "/orders", body, "rollback-create")
			if w.Code != 201 {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}
func TestP05DeliveryEveryPersistenceStageRollsBackCashAndIncome(t *testing.T) {
	for _, table := range []string{"cod_receipts", "rider_earnings", "order_events", "audit_events"} {
		t.Run(table, func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "ASAP", "COD")
			task := f.pickup(id)
			f.exec(fmt.Sprintf(`CREATE FUNCTION p05_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private cash fault'; END $$;CREATE TRIGGER p05_fault BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION p05_fault()`, table))
			w := f.request("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, "cash-rollback")
			if w.Code != 500 {
				t.Fatal(w.Code, w.Body)
			}
			if f.n(`SELECT count(*) FROM cod_receipts`) != 0 || f.n(`SELECT count(*) FROM rider_earnings`) != 0 || f.n(`SELECT count(*) FROM orders WHERE status='IN_DELIVERY'`) != 1 || f.n(`SELECT count(*) FROM settlements WHERE state='COD_UNCOLLECTED'`) != 1 || f.n(`SELECT count(*) FROM idempotency_keys WHERE key='cash-rollback'`) != 0 {
				t.Fatal("cash partial commit")
			}
			f.exec(fmt.Sprintf(`DROP TRIGGER p05_fault ON %s`, table))
			w = f.request("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, "cash-rollback")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}
func TestP05CancellationAndReturnSettlementFailureRollBack(t *testing.T) {
	for _, stage := range []string{"cancel", "return"} {
		t.Run(stage, func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "ASAP", "BALANCE")
			if stage == "return" {
				task := f.pickup(id)
				f.call("r1", "POST", "/rider/tasks/"+task+"/return", `{"reason_code":"REFUSED"}`, 200)
			}
			f.exec(`CREATE FUNCTION p05_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private refund fault'; END $$;CREATE TRIGGER p05_fault BEFORE INSERT ON refunds FOR EACH ROW EXECUTE FUNCTION p05_fault()`)
			path := "/admin/orders/" + id + "/cancel"
			body := `{"reason_code":"OPS_CANCEL"}`
			if stage == "return" {
				path = "/admin/orders/" + id + "/return-inspect"
				body = fmt.Sprintf(`{"reason_code":"ACCEPT","sellable":[{"inventory_id":%q,"sellable_units":1}]}`, f.inventory)
			}
			f.call("op", "POST", path, body, 500)
			if f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 9900 || f.n(`SELECT count(*) FROM refunds`) != 0 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 1 {
				t.Fatal("settlement rollback")
			}
			want := int64(1)
			if stage == "return" {
				want = 0
			}
			if f.n(`SELECT reserved FROM inventory WHERE id=$1`, f.inventory) != want {
				t.Fatal("stock rollback")
			}
			f.exec(`DROP TRIGGER p05_fault ON refunds`)
			f.call("op", "POST", path, body, 200)
		})
	}
}
func TestP05QuoteExpiryAddressPriceOriginalSlotAndBalance(t *testing.T) {
	for _, change := range []string{"expiry", "address", "price", "sale", "slot-full", "slot-expired", "balance"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			q := f.quote("buyer", "ASAP", "BALANCE")
			switch change {
			case "expiry":
				f.clock.Set(f.clock.Now().Add(5 * time.Minute))
			case "address":
				f.exec(`UPDATE addresses SET room='changed',version=version+1 WHERE id='a'`)
			case "price":
				f.exec(`UPDATE skus SET unit_price_fen=101 WHERE id=$1`, f.sku)
			case "sale":
				f.exec(`UPDATE products SET on_sale=false`)
			case "slot-full":
				f.exec(`UPDATE delivery_slots SET capacity=0 WHERE id='asap'`)
			case "slot-expired":
				f.clock.Set(f.clock.Now().Add(16 * time.Minute))
			case "balance":
				f.exec(`UPDATE wallets SET balance_fen=0 WHERE actor_id='buyer'`)
			}
			w := f.request("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), "revalidate")
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body)
			}
			code := "QUOTE_CHANGED"
			switch change {
			case "slot-full":
				code = "SLOT_FULL"
			case "slot-expired":
				code = "QUOTE_CHANGED"
			case "balance":
				code = "INSUFFICIENT_BALANCE"
			}
			if !strings.Contains(w.Body.String(), code) {
				t.Fatal(code, w.Body)
			}
			if f.n(`SELECT count(*) FROM orders`) != 0 || f.n(`SELECT used FROM delivery_slots WHERE id='later'`) != 0 || f.n(`SELECT reserved FROM inventory`) != 0 {
				t.Fatal("revalidation residue")
			}
		})
	}
}
func TestP05ScheduledLeaseRecoveryExactWakeAndOverdue(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "SCHEDULED", "BALANCE")
	f.call("op", "POST", "/admin/orders/"+id+"/prepare", `{"sorted":true}`, 409)
	base := f.clock.Now()
	f.clock.Set(base.Add(45*time.Minute - time.Nanosecond))
	if n, e := f.service.RunDue(f.ctx, 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	f.clock.Set(base.Add(45 * time.Minute))
	f.service.AcceptNewOrders = false
	f.service.AllowDispatch = false
	if n, e := f.service.RunDue(f.ctx, 100); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if f.n(`SELECT count(*) FROM orders WHERE status='PREPARING'`) != 1 || f.n(`SELECT count(*) FROM delivery_tasks`) != 0 {
		t.Fatal("wake dispatched prematurely")
	}
	if _, e := f.service.RunDue(f.ctx, 100); e != nil {
		t.Fatal(e)
	}
	if f.n(`SELECT count(*) FROM order_events WHERE kind='SCHEDULE_WOKE'`) != 1 {
		t.Fatal("wake duplicated")
	}
	f.exec(`UPDATE outbox_jobs SET lease_token='crashed',lease_until=$1 WHERE kind='ORDER_OVERDUE'`, base.Add(76*time.Minute))
	f.clock.Set(base.Add(75 * time.Minute))
	if n, e := f.service.RunDue(f.ctx, 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	f.clock.Set(base.Add(76 * time.Minute))
	if n, e := f.service.RunDue(f.ctx, 100); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if f.n(`SELECT count(*) FROM orders WHERE overdue AND slot_id='later' AND status='PREPARING'`) != 1 || f.n(`SELECT count(*) FROM outbox_jobs WHERE kind='OVERDUE_ALERT'`) != 1 {
		t.Fatal("late order not alerted/original slot lost")
	}
}
func TestP05ScanCompensationBeyondBoundedBatch(t *testing.T) {
	f := setup(t)
	ids := []string{}
	for i := 0; i < 3; i++ {
		ids = append(ids, f.order("buyer", "SCHEDULED", "BALANCE"))
	}
	f.exec(`DELETE FROM outbox_jobs WHERE dedupe_key=(SELECT max(id) FROM orders)`)
	f.clock.Set(f.clock.Now().Add(80 * time.Minute))
	for i := 0; i < 8; i++ {
		if _, e := f.service.RunDue(f.ctx, 1); e != nil {
			t.Fatal(e)
		}
	}
	if f.n(`SELECT count(*) FROM orders WHERE status='PREPARING' AND overdue`) != 3 {
		t.Fatal("bounded scan starved later order")
	}
	if f.n(`SELECT count(*) FROM order_events WHERE kind='SCHEDULE_WOKE'`) != 3 {
		t.Fatal("lost or duplicated wake")
	}
}
func TestP05ZeroFenCODAndUnsortedCancellationCapacity(t *testing.T) {
	f := setup(t)
	f.exec(`UPDATE skus SET unit_price_fen=0 WHERE id=$1`, f.sku)
	id := f.order("buyer", "ASAP", "COD")
	f.call("buyer", "POST", "/orders/"+id+"/cancel", `{"reason_code":"CUSTOMER_CANCEL"}`, 200)
	if f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 0 || f.n(`SELECT reserved FROM inventory`) != 0 || f.n(`SELECT count(*) FROM settlements WHERE state='VOID'`) != 1 {
		t.Fatal("unsorted cancel invariant")
	}
	id = f.order("buyer", "ASAP", "COD")
	task := f.pickup(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
	if f.n(`SELECT count(*) FROM cod_receipts WHERE amount_fen=0`) != 1 {
		t.Fatal("zero cash rejected")
	}
}
