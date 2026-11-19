//go:build integration

package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing/fstest"
)

func recheckHandler(pool *pgxpool.Pool, now time.Time, station string) http.Handler {
	h := HTTP{Pool: pool, Clock: fixedClock{now}, Authorize: func(http.ResponseWriter, *http.Request, identity.Entry, bool, ...contract.Role) (identity.Session, bool) {
		return identity.Session{AccountID: "operator", StationID: station}, true
	}}
	mux := http.NewServeMux()
	h.Register(mux)
	return platform.NewHandler(mux, nil, nil, 0)
}

func slotCall(t *testing.T, h http.Handler, method, path, body, key string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "https://admin.example.test/api/v1"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body)
	}
	if want >= 400 {
		assertSlotError(t, w)
	}
	return w
}

func assertSlotError(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var v map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || len(v) != 3 || len(v["message"]) == 0 || len(v["request_id"]) < 3 {
		t.Fatalf("invalid error envelope: %s", w.Body)
	}
	var code, id string
	_ = json.Unmarshal(v["code"], &code)
	_ = json.Unmarshal(v["request_id"], &id)
	if !slices.Contains(contract.ContractEnums()["ErrorCode"], code) || id == "" || id != w.Header().Get("X-Request-ID") {
		t.Fatalf("invalid error enum or request id")
	}
	for _, secret := range []string{"private-p03-diagnostic", "SQLSTATE", "postgres://", "INSERT INTO", "SELECT "} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private diagnostic leaked")
		}
	}
}

func scalar(t *testing.T, pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecheckHTTPAuditAtomicity(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	h := recheckHandler(pool, now, "s")
	w := slotCall(t, h, "PUT", "/admin/slots/early", `{"expected_version":1,"capacity":2,"accepting":false}`, "", 200)
	n := scalar(t, pool, ctx, `SELECT count(*) FROM audit_events WHERE station_id='s' AND actor_id='operator' AND action='updateAdminSlot' AND object_id='early' AND request_id=$1`, w.Header().Get("X-Request-ID"))
	if n != 1 {
		t.Errorf("configuration audit missing: %d", n)
	}
	body := `{"local_date":"2026-11-19","opens_at":"23:30","closes_at":"00:30","lead_minutes":120,"capacity":3}`
	w = slotCall(t, h, "POST", "/admin/slots/batches", body, "audit-batch", 201)
	if n := scalar(t, pool, ctx, `SELECT count(*) FROM audit_events a JOIN delivery_slots s ON s.id=a.object_id WHERE a.station_id='s' AND a.actor_id='operator' AND a.action='createSlotBatch' AND a.request_id=$1`, w.Header().Get("X-Request-ID")); n != 4 {
		t.Errorf("batch object audit missing: %d", n)
	}
	before := scalar(t, pool, ctx, `SELECT count(*) FROM audit_events`)
	slotCall(t, h, "POST", "/admin/slots/batches", body, "audit-batch", 201)
	if scalar(t, pool, ctx, `SELECT count(*) FROM audit_events`) != before {
		t.Fatal("replay duplicated audit")
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION p03_audit_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-p03-diagnostic'; END $$; CREATE TRIGGER p03_audit_fault BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION p03_audit_fault()`); err != nil {
		t.Fatal(err)
	}
	slotCall(t, h, "PUT", "/admin/slots/early", `{"expected_version":2,"capacity":4,"accepting":true}`, "", 500)
	if scalar(t, pool, ctx, `SELECT capacity FROM delivery_slots WHERE id='early'`) != 2 || scalar(t, pool, ctx, `SELECT version FROM delivery_slots WHERE id='early'`) != 2 {
		t.Fatal("audit failure did not roll back configuration")
	}
	slotCall(t, h, "POST", "/admin/slots/batches", strings.Replace(body, "2026-11-19", "2026-11-20", 1), "audit-fail", 500)
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots`) != 6 || scalar(t, pool, ctx, `SELECT count(*) FROM idempotency_keys WHERE key='audit-fail'`) != 0 {
		t.Fatal("audit failure left batch residue")
	}
}

func TestRecheckVersionUpperBound(t *testing.T) {
	pool, _, now := slotFixture(t)
	slotCall(t, recheckHandler(pool, now, "s"), "PUT", "/admin/slots/early", `{"expected_version":9007199254740991,"capacity":1,"accepting":true}`, "", 409)
}

func TestRecheckInvalidPortTimeAndInfrastructure(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	for _, method := range []string{"select", "reserve", "release"} {
		t.Run(method+"-nil-tx", func(t *testing.T) {
			defer func() {
				if recover() != nil {
					t.Error("nil transaction panicked")
				}
			}()
			var err error
			switch method {
			case "select":
				_, err = (Store{}).SelectSlot(ctx, nil, "s", contract.DeliveryModeAsap, "", now)
			case "reserve":
				err = (Store{}).ReserveSlot(ctx, nil, "early", "order", contract.DeliveryModeAsap, now)
			case "release":
				err = (Store{}).ReleaseSlot(ctx, nil, "order", now)
			}
			if err == nil {
				t.Error("missing transaction accepted")
			}
		})
	}
	for _, method := range []string{"select", "reserve"} {
		err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			if method == "select" {
				_, err := (Store{}).SelectSlot(ctx, tx, "s", contract.DeliveryModeAsap, "", time.Time{})
				return err
			}
			return (Store{}).ReserveSlot(ctx, tx, "early", "zero-time", contract.DeliveryModeAsap, time.Time{})
		})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted zero time: %v", method, err)
		}
	}
}

func TestRecheckSelectionAndPrecision(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	s := Store{}
	selectSlot := func(mode contract.DeliveryMode, id string, at time.Time) (contract.SlotSnapshot, error) {
		var out contract.SlotSnapshot
		err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { var err error; out, err = s.SelectSlot(ctx, tx, "s", mode, id, at); return err })
		return out, err
	}
	for _, req := range []struct {
		mode contract.DeliveryMode
		id   string
	}{{"UNKNOWN", ""}, {contract.DeliveryModeAsap, "early"}, {contract.DeliveryModeScheduled, ""}} {
		if _, err := selectSlot(req.mode, req.id, now); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid mode/id accepted: %v", err)
		}
	}
	for _, tc := range []struct {
		at       string
		mode     contract.DeliveryMode
		id, want string
		err      error
	}{
		{"2026-11-18T02:44:59.999999999Z", contract.DeliveryModeScheduled, "early", "early", nil},
		{"2026-11-18T02:45:00Z", contract.DeliveryModeScheduled, "early", "", ErrSlotExpired},
		{"2026-11-18T03:05:00Z", contract.DeliveryModeAsap, "", "early", nil},
		{"2026-11-18T03:05:00.000000001Z", contract.DeliveryModeAsap, "", "late", nil},
		{"2026-11-18T03:20:00Z", contract.DeliveryModeAsap, "", "late", nil},
		{"2026-11-18T03:20:00.000000001Z", contract.DeliveryModeAsap, "", "", ErrSlotExpired},
		{"2026-11-18T03:30:00Z", contract.DeliveryModeAsap, "", "", ErrSlotExpired},
	} {
		out, err := selectSlot(tc.mode, tc.id, mustTime(t, tc.at))
		if !errors.Is(err, tc.err) || out.ID != tc.want {
			t.Fatalf("%s got %s %v", tc.at, out.ID, err)
		}
	}
	assertSlotUsed(t, pool, ctx, "early", 0)
	if scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations`) != 0 {
		t.Fatal("selection reserved capacity")
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_slots SET used=capacity WHERE id='early'`); err != nil {
		t.Fatal(err)
	}
	if out, err := selectSlot(contract.DeliveryModeAsap, "", now); err != nil || out.ID != "late" {
		t.Fatalf("did not skip full window: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_slots SET used=capacity`); err != nil {
		t.Fatal(err)
	}
	if _, err := selectSlot(contract.DeliveryModeAsap, "", now); !errors.Is(err, ErrSlotFull) {
		t.Fatalf("all full: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_slots SET accepting=false`); err != nil {
		t.Fatal(err)
	}
	if _, err := selectSlot(contract.DeliveryModeAsap, "", now); !errors.Is(err, ErrSlotExpired) {
		t.Fatalf("all closed: %v", err)
	}
}

func TestRecheckMigrationHistoryAndDatabaseConstraints(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		return (Store{}).ReserveSlot(ctx, tx, "early", "existing", contract.DeliveryModeScheduled, now)
	}); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_object('history',(SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m),'slots',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM delivery_slots s),'reservations',(SELECT jsonb_agg(to_jsonb(r) ORDER BY order_id) FROM slot_reservations r))::text`
	var before, after string
	if err := pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	names, err := migrations.SQL.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range names {
		if !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		version := f.Name()[:7]
		if seen[version] {
			t.Fatal("duplicate migration version")
		}
		seen[version] = true
		if strings.HasPrefix(f.Name(), "000_") || strings.HasPrefix(f.Name(), "030_") {
			data, err := migrations.SQL.ReadFile(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			files[f.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if err := platform.Migrate(ctx, pool, files); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("repeat migration changed history or business data")
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('accounts','inventory','wallets','orders')`) != 0 {
		t.Fatal("core fixture depends on other packages")
	}
	for _, sql := range []string{
		`UPDATE delivery_slots SET used=-1 WHERE id='early'`,
		`UPDATE delivery_slots SET used=capacity+1 WHERE id='early'`,
		`UPDATE delivery_slots SET ends_at=starts_at WHERE id='early'`,
		`UPDATE delivery_slots SET dispatch_at=starts_at+interval '1 minute' WHERE id='early'`,
		`UPDATE delivery_slots SET dispatch_at=starts_at-interval '121 minutes' WHERE id='early'`,
		`UPDATE delivery_slots SET starts_at=(SELECT starts_at FROM delivery_slots WHERE id='early'),ends_at=(SELECT ends_at FROM delivery_slots WHERE id='early'),dispatch_at=(SELECT dispatch_at FROM delivery_slots WHERE id='early') WHERE id='late'`,
		`INSERT INTO slot_reservations SELECT * FROM slot_reservations WHERE order_id='existing'`,
		`INSERT INTO slot_reservations(order_id,slot_id,mode,reserved_at) VALUES('invalid','early','UNKNOWN',now())`,
		`UPDATE slot_reservations SET released_at=reserved_at-interval '1 microsecond' WHERE order_id='existing'`,
	} {
		_, err := pool.Exec(ctx, sql)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || (pgerr.Code != "23514" && pgerr.Code != "23505") {
			t.Fatalf("database failed to reject invalid data: %v", err)
		}
	}
	assertSlotUsed(t, pool, ctx, "early", 1)
}

// Exec barriers run after Begin and real row locks; no mock replaces SQL.
type insertBarrierTx struct {
	pgx.Tx
	ready chan<- struct{}
	start <-chan struct{}
}

func (b insertBarrierTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(sql, "INSERT INTO slot_reservations") {
		b.ready <- struct{}{}
		select {
		case <-b.start:
		case <-ctx.Done():
			return pgconn.CommandTag{}, ctx.Err()
		}
	}
	return b.Tx.Exec(ctx, sql, args...)
}

func TestRecheckSameOrderDifferentSlotsConcurrent(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, slot := range []string{"early", "late"} {
		go func() {
			results <- platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
				return (Store{}).ReserveSlot(ctx, insertBarrierTx{tx, ready, start}, slot, "one-order", contract.DeliveryModeScheduled, now)
			})
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("transaction insertion barrier not reached")
		}
	}
	close(start)
	a, b := <-results, <-results
	if a != nil {
		a, b = b, a
	}
	if a != nil || !errors.Is(b, ErrSlotConflict) {
		t.Fatalf("same order race: %v / %v", a, b)
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations`) != 1 || scalar(t, pool, ctx, `SELECT sum(used) FROM delivery_slots`) != 1 {
		t.Fatal("race leaked reservation or counter")
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots s WHERE used<>(SELECT count(*) FROM slot_reservations r WHERE r.slot_id=s.id AND r.released_at IS NULL)`) != 0 {
		t.Fatal("losing slot counter leaked")
	}
}

func TestRecheckCallerTransactionVisibilityAndRollback(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	s := Store{}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE delivery_slots SET capacity=3,accepting=false WHERE id='early'; INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity) VALUES('pending','s','2026-11-18T04:00:00Z','2026-11-18T04:15:00Z','2026-11-18T03:45:00Z',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectSlot(ctx, tx, "s", contract.DeliveryModeScheduled, "early", now); !errors.Is(err, ErrSlotExpired) {
		t.Fatalf("did not read pending closure: %v", err)
	}
	if out, err := s.SelectSlot(ctx, tx, "s", contract.DeliveryModeScheduled, "pending", now); err != nil || out.ID != "pending" {
		t.Fatalf("uncommitted window invisible: %v", err)
	}
	if err := s.ReserveSlot(ctx, tx, "pending", "pending-order", contract.DeliveryModeScheduled, now); err != nil {
		t.Fatal(err)
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations`) != 0 {
		t.Fatal("port committed caller transaction")
	}
	if err := s.ReleaseSlot(ctx, tx, "pending-order", now); err != nil {
		t.Fatal(err)
	}
	var used int64
	if err := tx.QueryRow(ctx, `SELECT used FROM delivery_slots WHERE id='pending'`).Scan(&used); err != nil || used != 0 {
		t.Fatal("release did not see uncommitted reservation")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots WHERE id='pending'`) != 0 || scalar(t, pool, ctx, `SELECT count(*) FROM slot_reservations`) != 0 || scalar(t, pool, ctx, `SELECT capacity FROM delivery_slots WHERE id='early'`) != 1 {
		t.Fatal("caller rollback left changes")
	}
}

func TestRecheckBatchFailureRollbackAndRetry(t *testing.T) {
	for _, fault := range []string{"middle", "idempotency"} {
		t.Run(fault, func(t *testing.T) {
			pool, ctx, now := slotFixture(t)
			h := recheckHandler(pool, now, "s")
			var trigger string
			if fault == "middle" {
				trigger = `CREATE TRIGGER p03_fault AFTER INSERT ON delivery_slots FOR EACH ROW WHEN (NEW.starts_at='2026-11-19T00:30:00Z'::timestamptz) EXECUTE FUNCTION p03_fault()`
			} else {
				trigger = `CREATE TRIGGER p03_fault BEFORE UPDATE ON idempotency_keys FOR EACH ROW EXECUTE FUNCTION p03_fault()`
			}
			if _, err := pool.Exec(ctx, `CREATE FUNCTION p03_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-p03-diagnostic'; END $$; `+trigger); err != nil {
				t.Fatal(err)
			}
			body := `{"local_date":"2026-11-19","opens_at":"08:00","closes_at":"09:00","lead_minutes":0,"capacity":2}`
			slotCall(t, h, "POST", "/admin/slots/batches", body, "retry", 500)
			if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots`) != 2 || scalar(t, pool, ctx, `SELECT count(*) FROM idempotency_keys`) != 0 || scalar(t, pool, ctx, `SELECT count(*) FROM audit_events`) != 0 {
				t.Fatal("failed batch did not fully roll back")
			}
			table := "delivery_slots"
			if fault == "idempotency" {
				table = "idempotency_keys"
			}
			if _, err := pool.Exec(ctx, "DROP TRIGGER p03_fault ON "+table); err != nil {
				t.Fatal(err)
			}
			first := slotCall(t, h, "POST", "/admin/slots/batches", body, "retry", 201)
			replay := slotCall(t, h, "POST", "/admin/slots/batches", body, "retry", 201)
			if !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
				t.Fatal("retry replay changed logical result")
			}
		})
	}
}
