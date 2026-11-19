//go:build integration

package integration

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

type pendingRequest struct {
	pids chan uint32
	done chan *httptest.ResponseRecorder
}

func (f *fixture) start(actor, method, path, body, key string) pendingRequest {
	p := pendingRequest{make(chan uint32, 8), make(chan *httptest.ResponseRecorder, 1)}
	ctx := context.WithValue(f.ctx, pidKey{}, p.pids)
	go func() { p.done <- f.requestContext(ctx, actor, method, path, body, key) }()
	return p
}
func (f *fixture) waitBlocked(p pendingRequest, blocker uint32) uint32 {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	seen := map[uint32]bool{}
	for time.Now().Before(deadline) {
		for {
			select {
			case pid := <-p.pids:
				seen[pid] = true
			default:
				goto inspect
			}
		}
	inspect:
		for pid := range seen {
			var waiting bool
			err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock' AND $2::int=ANY(pg_blocking_pids(pid)))`, pid, blocker).Scan(&waiting)
			if err != nil {
				f.t.Fatal(err)
			}
			if waiting {
				f.t.Logf("observed exact backend PID %d waiting on blocker PID %d", pid, blocker)
				return pid
			}
		}
		select {
		case w := <-p.done:
			f.t.Fatalf("request completed before intended lock barrier: %d %s", w.Code, w.Body)
		default:
		}
		runtime.Gosched()
	}
	f.t.Fatal("did not observe exact competing backend lock wait")
	return 0
}
func (f *fixture) finish(p pendingRequest, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	select {
	case w := <-p.done:
		if w.Code != want {
			f.t.Fatalf("race result status=%d want=%d body=%s", w.Code, want, w.Body)
		}
		return w
	case <-time.After(5 * time.Second):
		f.t.Fatal("request failed to finish after barrier")
		return nil
	}
}
func (f *fixture) barrier(kind string) (pgx.Tx, uint32) {
	f.t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	if _, err = tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock(2026111905)`); err != nil {
		f.t.Fatal(err)
	}
	f.exec(fmt.Sprintf(`CREATE FUNCTION p05_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='%s' THEN PERFORM pg_advisory_xact_lock(2026111905); END IF;RETURN NEW; END $$;CREATE TRIGGER p05_barrier BEFORE INSERT ON order_events FOR EACH ROW EXECUTE FUNCTION p05_barrier()`, kind))
	return tx, tx.Conn().PgConn().PID()
}
func (f *fixture) release(tx pgx.Tx) {
	f.t.Helper()
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

func TestP05TwoRidersOneTaskExactBarrier(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "COD")
	task := f.dispatch(id)
	lock, pid := f.barrier("CLAIMED")
	first := f.start("r1", "POST", "/rider/tasks/"+task+"/claim", "", "claim-first")
	winner := f.waitBlocked(first, pid)
	second := f.start("r2", "POST", "/rider/tasks/"+task+"/claim", "", "claim-second")
	f.waitBlocked(second, winner)
	f.release(lock)
	f.finish(first, 200)
	w := f.finish(second, 409)
	if !strings.Contains(w.Body.String(), "TASK_ALREADY_CLAIMED") {
		t.Fatal(w.Body)
	}
	if f.n(`SELECT count(*) FROM delivery_tasks WHERE state='ASSIGNED' AND rider_id='r1'`) != 1 || f.n(`SELECT count(*) FROM order_events WHERE kind='CLAIMED'`) != 1 {
		t.Fatal("double claim")
	}
}
func TestP05OneRiderTwoTasksExactBarrier(t *testing.T) {
	f := setup(t)
	a := f.dispatch(f.order("buyer", "ASAP", "BALANCE"))
	b := f.dispatch(f.order("other", "ASAP", "BALANCE"))
	lock, pid := f.barrier("CLAIMED")
	first := f.start("r1", "POST", "/rider/tasks/"+a+"/claim", "", "rider-first")
	winner := f.waitBlocked(first, pid)
	second := f.start("r1", "POST", "/rider/tasks/"+b+"/claim", "", "rider-second")
	f.waitBlocked(second, winner)
	f.release(lock)
	f.finish(first, 200)
	f.finish(second, 409)
	if f.n(`SELECT count(*) FROM delivery_tasks WHERE rider_id='r1' AND state='ASSIGNED'`) != 1 || f.n(`SELECT count(*) FROM orders WHERE status='READY_FOR_CLAIM'`) != 1 {
		t.Fatal("same rider dual active")
	}
}
func TestP05PickupVersusCancelAndReclaimExactBarrier(t *testing.T) {
	for _, action := range []string{"cancel", "reclaim"} {
		for _, winnerAction := range []string{"pickup", action} {
			t.Run(action+"-winner-"+winnerAction, func(t *testing.T) {
				f := setup(t)
				id := f.order("buyer", "ASAP", "BALANCE")
				task := f.dispatch(id)
				f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
				kind := "PICKED_UP"
				if winnerAction == "cancel" {
					kind = "CANCELED"
				} else if winnerAction == "reclaim" {
					kind = "RECLAIMED"
				}
				lock, pid := f.barrier(kind)
				firstActor, firstPath, firstBody := "r1", "/rider/tasks/"+task+"/pickup", ""
				secondActor, secondPath, secondBody := "op", "/admin/orders/"+id+"/"+action, `{"reason_code":"OPS_REVIEW"}`
				if winnerAction != "pickup" {
					firstActor, secondActor = secondActor, firstActor
					firstPath, secondPath = secondPath, firstPath
					firstBody, secondBody = secondBody, firstBody
				}
				first := f.start(firstActor, "POST", firstPath, firstBody, "winner")
				winner := f.waitBlocked(first, pid)
				second := f.start(secondActor, "POST", secondPath, secondBody, "loser")
				f.waitBlocked(second, winner)
				f.release(lock)
				f.finish(first, 200)
				want := 409
				if winnerAction == "reclaim" {
					want = 404
				}
				f.finish(second, want)
				if winnerAction == "pickup" {
					if f.n(`SELECT count(*) FROM orders WHERE status='IN_DELIVERY'`) != 1 || f.n(`SELECT on_hand FROM inventory`) != 99 || f.n(`SELECT reserved FROM inventory`) != 0 {
						t.Fatal("pickup winner invariant")
					}
				} else if winnerAction == "cancel" {
					if f.n(`SELECT count(*) FROM orders WHERE status='CANCELED' AND stock_status='RESTOCK_PENDING'`) != 1 || f.n(`SELECT on_hand FROM inventory`) != 100 || f.n(`SELECT reserved FROM inventory`) != 1 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000 {
						t.Fatal("cancel winner invariant")
					}
				} else if f.n(`SELECT count(*) FROM delivery_tasks WHERE state='OPEN' AND rider_id IS NULL`) != 1 || f.n(`SELECT reserved FROM inventory`) != 1 {
					t.Fatal("reclaim winner invariant")
				}
			})
		}
	}
}
func TestP05ContactReadRevalidatesAfterConcurrentReclaim(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "COD")
	task := f.dispatch(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	lock, pid := f.barrier("RECLAIMED")
	first := f.start("op", "POST", "/admin/orders/"+id+"/reclaim", `{"reason_code":"OPS_REVIEW"}`, "reclaim-contact")
	winner := f.waitBlocked(first, pid)
	second := f.start("r1", "GET", "/rider/tasks/"+task, "", "")
	f.waitBlocked(second, winner)
	f.release(lock)
	f.finish(first, 200)
	w := f.finish(second, 200)
	if strings.Contains(w.Body.String(), `"address"`) {
		t.Fatal("reclaimed lobby task retained stale contact")
	}
}
