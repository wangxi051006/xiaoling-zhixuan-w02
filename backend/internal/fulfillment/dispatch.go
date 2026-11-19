package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

type PrepareInput struct {
	Sorted *bool `json:"sorted"`
}

func (s *Service) prepare(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow, in PrepareInput) error {
	if in.Sorted == nil || !*in.Sorted {
		return ErrInvalid
	}
	if o.Status != contract.OrderStatusPreparing {
		return ErrInvalidState
	}
	if o.Stock == contract.StockStatusSorted {
		return nil
	}
	if o.Stock != contract.StockStatusUnsorted {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET stock_status='SORTED' WHERE id=$1`, o.ID); err != nil {
		return err
	}
	return s.event(ctx, tx, actor, o.ID, "PREPARED", "")
}
func (s *Service) dispatch(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow) error {
	if !s.AllowDispatch {
		return ErrNotReady
	}
	if o.Status == contract.OrderStatusReadyForClaim {
		return nil
	}
	if o.Status != contract.OrderStatusPreparing || o.Stock != contract.StockStatusSorted {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `INSERT INTO delivery_tasks(order_id,created_at) VALUES($1,$2)`, o.ID, s.Clock.Now().UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='READY_FOR_CLAIM' WHERE id=$1`, o.ID); err != nil {
		return err
	}
	return s.event(ctx, tx, actor, o.ID, "DISPATCHED", "")
}
func (s *Service) reclaim(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow, reason string) error {
	if !reasonPattern.MatchString(reason) {
		return ErrInvalid
	}
	if o.Status != contract.OrderStatusAwaitingPickup {
		return ErrInvalidState
	}
	result, err := tx.Exec(ctx, `UPDATE delivery_tasks SET state='OPEN',rider_id=NULL,claimed_at=NULL WHERE order_id=$1 AND state='ASSIGNED'`, o.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrInvalidState
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='READY_FOR_CLAIM' WHERE id=$1`, o.ID); err != nil {
		return err
	}
	return s.event(ctx, tx, actor, o.ID, "RECLAIMED", reason)
}

type taskRow struct {
	ID, OrderID, State string
	RiderID            *string
}

func taskIdentity(ctx context.Context, tx pgx.Tx, station, id string) (t taskRow, err error) {
	if !validID(id) {
		return t, ErrInvalid
	}
	err = tx.QueryRow(ctx, `SELECT t.id,t.order_id,t.state,t.rider_id FROM delivery_tasks t JOIN orders o ON o.id=t.order_id WHERE t.id=$1 AND o.station_id=$2`, id, station).Scan(&t.ID, &t.OrderID, &t.State, &t.RiderID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}
func (s *Service) taskCommand(ctx context.Context, tx pgx.Tx, actor identity.Session, id, action string, cash *bool, reason string) error {
	// Rider checks precede order locks consistently with identity enable/availability commands.
	if action == "claim" {
		if s.RiderState == nil {
			return ErrNotReady
		}
		available, err := s.RiderState.RiderAvailability(ctx, tx, actor.AccountID, actor.StationID)
		if err != nil {
			return err
		}
		if !available {
			return ErrInvalidState
		}
	} else {
		if err := s.Identity.AssertRider(ctx, tx, actor.AccountID, actor.StationID); err != nil {
			return err
		}
	}
	task, err := taskIdentity(ctx, tx, actor.StationID, id)
	if err != nil {
		return err
	}
	o, err := lockOrder(ctx, tx, actor.StationID, task.OrderID)
	if err != nil {
		return err
	}
	task, err = taskIdentity(ctx, tx, actor.StationID, id)
	if err != nil {
		return err
	}
	if action == "claim" {
		if task.State != "OPEN" || o.Status != contract.OrderStatusReadyForClaim {
			return ErrTaskClaimed
		}
		var active bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM delivery_tasks WHERE rider_id=$1 AND state IN ('ASSIGNED','PICKED_UP','RETURNING'))`, actor.AccountID).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrInvalidState
		}
		// The partial unique index also serializes simultaneous claims by this rider on distinct orders.
		if _, err = tx.Exec(ctx, `UPDATE delivery_tasks SET state='ASSIGNED',rider_id=$2,claimed_at=$3 WHERE id=$1`, id, actor.AccountID, s.Clock.Now().UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE orders SET status='AWAITING_PICKUP' WHERE id=$1`, o.ID); err != nil {
			return err
		}
		return s.event(ctx, tx, actor, o.ID, "CLAIMED", "")
	}
	if task.RiderID == nil || *task.RiderID != actor.AccountID {
		return ErrNotFound
	}
	switch action {
	case "pickup":
		if o.Status != contract.OrderStatusAwaitingPickup || task.State != "ASSIGNED" {
			return ErrInvalidState
		}
		if err = s.Catalog.Consume(ctx, tx, o.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE delivery_tasks SET state='PICKED_UP',picked_up_at=$2 WHERE id=$1`, id, s.Clock.Now().UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE orders SET status='IN_DELIVERY' WHERE id=$1`, o.ID); err != nil {
			return err
		}
		return s.event(ctx, tx, actor, o.ID, "PICKED_UP", "")
	case "deliver":
		return s.deliver(ctx, tx, actor, o, task, cash)
	case "return":
		if !reasonPattern.MatchString(reason) {
			return ErrInvalid
		}
		if o.Status != contract.OrderStatusInDelivery || task.State != "PICKED_UP" {
			return ErrInvalidState
		}
		if _, err = tx.Exec(ctx, `UPDATE delivery_tasks SET state='RETURNING' WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE orders SET status='RETURNING' WHERE id=$1`, o.ID); err != nil {
			return err
		}
		if err = s.event(ctx, tx, actor, o.ID, "RETURNING", reason); err != nil {
			return err
		}
		return s.notify(ctx, tx, "DELIVERY_INCIDENT", o)
	}
	return ErrInvalid
}
