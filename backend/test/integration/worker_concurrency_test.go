//go:build integration

package integration

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestP05TwoWorkersConsumeOneNotificationUnderExactBarrier(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "COD")
	task := f.dispatch(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	f.call("buyer", "POST", "/orders/"+id+"/urge", "", 202)
	blocker, e := f.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(f.ctx)
	if _, e = blocker.Exec(f.ctx, `SELECT pg_advisory_xact_lock(2026111923)`); e != nil {
		t.Fatal(e)
	}
	f.exec(`CREATE FUNCTION p05_notification_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(2026111923);RETURN NEW;END $$;CREATE TRIGGER p05_notification_barrier BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION p05_notification_barrier()`)
	first := pendingRequest{make(chan uint32, 8), make(chan *httptest.ResponseRecorder, 1)}
	go func() {
		n, err := f.service.RunDue(context.WithValue(f.ctx, pidKey{}, first.pids), 100)
		w := httptest.NewRecorder()
		if err != nil {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(200)
		}
		if n != 1 {
			w.WriteString("unexpected processed count")
		}
		first.done <- w
	}()
	f.waitBlocked(first, blocker.Conn().PgConn().PID())
	if n, err := f.service.RunDue(f.ctx, 100); err != nil || n != 0 {
		t.Fatal("second worker did not respect live lease", n, err)
	}
	if f.n(`SELECT attempts FROM outbox_jobs WHERE kind='ORDER_URGE'`) != 1 {
		t.Fatal("duplicate concurrent claim")
	}
	f.release(blocker)
	w := f.finish(first, 200)
	if w.Body.Len() != 0 {
		t.Fatal(w.Body)
	}
	if n, err := f.service.RunDue(f.ctx, 100); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if f.n(`SELECT count(*) FROM notifications`) != 1 || f.n(`SELECT count(*) FROM outbox_jobs WHERE kind='ORDER_URGE' AND finished_at IS NOT NULL`) != 1 {
		t.Fatal("notification/job completion not atomic")
	}
}
