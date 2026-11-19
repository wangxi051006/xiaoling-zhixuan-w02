//go:build integration

package integration

import (
	"campus.local/xiaoling/internal/fulfillment"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingAlertSink struct {
	mu    sync.Mutex
	calls []fulfillment.Alert
	fail  bool
}

func (s *recordingAlertSink) SendAlert(_ context.Context, a fulfillment.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, a)
	if s.fail {
		return errors.New("synthetic sink failure")
	}
	return nil
}
func TestP05AlertOutboxRetryStableIDAndCurrentRecipients(t *testing.T) {
	f := setup(t)
	sink := &recordingAlertSink{fail: true}
	f.service.AlertSink = sink
	id := f.order("buyer", "SCHEDULED", "COD")
	f.call("buyer", "POST", "/orders/"+id+"/urge", "", 202)
	if _, e := f.service.RunDue(f.ctx, 100); e == nil {
		t.Fatal("unconsumed urgent alert or missing retry failure")
	}
	if len(sink.calls) != 1 || sink.calls[0].Kind != "ORDER_URGE" || len(sink.calls[0].RecipientIDs) != 1 || sink.calls[0].RecipientIDs[0] != "op" {
		t.Fatal(sink.calls)
	}
	key := sink.calls[0].IdempotencyKey
	f.clock.Set(f.clock.Now().Add(31 * time.Second))
	sink.fail = false
	if _, e := f.service.RunDue(f.ctx, 100); e != nil {
		t.Fatal(e)
	}
	if len(sink.calls) != 2 || sink.calls[1].IdempotencyKey != key || f.n(`SELECT count(*) FROM outbox_jobs WHERE kind='ORDER_URGE' AND finished_at IS NOT NULL`) != 1 {
		t.Fatal("retry lost stable identity")
	}
	// An urge queued before transfer must only notify the current assignee.
	second := f.order("other", "ASAP", "BALANCE")
	task := f.dispatch(second)
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	f.call("other", "POST", "/orders/"+second+"/urge", "", 202)
	f.call("op", "POST", "/admin/orders/"+second+"/reclaim", `{"reason_code":"TRANSFER"}`, 200)
	f.call("r2", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	if _, e := f.service.RunDue(f.ctx, 100); e != nil {
		t.Fatal(e)
	}
	if f.n(`SELECT count(*) FROM notifications WHERE recipient_id='r1'`) != 0 || f.n(`SELECT count(*) FROM notifications WHERE recipient_id='r2'`) != 1 {
		t.Fatal("stale or duplicated urge recipient")
	}
	if _, e := f.service.RunDue(f.ctx, 100); e != nil {
		t.Fatal(e)
	}
	if f.n(`SELECT count(*) FROM notifications`) != 1 {
		t.Fatal("notification duplicated on repeat worker")
	}
	// The overdue operational outbox is actually delivered, and terminal stale urges are skipped.
	f.clock.Set(f.clock.Now().Add(80 * time.Minute))
	for i := 0; i < 3; i++ {
		if _, e := f.service.RunDue(f.ctx, 100); e != nil {
			t.Fatal(e)
		}
	}
	overdue := 0
	for _, a := range sink.calls {
		if a.Kind == "OVERDUE_ALERT" {
			overdue++
		}
	}
	if overdue != 2 {
		t.Fatalf("overdue operational deliveries=%d calls=%v", overdue, sink.calls)
	}
	f.call("other", "POST", "/orders/"+second+"/urge", "", 202)
	f.call("op", "POST", "/admin/orders/"+second+"/cancel", `{"reason_code":"CLOSED"}`, 200)
	if _, e := f.service.RunDue(f.ctx, 100); e != nil {
		t.Fatal(e)
	}
	if f.n(`SELECT count(*) FROM notifications`) != 1 {
		t.Fatal("terminal urge sent")
	}
}
