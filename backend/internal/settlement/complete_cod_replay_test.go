package settlement

import (
	"encoding/json"
	"strconv"
	"testing"
)

// P08-F01: an idempotency cache written before difference_fen existed must still
// answer with the stored ledger direction on replay.
func TestCompleteCODReplay(t *testing.T) {
	const (
		create = "createCODBatch"
		verify = "verifyCODBatch"
	)
	const (
		batchJSON = `{"id":"b1","rider_id":"r","local_date":"2026-11-19","expected_fen":1000,"actual_fen":null,"status":"IN_BATCH"}`
		shortJSON = `{"id":"b1","rider_id":"r","local_date":"2026-11-19","expected_fen":1000,"actual_fen":990,"status":"DISCREPANCY"}`
		overJSON  = `{"id":"b1","rider_id":"r","local_date":"2026-11-19","expected_fen":1000,"actual_fen":1010,"status":"DISCREPANCY"}`
		evenJSON  = `{"id":"b1","rider_id":"r","local_date":"2026-11-19","expected_fen":1000,"actual_fen":1000,"status":"VERIFIED"}`
		doneJSON  = `{"id":"b1","rider_id":"r","local_date":"2026-11-19","expected_fen":1000,"actual_fen":990,"difference_fen":10,"status":"DISCREPANCY"}`
	)
	cases := []struct {
		name, command, body string
		unchanged           bool
		want                string
	}{
		{"unrelated command", "approveRefund", `{"id":"b1"}`, true, ""},
		{"malformed body", verify, "not-json", true, ""},
		{"already completed", verify, doneJSON, true, ""},
		{"legacy unverified batch", create, batchJSON, false, "null"},
		{"legacy short payment", verify, shortJSON, false, "10"},
		{"legacy over payment", verify, overJSON, false, "-10"},
		{"legacy balanced", verify, evenJSON, false, "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reply := completeCODReplay(c.command, json.RawMessage(c.body))
			if c.unchanged {
				if string(reply) != c.body {
					t.Fatalf("body must replay unchanged: %s", reply)
				}
				return
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(reply, &fields); err != nil {
				t.Fatalf("reply is not JSON: %v (%s)", err, reply)
			}
			raw, ok := fields["difference_fen"]
			if !ok {
				t.Fatalf("difference_fen missing: %s", reply)
			}
			if string(raw) != c.want {
				t.Fatalf("difference_fen = %s, want %s", raw, c.want)
			}
			var batch CODBatch
			if err := json.Unmarshal(reply, &batch); err != nil {
				t.Fatalf("reply does not decode as CODBatch: %v", err)
			}
			if batch.ID != "b1" || batch.ExpectedFen != 1000 || batch.RiderID != "r" {
				t.Fatalf("other fields changed: %+v", batch)
			}
			if batch.DifferenceFen == nil {
				if c.want != "null" || batch.ActualFen != nil {
					t.Fatalf("only an unverified batch may have a null difference: %+v", batch)
				}
				return
			}
			want, err := strconv.ParseInt(c.want, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if *batch.DifferenceFen != want {
				t.Fatalf("difference = %d, want %d", *batch.DifferenceFen, want)
			}
			if batch.ActualFen == nil || *batch.DifferenceFen != batch.ExpectedFen-*batch.ActualFen {
				t.Fatalf("difference does not match expected-actual: %+v", batch)
			}
		})
	}
}
