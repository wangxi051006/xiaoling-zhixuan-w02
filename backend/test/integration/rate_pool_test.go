//go:build integration

package integration

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"testing"
	"time"
)

func TestP05RateAdmissionDoesNotNestPoolAcquisition(t *testing.T) {
	f := setup(t)
	q1 := f.quote("buyer", "ASAP", "BALANCE")
	q2 := f.quote("other", "ASAP", "BALANCE")
	cfg := f.pool.Config()
	cfg.MaxConns = 2
	small, e := pgxpool.NewWithConfig(f.ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer small.Close()
	f.service.Pool = small
	blocker, e := f.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(f.ctx)
	if _, e = blocker.Exec(f.ctx, `SELECT pg_advisory_xact_lock(2026111922)`); e != nil {
		t.Fatal(e)
	}
	f.exec(`CREATE FUNCTION p05_two_outer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.command='createOrder' THEN PERFORM pg_advisory_xact_lock(2026111922);END IF;RETURN NEW;END $$;CREATE TRIGGER p05_two_outer BEFORE INSERT ON idempotency_keys FOR EACH ROW EXECUTE FUNCTION p05_two_outer()`)
	start := func(actor, quote string) pendingRequest {
		p := pendingRequest{make(chan uint32, 8), make(chan *httptest.ResponseRecorder, 1)}
		ctx, cancel := context.WithTimeout(context.WithValue(f.ctx, pidKey{}, p.pids), 3*time.Second)
		t.Cleanup(cancel)
		go func() {
			p.done <- f.requestContext(ctx, actor, "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, quote), actor+"-bounded-pool")
		}()
		return p
	}
	a := start("buyer", q1)
	f.waitBlocked(a, blocker.Conn().PgConn().PID())
	b := start("other", q2)
	f.waitBlocked(b, blocker.Conn().PgConn().PID())
	f.release(blocker)
	f.finish(a, 201)
	f.finish(b, 201)
	if f.n(`SELECT count(*) FROM orders`) != 2 {
		t.Fatal("small pool lost concurrent orders")
	}
}
