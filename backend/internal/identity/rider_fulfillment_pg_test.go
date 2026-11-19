//go:build integration

package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFulfillmentRiderAvailabilitySerializesWritersAndClaimRead(t *testing.T) {
	p := identityPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := p.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');INSERT INTO accounts(id,station_id,issuer,subject)VALUES('r','s','https://idp.example.test','r');INSERT INTO role_bindings(account_id,role)VALUES('r','RIDER');INSERT INTO rider_profiles(account_id)VALUES('r')`); err != nil {
		t.Fatal(err)
	}
	d := NewDirectory(p)
	for _, readFirst := range []bool{false, true} {
		first, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Rollback(context.Background())
		second, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Rollback(context.Background())
		var blocker, waiter int32
		if err = first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
			t.Fatal(err)
		}
		if err = second.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&waiter); err != nil {
			t.Fatal(err)
		}
		if readFirst {
			if _, err = d.RiderAvailability(ctx, first, "r", "s"); err != nil {
				t.Fatal(err)
			}
		} else if err = d.SetRiderAvailability(ctx, first, "r", "s", true); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			err := d.SetRiderAvailability(ctx, second, "r", "s", false)
			if err == nil {
				err = second.Commit(ctx)
			}
			result <- err
		}()
		for {
			var blocked bool
			if err = p.QueryRow(ctx, `SELECT $1::int=ANY(pg_blocking_pids($2::int))`, blocker, waiter).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			select {
			case err := <-result:
				t.Fatalf("writer did not wait for caller lock: %v", err)
			default:
			}
		}
		if err = first.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-result; err != nil {
			t.Fatal(err)
		}
		var available bool
		if err = p.QueryRow(ctx, `SELECT available FROM rider_profiles WHERE account_id='r'`).Scan(&available); err != nil || available {
			t.Fatal("offline writer failed", err)
		}
		t.Logf("readFirst=%v blocker=%d waiter=%d lock barrier observed; writer committed offline", readFirst, blocker, waiter)
	}
}

func TestFulfillmentRiderAvailabilityUsesCallerTransaction(t *testing.T) {
	p := identityPool(t)
	ctx := t.Context()
	if _, e := p.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');INSERT INTO accounts(id,station_id,issuer,subject)VALUES('r','s','https://idp.example.test','r');INSERT INTO role_bindings(account_id,role)VALUES('r','RIDER');INSERT INTO rider_profiles(account_id)VALUES('r')`); e != nil {
		t.Fatal(e)
	}
	d := NewDirectory(p)
	clock := &fakeClock{time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)}
	sessions := NewSessions(p, clock)
	token, _, e := sessions.Issue(ctx, "r", RiderEntry, clock.Now())
	if e != nil {
		t.Fatal(e)
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if e = d.SetRiderAvailability(ctx, tx, "r", "s", true); e != nil {
		t.Fatalf("enable rider: %v", e)
	}
	if available, e := d.RiderAvailability(ctx, tx, "r", "s"); e != nil || !available {
		t.Fatalf("caller cannot see availability: %v", e)
	}
	var outside bool
	if e = p.QueryRow(ctx, `SELECT available FROM rider_profiles WHERE account_id='r'`).Scan(&outside); e != nil || outside {
		t.Fatal("uncommitted availability escaped")
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `SELECT available FROM rider_profiles WHERE account_id='r'`).Scan(&outside); e != nil {
		t.Fatal(e)
	}
	if outside {
		t.Fatal("rollback did not undo availability")
	}
	tx, e = p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if e = d.SetRiderAvailability(ctx, tx, "r", "s", true); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `SELECT available FROM rider_profiles WHERE account_id='r'`).Scan(&outside); e != nil || !outside {
		t.Fatal("committed online availability not visible", e)
	}
	tx, e = p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if e = d.SetRiderAvailability(ctx, tx, "r", "s", false); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `SELECT available FROM rider_profiles WHERE account_id='r'`).Scan(&outside); e != nil || outside {
		t.Fatal("committed offline availability not visible", e)
	}
	if _, e = sessions.Lookup(ctx, token, RiderEntry); e != nil {
		t.Fatal("going offline revoked assigned rider session", e)
	}
	for _, station := range []string{"other", "s"} {
		if station == "s" {
			if _, e = p.Exec(ctx, `UPDATE rider_profiles SET enabled=false WHERE account_id='r'`); e != nil {
				t.Fatal(e)
			}
		}
		tx, e = p.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = d.RiderAvailability(ctx, tx, "r", station); !errors.Is(e, ErrObjectNotFound) {
			t.Fatal("wrong-station/disabled rider readable", e)
		}
		tx.Rollback(ctx)
		tx, e = p.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if e = d.SetRiderAvailability(ctx, tx, "r", station, true); !errors.Is(e, ErrObjectNotFound) {
			t.Fatal("wrong-station/disabled rider writable", e)
		}
		tx.Rollback(ctx)
	}
	if _, e = d.RiderAvailability(ctx, nil, "r", "s"); e == nil {
		t.Fatal("nil tx accepted")
	}
	if e = d.SetRiderAvailability(ctx, nil, "r", "s", true); e == nil {
		t.Fatal("nil tx accepted")
	}
}
