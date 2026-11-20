//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestStationDeliveryFeeCheckoutAndRiderEarnings(t *testing.T) {
	f := setup(t)
	const settings = "/admin/delivery-fee"
	assertAmount := func(body map[string]any, field string, want float64) {
		t.Helper()
		if body[field] != want {
			t.Fatalf("%s = %v, want %v in %v", field, body[field], want, body)
		}
	}
	quote := func(method string) map[string]any {
		t.Helper()
		return f.call("buyer", "POST", "/checkout/quote", fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":1}],"address_id":"a","delivery_mode":"ASAP","payment_method":%q}`, f.sku, method), 201)
	}
	orderBody := func(q map[string]any) string { return fmt.Sprintf(`{"quote_id":%q}`, q["quote_id"]) }
	setFee := func(fee, version int) string {
		return fmt.Sprintf(`{"delivery_fee_fen":%d,"expected_version":%d}`, fee, version)
	}

	initial := f.call("op", "GET", settings, "", 200)
	assertAmount(initial, "delivery_fee_fen", 0)
	assertAmount(initial, "cod_commission_bps", 1000)
	assertAmount(initial, "version", 1)
	f.call("fin", "PUT", settings, setFee(250, 1), 403)
	f.call("op", "PUT", settings, `{"expected_version":1}`, 400)
	f.call("op", "PUT", settings, setFee(-1, 1), 400)
	f.call("op", "PUT", settings, `{"delivery_fee_fen":2.5,"expected_version":1}`, 400)
	f.call("op", "PUT", settings, `{"delivery_fee_fen":0,"cod_commission_bps":10001,"expected_version":1}`, 400)

	legacy := quote("COD")
	assertAmount(legacy, "total_fen", 100)
	oldOrder := f.call("buyer", "POST", "/orders", orderBody(legacy), 201)
	stale := quote("COD")

	updated := f.call("op", "PUT", settings, setFee(250, 1), 200)
	assertAmount(updated, "delivery_fee_fen", 250)
	assertAmount(updated, "version", 2)
	f.call("op", "PUT", settings, setFee(999, 1), 409)
	if rejected := f.call("buyer", "POST", "/orders", orderBody(stale), 409); rejected["code"] != "QUOTE_CHANGED" {
		t.Fatal(rejected)
	}
	assertAmount(f.call("buyer", "GET", "/orders/"+oldOrder["id"].(string), "", 200), "delivery_fee_fen", 0)

	paidQuote := quote("COD")
	assertAmount(paidQuote, "subtotal_fen", 100)
	assertAmount(paidQuote, "delivery_fee_fen", 250)
	assertAmount(paidQuote, "total_fen", 350)
	newOrder := f.call("buyer", "POST", "/orders", orderBody(paidQuote), 201)
	assertAmount(newOrder, "subtotal_fen", 100)
	assertAmount(newOrder, "delivery_fee_fen", 250)
	assertAmount(newOrder, "total_fen", 350)
	orderID := newOrder["id"].(string)
	assertAmount(f.call("op", "GET", "/admin/orders/"+orderID, "", 200), "delivery_fee_fen", 250)
	if f.n(`SELECT amount_fen FROM settlements WHERE order_id=$1`, orderID) != 350 {
		t.Fatal("COD settlement did not include delivery fee")
	}

	f.call("op", "PUT", settings, `{"delivery_fee_fen":175,"cod_commission_bps":1250,"expected_version":2}`, 200)
	balanceQuote := quote("BALANCE")
	assertAmount(balanceQuote, "delivery_fee_fen", 175)
	balance := f.call("buyer", "POST", "/orders", orderBody(balanceQuote), 201)
	assertAmount(balance, "total_fen", 275)
	if f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 9725 {
		t.Fatal("balance payment did not include delivery fee")
	}

	task := f.dispatch(orderID)
	lobby := f.call("r1", "GET", "/rider/tasks", "", 200)["items"].([]any)
	card := lobby[0].(map[string]any)
	assertAmount(card, "fee_fen", 250)
	if _, ok := card["cod_due_fen"]; ok {
		t.Fatal("lobby exposed COD collection amount")
	}
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	pickup := f.call("r1", "POST", "/rider/tasks/"+task+"/pickup", "", 200)
	assertAmount(pickup, "cod_due_fen", 350)
	delivered := f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":true}`, 200)
	if _, ok := delivered["cod_due_fen"]; ok {
		t.Fatal("delivered task still shows payment due")
	}
	if got := f.n(`SELECT amount_fen FROM cod_receipts WHERE order_id=$1`, orderID); got != 350 {
		t.Fatalf("cash received = %d, want 350", got)
	}
	income := f.call("r1", "GET", "/rider/earnings", "", 200)["items"].([]any)
	if len(income) != 1 {
		t.Fatalf("unexpected earnings: %v", income)
	}
	assertAmount(income[0].(map[string]any), "amount_fen", 260)
	assertAmount(income[0].(map[string]any), "commission_fen", 10)

	var collectedDay string
	if err := f.pool.QueryRow(f.ctx, `SELECT (collected_at AT TIME ZONE 'Asia/Shanghai')::date::text FROM cod_receipts WHERE order_id=$1`, orderID).Scan(&collectedDay); err != nil {
		t.Fatal(err)
	}
	const settlements = "/admin/cod/merchant-settlements"
	payoutPath := settlements + "/" + orderID + "/record-payout"
	payoutBody := `{"receipt_reference":"TEST-MERCHANT-TRANSFER"}`
	list := func() map[string]any {
		t.Helper()
		items := f.call("fin", "GET", settlements+"?date="+collectedDay, "", 200)["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("unexpected merchant settlements: %v", items)
		}
		return items[0].(map[string]any)
	}
	merchant := list()
	assertAmount(merchant, "amount_fen", 90)
	if merchant["status"] != "WAITING_REMITTANCE" {
		t.Fatal(merchant)
	}
	f.call("fin", "POST", payoutPath, payoutBody, 409)
	batchBody := fmt.Sprintf(`{"rider_id":"r1","local_date":%q,"receipt_ids":[%q]}`, collectedDay, orderID)
	batch := f.call("fin", "POST", "/admin/cod/batches", batchBody, 201)["id"].(string)
	f.call("fin2", "POST", "/admin/cod/batches/"+batch+"/verify", `{"actual_fen":350,"reason_code":"COUNTED"}`, 200)
	if merchant = list(); merchant["status"] != "PAYABLE" {
		t.Fatal(merchant)
	}
	f.call("op", "POST", payoutPath, payoutBody, 403)
	first := f.request("fin", "POST", payoutPath, payoutBody, "merchant-transfer")
	replay := f.request("fin", "POST", payoutPath, payoutBody, "merchant-transfer")
	var firstResult, replayResult map[string]any
	if first.Code != 200 || replay.Code != 200 ||
		json.Unmarshal(first.Body.Bytes(), &firstResult) != nil ||
		json.Unmarshal(replay.Body.Bytes(), &replayResult) != nil ||
		!reflect.DeepEqual(firstResult, replayResult) {
		t.Fatalf("unexpected payout or replay: %d %s / %d %s", first.Code, first.Body, replay.Code, replay.Body)
	}
	f.call("fin", "POST", payoutPath, payoutBody, 409)
	if merchant = list(); merchant["status"] != "PAID" || merchant["receipt_reference"] != "TEST-MERCHANT-TRANSFER" {
		t.Fatal(merchant)
	}

	balanceTask := f.pickup(balance["id"].(string))
	f.call("r1", "POST", "/rider/tasks/"+balanceTask+"/deliver", "{}", 200)
	if got := f.n(`SELECT amount_fen FROM rider_earnings WHERE task_id=$1`, balanceTask); got != 175 {
		t.Fatalf("balance order delivery earnings = %d, want 175", got)
	}

	f.call("op", "PUT", settings, setFee(0, 3), 200)
	free := quote("COD")
	assertAmount(free, "delivery_fee_fen", 0)
	assertAmount(free, "total_fen", 100)
	configured := f.call("buyer", "POST", "/orders", orderBody(free), 201)["id"].(string)
	configuredTask := f.pickup(configured)
	f.call("r1", "POST", "/rider/tasks/"+configuredTask+"/deliver", `{"cod_received":true}`, 200)
	if got := f.n(`SELECT amount_fen FROM rider_earnings WHERE task_id=$1`, configuredTask); got != 13 {
		t.Fatalf("configured commission at 12.5%% = %d, want 13 fen", got)
	}
}
