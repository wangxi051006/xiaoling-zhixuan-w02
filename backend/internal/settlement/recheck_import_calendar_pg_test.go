//go:build integration

package settlement

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

func TestRecheckImportAgainstDebitAndReturn(t *testing.T) {
	for _, refund := range []bool{false, true} {
		t.Run(fmt.Sprint(refund), func(t *testing.T) {
			p, ctx := settlementFixture(t)
			rcWallet(t, p, ctx, 100)
			if refund {
				if err := rcCreate(ctx, p, "paid", contract.PaymentMethodBalance, 100); err != nil {
					t.Fatal(err)
				}
			}
			data := []byte(`{"station_id":"s","total_fen":200,"entries":[{"actor_id":"z-other","external_reference":"r-z","amount_fen":100},{"actor_id":"buyer","external_reference":"r-a","amount_fen":100}]}`)
			auth := ImportAuthorization{ApprovalID: "CONCURRENT", ApproverID: "fin", ExecutorID: "exec", SHA256: sha256.Sum256(data)}
			imp := func(tx pgx.Tx) error { return (Store{}).ImportBalance(ctx, tx, data, auth, false) }
			trade := func(tx pgx.Tx) error {
				if refund {
					return (Store{}).FinalizeReturnSettlement(ctx, tx, "paid")
				}
				return (Store{Policy: PaymentPolicy{BalanceEnabled: true}}).CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: "paid", ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodBalance, TotalFen: 100})
			}
			rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM wallets WHERE actor_id='buyer' FOR UPDATE`, imp, trade), 2, ErrInvalidState)
			want := int64(100)
			if refund {
				want = 200
			}
			if rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets WHERE actor_id='buyer'`) != want || rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets WHERE actor_id='z-other'`) != 100 {
				t.Fatal("import and trade drift")
			}
			if err := rcDo(ctx, p, imp); err != nil {
				t.Fatal(err)
			}
			if rcScalar(t, p, ctx, `SELECT count(*) FROM balance_import_batches`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM wallet_entries WHERE kind='IMPORT'`) != 2 {
				t.Fatal("concurrent import replay duplicated")
			}
			if err := rcDo(ctx, p, func(tx pgx.Tx) error {
				r, err := (Store{}).ReconcileWallets(ctx, tx, "s")
				if err == nil && (r.BalanceMismatches != 0 || r.OrderMismatches != 0) {
					return fmt.Errorf("reconciliation drift %+v", r)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecheckConcurrentImportReplayAndOverflow(t *testing.T) {
	p, ctx := settlementFixture(t)
	rcWallet(t, p, ctx, 0)
	data := []byte(`{"station_id":"s","total_fen":100,"entries":[{"actor_id":"buyer","external_reference":"r","amount_fen":100}]}`)
	auth := ImportAuthorization{ApprovalID: "ONE", ApproverID: "fin", ExecutorID: "exec", SHA256: sha256.Sum256(data)}
	imp := func(tx pgx.Tx) error { return (Store{}).ImportBalance(ctx, tx, data, auth, false) }
	rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM wallets FOR UPDATE`, imp, imp), 2, ErrInvalidState)
	if rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets`) != 100 || rcScalar(t, p, ctx, `SELECT count(*) FROM wallet_entries`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM balance_import_batches`) != 1 {
		t.Fatal("concurrent approved batch duplicated")
	}
	if _, err := p.Exec(ctx, `UPDATE wallets SET balance_fen=9007199254740991`); err != nil {
		t.Fatal(err)
	}
	data = []byte(`{"station_id":"s","total_fen":1,"entries":[{"actor_id":"buyer","external_reference":"overflow","amount_fen":1}]}`)
	auth.ApprovalID = "TWO"
	auth.SHA256 = sha256.Sum256(data)
	for _, dry := range []bool{true, false} {
		err := rcDo(ctx, p, func(tx pgx.Tx) error { return (Store{}).ImportBalance(ctx, tx, data, auth, dry) })
		if !errors.Is(err, ErrInvalidState) {
			t.Fatalf("wallet overflow dry=%v: %v", dry, err)
		}
	}
	if rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets`) != maxMoneyFen || rcScalar(t, p, ctx, `SELECT count(*) FROM balance_import_batches`) != 1 {
		t.Fatal("overflow partially committed")
	}
}

func TestRecheckCODShanghaiCalendarBoundaries(t *testing.T) {
	for _, tc := range []struct{ date, before, start, last, next string }{
		{"2026-12-31", "2026-12-30T15:59:59.999999Z", "2026-12-30T16:00:00Z", "2026-12-31T15:59:59.999999Z", "2026-12-31T16:00:00Z"},
		{"2027-01-01", "2026-12-31T15:59:59.999999Z", "2026-12-31T16:00:00Z", "2027-01-01T15:59:59.999999Z", "2027-01-01T16:00:00Z"},
		{"2028-02-29", "2028-02-28T15:59:59.999999Z", "2028-02-28T16:00:00Z", "2028-02-29T15:59:59.999999Z", "2028-02-29T16:00:00Z"},
	} {
		t.Run(tc.date, func(t *testing.T) {
			p, ctx := settlementFixture(t)
			for i, at := range []string{tc.before, tc.start, tc.last, tc.next} {
				id := fmt.Sprintf("o%d", i)
				if err := rcCreate(ctx, p, id, contract.PaymentMethodCod, int64(i+1)); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Exec(ctx, `INSERT INTO cod_receipts(order_id,rider_id,amount_fen,collected_at)VALUES($1,'rider',$2,$3::timestamptz);`, id, i+1, at); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.Exec(ctx, `UPDATE settlements SET state='COD_COLLECTED'`); err != nil {
				t.Fatal(err)
			}
			if err := rcDo(ctx, p, func(tx pgx.Tx) error {
				r, err := (Store{}).ReconcileCODDay(ctx, tx, "s", tc.date)
				if err == nil && (r.CollectedFen != "5" || r.UnbatchedFen != "5") {
					return fmt.Errorf("Shanghai boundary totals %+v", r)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := rcDo(ctx, p, func(tx pgx.Tx) error {
				b, err := (Store{}).CreateCODBatch(ctx, tx, "s", "fin-a", "rider", tc.date, []string{"o1", "o2"})
				if err == nil && b.ExpectedFen != 5 {
					return fmt.Errorf("boundary batch total %d", b.ExpectedFen)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"o0", "o3"} {
				if err := rcDo(ctx, p, func(tx pgx.Tx) error {
					_, err := (Store{}).CreateCODBatch(ctx, tx, "s", "fin-a", "rider", tc.date, []string{id})
					return err
				}); !errors.Is(err, ErrInvalidState) {
					t.Fatalf("out-of-day receipt %s: %v", id, err)
				}
			}
		})
	}
}
