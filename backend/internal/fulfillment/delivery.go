package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"context"
	"github.com/jackc/pgx/v5"
)

type DeliverInput struct {
	Cash *bool `json:"cod_received,omitempty"`
}

func (s *Service) deliver(ctx context.Context, tx pgx.Tx, actor identity.Session, o orderRow, t taskRow, cash *bool) error {
	if o.Status != contract.OrderStatusInDelivery || t.State != "PICKED_UP" {
		return ErrInvalidState
	}
	if o.Method == contract.PaymentMethodCod {
		if cash == nil || !*cash {
			return ErrInvalidState
		}
		if err := s.Settlement.RecordCODCollection(ctx, tx, o.ID, actor.AccountID); err != nil {
			return err
		}
	}
	// Collection and state, event and unique income are committed by the caller's single transaction.
	now := s.Clock.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE delivery_tasks SET state='DELIVERED',finished_at=$2 WHERE id=$1`, t.ID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rider_earnings(task_id,rider_id,amount_fen,recorded_at) VALUES($1,$2,$3,$4)`, t.ID, actor.AccountID, o.FeeFen, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET status='DELIVERED' WHERE id=$1`, o.ID); err != nil {
		return err
	}
	return s.event(ctx, tx, actor, o.ID, "DELIVERED", "")
}
