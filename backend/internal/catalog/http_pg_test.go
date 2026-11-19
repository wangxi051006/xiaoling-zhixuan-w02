//go:build integration

package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
)

func TestCatalogHTTPContract(t *testing.T) {
	pool, ctx := stockFixture(t)
	mux := http.NewServeMux()
	allowed := true
	auth := func(w http.ResponseWriter, r *http.Request, entry identity.Entry, mfa bool, roles ...contract.Role) (identity.Session, bool) {
		if entry != identity.AdminEntry || mfa || len(roles) != 1 || roles[0] != contract.RoleOperator {
			t.Fatalf("unexpected authorization policy")
		}
		if !allowed {
			w.WriteHeader(http.StatusForbidden)
		}
		return identity.Session{AccountID: "operator", StationID: "s"}, allowed
	}
	(&HTTP{Pool: pool, Authorize: auth}).Register(mux)
	call := func(method, path, key, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: want %d, got %d %s", method, path, status, w.Code, w.Body.String())
		}
		return w
	}
	call("GET", "/catalog/products?limit=0", "", "", 400)
	call("GET", "/catalog/products?q=Snack&category=uncategorized", "", "", 200)
	w := call("GET", "/catalog/products/p", "", "", 200)
	if strings.Contains(w.Body.String(), "inventory") || strings.Contains(w.Body.String(), "version") {
		t.Fatal(w.Body.String())
	}
	allowed = false
	call("GET", "/admin/products", "", "", 403)
	allowed = true
	const create = `{"name":"Soda","description":"","on_sale":false,"reorder_point":2,"skus":[{"label":"Bottle","units_per_pack":1,"unit_price_fen":300}]}`
	call("POST", "/admin/products", "", ""+create, 400)
	w = call("POST", "/admin/products", "create", create, 201)
	var p AdminProduct
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.ID == "" || !p.Inventory.LowStock() {
		t.Fatalf("create response %s: %v", w.Body.String(), err)
	}
	if replay := call("POST", "/admin/products", "create", create, 201); replay.Body.String() != w.Body.String() {
		t.Fatal("create replay changed")
	}
	call("POST", "/admin/products", "create", strings.Replace(create, "Soda", "Juice", 1), 409)
	call("GET", "/catalog/products/"+p.ID, "", "", 404)
	call("PATCH", "/admin/products/"+p.ID, "", `{"expected_version":1,"on_sale":true}`, 200)
	call("PATCH", "/admin/products/"+p.ID, "", `{"expected_version":1,"on_sale":false}`, 409)
	call("GET", "/catalog/products/"+p.ID, "", "", 200)
	call("PUT", "/admin/products/"+p.ID+"/promotion", "", `{"expected_version":2,"promotion":{"starts_at":"2026-11-18T08:00:00+08:00","ends_at":"2026-11-19T08:00:00+08:00","tiers":[{"min_qty":2,"discount_bps":1500}]}}`, 200)
	call("PUT", "/admin/products/"+p.ID+"/promotion", "", `{"expected_version":3,"promotion":null}`, 200)
	call("POST", "/admin/inventory/adjustments", "adjust", `{"inventory_id":"i","reason_code":"COUNT","delta":2,"expected_version":1,"reserved":0}`, 400)
	const adjustment = `{"inventory_id":"i","reason_code":"COUNT","delta":2,"expected_version":1}`
	w = call("POST", "/admin/inventory/adjustments", "adjust", adjustment, 201)
	if replay := call("POST", "/admin/inventory/adjustments", "adjust", adjustment, 201); replay.Body.String() != w.Body.String() {
		t.Fatal("adjustment replay changed")
	}
	assertStock(t, pool, ctx, 5, 0, 1)
}
