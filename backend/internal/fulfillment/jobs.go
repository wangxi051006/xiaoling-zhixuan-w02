package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"time"
)

type leasedJob struct {
	ID                              int64
	Token, StationID, Kind, OrderID string
}

func jobToken() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

// RunDue repairs missing jobs, claims short database leases and processes bounded work.
// A crash after claiming is recovered when the lease expires. New-order/dispatch switches do not apply.
func (s *Service) RunDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalid
	}
	now := s.Clock.Now().UTC()
	jobs := []leasedJob{}
	err := platform.InTransaction(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,station_id,status,slot_snapshot FROM orders o
 WHERE status NOT IN ('DELIVERED','CANCELED','RETURNED') AND
 (NOT EXISTS(SELECT 1 FROM outbox_jobs j WHERE j.station_id=o.station_id AND j.kind='ORDER_OVERDUE' AND j.dedupe_key=o.id)
 OR (o.status='SCHEDULED' AND NOT EXISTS(SELECT 1 FROM outbox_jobs j WHERE j.station_id=o.station_id AND j.kind='SCHEDULE_WAKE' AND j.dedupe_key=o.id)))
 ORDER BY created_at,id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		type pending struct {
			id, station, status string
			slot                contract.SlotSnapshot
		}
		items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
			var p pending
			var raw []byte
			err := r.Scan(&p.id, &p.station, &p.status, &raw)
			if err == nil {
				err = json.Unmarshal(raw, &p.slot)
			}
			return p, err
		})
		if err != nil {
			return err
		}
		for _, p := range items {
			if p.status == "SCHEDULED" {
				if err = s.enqueue(ctx, tx, p.station, p.id, "SCHEDULE_WAKE", p.slot.DispatchAt); err != nil {
					return err
				}
			}
			if err = s.enqueue(ctx, tx, p.station, p.id, "ORDER_OVERDUE", p.slot.EndsAt); err != nil {
				return err
			}
		}
		rows, err = tx.Query(ctx, `SELECT id,station_id,kind,payload->>'order_id' FROM outbox_jobs WHERE kind IN ('SCHEDULE_WAKE','ORDER_OVERDUE','ORDER_URGE','OVERDUE_ALERT') AND finished_at IS NULL AND available_at<=$1 AND (lease_until IS NULL OR lease_until<=$1) ORDER BY available_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
		if err != nil {
			return err
		}
		jobs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (leasedJob, error) {
			var j leasedJob
			e := r.Scan(&j.ID, &j.StationID, &j.Kind, &j.OrderID)
			j.Token = jobToken()
			return j, e
		})
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if _, err = tx.Exec(ctx, `UPDATE outbox_jobs SET lease_token=$2,lease_until=$3,attempts=attempts+1 WHERE id=$1`, j.ID, j.Token, now.Add(30*time.Second)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	done := 0
	var firstErr error
	for _, j := range jobs {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		err = platform.InTransaction(ctx, s.Pool, func(tx pgx.Tx) error {
			o, err := lockOrder(ctx, tx, j.StationID, j.OrderID)
			if err != nil {
				return err
			}
			var owned bool
			if err = tx.QueryRow(ctx, `SELECT lease_token=$2 AND lease_until>$3 AND finished_at IS NULL FROM outbox_jobs WHERE id=$1 FOR UPDATE`, j.ID, j.Token, s.Clock.Now().UTC()).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return nil
			}
			at := s.Clock.Now().UTC()
			if j.Kind == "ORDER_URGE" || j.Kind == "OVERDUE_ALERT" {
				if err = s.deliverAlert(ctx, tx, j, o); err != nil {
					return err
				}
			}
			actor := identity.Session{AccountID: "system", StationID: j.StationID}
			if j.Kind == "SCHEDULE_WAKE" && o.Status == contract.OrderStatusScheduled && !at.Before(o.Slot.DispatchAt) {
				if _, err = tx.Exec(ctx, `UPDATE orders SET status='PREPARING' WHERE id=$1 AND status='SCHEDULED'`, o.ID); err != nil {
					return err
				}
				if err = s.event(ctx, tx, actor, o.ID, "SCHEDULE_WOKE", ""); err != nil {
					return err
				}
			}
			if j.Kind == "ORDER_OVERDUE" && !at.Before(o.Slot.EndsAt) && !o.Overdue && o.Status != contract.OrderStatusDelivered && o.Status != contract.OrderStatusCanceled && o.Status != contract.OrderStatusReturned {
				if _, err = tx.Exec(ctx, `UPDATE orders SET overdue=true WHERE id=$1`, o.ID); err != nil {
					return err
				}
				if err = s.event(ctx, tx, actor, o.ID, "OVERDUE", ""); err != nil {
					return err
				}
				if err = s.enqueue(ctx, tx, j.StationID, o.ID, "OVERDUE_ALERT", at); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE outbox_jobs SET lease_token=NULL,lease_until=NULL,finished_at=$3 WHERE id=$1 AND lease_token=$2`, j.ID, j.Token, at)
			return err
		})
		if err != nil {
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			// Each job has its own transaction and recoverable lease. A failing
			// notification must not strand unrelated lifecycle work already claimed.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		done++
	}
	return done, firstErr
}
