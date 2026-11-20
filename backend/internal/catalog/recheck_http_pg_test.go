//go:build integration

package catalog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
)

func fixtureHandler(t *testing.T, h *HTTP) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	return platform.NewHandler(mux, nil, nil, 0, platform.BodyLimit{Pattern: ImageUploadPattern, Bytes: MaxImageBytes})
}
func catalogCall(t *testing.T, h http.Handler, method, path, key, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "https://admin.test/api/v1"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: want %d got %d: %s", method, path, status, w.Code, w.Body.String())
	}
	return w
}
func operatorFixture(w http.ResponseWriter, r *http.Request, e identity.Entry, mfa bool, roles ...contract.Role) (identity.Session, bool) {
	return identity.Session{AccountID: "operator", StationID: "s"}, true
}

func TestCatalogHTTPWritesAuditAndRollsBackOnAuditFailure(t *testing.T) {
	pool, ctx := stockFixture(t)
	h := fixtureHandler(t, &HTTP{Pool: pool, Authorize: operatorFixture})
	adjustment := `{"inventory_id":"i","delta":2,"expected_version":1,"reason_code":"COUNT"}`
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "audit", adjustment, 201)
	var n int
	var actor, station, object, reason, request string
	err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("adjustment missing audit: %d %v", n, err)
	}
	err = pool.QueryRow(ctx, `SELECT actor_id,station_id,object_id,reason_code,request_id FROM audit_events`).Scan(&actor, &station, &object, &reason, &request)
	if err != nil || actor != "operator" || station != "s" || object != "i" || reason != "COUNT" || request == "" {
		t.Fatal("incomplete audit context")
	}
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "audit", adjustment, 201)
	if _, err = pool.Exec(ctx, `CREATE FUNCTION fail_catalog_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private_diagnostic'; END $$; CREATE TRIGGER fail_catalog_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_catalog_audit()`); err != nil {
		t.Fatal(err)
	}
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "retry", `{"inventory_id":"i","delta":1,"expected_version":2,"reason_code":"COUNT"}`, 500)
	assertStock(t, pool, ctx, 5, 0, 1)
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE key='retry'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failure cached idempotency success")
	}
	catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":1,"name":"Changed"}`, 500)
	var name string
	var v int
	if err = pool.QueryRow(ctx, `SELECT name,version FROM products WHERE id='p'`).Scan(&name, &v); err != nil || name != "Snack" || v != 1 {
		t.Fatal("product change escaped failed audit")
	}
	catalogCall(t, h, "PUT", "/admin/products/p/promotion", "", `{"expected_version":1,"promotion":null}`, 500)
	catalogCall(t, h, "POST", "/admin/products", "create-retry", `{"name":"Soda","description":"","skus":[{"label":"Bottle","units_per_pack":1,"unit_price_fen":300}],"on_sale":true,"reorder_point":1}`, 500)
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM products`).Scan(&n); err != nil || n != 1 {
		t.Fatal("create escaped failed audit")
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_catalog_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "retry", `{"inventory_id":"i","delta":1,"expected_version":2,"reason_code":"COUNT"}`, 201)
	assertStock(t, pool, ctx, 6, 0, 2)
	catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":1,"name":"Changed"}`, 200)
	catalogCall(t, h, "PUT", "/admin/products/p/promotion", "", `{"expected_version":2,"promotion":null}`, 200)
	catalogCall(t, h, "POST", "/admin/products", "create-retry", `{"name":"Soda","description":"","skus":[{"label":"Bottle","units_per_pack":1,"unit_price_fen":300}],"on_sale":true,"reorder_point":1}`, 201)
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("audit duplicate or missing: %d %v", n, err)
	}
}

func TestCatalogHTTPInputPaginationPrivacyAndReplay(t *testing.T) {
	pool, ctx := stockFixture(t)
	var logs bytes.Buffer
	mux := http.NewServeMux()
	(&HTTP{Pool: pool, Authorize: operatorFixture}).Register(mux)
	h := platform.NewHandler(mux, nil, slog.New(slog.NewJSONHandler(&logs, nil)), 0)
	for _, query := range []string{"?limit=101", "?limit=-1", "?limit=1&limit=2", "?cursor=", "?q=", "?unknown=x"} {
		catalogCall(t, h, "GET", "/catalog/products"+query, "", "", 400)
	}
	w := catalogCall(t, h, "GET", "/catalog/products?q=absent", "", "", 200)
	if strings.TrimSpace(w.Body.String()) != `{"items":[],"next_cursor":null}` {
		t.Fatal("empty shape", w.Body.String())
	}
	w = catalogCall(t, h, "GET", "/catalog/products/p", "", "", 200)
	var public map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &public); err != nil {
		t.Fatal(err)
	}
	if len(public) != 4 || public["inventory"] != nil || public["promotion"] != nil || public["on_sale"] != nil || public["version"] != nil {
		t.Fatal("public fields leaked")
	}
	for _, body := range []string{`{"inventory_id":"i","delta":0,"expected_version":1,"reason_code":"COUNT"}`, `{"inventory_id":"i","delta":1.5,"expected_version":1,"reason_code":"COUNT"}`, `{"inventory_id":"i","delta":1,"expected_version":1,"reason_code":"bad"}`, `{"inventory_id":"i","delta":1,"expected_version":1,"reason_code":"COUNT","reserved":0}`} {
		catalogCall(t, h, "POST", "/admin/inventory/adjustments", "invalid", body, 400)
	}
	const adj = `{"inventory_id":"i","delta":1,"expected_version":1,"reason_code":"COUNT"}`
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "", adj, 400)
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", strings.Repeat("x", 129), adj, 400)
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "same", adj, 201)
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "same", strings.Replace(adj, `"delta":1`, `"delta":2`, 1), 409)
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "same", adj, 201)
	assertStock(t, pool, ctx, 4, 0, 1)
	catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":1,"category":"new"}`, 400)
	catalogCall(t, h, "POST", "/admin/categories", "", `{}`, 404)
	catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":1,"name":"private_marker"}`, 200)
	if strings.Contains(logs.String(), "private_marker") || strings.Contains(logs.String(), "COUNT") || strings.Contains(logs.String(), "Cookie") {
		t.Fatal("request contents logged")
	}
}

func TestCatalogHTTPRealPaginationLimitsAndLowStock(t *testing.T) {
	pool, ctx := stockFixture(t)
	h := fixtureHandler(t, &HTTP{Pool: pool, Authorize: operatorFixture})
	if _, err := pool.Exec(ctx, `INSERT INTO products(id,station_id,name,on_sale) SELECT 'page-'||lpad(n::text,2,'0'),'s','Paged',true FROM generate_series(1,21) n;INSERT INTO inventory(id,product_id) SELECT 'inventory-'||id,id FROM products WHERE id LIKE 'page-%'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query  string
		length int
		more   bool
	}{{"", 20, true}, {"?limit=1", 1, true}, {"?limit=100", 22, false}} {
		w := catalogCall(t, h, "GET", "/catalog/products"+tc.query, "", "", 200)
		var page catalogPage[Product]
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != tc.length || (page.Next != nil) != tc.more {
			t.Fatalf("pagination length/cursor mismatch: %d", len(page.Items))
		}
		if page.Next != nil {
			next := catalogCall(t, h, "GET", "/catalog/products?limit=100&cursor="+*page.Next, "", "", 200)
			var remaining catalogPage[Product]
			if err := json.Unmarshal(next.Body.Bytes(), &remaining); err != nil {
				t.Fatal(err)
			}
			if len(remaining.Items) != 22-tc.length || remaining.Next != nil {
				t.Fatal("cursor duplicated or omitted items")
			}
		}
	}
	p, err := (Store{}).GetProduct(ctx, pool, "s", "p", false)
	if err != nil || p.Inventory.LowStock() {
		t.Fatal("unexpected initial low stock")
	}
	if _, err = pool.Exec(ctx, `UPDATE inventory SET reserved=3 WHERE id='i'`); err != nil {
		t.Fatal(err)
	}
	p, err = (Store{}).GetProduct(ctx, pool, "s", "p", false)
	if err != nil || !p.Inventory.LowStock() {
		t.Fatal("low stock ignored actual reserved units")
	}
}

func TestCatalogIdempotencyCompletionFailureRollsBackAuditAndStock(t *testing.T) {
	pool, ctx := stockFixture(t)
	h := fixtureHandler(t, &HTTP{Pool: pool, Authorize: operatorFixture})
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_idempotency_completion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture_idempotency_completion'; END $$;CREATE TRIGGER fail_idempotency_completion BEFORE UPDATE ON idempotency_keys FOR EACH ROW EXECUTE FUNCTION fail_idempotency_completion()`); err != nil {
		t.Fatal(err)
	}
	body := `{"inventory_id":"i","delta":1,"expected_version":1,"reason_code":"COUNT"}`
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "completion", body, 500)
	assertStock(t, pool, ctx, 3, 0, 0)
	var n int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events)+(SELECT count(*) FROM idempotency_keys)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("completion failure left audit or key")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_idempotency_completion ON idempotency_keys`); err != nil {
		t.Fatal(err)
	}
	catalogCall(t, h, "POST", "/admin/inventory/adjustments", "completion", body, 201)
	assertStock(t, pool, ctx, 4, 0, 1)
}
