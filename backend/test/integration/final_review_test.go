//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"
)

func TestP05FailedAlertDoesNotStarveLeasedWakeAndOverdue(t *testing.T) {
	f := setup(t)
	sink := &recordingAlertSink{fail: true}
	f.service.AlertSink = sink
	urgent := f.order("buyer", "ASAP", "COD")
	f.call("buyer", "POST", "/orders/"+urgent+"/urge", "", 202)
	scheduled := f.order("other", "SCHEDULED", "BALANCE")
	base := f.clock.Now()
	for round, elapsed := range []time.Duration{45 * time.Minute, 76 * time.Minute, 77 * time.Minute} {
		f.clock.Set(base.Add(elapsed))
		done, err := f.service.RunDue(f.ctx, 100)
		if err == nil || err.Error() != "synthetic sink failure" {
			t.Fatalf("round %d lost the first job error: done=%d err=%v", round, done, err)
		}
		if round < 2 && done == 0 {
			t.Errorf("round %d failed alert prevented independent lifecycle progress", round)
		}
		if f.n(`SELECT count(*) FROM orders WHERE id=$1 AND status='PREPARING'`, scheduled) != 1 {
			t.Errorf("round %d scheduled wake starved behind earlier failing alert", round)
		}
		if round > 0 && f.n(`SELECT count(*) FROM orders WHERE id=$1 AND overdue`, scheduled) != 1 {
			t.Errorf("round %d overdue transition starved across lease retry", round)
		}
		if f.n(`SELECT count(*) FROM outbox_jobs WHERE kind='ORDER_URGE' AND finished_at IS NULL AND lease_until IS NOT NULL`) != 1 {
			t.Fatal("failed alert lost its recoverable lease")
		}
	}
	if f.n(`SELECT count(*) FROM order_events WHERE order_id=$1 AND kind='SCHEDULE_WOKE'`, scheduled) != 1 || f.n(`SELECT count(*) FROM order_events WHERE order_id=$1 AND kind='OVERDUE'`, scheduled) != 1 {
		t.Error("lifecycle jobs lost or repeated their transitions")
	}
	if f.n(`SELECT attempts FROM outbox_jobs WHERE kind='ORDER_URGE'`) != 3 {
		t.Error("failed alert was not reclaimed on each expired lease")
	}
	var key string
	tries := 0
	for _, alert := range sink.calls {
		if alert.Kind != "ORDER_URGE" {
			continue
		}
		tries++
		if key == "" {
			key = alert.IdempotencyKey
		}
		if alert.IdempotencyKey != key {
			t.Error("retry changed failed alert identity")
		}
	}
	if tries != 3 {
		t.Errorf("failed alert attempts=%d want=3", tries)
	}
}

func TestP05UrgeUsesEnabledProfileAndKeepsOfflineAssignedRider(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			f := setup(t)
			f.exec(`UPDATE accounts SET display_name='Synthetic rider' WHERE id='r1'`)
			sink := &recordingAlertSink{}
			f.service.AlertSink = sink
			id := f.order("buyer", "ASAP", "COD")
			task := f.dispatch(id)
			f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
			if disabled {
				f.call("op", "PATCH", "/admin/riders/r1", `{"enabled":false,"expected_version":1,"reason_code":"OPS_DISABLED"}`, 200)
				if f.n(`SELECT count(*) FROM rider_profiles WHERE account_id='r1' AND NOT enabled AND NOT available`) != 1 || f.n(`SELECT count(*) FROM sessions WHERE account_id='r1' AND entry='rider'`) != 0 {
					t.Fatal("real operator path did not disable/revoke rider")
				}
			} else {
				f.call("r1", "PUT", "/rider/availability", `{"available":false}`, 200)
			}
			f.call("buyer", "POST", "/orders/"+id+"/urge", "", 202)
			if done, err := f.service.RunDue(f.ctx, 100); err != nil || done != 1 {
				t.Fatal(done, err)
			}
			if disabled {
				if f.n(`SELECT count(*) FROM notifications WHERE recipient_id='r1'`) != 0 || len(sink.calls) != 1 || len(sink.calls[0].RecipientIDs) != 1 || sink.calls[0].RecipientIDs[0] != "op" || sink.calls[0].OrderID != id {
					t.Fatal("disabled assigned rider swallowed urge instead of operator fallback", sink.calls)
				}
			} else if f.n(`SELECT count(*) FROM notifications WHERE recipient_id='r1'`) != 1 || len(sink.calls) != 0 {
				t.Fatal("temporary offline rider lost existing assignment urge", sink.calls)
			}
			if f.n(`SELECT count(*) FROM outbox_jobs WHERE kind='ORDER_URGE' AND finished_at IS NOT NULL`) != 1 {
				t.Fatal("delivery/fallback did not finish the outbox")
			}
		})
	}
}
