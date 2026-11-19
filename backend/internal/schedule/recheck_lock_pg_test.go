//go:build integration

package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type lockTraceTx struct {
	pgx.Tx
	steps *[]string
}

func (t lockTraceTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "FOR UPDATE") || strings.Contains(sql, "FOR NO KEY UPDATE") {
		*t.steps = append(*t.steps, sql)
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}
func (t lockTraceTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(sql, "INSERT INTO slot_reservations") || strings.HasPrefix(sql, "UPDATE slot_reservations") || strings.HasPrefix(sql, "UPDATE delivery_slots") {
		*t.steps = append(*t.steps, sql)
	}
	return t.Tx.Exec(ctx, sql, args...)
}

func TestRecheckActualLockProtocol(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	var steps []string
	err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		traced := lockTraceTx{tx, &steps}
		if err := (Store{}).ReserveSlot(ctx, traced, "early", "order", contract.DeliveryModeAsap, now); err != nil {
			return err
		}
		return (Store{}).ReleaseSlot(ctx, traced, "order", now)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 6 || !strings.Contains(steps[0], "FROM delivery_slots") || !strings.HasPrefix(steps[1], "INSERT INTO slot_reservations") || !strings.Contains(steps[2], "used=used+1") || !strings.Contains(steps[3], "FROM delivery_slots") || !strings.HasPrefix(steps[4], "UPDATE slot_reservations") || !strings.Contains(steps[5], "used=used-1") {
		t.Fatal("reservation/release changed slot-first lock protocol")
	}
	steps = nil
	windows, err := generateWindows("2026-11-19", "08:00", "09:00", 0)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			_, err := insertBatch(ctx, lockTraceTx{tx, &steps}, "s", now, windows, 1)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(steps) != 10 {
		t.Fatalf("expected station plus four window probes in each batch, got %d", len(steps))
	}
	for _, i := range []int{0, 5} {
		if !strings.Contains(steps[i], "FROM stations") || !strings.Contains(steps[i], "FOR NO KEY UPDATE") {
			t.Fatal("batch did not acquire station guard first")
		}
		for j := i + 1; j < i+5; j++ {
			if !strings.Contains(steps[j], "ORDER BY starts_at") || !strings.Contains(steps[j], "FOR UPDATE") {
				t.Fatal("window locks not stable within batch")
			}
		}
	}
}

func TestRecheckOriginalSlotRevalidationAndConfigVersion(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	for _, tc := range []struct {
		at   string
		mode contract.DeliveryMode
		want error
	}{
		{"2026-11-18T03:05:00Z", contract.DeliveryModeAsap, nil},
		{"2026-11-18T03:05:00.000000001Z", contract.DeliveryModeAsap, ErrSlotExpired},
		{"2026-11-18T02:44:59.999999999Z", contract.DeliveryModeScheduled, nil},
		{"2026-11-18T02:45:00Z", contract.DeliveryModeScheduled, ErrSlotExpired},
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = (Store{}).ReserveSlot(ctx, tx, "early", "test-order", tc.mode, mustTime(t, tc.at))
		_ = tx.Rollback(ctx)
		if !errors.Is(err, tc.want) {
			t.Fatalf("reserve %s: %v", tc.at, err)
		}
	}
	var version int64
	var updated time.Time
	if err := pool.QueryRow(ctx, `SELECT version,updated_at FROM delivery_slots WHERE id='early'`).Scan(&version, &updated); err != nil {
		t.Fatal(err)
	}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return (Store{}).ReserveSlot(ctx, tx, "early", "order", contract.DeliveryModeAsap, now)
	}); err != nil {
		t.Fatal(err)
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots WHERE id='early' AND version=$1 AND updated_at=$2`, version, updated) != 1 {
		t.Fatal("reserve changed configuration metadata")
	}
	// Once the quoted slot is full, submission refuses it even if late is free.
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return (Store{}).ReserveSlot(ctx, tx, "early", "new-order", contract.DeliveryModeAsap, now)
	}); !errors.Is(err, ErrSlotFull) {
		t.Fatal("submission silently switched quoted slot")
	}
	assertSlotUsed(t, pool, ctx, "late", 0)
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return (Store{}).ReleaseSlot(ctx, tx, "order", now) }); err != nil {
		t.Fatal(err)
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots WHERE id='early' AND version=$1 AND updated_at=$2`, version, updated) != 1 {
		t.Fatal("release changed configuration metadata")
	}
	h := recheckHandler(pool, now.Add(time.Minute), "s")
	slotCall(t, h, "PUT", "/admin/slots/early", `{"expected_version":1,"capacity":3,"accepting":true}`, "", 200)
	slotCall(t, h, "PUT", "/admin/slots/early", `{"expected_version":2,"capacity":1,"accepting":false}`, "", 200)
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots WHERE id='early' AND capacity=1 AND used=0 AND version=3 AND NOT accepting AND updated_at=$1`, now.Add(time.Minute)) != 1 {
		t.Fatal("configuration did not retain real timestamp/version")
	}
}
