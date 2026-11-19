package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RunEmailJobs drives bounded email outbox leases until shutdown. It is
// deliberately separate from order jobs so a mail outage cannot stall lifecycle
// work, and vice versa.
func RunEmailJobs(ctx context.Context, runDue func(context.Context, int) (int, error), ticks <-chan time.Time, logger *slog.Logger) error {
	if runDue == nil || ticks == nil {
		return errors.New("email jobs require a runner and tick source")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := runDue(ctx, 20); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("email_jobs_retry")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ticks:
			if !ok {
				return errors.New("email job tick source closed")
			}
		}
	}
}
