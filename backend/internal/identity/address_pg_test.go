//go:build integration

package identity

import (
	"errors"
	"testing"

	"campus.local/xiaoling/internal/contract"
)

func TestAddressOwnershipVersionAndSoftDelete(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Campus');
		INSERT INTO service_buildings(id,station_id,name) VALUES ('b','s','Building');
		INSERT INTO accounts(id,station_id,phone) VALUES ('a','s','+8613800000000'),('b','s','+8613800000001');
		INSERT INTO role_bindings(account_id,role) VALUES ('a','CUSTOMER'),('b','CUSTOMER')`)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewDirectory(pool)
	input := AddressInput{BuildingID: "b", Room: "202", ContactName: "Sample", ContactPhone: "+8613800000000"}
	bad := input
	bad.BuildingID = "outside"
	if _, err := svc.CreateAddress(ctx, "a", "s", bad); !errors.Is(err, ErrAddressInput) {
		t.Fatalf("invalid building: %v", err)
	}
	created, err := svc.CreateAddress(ctx, "a", "s", input)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := svc.ResolveAddress(ctx, tx, "b", created.AddressID); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("cross-account lookup: %v", err)
	}
	snapshot, err := svc.ResolveAddress(ctx, tx, "a", created.AddressID)
	if err != nil || snapshot.Room != "202" {
		t.Fatalf("snapshot: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	newRoom := "203"
	updated, err := svc.UpdateAddress(ctx, "a", "s", created.AddressID, 1, AddressPatch{Room: &newRoom})
	if err != nil || updated.Version != 2 || updated.Room != "203" {
		t.Fatalf("update: %v", err)
	}
	if err := svc.DeleteAddress(ctx, "a", "s", created.AddressID, 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := svc.DeleteAddress(ctx, "b", "s", created.AddressID, 2); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("cross-account delete: %v", err)
	}
	if err := svc.DeleteAddress(ctx, "a", "s", created.AddressID, 2); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListAddresses(ctx, "a", "s", "", 20)
	if err != nil || len(list) != 0 || snapshot.Room != "202" {
		t.Fatalf("soft deletion changed visible history: %v", err)
	}
}

func TestAssertRiderChecksRoleStationAndEnabled(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Campus');
		INSERT INTO accounts(id,station_id,issuer,subject) VALUES ('r','s','https://idp.example.test','r'),('f','s','https://idp.example.test','f');
		INSERT INTO role_bindings(account_id,role) VALUES ('r','RIDER'),('f','FINANCE');
		INSERT INTO rider_profiles(account_id) VALUES ('r')`)
	if err != nil {
		t.Fatal(err)
	}
	var svc contract.IdentityPort = NewDirectory(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := svc.AssertRider(ctx, tx, "r", "s"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][2]string{{"f", "s"}, {"r", "outside"}} {
		if err := svc.AssertRider(ctx, tx, tc[0], tc[1]); !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("unexpected member: %v", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE rider_profiles SET enabled=false WHERE account_id='r'`); err != nil {
		t.Fatal(err)
	}
	if err := svc.AssertRider(ctx, tx, "r", "s"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("disabled rider: %v", err)
	}
}
