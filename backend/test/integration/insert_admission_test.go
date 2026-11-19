//go:build integration

package integration

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// Pause after INSERT's immediate RI triggers, before any reservation port runs.
// Both customers must be admitted without mutually upgrading slot KEY SHARE locks.
func TestP05OrderInsertSlotFKDoesNotDeadlockBeforeAdmission(t *testing.T) {
	f := setup(t)
	q1 := f.quote("buyer", "ASAP", "BALANCE")
	q2 := f.quote("other", "ASAP", "BALANCE")
	b1, e := f.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer b1.Rollback(f.ctx)
	b2, e := f.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer b2.Rollback(f.ctx)
	if _, e = b1.Exec(f.ctx, `SELECT pg_advisory_xact_lock(2026111911)`); e != nil {
		t.Fatal(e)
	}
	if _, e = b2.Exec(f.ctx, `SELECT pg_advisory_xact_lock(2026111912)`); e != nil {
		t.Fatal(e)
	}
	f.exec(`CREATE FUNCTION p05_insert_admission() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.actor_id='buyer' THEN PERFORM pg_advisory_xact_lock(2026111911);ELSE PERFORM pg_advisory_xact_lock(2026111912);END IF;RETURN NEW;END $$;CREATE TRIGGER zz_p05_insert_admission AFTER INSERT ON orders FOR EACH ROW EXECUTE FUNCTION p05_insert_admission()`)
	first := f.start("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q1), "insert-first")
	pid1 := f.waitBlocked(first, b1.Conn().PgConn().PID())
	second := f.start("other", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q2), "insert-second")
	pid2 := f.waitBlocked(second, b2.Conn().PgConn().PID())
	f.release(b1)
	var finished bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case w := <-first.done:
			first.done <- w
			finished = true
		default:
		}
		if finished {
			t.Logf("backend PID %d admitted while backend PID %d remained at INSERT barrier", pid1, pid2)
			break
		}
		var blocked bool
		if e = f.pool.QueryRow(f.ctx, `SELECT $2::int=ANY(pg_blocking_pids($1))`, pid1, pid2).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			t.Logf("observed INSERT FK upgrade wait: backend PID %d blocked by PID %d", pid1, pid2)
			break
		}
		runtime.Gosched()
	}
	f.release(b2)
	f.finish(first, 201)
	f.finish(second, 201)
	if f.n(`SELECT count(*) FROM orders`) != 2 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 2 || f.n(`SELECT reserved FROM inventory`) != 2 || f.n(`SELECT sum(balance_fen) FROM wallets`) != 19800 || f.n(`SELECT count(*) FROM wallet_entries WHERE kind='DEBIT'`) != 2 || f.n(`SELECT count(*) FROM order_events WHERE kind='CREATED'`) != 2 {
		t.Fatal("admission lost or duplicated a reservation/settlement")
	}
}
