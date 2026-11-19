package contract

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAllDomainPortsShareCallerTransaction(t *testing.T) {
	for _, port := range []reflect.Type{reflect.TypeFor[IdentityPort](), reflect.TypeFor[CatalogPort](), reflect.TypeFor[SchedulePort](), reflect.TypeFor[SettlementPort]()} {
		for i := range port.NumMethod() {
			m := port.Method(i)
			if m.Type.NumIn() < 2 || m.Type.In(1) != reflect.TypeFor[pgx.Tx]() {
				t.Errorf("%s.%s must accept the caller's pgx.Tx", port.Name(), m.Name)
			}
		}
	}
	if _, ok := reflect.TypeFor[CatalogPort]().MethodByName("RestockCanceled"); !ok {
		t.Error("canceled pre-pickup stock needs an atomic loss and release port")
	}
}

func TestOpenAPIEnumAndIdempotencyContract(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Paths map[string]map[string]struct {
			Idempotent bool `json:"x-idempotency-required"`
			Parameters []struct {
				Ref string `json:"$ref"`
			} `json:"parameters"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Enum []string `json:"enum"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	for k, want := range ContractEnums() {
		if !slices.Equal(schema.Components.Schemas[k].Enum, want) {
			t.Errorf("%s Go/OpenAPI mismatch", k)
		}
	}
	if len(schema.Paths) < 36 {
		t.Fatalf("only %d paths; public contract not established", len(schema.Paths))
	}
	count := 0
	for path, methods := range schema.Paths {
		for method, op := range methods {
			hasKey := slices.ContainsFunc(op.Parameters, func(p struct {
				Ref string `json:"$ref"`
			}) bool { return p.Ref == "#/components/parameters/IdempotencyKey" })
			if op.Idempotent != hasKey {
				t.Errorf("%s %s idempotency parameter mismatch", method, path)
			}
			if hasKey {
				count++
			}
		}
	}
	if count < 15 || !schema.Paths["/orders"]["post"].Idempotent || !schema.Paths["/rider/tasks/{id}/deliver"]["post"].Idempotent {
		t.Fatalf("idempotency coverage incomplete: %d writes", count)
	}
}
