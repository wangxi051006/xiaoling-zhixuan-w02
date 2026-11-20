//go:build integration

package integration

import (
	"encoding/json"
	"testing"
)

func batchBody(ids ...string) string {
	body, _ := json.Marshal(map[string][]string{"ids": ids})
	return string(body)
}

func TestOrderBatchesRespectSettingsAndPayment(t *testing.T) {
	f := setup(t)
	const settings = "/admin/order-limits"
	const accept = "/admin/order-batches/accept"
	const ready = "/admin/order-batches/ready"
	const claim = "/rider/task-batches/claim"
	const deliver = "/rider/task-batches/deliver"
	limits := f.call("op", "GET", settings, "", 200)
	if limits["merchant_batch_limit"] != float64(15) || limits["rider_active_limit"] != float64(8) {
		t.Fatal(limits)
	}
	set := `{"merchant_batch_limit":2,"rider_active_limit":2,"expected_version":1}`
	f.call("fin", "PUT", settings, set, 403)
	f.call("op", "PUT", settings, set, 200)
	f.call("op", "PUT", settings, set, 409)
	f.call("op", "PUT", settings, `{"merchant_batch_limit":0,"rider_active_limit":2,"expected_version":2}`, 400)

	cod := f.order("buyer", "ASAP", "COD")
	paid := f.order("buyer", "ASAP", "BALANCE")
	extra := f.order("other", "ASAP", "BALANCE")
	f.call("op", "POST", accept, batchBody(cod, paid, extra), 409)
	f.call("op", "POST", accept, batchBody(cod, cod), 400)
	f.call("op", "POST", accept, batchBody(cod, paid), 200)
	f.call("op", "POST", ready, batchBody(cod, extra), 409)
	if f.n(`SELECT count(*) FROM delivery_tasks`) != 0 {
		t.Fatal("failed batch released a task")
	}
	f.call("op", "POST", ready, batchBody(cod, paid), 200)
	f.call("op", "POST", accept, batchBody(extra), 200)
	f.call("op", "POST", ready, batchBody(extra), 200)

	tasks := make(map[string]string)
	for _, item := range f.call("r1", "GET", "/rider/tasks", "", 200)["items"].([]any) {
		task := item.(map[string]any)
		if task["payment_method"] == "COD" {
			tasks["cod"] = task["id"].(string)
		}
		if task["payment_method"] != "COD" && task["payment_method"] != "BALANCE" {
			t.Fatal(task)
		}
		if _, exposed := task["address"]; exposed {
			t.Fatal("lobby exposed address")
		}
	}
	var paidTask, extraTask string
	for orderID, target := range map[string]*string{paid: &paidTask, extra: &extraTask} {
		if err := f.pool.QueryRow(f.ctx, `SELECT id FROM delivery_tasks WHERE order_id=$1`, orderID).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	codTask := tasks["cod"]
	f.call("r1", "POST", claim, batchBody(codTask, paidTask), 409)
	if f.n(`SELECT count(*) FROM delivery_tasks WHERE rider_id='r1'`) != 0 {
		t.Fatal("mixed claim changed assignments")
	}
	claimed := f.request("r1", "POST", claim, batchBody(paidTask, extraTask), "claim-batch")
	if claimed.Code != 200 {
		t.Fatal(claimed.Code, claimed.Body)
	}
	for _, task := range []string{extraTask, paidTask} {
		f.call("r1", "POST", "/rider/tasks/"+task+"/pickup", "", 200)
	}
	full := f.call("r1", "GET", "/rider/capacity", "", 200)
	if full["active"] != float64(2) || full["remaining"] != float64(0) {
		t.Fatal(full)
	}
	f.call("r1", "POST", "/rider/tasks/"+codTask+"/claim", "", 409)
	f.call("r2", "POST", deliver, batchBody(paidTask), 404)
	if f.n(`SELECT count(*) FROM rider_earnings`) != 0 {
		t.Fatal("a rejected delivery batch recorded earnings")
	}
	f.call("r1", "POST", deliver, batchBody(paidTask), 200)
	after := f.call("r1", "GET", "/rider/capacity", "", 200)
	if after["active"] != float64(1) || after["remaining"] != float64(1) {
		t.Fatal(after)
	}
	f.call("r1", "POST", claim, batchBody(codTask), 409)
	f.call("r1", "POST", deliver, batchBody(extraTask), 200)
	replay := f.request("r1", "POST", claim, batchBody(paidTask, extraTask), "claim-batch")
	if replay.Code != 200 || replay.Body.String() != claimed.Body.String() {
		t.Fatal("claim replay changed after delivery")
	}
	f.call("r1", "POST", claim, batchBody(codTask), 200)
	locked := f.call("r1", "GET", "/rider/capacity", "", 200)
	if locked["remaining"] != float64(0) || locked["cod_active"] != true {
		t.Fatal(locked)
	}
	nextTask := f.dispatch(f.order("other", "ASAP", "BALANCE"))
	if rejected := f.call("r1", "POST", "/rider/tasks/"+nextTask+"/claim", "", 409); rejected["code"] != "COD_CLAIM_EXCLUSIVE" {
		t.Fatal(rejected)
	}
	f.call("r1", "POST", "/rider/tasks/"+codTask+"/pickup", "", 200)
	f.call("r1", "POST", deliver, batchBody(codTask), 409)
	f.call("r1", "POST", "/rider/tasks/"+codTask+"/deliver", `{}`, 409)
	f.call("r1", "POST", "/rider/tasks/"+codTask+"/deliver", `{"cod_received":true}`, 200)
	f.call("r1", "POST", "/rider/tasks/"+nextTask+"/claim", "", 200)
	if f.n(`SELECT count(*) FROM rider_earnings`) != 3 || f.n(`SELECT count(*) FROM cod_receipts`) != 1 {
		t.Fatal("unexpected delivery or COD records")
	}
}
