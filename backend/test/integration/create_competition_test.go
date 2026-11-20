//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
)

func (f *fixture) quoteSKU(actor, sku, mode, slot string, qty int) string {
	f.t.Helper()
	address := "a"
	if actor == "other" {
		address = "a2"
	}
	extra := ""
	if mode == "SCHEDULED" {
		extra = fmt.Sprintf(`,"slot_id":%q`, slot)
	}
	out := f.call(actor, "POST", "/checkout/quote", fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":%d}],"address_id":%q,"delivery_mode":%q,"payment_method":"BALANCE"%s}`, sku, qty, address, mode, extra), 201)
	return out["quote_id"].(string)
}
func TestP05WholeOrderLastPhysicalStockExactBarrier(t *testing.T) {
	for _, packWins := range []bool{true, false} {
		t.Run(fmt.Sprint(packWins), func(t *testing.T) {
			f := setup(t)
			f.exec(`UPDATE inventory SET on_hand=3 WHERE id=$1`, f.inventory)
			sku1, sku2 := f.pack, f.sku
			units, amount := int64(3), int64(270)
			if !packWins {
				sku1, sku2 = sku2, sku1
				units, amount = 1, 100
			}
			q1 := f.quoteSKU("buyer", sku1, "ASAP", "", 1)
			q2 := f.quoteSKU("other", sku2, "SCHEDULED", "later", 1)
			lock, pid := f.barrier("CREATED")
			first := f.start("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q1), "stock-first")
			winner := f.waitBlocked(first, pid)
			second := f.start("other", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q2), "stock-second")
			f.waitBlocked(second, winner)
			f.release(lock)
			f.finish(first, 201)
			w := f.finish(second, 409)
			if !strings.Contains(w.Body.String(), "OUT_OF_STOCK") {
				t.Fatal(w.Body)
			}
			if f.n(`SELECT count(*) FROM orders`) != 1 || f.n(`SELECT reserved FROM inventory`) != units || f.n(`SELECT on_hand FROM inventory`) != 3 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 1 || f.n(`SELECT used FROM delivery_slots WHERE id='later'`) != 0 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000-amount || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='other'`) != 10000 || f.n(`SELECT count(*) FROM wallet_entries WHERE kind='DEBIT'`) != 1 || f.n(`SELECT count(*) FROM idempotency_keys WHERE key='stock-second'`) != 0 {
				t.Fatal("whole-order stock race left partial ports")
			}
		})
	}
}
func TestP05WholeOrderASAPAndScheduledLastCapacityExactBarrier(t *testing.T) {
	for _, scheduledWins := range []bool{true, false} {
		t.Run(fmt.Sprint(scheduledWins), func(t *testing.T) {
			f := setup(t)
			f.exec(`UPDATE delivery_slots SET capacity=1,dispatch_at=$1 WHERE id='asap'`, f.clock.Now().Add(5*60*1000000000))
			mode1, mode2 := "SCHEDULED", "ASAP"
			if !scheduledWins {
				mode1, mode2 = mode2, mode1
			}
			q1 := f.quoteSKU("buyer", f.sku, mode1, "asap", 1)
			q2 := f.quoteSKU("other", f.sku, mode2, "asap", 1)
			lock, pid := f.barrier("CREATED")
			first := f.start("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q1), "slot-first")
			winner := f.waitBlocked(first, pid)
			second := f.start("other", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q2), "slot-second")
			f.waitBlocked(second, winner)
			f.release(lock)
			f.finish(first, 201)
			w := f.finish(second, 409)
			if !strings.Contains(w.Body.String(), "SLOT_FULL") {
				t.Fatal(w.Body)
			}
			if f.n(`SELECT count(*) FROM orders`) != 1 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 1 || f.n(`SELECT reserved FROM inventory`) != 1 || f.n(`SELECT count(*) FROM settlements`) != 1 || f.n(`SELECT count(*) FROM slot_reservations`) != 1 || f.n(`SELECT count(*) FROM order_events WHERE kind='CREATED'`) != 1 || f.n(`SELECT count(*) FROM idempotency_keys WHERE key='slot-second'`) != 0 {
				t.Fatal("capacity race partial ports")
			}
		})
	}
}
func TestP05WholeOrderConcurrentCODOrdersAndIdempotentReplayExactBarrier(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(fmt.Sprint(sameKey), func(t *testing.T) {
			f := setup(t)
			q1 := f.quote("buyer", "ASAP", "COD")
			q2 := q1
			key2 := "create-first"
			wantOrders := int64(1)
			if !sameKey {
				q2 = f.quote("buyer", "ASAP", "COD")
				key2 = "create-second"
				wantOrders = 2
			}
			lock, pid := f.barrier("CREATED")
			first := f.start("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q1), "create-first")
			winner := f.waitBlocked(first, pid)
			second := f.start("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q2), key2)
			f.waitBlocked(second, winner)
			f.release(lock)
			a := f.finish(first, 201)
			b := f.finish(second, 201)
			if (a.Body.String() == b.Body.String()) != sameKey {
				t.Fatal("only a replay should return the same order")
			}
			if f.n(`SELECT count(*) FROM orders WHERE actor_id='buyer' AND method='COD' AND status='PREPARING'`) != wantOrders ||
				f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != wantOrders ||
				f.n(`SELECT reserved FROM inventory`) != wantOrders ||
				f.n(`SELECT count(*) FROM settlements WHERE state='COD_UNCOLLECTED'`) != wantOrders ||
				f.n(`SELECT count(*) FROM cod_receipts`) != 0 {
				t.Fatal("unexpected COD orders, reservations or settlements")
			}
		})
	}
}
func TestP05PrepareVersusCustomerCancelExactBarrier(t *testing.T) {
	for _, prepareWins := range []bool{true, false} {
		t.Run(fmt.Sprint(prepareWins), func(t *testing.T) {
			f := setup(t)
			id := f.order("buyer", "ASAP", "BALANCE")
			kind := "PREPARED"
			actor1, actor2 := "op", "buyer"
			path1, path2 := "/admin/orders/"+id+"/prepare", "/orders/"+id+"/cancel"
			body1, body2 := `{"sorted":true}`, `{"reason_code":"CUSTOMER_CANCEL"}`
			if !prepareWins {
				kind = "CANCELED"
				actor1, actor2 = actor2, actor1
				path1, path2 = path2, path1
				body1, body2 = body2, body1
			}
			lock, pid := f.barrier(kind)
			first := f.start(actor1, "POST", path1, body1, "prepare-winner")
			winner := f.waitBlocked(first, pid)
			second := f.start(actor2, "POST", path2, body2, "prepare-loser")
			f.waitBlocked(second, winner)
			f.release(lock)
			f.finish(first, 200)
			f.finish(second, 409)
			if prepareWins {
				if f.n(`SELECT count(*) FROM orders WHERE status='PREPARING' AND stock_status='SORTED'`) != 1 || f.n(`SELECT reserved FROM inventory`) != 1 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 1 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 9900 {
					t.Fatal("prepare winner invariant")
				}
			} else if f.n(`SELECT count(*) FROM orders WHERE status='CANCELED' AND stock_status='RESTOCKED'`) != 1 || f.n(`SELECT reserved FROM inventory`) != 0 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 0 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000 {
				t.Fatal("cancel winner invariant")
			}
		})
	}
}
