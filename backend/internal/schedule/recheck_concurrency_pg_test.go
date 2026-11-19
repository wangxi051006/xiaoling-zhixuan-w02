//go:build integration

package schedule

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A held real row lock makes contention observable. PostgreSQL reports each
// contender as blocked before the gate is released; elapsed time is not evidence.
func waitSlotBlockers(t *testing.T, pool *pgxpool.Pool, ctx context.Context, pids ...int32) {
	t.Helper()
	if len(pids) != 2 || pids[0] == pids[1] {
		t.Fatal("expected two distinct contender backends")
	}
	for {
		if scalar(t, pool, ctx, `SELECT count(*) FROM unnest($1::integer[]) contender(pid) WHERE cardinality(pg_blocking_pids(pid))>0`, pids) == int64(len(pids)) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected PostgreSQL lock contenders not observed")
		default:
		}
	}
}

func holdEarly(t *testing.T, pool *pgxpool.Pool, ctx context.Context) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := lockSlot(ctx, tx, "early"); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestRecheckLastSeatTransactionBarrier(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	blocker := holdEarly(t, pool, ctx)
	ready := make(chan int32, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i, mode := range []contract.DeliveryMode{contract.DeliveryModeAsap, contract.DeliveryModeScheduled} {
		go func() {
			results <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				id := ""
				if mode == contract.DeliveryModeScheduled {
					id = "early"
				}
				q, err := (Store{}).SelectSlot(ctx, tx, "s", mode, id, now)
				if err != nil {
					return err
				}
				if q.ID != "early" {
					return errors.New("unexpected quoted slot")
				}
				ready <- int32(tx.Conn().PgConn().PID())
				select {
				case <-start:
				case <-ctx.Done():
					return ctx.Err()
				}
				return (Store{}).ReserveSlot(ctx, tx, q.ID, []string{"asap", "scheduled"}[i], mode, now)
			})
		}()
	}
	pids := make([]int32, 0, 2)
	for range 2 {
		select {
		case pid := <-ready:
			pids = append(pids, pid)
		case <-ctx.Done():
			t.Fatal("transaction barrier failed")
		}
	}
	close(start)
	waitSlotBlockers(t, pool, ctx, pids...)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := <-results, <-results
	if a != nil {
		a, b = b, a
	}
	if a != nil || !errors.Is(b, ErrSlotFull) {
		t.Fatalf("last seat result: %v / %v", a, b)
	}
	assertSlotUsed(t, pool, ctx, "early", 1)
	if scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations`) != 1 || scalar(t, pool, ctx, `SELECT capacity-used FROM delivery_slots WHERE id='early'`) != 0 {
		t.Fatal("last seat invariant failed")
	}
}

func TestRecheckConcurrentSameOrderReplay(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	blocker := holdEarly(t, pool, ctx)
	results := make(chan error, 2)
	ready := make(chan int32, 2)
	for range 2 {
		go func() {
			results <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				ready <- int32(tx.Conn().PgConn().PID())
				return (Store{}).ReserveSlot(ctx, tx, "early", "same-order", contract.DeliveryModeAsap, now)
			})
		}()
	}
	pids := make([]int32, 0, 2)
	for range 2 {
		select {
		case pid := <-ready:
			pids = append(pids, pid)
		case <-ctx.Done():
			t.Fatal("contender transaction did not begin")
		}
	}
	waitSlotBlockers(t, pool, ctx, pids...)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertSlotUsed(t, pool, ctx, "early", 1)
	if scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations`) != 1 {
		t.Fatal("duplicate reserve persisted")
	}
}

func TestRecheckReserveReleaseAgainstAdmin(t *testing.T) {
	for _, operation := range []string{"reserve", "release"} {
		for _, change := range []string{"capacity", "closure"} {
			t.Run(operation+"-"+change, func(t *testing.T) {
				pool, ctx, now := slotFixture(t)
				cfg := pool.Config()
				cfg.MaxConns, cfg.MinConns = 1, 0
				httpPool, err := pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(httpPool.Close)
				conn, err := httpPool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				httpPID := int32(conn.Conn().PgConn().PID())
				conn.Release()
				h := recheckHandler(httpPool, now, "s")
				if operation == "release" {
					if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
						return (Store{}).ReserveSlot(ctx, tx, "early", "order", contract.DeliveryModeAsap, now)
					}); err != nil {
						t.Fatal(err)
					}
				}
				blocker := holdEarly(t, pool, ctx)
				domain := make(chan error, 1)
				httpResult := make(chan int, 1)
				ready := make(chan int32, 1)
				go func() {
					domain <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
						ready <- int32(tx.Conn().PgConn().PID())
						if operation == "release" {
							return (Store{}).ReleaseSlot(ctx, tx, "order", now)
						}
						return (Store{}).ReserveSlot(ctx, tx, "early", "order", contract.DeliveryModeAsap, now)
					})
				}()
				go func() {
					body := `{"expected_version":1,"capacity":0,"accepting":true}`
					if change == "closure" {
						body = `{"expected_version":1,"capacity":1,"accepting":false}`
					}
					r := httptest.NewRequest("PUT", "https://admin.example.test/api/v1/admin/slots/early", strings.NewReader(body))
					r.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					httpResult <- w.Code
				}()
				var domainPID int32
				select {
				case domainPID = <-ready:
				case <-ctx.Done():
					t.Fatal("domain contender did not begin")
				}
				waitSlotBlockers(t, pool, ctx, domainPID, httpPID)
				if err := blocker.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				domainErr, status := <-domain, <-httpResult
				used := scalar(t, pool, ctx, `SELECT used FROM delivery_slots WHERE id='early'`)
				if operation == "reserve" {
					if change == "closure" {
						if status != 200 || (!errors.Is(domainErr, ErrSlotExpired) && domainErr != nil) || (domainErr == nil && used != 1) || (domainErr != nil && used != 0) {
							t.Fatalf("reserve/close inconsistent: %v %d used=%d", domainErr, status, used)
						}
					} else {
						if !((status == 200 && errors.Is(domainErr, ErrSlotFull) && used == 0) || (status == 409 && domainErr == nil && used == 1)) {
							t.Fatalf("reserve/adjust inconsistent: %v %d used=%d", domainErr, status, used)
						}
					}
				} else if domainErr != nil || used != 0 || (status != 200 && status != 409) || (change == "closure" && status != 200) {
					t.Fatalf("release/admin inconsistent: %v %d used=%d", domainErr, status, used)
				}
				if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots WHERE used<0 OR used>capacity`) != 0 {
					t.Fatal("capacity invariant violated")
				}
				if scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations WHERE released_at IS NULL`) != used {
					t.Fatal("reservation/counter mismatch")
				}
			})
		}
	}
}

func TestRecheckPortFaultsRollback(t *testing.T) {
	for _, point := range []string{"reserve-record", "reserve-count", "release-record", "release-count"} {
		t.Run(point, func(t *testing.T) {
			pool, ctx, now := slotFixture(t)
			release := strings.HasPrefix(point, "release")
			if release {
				if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
					return (Store{}).ReserveSlot(ctx, tx, "early", "order", contract.DeliveryModeAsap, now)
				}); err != nil {
					t.Fatal(err)
				}
			}
			table, event, condition := "slot_reservations", "INSERT", ""
			switch point {
			case "reserve-count":
				table, event, condition = "delivery_slots", "UPDATE", " WHEN (NEW.used>OLD.used)"
			case "release-record":
				event = "UPDATE"
			case "release-count":
				table, event, condition = "delivery_slots", "UPDATE", " WHEN (NEW.used<OLD.used)"
			}
			if _, err := pool.Exec(ctx, `CREATE FUNCTION p03_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-p03-diagnostic'; END $$; CREATE TRIGGER p03_fault AFTER `+event+` ON `+table+` FOR EACH ROW`+condition+` EXECUTE FUNCTION p03_fault()`); err != nil {
				t.Fatal(err)
			}
			err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				if release {
					return (Store{}).ReleaseSlot(ctx, tx, "order", now)
				}
				return (Store{}).ReserveSlot(ctx, tx, "early", "order", contract.DeliveryModeAsap, now)
			})
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "P0001" {
				t.Fatalf("fault not reached: %v", err)
			}
			want := int64(0)
			if release {
				want = 1
			}
			assertSlotUsed(t, pool, ctx, "early", want)
			if scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations`) != want || scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations WHERE released_at IS NOT NULL`) != 0 {
				t.Fatal("fault left phantom reservation or release")
			}
			if _, err := pool.Exec(ctx, "DROP TRIGGER p03_fault ON "+table); err != nil {
				t.Fatal(err)
			}
			if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				if release {
					return (Store{}).ReleaseSlot(ctx, tx, "order", now)
				}
				return (Store{}).ReserveSlot(ctx, tx, "early", "order", contract.DeliveryModeAsap, now)
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecheckReplayLifecycleAfterClosureAndExpiry(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	s := Store{}
	reserve := func(slot, order string, mode contract.DeliveryMode, when time.Time) error {
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return s.ReserveSlot(ctx, tx, slot, order, mode, when) })
	}
	release := func(order string, when time.Time) error {
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return s.ReleaseSlot(ctx, tx, order, when) })
	}
	if err := release("unreserved", now); !errors.Is(err, ErrSlotConflict) {
		t.Fatalf("unreserved release: %v", err)
	}
	if err := reserve("early", "order", contract.DeliveryModeScheduled, now); err != nil {
		t.Fatal(err)
	}
	if err := reserve("early", "order", contract.DeliveryModeAsap, now); !errors.Is(err, ErrSlotConflict) {
		t.Fatal("different-mode replay accepted")
	}
	if err := release("order", now.Add(-time.Microsecond)); !errors.Is(err, ErrSlotConflict) {
		t.Fatal("release before reserve accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_slots SET accepting=false WHERE id='early'`); err != nil {
		t.Fatal(err)
	}
	end := mustTime(t, "2026-11-18T03:15:00Z")
	if err := reserve("early", "order", contract.DeliveryModeScheduled, end.Add(time.Hour)); err != nil {
		t.Fatalf("exact replay after closure/expiry: %v", err)
	}
	if err := reserve("early", "new", contract.DeliveryModeScheduled, now); !errors.Is(err, ErrSlotExpired) {
		t.Fatal("new reserve accepted closed slot")
	}
	for _, when := range []time.Time{end, end.Add(time.Nanosecond)} {
		if err := release("order", when); !errors.Is(err, ErrSlotExpired) {
			t.Fatal("expired release returned capacity")
		}
	}
	assertSlotUsed(t, pool, ctx, "early", 1)
	if err := release("order", end.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := release("order", end.Add(time.Hour)); err != nil {
		t.Fatal("released replay did not remain idempotent")
	}
	assertSlotUsed(t, pool, ctx, "early", 0)
	if scalar(t, pool, ctx, `SELECT version FROM delivery_slots WHERE id='early'`) != 1 {
		t.Fatal("counter operation changed config version")
	}
	if err := reserve("early", "order", contract.DeliveryModeScheduled, now); !errors.Is(err, ErrSlotConflict) {
		t.Fatal("released reservation resurrected")
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_slots SET accepting=true WHERE id='early'`); err != nil {
		t.Fatal(err)
	}
	if err := reserve("early", "new", contract.DeliveryModeAsap, now); err != nil {
		t.Fatal("released capacity not resalable")
	}
}

func TestRecheckBatchesReverseInputAndConcurrentGeneration(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	windows, err := generateWindows("2026-11-19", "23:30", "00:30", 0)
	if err != nil {
		t.Fatal(err)
	}
	reversed := append([]contract.SlotSnapshot(nil), windows...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, input := range [][]contract.SlotSnapshot{windows, reversed} {
		go func() {
			results <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				ready <- struct{}{}
				select {
				case <-start:
				case <-ctx.Done():
					return ctx.Err()
				}
				_, err := insertBatch(ctx, tx, "s", now, input, 2)
				return err
			})
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("batch transaction barrier failed")
		}
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots`) != 6 {
		t.Fatal("concurrent batch duplicated slots")
	}
	h := recheckHandler(pool, now, "s")
	adjacent := `{"local_date":"2026-11-20","opens_at":"00:30","closes_at":"01:00","lead_minutes":0,"capacity":2}`
	slotCall(t, h, "POST", "/admin/slots/batches", adjacent, "adjacent", 201)
	conflict := `{"local_date":"2026-11-20","opens_at":"00:15","closes_at":"00:45","lead_minutes":0,"capacity":3}`
	slotCall(t, h, "POST", "/admin/slots/batches", conflict, "different-config", 409)
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots`) != 8 {
		t.Fatal("cross-midnight conflict changed data")
	}
}
