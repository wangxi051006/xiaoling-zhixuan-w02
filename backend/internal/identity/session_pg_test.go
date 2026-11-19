//go:build integration

package identity

import (
	"errors"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
)

func TestSessionRotationIsolationAndDisable(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Fixture');
		INSERT INTO accounts(id,station_id,phone) VALUES ('a','s','+8613800000000');
		INSERT INTO role_bindings(account_id,role) VALUES ('a','CUSTOMER')`); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{time.Unix(1780000000, 0)}
	store := NewSessions(pool, clock)
	first, _, err := store.Issue(ctx, "a", UserEntry, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	second, current, err := store.Issue(ctx, "a", UserEntry, time.Time{})
	if err != nil || second == first || current.CSRFToken == "" {
		t.Fatalf("failed rotation: %v", err)
	}
	if _, err := store.Lookup(ctx, first, UserEntry); !errors.Is(err, ErrSession) {
		t.Fatalf("old token accepted: %v", err)
	}
	if _, err := store.Lookup(ctx, second, RiderEntry); !errors.Is(err, ErrSession) {
		t.Fatalf("wrong entry accepted: %v", err)
	}
	got, err := store.Lookup(ctx, second, UserEntry)
	if err != nil || got.AccountID != "a" || got.StationID != "s" || got.CSRFToken != current.CSRFToken || len(got.Roles) != 1 || got.Roles[0] != contract.RoleCustomer {
		t.Fatalf("session lookup: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, second, UserEntry); !errors.Is(err, ErrSession) {
		t.Fatalf("disabled account accepted: %v", err)
	}
}

func TestSessionRejectsStaffWithoutMFAAndRevokes(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Fixture');
		INSERT INTO accounts(id,station_id,issuer,subject) VALUES ('staff','s','https://idp.example.test','sub');
		INSERT INTO role_bindings(account_id,role) VALUES ('staff','FINANCE')`); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{time.Unix(1780000000, 0)}
	store := NewSessions(pool, clock)
	if _, _, err := store.Issue(ctx, "staff", AdminEntry, time.Time{}); !errors.Is(err, ErrSession) {
		t.Fatalf("missing MFA accepted: %v", err)
	}
	token, _, err := store.Issue(ctx, "staff", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, token, AdminEntry); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, token, AdminEntry); !errors.Is(err, ErrSession) {
		t.Fatalf("revoked token accepted: %v", err)
	}
}
