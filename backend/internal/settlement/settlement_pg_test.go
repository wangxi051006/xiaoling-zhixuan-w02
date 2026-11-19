//go:build integration

package settlement

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func settlementFixture(t *testing.T) (*pgxpool.Pool, context.Context) {
	return settlementFixtureWithIdentity(t, false)
}

func settlementFixtureWithIdentity(t *testing.T, withIdentity bool) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("isolated TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var suffix [12]byte
	rand.Read(suffix[:])
	schema := pgx.Identifier{"p04_" + hex.EncodeToString(suffix[:])}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	files := fstest.MapFS{}
	names := []string{"000_001_base.sql", "040_001_settlement.sql"}
	if withIdentity {
		names = append(names, "010_001_identity.sql", "060_001_account_identity_email.sql")
	}
	for _, name := range names {
		data, err := migrations.SQL.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	for range 2 {
		if err := platform.Migrate(ctx, pool, files); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Station')`); err != nil {
		t.Fatal(err)
	}
	return pool, ctx
}

func TestCODCollectionReplayAndCancellation(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{Policy: PaymentPolicy{CODEnabled: true}}
	create := func(id string, s Store) error {
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return s.CreateSettlement(ctx, tx, contract.OrderMoney{
				OrderID: id, ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodCod, TotalFen: 1350,
			})
		})
	}
	if err := create("disabled", Store{}); !errors.Is(err, ErrPaymentMethodDisabled) {
		t.Fatalf("disabled write: %v", err)
	}
	for _, id := range []string{"collected", "void", "rollback"} {
		if err := create(id, store); err != nil {
			t.Fatal(err)
		}
	}
	if err := create("collected", store); err != nil {
		t.Fatalf("same snapshot replay: %v", err)
	}
	collect := func(id, rider string) error {
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return (Store{}).RecordCODCollection(ctx, tx, id, rider)
		})
	}
	assertState := func(id, want string) {
		t.Helper()
		var status string
		var amount int64
		if err := pool.QueryRow(ctx, `SELECT state,amount_fen FROM settlements WHERE order_id=$1`, id).Scan(&status, &amount); err != nil || status != want || amount != 1350 {
			t.Fatalf("%s = %s/%d, %v; want %s/1350", id, status, amount, err, want)
		}
	}
	assertState("collected", "COD_UNCOLLECTED")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- collect("collected", "rider") })
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertState("collected", "COD_COLLECTED")
	if err := collect("collected", "other-rider"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("different rider replay: %v", err)
	}
	for range 2 {
		if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return (Store{}).CancelSettlement(ctx, tx, "void")
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertState("void", "VOID")
	if err := collect("void", "rider"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("collection after void: %v", err)
	}
	rollback := errors.New("injected order write failure")
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if err := store.RecordCODCollection(ctx, tx, "rollback", "rider"); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	assertState("rollback", "COD_UNCOLLECTED")
	var count int
	var total int64
	if err := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(amount_fen),0) FROM cod_receipts`).Scan(&count, &total); err != nil || count != 1 || total != 1350 {
		t.Fatalf("receipt count/total: %d/%d, %v", count, total, err)
	}
}

func TestWalletAtomicDebitAndHistoricalReturn(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{Policy: PaymentPolicy{BalanceEnabled: true}}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets(station_id,actor_id,balance_fen,opening_fen) VALUES ('s','buyer',1350,1350)`); err != nil {
		t.Fatal(err)
	}
	create := func(tx pgx.Tx, id string) error {
		return store.CreateSettlement(ctx, tx, contract.OrderMoney{
			OrderID: id, ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodBalance, TotalFen: 1350,
		})
	}
	rollback := errors.New("injected order write failure")
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if err := create(tx, "rolled-back"); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("rollback returned %v", err)
	}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, id := range []string{"first", "second"} {
		go func() {
			<-start
			results <- result{id, platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return create(tx, id) })}
		}()
	}
	close(start)
	var winner string
	for range 2 {
		r := <-results
		if r.err == nil && winner == "" {
			winner = r.id
		} else if !errors.Is(r.err, ErrInsufficientBalance) {
			t.Fatalf("unexpected debit outcome: %s %v", r.id, r.err)
		}
	}
	if winner == "" {
		t.Fatal("both debits failed")
	}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return create(tx, winner) }); err != nil {
		t.Fatalf("successful debit replay: %v", err)
	}
	checkWallet := func(wantBalance int64, wantEntries int) {
		t.Helper()
		var balance, rebuilt int64
		var entries, settlements int
		if err := pool.QueryRow(ctx, `SELECT balance_fen, opening_fen + coalesce(sum(delta_fen),0), count(e.id),
			(SELECT count(*) FROM settlements) FROM wallets w LEFT JOIN wallet_entries e USING (station_id,actor_id)
			WHERE w.actor_id='buyer' GROUP BY w.station_id,w.actor_id`).Scan(&balance, &rebuilt, &entries, &settlements); err != nil {
			t.Fatal(err)
		}
		if balance != wantBalance || rebuilt != wantBalance || entries != wantEntries || settlements != 1 {
			t.Fatalf("wallet=%d rebuilt=%d entries=%d settlements=%d", balance, rebuilt, entries, settlements)
		}
	}
	checkWallet(0, 1)
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if err := (Store{}).FinalizeReturnSettlement(ctx, tx, winner); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("refund rollback returned %v", err)
	}
	checkWallet(0, 1)
	for range 2 {
		if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return (Store{}).FinalizeReturnSettlement(ctx, tx, winner)
		}); err != nil {
			t.Fatalf("historical return: %v", err)
		}
	}
	checkWallet(1350, 2)
	if _, err := pool.Exec(ctx, `UPDATE wallet_entries SET delta_fen=0`); err == nil {
		t.Fatal("wallet ledger allowed mutation")
	}
}

func TestManualCODRefundRequiresIndependentApprovalAndPayout(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{}
	do := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	for _, id := range []string{"one", "two"} {
		if err := do(func(tx pgx.Tx) error {
			return (Store{Policy: PaymentPolicy{CODEnabled: true}}).CreateSettlement(ctx, tx, contract.OrderMoney{
				OrderID: id, ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodCod, TotalFen: 1350,
			})
		}); err != nil {
			t.Fatal(err)
		}
		request := contract.RefundRequest{OrderID: id, OperatorID: "operator", ReasonCode: "SERVICE"}
		if err := do(func(tx pgx.Tx) error { return store.RequestRefund(ctx, tx, request) }); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("refund before collection: %v", err)
		}
		if err := do(func(tx pgx.Tx) error { return store.RecordCODCollection(ctx, tx, id, "rider") }); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := do(func(tx pgx.Tx) error { return store.RequestRefund(ctx, tx, request) }); err != nil {
				t.Fatal(err)
			}
		}
		var refundID string
		if err := pool.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id=$1`, id).Scan(&refundID); err != nil {
			t.Fatal(err)
		}
		payout := contract.CODPayout{OrderID: id, RefundID: refundID, FinanceID: "finance", ReceiptReference: "offline-1"}
		if err := do(func(tx pgx.Tx) error { return store.RecordCODPayout(ctx, tx, payout) }); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("payout before approval: %v", err)
		}
		approval := contract.RefundApproval{OrderID: id, RefundID: refundID, FinanceID: "operator", ReasonCode: "APPROVED"}
		if err := do(func(tx pgx.Tx) error { return store.ApproveRefund(ctx, tx, approval) }); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("self approval: %v", err)
		}
		approval.FinanceID = "finance"
		for range 2 {
			if err := do(func(tx pgx.Tx) error { return store.ApproveRefund(ctx, tx, approval) }); err != nil {
				t.Fatal(err)
			}
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT state FROM settlements WHERE order_id=$1`, id).Scan(&status); err != nil || status != "REFUND_PENDING" {
			t.Fatalf("approval status = %s, %v", status, err)
		}
		for range 2 {
			err := do(func(tx pgx.Tx) error { return store.RecordCODPayout(ctx, tx, payout) })
			if (id == "one" && err != nil) || (id == "two" && !errors.Is(err, ErrInvalidState)) {
				t.Fatalf("payout %s: %v", id, err)
			}
		}
	}
	var receipts, refunded int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM cod_receipts), count(*) FROM settlements WHERE state='REFUNDED'`).Scan(&receipts, &refunded); err != nil || receipts != 2 || refunded != 1 {
		t.Fatalf("receipts=%d refunded=%d: %v", receipts, refunded, err)
	}
}

func TestCODBatchMembershipAndIndependentVerification(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{Policy: PaymentPolicy{CODEnabled: true}}
	do := func(fn func(pgx.Tx) error) error { return platform.InTransaction(ctx, pool, fn) }
	if err := do(func(tx pgx.Tx) error {
		for _, id := range []string{"a", "b"} {
			if err := store.CreateSettlement(ctx, tx, contract.OrderMoney{
				OrderID: id, ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodCod, TotalFen: 1350,
			}); err != nil {
				return err
			}
		}
		// Both receipts belong to November 19 in Shanghai, although UTC is still November 18.
		_, err := tx.Exec(ctx, `INSERT INTO cod_receipts(order_id,rider_id,amount_fen,collected_at) VALUES
			('a','rider',1350,'2026-11-18 17:00:00Z'),('b','rider',1350,'2026-11-18 18:00:00Z')`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE settlements SET state='COD_COLLECTED'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var batch CODBatch
	create := func(ids []string, date string) error {
		return do(func(tx pgx.Tx) error {
			var err error
			batch, err = store.CreateCODBatch(ctx, tx, "s", "finance-a", "rider", date, ids)
			return err
		})
	}
	if err := create([]string{"a", "b"}, "2026-11-18"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("wrong local date: %v", err)
	}
	if err := create([]string{"b", "a"}, "2026-11-19"); err != nil || batch.ExpectedFen != 2700 || batch.Status != contract.RemittanceStatusInBatch {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
	id := batch.ID
	if err := create([]string{"a"}, "2026-11-19"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reusing a receipt: %v", err)
	}
	verify := func(finance string, actual int64) error {
		return do(func(tx pgx.Tx) error {
			var err error
			batch, err = store.VerifyCODBatch(ctx, tx, "s", finance, id, actual, "COUNTED")
			return err
		})
	}
	if err := verify("finance-a", 2500); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("self verification: %v", err)
	}
	for range 2 {
		if err := verify("finance-b", 2500); err != nil || batch.Status != contract.RemittanceStatusDiscrepancy || batch.ActualFen == nil || *batch.ActualFen != 2500 {
			t.Fatalf("verification = %+v, %v", batch, err)
		}
	}
	if err := verify("finance-b", 2700); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("overwriting discrepancy: %v", err)
	}
	var count int
	var difference int64
	if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(difference_fen),0) FROM cod_batch_verifications`).Scan(&count, &difference); err != nil || count != 1 || difference != 200 {
		t.Fatalf("verification count=%d difference=%d: %v", count, difference, err)
	}
}

func TestBalanceImportDryRunAndReplay(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{}
	apply := func(approval, payload string, dry bool) error {
		data := []byte(payload)
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			return store.ImportBalance(ctx, tx, data, ImportAuthorization{
				ApprovalID: approval, ApproverID: "finance", ExecutorID: "executor", SHA256: sha256.Sum256(data),
			}, dry)
		})
	}
	const first = `{"station_id":"s","total_fen":1000,"entries":[{"actor_id":"buyer","external_reference":"a","amount_fen":1000}]}`
	const second = `{"station_id":"s","total_fen":1300,"entries":[{"actor_id":"buyer","external_reference":"b","amount_fen":300},{"actor_id":"other","external_reference":"a","amount_fen":1000}]}`
	check := func(wantWallets, wantBatches, wantEntries int, wantAmount int64) {
		t.Helper()
		var wallets, batches, entries int
		var amount int64
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM wallets), (SELECT count(*) FROM balance_import_batches),
			(SELECT count(*) FROM wallet_entries), (SELECT coalesce(sum(balance_fen),0) FROM wallets)`).Scan(&wallets, &batches, &entries, &amount); err != nil {
			t.Fatal(err)
		}
		if wallets != wantWallets || batches != wantBatches || entries != wantEntries || amount != wantAmount {
			t.Fatalf("wallets=%d batches=%d entries=%d balance=%d", wallets, batches, entries, amount)
		}
	}
	if err := apply("APPROVAL_1", first, true); err != nil {
		t.Fatal(err)
	}
	check(0, 0, 0, 0)
	if err := apply("APPROVAL_1", first, false); err != nil {
		t.Fatal(err)
	}
	if err := apply("APPROVAL_1", first, false); err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	if err := apply("APPROVAL_1", second, false); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("approval reuse with changed input: %v", err)
	}
	for _, dry := range []bool{true, false} {
		if err := apply("APPROVAL_2", second, dry); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("external reference reused, dry=%v: %v", dry, err)
		}
	}
	check(1, 1, 1, 1000)
}

func TestSettlementHTTPBatchReplayAndRefundFailClosed(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{Policy: PaymentPolicy{CODEnabled: true}}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if err := store.CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: "receipt", ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodCod, TotalFen: 900}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO cod_receipts(order_id,rider_id,amount_fen,collected_at) VALUES ('receipt','rider',900,'2026-11-18 17:00:00Z')`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE settlements SET state='COD_COLLECTED' WHERE order_id='receipt'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h := HTTP{Pool: pool, Clock: &recheckClock{time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC)}, Store: store, Authorize: func(_ http.ResponseWriter, r *http.Request, _ identity.Entry, _ bool, _ ...contract.Role) (identity.Session, bool) {
		return identity.Session{AccountID: r.Header.Get("Test-Actor"), StationID: r.Header.Get("Test-Station")}, true
	}}
	mux := http.NewServeMux()
	h.Register(mux)
	call := func(method, path, station, actor, key, body string, want int) []byte {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, method, "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Test-Actor", actor)
		r.Header.Set("Test-Station", station)
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s = %d; want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	const batchPath = "/admin/cod/batches"
	const input = `{"rider_id":"rider","local_date":"2026-11-19","receipt_ids":["receipt"]}`
	call("POST", batchPath, "s", "finance-a", "", input, 400)
	call("POST", batchPath, "elsewhere", "finance-a", "batch-key", input, 409)
	var batch, replay CODBatch
	if err := json.Unmarshal(call("POST", batchPath, "s", "finance-a", "batch-key", input, 201), &batch); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(call("POST", batchPath, "s", "finance-a", "batch-key", input, 201), &replay); err != nil || batch.ID == "" || batch.ID != replay.ID {
		t.Fatalf("bad replay: %+v / %+v: %v", batch, replay, err)
	}
	call("POST", batchPath, "s", "finance-a", "batch-key", strings.Replace(input, "rider\"", "other\"", 1), 409)
	verify := batchPath + "/" + batch.ID + "/verify"
	call("POST", verify, "s", "finance-a", "verify-key", `{"actual_fen":800,"reason_code":"COUNTED"}`, 409)
	call("POST", verify, "s", "finance-b", "verify-key", `{"reason_code":"COUNTED"}`, 400)
	call("POST", verify, "s", "finance-b", "verify-key", `{"actual_fen":800,"reason_code":"COUNTED"}`, 200)
	var receipts page[codReceipt]
	path := "/admin/cod/receipts?date=2026-11-19"
	if err := json.Unmarshal(call("GET", path, "s", "finance-b", "", "", 200), &receipts); err != nil || len(receipts.Items) != 1 || receipts.Items[0].Remittance != contract.RemittanceStatusDiscrepancy {
		t.Fatalf("receipts=%+v / %v", receipts, err)
	}
	if err := json.Unmarshal(call("GET", path, "elsewhere", "finance-b", "", "", 200), &receipts); err != nil || len(receipts.Items) != 0 {
		t.Fatalf("cross-station receipts=%+v / %v", receipts, err)
	}
	call("POST", "/admin/refunds/unused/approve", "s", "finance-b", "refund-key", `{"reason_code":"APPROVED"}`, 503)
	var audits, keys int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_events), (SELECT count(*) FROM idempotency_keys)`).Scan(&audits, &keys); err != nil || audits != 2 || keys != 2 {
		t.Fatalf("audits=%d keys=%d err=%v", audits, keys, err)
	}
}

// P08-F01: a cache written before difference_fen existed replays with the stored
// ledger direction. The cached bytes, the audit trail, the verification row and
// the money records must stay exactly as they were, and nothing re-executes.
func TestCODBatchLegacyCacheReplay(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{Policy: PaymentPolicy{CODEnabled: true}}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if err := store.CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: "receipt", ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodCod, TotalFen: 900}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cod_receipts(order_id,rider_id,amount_fen,collected_at) VALUES ('receipt','rider',900,'2026-11-18 17:00:00Z')`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE settlements SET state='COD_COLLECTED' WHERE order_id='receipt'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h := HTTP{Pool: pool, Clock: &recheckClock{time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC)}, Store: store, Authorize: func(_ http.ResponseWriter, r *http.Request, _ identity.Entry, _ bool, _ ...contract.Role) (identity.Session, bool) {
		return identity.Session{AccountID: r.Header.Get("Test-Actor"), StationID: r.Header.Get("Test-Station")}, true
	}}
	mux := http.NewServeMux()
	h.Register(mux)
	send := func(actor, key, path, body string) (int, string) {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, "POST", "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Test-Actor", actor)
		r.Header.Set("Test-Station", "s")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	const batchPath = "/admin/cod/batches"
	const input = `{"rider_id":"rider","local_date":"2026-11-19","receipt_ids":["receipt"]}`
	if code, body := send("finance-a", "batch-key", batchPath, input); code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	var id string
	if err := pool.QueryRow(ctx, `SELECT id FROM cod_batches`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	verifyPath := batchPath + "/" + id + "/verify"
	const verifyBody = `{"actual_fen":880,"reason_code":"COUNTED"}`
	if code, body := send("finance-b", "verify-key", verifyPath, verifyBody); code != 200 {
		t.Fatalf("verify = %d %s", code, body)
	}
	// Freeze both cached bodies the way an upgrade leaves them: no difference_fen.
	legacyBatch := strings.Replace(`{"id":"BATCH","rider_id":"rider","local_date":"2026-11-19","expected_fen":900,"actual_fen":null,"status":"IN_BATCH"}`, "BATCH", id, 1)
	legacyVerify := strings.Replace(`{"id":"BATCH","rider_id":"rider","local_date":"2026-11-19","expected_fen":900,"actual_fen":880,"status":"DISCREPANCY"}`, "BATCH", id, 1)
	for command, body := range map[string]string{"createCODBatch": legacyBatch, "verifyCODBatch": legacyVerify} {
		if _, err := pool.Exec(ctx, `UPDATE idempotency_keys SET response_body=$1::jsonb WHERE command=$2`, body, command); err != nil {
			t.Fatal(err)
		}
	}
	before := rcBusinessFingerprint(t, pool, ctx)
	var replay CODBatch
	code, body := send("finance-a", "batch-key", batchPath, input)
	if code != 201 {
		t.Fatalf("create replay = %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.ID != id || replay.ActualFen != nil || replay.DifferenceFen != nil || replay.Status != contract.RemittanceStatusInBatch {
		t.Fatalf("create replay = %+v", replay)
	}
	if !strings.Contains(body, "difference_fen") {
		t.Fatalf("create replay must carry a null difference_fen: %s", body)
	}
	code, body = send("finance-b", "verify-key", verifyPath, verifyBody)
	if code != 200 {
		t.Fatalf("verify replay = %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.DifferenceFen == nil || *replay.DifferenceFen != 20 || replay.ActualFen == nil || *replay.ActualFen != 880 {
		t.Fatalf("verify replay = %+v", replay)
	}
	if replay.Status != contract.RemittanceStatusDiscrepancy {
		t.Fatalf("status = %s", replay.Status)
	}
	// Same key with different input, and a second amount, stay rejected.
	if code, body := send("finance-a", "batch-key", batchPath, strings.Replace(input, `"rider"`, `"other"`, 1)); code != 409 || !strings.Contains(body, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("changed batch input = %d %s", code, body)
	}
	if code, body := send("finance-b", "verify-key", verifyPath, `{"actual_fen":881,"reason_code":"COUNTED"}`); code != 409 || !strings.Contains(body, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("changed amount = %d %s", code, body)
	}
	// The maker still cannot verify its own batch, even under a fresh key.
	if code, body := send("finance-a", "maker-key", verifyPath, `{"actual_fen":900,"reason_code":"COUNTED"}`); code != 409 || !strings.Contains(body, "INVALID_STATE") {
		t.Fatalf("maker verification = %d %s", code, body)
	}
	if after := rcBusinessFingerprint(t, pool, ctx); after != before {
		t.Fatal("legacy replay rewrote the cache, an audit row or a verification record")
	}
	var cached string
	if err := pool.QueryRow(ctx, `SELECT response_body::text FROM idempotency_keys WHERE command='verifyCODBatch'`).Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cached, "difference_fen") {
		t.Fatalf("cached body was rewritten: %s", cached)
	}
}

func TestWalletReconciliationDetectsDriftAndMissingDebit(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{Policy: PaymentPolicy{BalanceEnabled: true}}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen) VALUES ('s','buyer',500,500)`); err != nil {
			return err
		}
		for _, id := range []string{"refunded", "paid"} {
			if err := store.CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: id, ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodBalance, TotalFen: 200}); err != nil {
				return err
			}
		}
		return store.CancelSettlement(ctx, tx, "refunded")
	}); err != nil {
		t.Fatal(err)
	}
	check := func(want WalletReconciliation) {
		t.Helper()
		if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
			got, err := store.ReconcileWallets(ctx, tx, "s")
			if err != nil || got != want {
				t.Fatalf("wallet audit: %+v, %v; want %+v", got, err, want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	want := WalletReconciliation{Accounts: 1, OpeningFen: "500", ImportFen: "0", DebitFen: "400", RefundFen: "200", ClosingFen: "300"}
	check(want)
	if _, err := pool.Exec(ctx, `UPDATE wallets SET balance_fen=301 WHERE station_id='s' AND actor_id='buyer';
		INSERT INTO settlements(order_id,station_id,actor_id,method,amount_fen,state) VALUES ('missing','s','buyer','BALANCE',1,'BALANCE_PAID')`); err != nil {
		t.Fatal(err)
	}
	want.ClosingFen, want.BalanceMismatches, want.OrderMismatches = "301", 1, 1
	check(want)
}

func TestManualBalanceRefundCannotAlsoReverseOrder(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{Policy: PaymentPolicy{BalanceEnabled: true}}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen) VALUES ('s','buyer',500,500)`); err != nil {
			return err
		}
		if err := store.CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: "order", ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodBalance, TotalFen: 200}); err != nil {
			return err
		}
		return store.RequestRefund(ctx, tx, contract.RefundRequest{OrderID: "order", OperatorID: "ops", ReasonCode: "REQUESTED"})
	}); err != nil {
		t.Fatal(err)
	}
	approval := contract.RefundApproval{OrderID: "order", FinanceID: "ops", ReasonCode: "APPROVED"}
	if err := pool.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id='order'`).Scan(&approval.RefundID); err != nil {
		t.Fatal(err)
	}
	approve := func() error {
		return platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return (Store{}).ApproveRefund(ctx, tx, approval) })
	}
	if err := approve(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("same-actor approval: %v", err)
	}
	approval.FinanceID = "finance"
	for range 2 {
		if err := approve(); err != nil {
			t.Fatal(err)
		}
	}
	for _, reverse := range []func(context.Context, pgx.Tx, string) error{store.CancelSettlement, store.FinalizeReturnSettlement} {
		if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error { return reverse(ctx, tx, "order") }); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("reversal after manual refund: %v", err)
		}
	}
	var state string
	var amount, count int64
	if err := pool.QueryRow(ctx, `SELECT state, (SELECT balance_fen FROM wallets WHERE station_id='s' AND actor_id='buyer'),
		(SELECT count(*) FROM wallet_entries WHERE order_id='order' AND kind='REFUND') FROM settlements WHERE order_id='order'`).Scan(&state, &amount, &count); err != nil || state != "REFUNDED" || amount != 500 || count != 1 {
		t.Fatalf("refund state=%s balance=%d entries=%d: %v", state, amount, count, err)
	}
}

func TestCODDayReconciliationSeparatesRefundFromRemittance(t *testing.T) {
	pool, ctx := settlementFixture(t)
	store := Store{}
	if err := platform.InTransaction(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO settlements(order_id,actor_id,station_id,method,amount_fen,state,created_at) VALUES
			('paid','buyer','s','COD',900,'REFUNDED','2026-11-18 16:00Z'),
			('unbatched','buyer','s','COD',200,'COD_COLLECTED','2026-11-18 17:00Z'),
			('new','buyer','s','COD',500,'COD_UNCOLLECTED','2026-11-19 15:59Z'),
			('void','buyer','s','COD',100,'VOID','2026-11-19 12:00Z'),
			('next','buyer','s','COD',999,'COD_UNCOLLECTED','2026-11-19 16:00Z');
			INSERT INTO cod_receipts(order_id,rider_id,amount_fen,collected_at) VALUES
			('paid','rider',900,'2026-11-18 18:00Z'),('unbatched','rider',200,'2026-11-19 11:00Z');
			INSERT INTO refunds(id,order_id,source,operator_id,request_reason,finance_id,approval_reason,approved_at)
			VALUES ('refund','paid','MANUAL','ops','REQUESTED','finance-b','APPROVED','2026-11-19 12:00Z');
			INSERT INTO cod_refund_payouts(refund_id,finance_id,receipt_reference,recorded_at)
			VALUES ('refund','finance-b','synthetic-refund','2026-11-19 13:00Z')`)
		if err != nil {
			return err
		}
		b, err := store.CreateCODBatch(ctx, tx, "s", "finance-a", "rider", "2026-11-19", []string{"paid"})
		if err != nil {
			return err
		}
		if _, err := store.VerifyCODBatch(ctx, tx, "s", "finance-b", b.ID, 800, "COUNTED"); err != nil {
			return err
		}
		got, err := store.ReconcileCODDay(ctx, tx, "s", "2026-11-19")
		want := CODDayReconciliation{ReceivableFen: "1700", VoidedFen: "100", CollectedFen: "1100", UnbatchedFen: "200", BatchExpectedFen: "900", PendingCountFen: "0", CountedFen: "800", DifferenceFen: "100", PayoutFen: "900"}
		if err != nil || got != want {
			t.Fatalf("COD audit: %+v, %v; want %+v", got, err, want)
		}
		got, err = store.ReconcileCODDay(ctx, tx, "other", "2026-11-19")
		if err != nil || got != (CODDayReconciliation{"0", "0", "0", "0", "0", "0", "0", "0", "0"}) {
			t.Fatalf("out-of-station COD audit: %+v, %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
