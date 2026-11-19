package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOrderJobsScanImmediatelyRetryAndStopOnCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ticks := make(chan time.Time)
	calls := make(chan int, 2)
	done := make(chan error, 1)
	attempts := 0
	go func() {
		done <- RunOrderJobs(ctx, func(ctx context.Context, limit int) (int, error) {
			attempts++
			calls <- limit
			if attempts == 1 {
				return 0, errors.New("synthetic transient database failure")
			}
			return 1, nil
		}, ticks, nil)
	}()
	select {
	case n := <-calls:
		if n != 100 {
			t.Fatalf("unbounded scan: %d", n)
		}
	case err := <-done:
		t.Fatalf("no initial scan: %v", err)
	case <-ctx.Done():
		t.Fatal("initial scan timed out")
	}
	select {
	case ticks <- time.Now():
	case <-ctx.Done():
		t.Fatal("retry not listening")
	}
	select {
	case <-calls:
	case err := <-done:
		t.Fatalf("transient error stopped compensation: %v", err)
	case <-ctx.Done():
		t.Fatal("retry timed out")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("worker did not return cancellation", err)
	}
}

func TestOrderJobsRequireRunnerAndLiveTickSource(t *testing.T) {
	if err := RunOrderJobs(t.Context(), nil, make(chan time.Time), nil); err == nil {
		t.Fatal("missing runner accepted")
	}
	if err := RunOrderJobs(t.Context(), func(context.Context, int) (int, error) { return 0, nil }, nil, nil); err == nil {
		t.Fatal("missing tick source accepted")
	}
}
