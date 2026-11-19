package bootstrap

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
	"time"
)

func TestG1MissingApplicationDependenciesFailClosed(t *testing.T) {
	for _, o := range []ApplicationOptions{{}, {Pool: new(pgxpool.Pool)}, {Pool: new(pgxpool.Pool), Clock: &g1ZeroClock{}}, {Pool: new(pgxpool.Pool), Clock: &g1ValidClock{}}} {
		if h, err := NewApplication(o); err == nil || h != nil {
			t.Fatal("incomplete application was usable")
		}
	}
}

type g1ZeroClock struct{}

func (*g1ZeroClock) Now() time.Time { return time.Time{} }

type g1ValidClock struct{}

func (*g1ValidClock) Now() time.Time { return time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC) }
