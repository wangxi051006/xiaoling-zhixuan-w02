package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"campus.local/xiaoling/internal/fulfillment"
)

// Keep only recent successful sends. Durable consumers deduplicate the stable
// event_id across process restarts; this cache bounds local retry noise.
const recentOrderAlerts = 1024

type orderAlertLog struct {
	logger *slog.Logger
	mu     sync.Mutex
	seen   map[string]struct{}
	order  []string
	next   int
}

func newOrderAlertLog(logger *slog.Logger) *orderAlertLog {
	return &orderAlertLog{logger: logger, seen: make(map[string]struct{}), order: make([]string, 0, recentOrderAlerts)}
}

func (s *orderAlertLog) SendAlert(ctx context.Context, alert fulfillment.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := s.seen[alert.IdempotencyKey]; ok {
		return nil
	}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "order_operational_alert", 0)
	record.Add("event_id", alert.IdempotencyKey, "kind", alert.Kind, "station_id", alert.StationID, "order_id", alert.OrderID, "recipient_ids", alert.RecipientIDs)
	if err := s.logger.Handler().Handle(ctx, record); err != nil {
		return err
	}
	if len(s.order) < recentOrderAlerts {
		s.order = append(s.order, alert.IdempotencyKey)
	} else {
		delete(s.seen, s.order[s.next])
		s.order[s.next] = alert.IdempotencyKey
		s.next = (s.next + 1) % recentOrderAlerts
	}
	s.seen[alert.IdempotencyKey] = struct{}{}
	return nil
}
