//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"campus.local/xiaoling/internal/bootstrap"
	"campus.local/xiaoling/internal/catalog"
	"campus.local/xiaoling/internal/fulfillment"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/internal/schedule"
	"campus.local/xiaoling/internal/settlement"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pidKey struct{}
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) Now() time.Time   { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *testClock) Set(at time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.at = at }

type fixture struct {
	t                    *testing.T
	ctx                  context.Context
	pool                 *pgxpool.Pool
	clock                *testClock
	service              *fulfillment.Service
	h                    http.Handler
	sku, pack, inventory string
	seq                  int
	credentials          map[string]credential
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required; missing PG must fail")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	schema := "p05_" + hex.EncodeToString(raw[:])
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 12
	cfg.BeforeAcquire = func(ctx context.Context, conn *pgx.Conn) bool {
		if ch, ok := ctx.Value(pidKey{}).(chan uint32); ok {
			select {
			case ch <- conn.PgConn().PID():
			default:
			}
		}
		return true
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Close()
		if _, e := admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	if err = platform.Migrate(ctx, p, migrations.SQL); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, pool: p, clock: &testClock{at: time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)}}
	seed := `INSERT INTO stations(id,name) VALUES('s','Synthetic station');INSERT INTO accounts(id,station_id,phone) VALUES('buyer','s','+8613800000001'),('other','s','+8613800000002');INSERT INTO accounts(id,station_id,issuer,subject) VALUES('r1','s','synthetic','r1'),('r2','s','synthetic','r2'),('op','s','synthetic','op'),('fin','s','synthetic','fin'),('fin2','s','synthetic','fin2');INSERT INTO role_bindings(account_id,role) VALUES('buyer','CUSTOMER'),('other','CUSTOMER'),('r1','RIDER'),('r2','RIDER'),('op','OPERATOR'),('fin','FINANCE'),('fin2','FINANCE');INSERT INTO rider_profiles(account_id,available) VALUES('r1',true),('r2',true);INSERT INTO service_buildings(id,station_id,name) VALUES('b','s','Synthetic building');INSERT INTO addresses(id,account_id,building_id,room,contact_name,contact_phone) VALUES('a','buyer','b','private-room-517','Synthetic contact','+8613800000001'),('a2','other','b','private-room-618','Other contact','+8613800000002');INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen) VALUES('s','buyer',10000,10000),('s','other',10000,10000);`
	f.exec(seed)
	f.exec(`INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity) VALUES('asap','s',$1,$2,$3,20),('later','s',$4,$5,$6,20)`, f.clock.Now().Add(10*time.Minute), f.clock.Now().Add(25*time.Minute), f.clock.Now().Add(-5*time.Minute), f.clock.Now().Add(time.Hour), f.clock.Now().Add(75*time.Minute), f.clock.Now().Add(45*time.Minute))
	c := catalog.Store{Clock: f.clock}
	err = platform.InTransaction(ctx, p, func(tx pgx.Tx) error {
		product, e := c.CreateProduct(ctx, tx, "s", catalog.ProductInput{Name: "Synthetic snack", OnSale: true, SKUs: []catalog.SKUInput{{Label: "single", UnitsPerPack: 1, UnitPriceFen: 100}, {Label: "three", UnitsPerPack: 3, UnitPriceFen: 270}}})
		if e != nil {
			return e
		}
		f.sku, f.pack, f.inventory = product.SKUs[0].ID, product.SKUs[1].ID, product.Inventory.ID
		if product.SKUs[0].UnitsPerPack != 1 {
			f.sku, f.pack = f.pack, f.sku
		}
		_, e = tx.Exec(ctx, `UPDATE inventory SET on_hand=100 WHERE id=$1`, f.inventory)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := identity.NewDirectory(p)
	riderState, _ := any(directory).(fulfillment.RiderStatePort)
	policy := settlement.PaymentPolicy{CODEnabled: true, BalanceEnabled: true}
	f.service = &fulfillment.Service{Pool: p, Clock: f.clock, Identity: directory, Catalog: c, Schedule: schedule.Store{}, Settlement: settlement.Store{Policy: policy}, RiderState: riderState, AlertSink: &recordingAlertSink{}, Policy: policy, AcceptNewOrders: true, AllowDispatch: true}
	f.h, err = bootstrap.NewApplication(bootstrap.ApplicationOptions{Pool: p, Clock: f.clock, UserOrigin: "https://user.synthetic.test", RiderOrigin: "https://rider.synthetic.test", AdminOrigin: "https://admin.synthetic.test", Payments: policy, Orders: f.service})
	if err != nil {
		t.Fatal(err)
	}
	f.credentials = map[string]credential{}
	for _, actor := range []string{"buyer", "other", "r1", "r2", "op", "fin", "fin2"} {
		f.issue(actor)
	}
	return f
}
func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) n(sql string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

type credential struct{ Token, CSRF, Origin string }

func (f *fixture) issue(actor string) {
	entry := identity.UserEntry
	origin := "https://user.synthetic.test"
	mfa := time.Time{}
	if actor == "r1" || actor == "r2" {
		entry = identity.RiderEntry
		origin = "https://rider.synthetic.test"
		mfa = f.clock.Now()
	} else if actor == "op" || actor == "fin" || actor == "fin2" {
		entry = identity.AdminEntry
		origin = "https://admin.synthetic.test"
		mfa = f.clock.Now()
	}
	token, session, err := identity.NewSessions(f.pool, f.clock).Issue(f.ctx, actor, entry, mfa)
	if err != nil {
		f.t.Fatal(err)
	}
	f.credentials[actor] = credential{token, session.CSRFToken, origin}
}
func (f *fixture) requestContext(ctx context.Context, actor, method, path, body, key string) *httptest.ResponseRecorder {
	return f.requestFrom(ctx, actor, method, path, body, key, "")
}
func (f *fixture) requestFrom(ctx context.Context, actor, method, path, body, key, source string) *httptest.ResponseRecorder {
	c := f.credentials[actor]
	r := httptest.NewRequest(method, c.Origin+"/api/v1"+path, strings.NewReader(body)).WithContext(ctx)
	if source != "" {
		r.RemoteAddr = source
	}
	r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: c.Token})
	r.Header.Set("Origin", c.Origin)
	r.Header.Set("X-CSRF-Token", c.CSRF)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	f.assertWire(method, path, w.Code, w.Body.Bytes())
	return w
}
func (f *fixture) request(actor, method, path, body, key string) *httptest.ResponseRecorder {
	return f.requestContext(f.ctx, actor, method, path, body, key)
}
func (f *fixture) call(actor, method, path, body string, want int) map[string]any {
	f.t.Helper()
	f.seq++
	w := f.request(actor, method, path, body, fmt.Sprintf("key-%d", f.seq))
	if w.Code != want {
		f.t.Fatalf("%s %s %s status=%d want=%d body=%s", actor, method, path, w.Code, want, w.Body)
	}
	out := map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *fixture) quote(actor, mode, method string) string {
	f.t.Helper()
	address := "a"
	if actor == "other" {
		address = "a2"
	}
	slot := ""
	if mode == "SCHEDULED" {
		slot = `,"slot_id":"later"`
	}
	v := f.call(actor, "POST", "/checkout/quote", fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":1}],"address_id":%q,"delivery_mode":%q,"payment_method":%q%s}`, f.sku, address, mode, method, slot), 201)
	return v["quote_id"].(string)
}
func (f *fixture) order(actor, mode, method string) string {
	f.t.Helper()
	q := f.quote(actor, mode, method)
	return f.call(actor, "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), 201)["id"].(string)
}
func (f *fixture) dispatch(id string) string {
	f.call("op", "POST", "/admin/orders/"+id+"/prepare", `{"sorted":true}`, 200)
	f.call("op", "POST", "/admin/orders/"+id+"/dispatch", "", 200)
	var task string
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM delivery_tasks WHERE order_id=$1`, id).Scan(&task); err != nil {
		f.t.Fatal(err)
	}
	return task
}
func (f *fixture) pickup(id string) string {
	task := f.dispatch(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	f.call("r1", "POST", "/rider/tasks/"+task+"/pickup", "", 200)
	return task
}

func TestP05CODLifecyclePrivacyAndUniqueCashIncome(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "COD")
	task := f.dispatch(id)
	lobby := f.call("r2", "GET", "/rider/tasks", "", 200)
	encoded, _ := json.Marshal(lobby)
	if strings.Contains(string(encoded), "private-room") || strings.Contains(string(encoded), "138000") {
		t.Fatal("lobby leaked contact")
	}
	f.call("other", "GET", "/orders/"+id, "", 404)
	f.call("r1", "POST", "/rider/tasks/"+task+"/claim", "", 200)
	f.call("r2", "GET", "/rider/tasks/"+task, "", 404)
	f.call("r1", "PUT", "/rider/availability", `{"available":false}`, 200)
	f.call("r1", "POST", "/rider/tasks/"+task+"/pickup", "", 200)
	f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{"cod_received":false}`, 409)
	f.call("r1", "POST", "/rider/tasks/"+task+"/deliver", `{}`, 409)
	body := `{"cod_received":true}`
	w := f.request("r1", "POST", "/rider/tasks/"+task+"/deliver", body, "cash-delivery")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	replay := f.request("r1", "POST", "/rider/tasks/"+task+"/deliver", body, "cash-delivery")
	if replay.Code != 200 || replay.Body.String() != w.Body.String() {
		t.Fatal("delivery replay differs")
	}
	if f.n(`SELECT count(*) FROM cod_receipts`) != 1 || f.n(`SELECT count(*) FROM rider_earnings`) != 1 || f.n(`SELECT reserved FROM inventory WHERE id=$1`, f.inventory) != 0 || f.n(`SELECT on_hand FROM inventory WHERE id=$1`, f.inventory) != 99 {
		t.Fatal("cash/income/stock invariant")
	}
	v := f.call("r1", "GET", "/rider/tasks/"+task, "", 200)
	if _, ok := v["address"]; ok {
		t.Fatal("completed contact retained")
	}
	f.call("r2", "GET", "/rider/history", "", 200)
	f.call("r1", "GET", "/rider/earnings", "", 200)
	var receiptDate string
	if err := f.pool.QueryRow(f.ctx, `SELECT (collected_at AT TIME ZONE 'Asia/Shanghai')::date::text FROM cod_receipts WHERE order_id=$1`, id).Scan(&receiptDate); err != nil {
		t.Fatal(err)
	}
	dashboard := f.call("op", "GET", "/admin/dashboard?date="+receiptDate, "", 200)
	if dashboard["net_collected_fen"] != float64(100) {
		t.Fatal(dashboard)
	}
}

func TestP05BoundQuotesRevalidationAndCreationIdempotency(t *testing.T) {
	f := setup(t)
	q := f.quote("buyer", "ASAP", "COD")
	f.call("other", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), 404)
	w := f.request("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), "create-replay")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	r := f.request("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), "create-replay")
	if r.Code != 201 || r.Body.String() != w.Body.String() {
		t.Fatal("create replay")
	}
	r = f.request("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q,"note":"changed"}`, q), "create-replay")
	if r.Code != 409 || !strings.Contains(r.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal(r.Code, r.Body)
	}
	// A different idempotency key cannot submit the already used quote twice.
	f.call("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), 409)
	if f.n(`SELECT count(*) FROM orders`) != 1 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 1 {
		t.Fatal("duplicate reservations")
	}
}

func TestP05SortedCancelRestockAndReturnBalance(t *testing.T) {
	f := setup(t)
	id := f.order("buyer", "ASAP", "BALANCE")
	f.call("op", "POST", "/admin/orders/"+id+"/prepare", `{"sorted":true}`, 200)
	f.call("buyer", "POST", "/orders/"+id+"/cancel", `{"reason_code":"CUSTOMER_CANCEL"}`, 409)
	f.call("op", "POST", "/admin/orders/"+id+"/cancel", `{"reason_code":"OPS_CANCEL"}`, 200)
	if f.n(`SELECT reserved FROM inventory WHERE id=$1`, f.inventory) != 1 || f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000 || f.n(`SELECT used FROM delivery_slots WHERE id='asap'`) != 1 {
		t.Fatal("sorted cancel premature release")
	}
	f.call("op", "POST", "/admin/orders/"+id+"/restock", fmt.Sprintf(`{"reason_code":"DAMAGE","damaged":[{"inventory_id":%q,"damaged_units":1}]}`, f.inventory), 200)
	if f.n(`SELECT on_hand FROM inventory WHERE id=$1`, f.inventory) != 99 || f.n(`SELECT reserved FROM inventory WHERE id=$1`, f.inventory) != 0 {
		t.Fatal("damage restock")
	}
	id = f.order("buyer", "ASAP", "BALANCE")
	task := f.pickup(id)
	f.call("r1", "POST", "/rider/tasks/"+task+"/return", `{"reason_code":"REFUSED"}`, 200)
	f.service.Policy = settlement.PaymentPolicy{}
	f.service.AcceptNewOrders = false
	f.service.AllowDispatch = false
	f.call("op", "POST", "/admin/orders/"+id+"/return-inspect", fmt.Sprintf(`{"reason_code":"ACCEPT_RETURN","sellable":[{"inventory_id":%q,"sellable_units":1}]}`, f.inventory), 200)
	if f.n(`SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != 10000 || f.n(`SELECT count(*) FROM rider_earnings`) != 0 || f.n(`SELECT on_hand FROM inventory WHERE id=$1`, f.inventory) != 99 {
		t.Fatal("return settlement invariant")
	}
}

func TestP05StrictInputsAndDirectPaymentSwitch(t *testing.T) {
	f := setup(t)
	for _, body := range []string{`{"lines":[],"address_id":"a","delivery_mode":"ASAP","payment_method":"COD"}`, fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":1}],"address_id":"a","delivery_mode":"ASAP","payment_method":"COD","total_fen":1}`, f.sku), `{"sorted":true,"sorted":true}`} {
		f.call("buyer", "POST", "/checkout/quote", body, 400)
	}
	f.call("buyer", "POST", "/checkout/quote", fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":1}],"address_id":"a","delivery_mode":"ASAP","payment_method":"WECHAT"}`, f.sku), 409)
	q := f.quote("buyer", "ASAP", "BALANCE")
	f.service.Policy = settlement.PaymentPolicy{CODEnabled: true}
	f.call("buyer", "POST", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), 409)
	if f.n(`SELECT count(*) FROM orders`) != 0 {
		t.Fatal("disabled method created order")
	}
}
