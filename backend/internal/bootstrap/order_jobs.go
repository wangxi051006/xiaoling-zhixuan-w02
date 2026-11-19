package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RunOrderJobs drives bounded database lease scans until service shutdown.
func RunOrderJobs(ctx context.Context, runDue func(context.Context, int) (int, error), ticks <-chan time.Time, logger *slog.Logger) error {
	if runDue == nil || ticks == nil {
		return errors.New("order jobs require a runner and tick source")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := runDue(ctx, 100); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("order_jobs_retry")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ticks:
			if !ok {
				return errors.New("order job tick source closed")
			}
		}
	}
}
