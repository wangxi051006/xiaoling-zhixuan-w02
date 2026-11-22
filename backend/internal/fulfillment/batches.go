package fulfillment

import (
	"context"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"github.com/jackc/pgx/v5"
)

type batchInput struct {
	IDs []string `json:"ids"`
}

type batchResult struct {
	Count int `json:"count"`
}

func validateBatch(ids []string) error {
	if len(ids) == 0 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validID(id) || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	return nil
}

func lockBatchOrders(ctx context.Context, tx pgx.Tx, station string, ids []string, merchantID string) ([]string, error) {
	// Every batch acquires order locks in the same order, including batches
	// started by different roles or submitted with different selection orders.
	rows, err := tx.Query(ctx, `SELECT id FROM orders WHERE station_id=$1 AND id=ANY($2)
		AND ($3='' OR merchant_id=$3) ORDER BY id FOR UPDATE`, station, ids, merchantID)
	if err != nil {
		return nil, err
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(locked) != len(ids) {
		return nil, ErrNotFound
	}
	return locked, nil
}

func (s *Service) merchantBatch(ctx context.Context, tx pgx.Tx, actor identity.Session, ids []string, accept bool) error {
	if err := validateBatch(ids); err != nil {
		return err
	}
	if accept {
		limits, err := readOrderLimits(ctx, tx, actor.StationID, true)
		if err != nil {
			return err
		}
		if len(ids) > limits.MerchantBatch {
			return ErrOrderLimit
		}
	}
	merchantID := ""
	if actor.Entry == identity.MerchantEntry {
		var err error
		merchantID, err = identity.MerchantID(ctx, tx, actor)
		if err != nil {
			return err
		}
	}
	locked, err := lockBatchOrders(ctx, tx, actor.StationID, ids, merchantID)
	if err != nil {
		return err
	}
	for _, orderID := range locked {
		order, err := lockOrder(ctx, tx, actor.StationID, orderID)
		if err != nil {
			return err
		}
		if accept {
			if err := s.acceptOrder(ctx, tx, actor, order); err != nil {
				return err
			}
			continue
		}
		if order.Status != contract.OrderStatusPreparing || !order.MerchantAccepted {
			return ErrInvalidState
		}
		sorted := true
		if err := s.prepare(ctx, tx, actor, order, PrepareInput{Sorted: &sorted}); err != nil {
			return err
		}
		order.Stock = contract.StockStatusSorted
		if err := s.dispatch(ctx, tx, actor, order); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) riderBatch(ctx context.Context, tx pgx.Tx, actor identity.Session, ids []string, action string) error {
	if err := validateBatch(ids); err != nil {
		return err
	}
	if err := s.authorizeTaskCommand(ctx, tx, actor, action, len(ids)); err != nil {
		return err
	}
	orderIDs := make([]string, 0, len(ids))
	taskIDs := make(map[string]string, len(ids))
	for _, taskID := range ids {
		task, err := taskIdentity(ctx, tx, actor.StationID, taskID)
		if err != nil {
			return err
		}
		orderIDs = append(orderIDs, task.OrderID)
		taskIDs[task.OrderID] = taskID
	}
	locked, err := lockBatchOrders(ctx, tx, actor.StationID, orderIDs, "")
	if err != nil {
		return err
	}
	for _, orderID := range locked {
		order, err := lockOrder(ctx, tx, actor.StationID, orderID)
		if err != nil {
			return err
		}
		task, err := taskIdentity(ctx, tx, actor.StationID, taskIDs[orderID])
		if err != nil {
			return err
		}
		if action == "deliver" {
			if task.RiderID == nil || *task.RiderID != actor.AccountID {
				return ErrNotFound
			}
			if order.Method != contract.PaymentMethodBalance {
				return ErrBatchCOD
			}
		}
		if err := s.applyTaskCommand(ctx, tx, actor, order, task, action, len(ids), nil, ""); err != nil {
			return err
		}
	}
	return nil
}
