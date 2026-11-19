package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
)

// The order lock is held by RunDue throughout recipient revalidation and send.
// An external receiver can observe a send before the DB commit; the stable key
// lets it merge retries after crashes or transaction failures.
func (s *Service) deliverAlert(ctx context.Context, tx pgx.Tx, j leasedJob, o orderRow) error {
	switch o.Status {
	case contract.OrderStatusDelivered, contract.OrderStatusCanceled, contract.OrderStatusReturned:
		return nil
	}
	if j.Kind == "OVERDUE_ALERT" && (!o.Overdue || s.Clock.Now().UTC().Before(o.Slot.EndsAt)) {
		return nil
	}
	if j.Kind == "ORDER_URGE" {
		var taskID, state string
		var rider *string
		err := tx.QueryRow(ctx, `SELECT id,state,rider_id FROM delivery_tasks WHERE order_id=$1`, o.ID).Scan(&taskID, &state, &rider)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && rider != nil && (state == "ASSIGNED" || state == "PICKED_UP" || state == "RETURNING") {
			// The owning identity port includes profile enablement. Temporary
			// unavailability does not revoke an existing assignment's notifications.
			err = s.Identity.AssertRider(ctx, tx, *rider, j.StationID)
			if err != nil && !errors.Is(err, identity.ErrObjectNotFound) {
				return err
			}
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO notifications(station_id,recipient_id,kind,task_id,outbox_job_id,created_at) VALUES($1,$2,'ORDER_URGE',$3,$4,$5) ON CONFLICT(outbox_job_id,recipient_id) DO NOTHING`, j.StationID, *rider, taskID, j.ID, s.Clock.Now().UTC())
				return err
			}
		}
	}
	rows, err := tx.Query(ctx, `SELECT a.id FROM accounts a JOIN role_bindings r ON r.account_id=a.id AND r.role='OPERATOR' WHERE a.station_id=$1 AND a.enabled ORDER BY a.id FOR SHARE OF a,r`, j.StationID)
	if err != nil {
		return err
	}
	recipients, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	// Preserve retryable work when no configured sink can deliver the alert.
	if s.AlertSink == nil || len(recipients) == 0 {
		return ErrNotReady
	}
	return s.AlertSink.SendAlert(ctx, Alert{IdempotencyKey: fmt.Sprintf("fulfillment-outbox:%d", j.ID), Kind: j.Kind, StationID: j.StationID, OrderID: j.OrderID, RecipientIDs: recipients})
}
