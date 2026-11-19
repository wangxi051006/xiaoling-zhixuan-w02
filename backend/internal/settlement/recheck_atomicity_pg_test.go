//go:build integration

package settlement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rcFault(t *testing.T, p *pgxpool.Pool, ctx context.Context, table, event, when string) {
	t.Helper()
	q := fmt.Sprintf(`CREATE FUNCTION recheck_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN RAISE EXCEPTION 'synthetic P04 failure' USING ERRCODE='P0001'; END IF; RETURN NEW; END $$; CREATE TRIGGER recheck_fault AFTER %s ON %s FOR EACH ROW EXECUTE FUNCTION recheck_fault()`, when, event, table)
	if _, err := p.Exec(ctx, q); err != nil {
		t.Fatal(err)
	}
}
func rcClearFault(t *testing.T, p *pgxpool.Pool, ctx context.Context, table string) {
	t.Helper()
	if _, err := p.Exec(ctx, `DROP TRIGGER recheck_fault ON `+table+`;DROP FUNCTION recheck_fault()`); err != nil {
		t.Fatal(err)
	}
}
func rcInjected(t *testing.T, err error) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "P0001" {
		t.Fatalf("expected real SQL fault, got %v", err)
	}
}

func TestRecheckCallerTransactionAllSevenMethods(t *testing.T) {
	p, ctx := settlementFixture(t)
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	s := Store{Policy: PaymentPolicy{CODEnabled: true, BalanceEnabled: true}}
	if _, err := tx.Exec(ctx, `INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen) VALUES('s','buyer',200,200)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cod", "cancel", "return"} {
		method := contract.PaymentMethodBalance
		if id == "cod" {
			method = contract.PaymentMethodCod
		}
		if err := s.CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: id, StationID: "s", ActorID: "buyer", PaymentMethod: method, TotalFen: 100}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordCODCollection(ctx, tx, "cod", "rider"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelSettlement(ctx, tx, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeReturnSettlement(ctx, tx, "return"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestRefund(ctx, tx, contract.RefundRequest{OrderID: "cod", OperatorID: "ops", ReasonCode: "R"}); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id='cod'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveRefund(ctx, tx, contract.RefundApproval{OrderID: "cod", RefundID: id, FinanceID: "fin", ReasonCode: "OK"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCODPayout(ctx, tx, contract.CODPayout{OrderID: "cod", RefundID: id, FinanceID: "fin", ReceiptReference: "proof"}); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"settlements": 3, "wallets": 1, "wallet_entries": 4, "cod_receipts": 1, "refunds": 3, "cod_refund_payouts": 1} {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != want {
			t.Fatalf("caller visibility %s=%d want=%d: %v", table, n, want, err)
		}
		if n := rcScalar(t, p, ctx, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("external connection saw uncommitted %s", table)
		}
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"settlements", "wallets", "wallet_entries", "cod_receipts", "refunds", "cod_refund_payouts"} {
		if rcScalar(t, p, ctx, `SELECT count(*) FROM `+table) != 0 {
			t.Fatalf("caller rollback leaked %s", table)
		}
	}
}

func TestRecheckMoneySnapshotAndBounds(t *testing.T) {
	p, ctx := settlementFixture(t)
	s := Store{Policy: PaymentPolicy{CODEnabled: true, BalanceEnabled: true}}
	rcWallet(t, p, ctx, maxMoneyFen)
	base := contract.OrderMoney{OrderID: "o", ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodBalance, TotalFen: maxMoneyFen}
	create := func(m contract.OrderMoney) error {
		return rcDo(ctx, p, func(tx pgx.Tx) error { return s.CreateSettlement(ctx, tx, m) })
	}
	if err := create(base); err != nil {
		t.Fatal(err)
	}
	if err := create(base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*contract.OrderMoney){func(m *contract.OrderMoney) { m.ActorID = "other" }, func(m *contract.OrderMoney) { m.StationID = "other" }, func(m *contract.OrderMoney) { m.TotalFen-- }, func(m *contract.OrderMoney) { m.PaymentMethod = contract.PaymentMethodCod }} {
		bad := base
		mutate(&bad)
		if err := create(bad); !errors.Is(err, ErrInvalidState) {
			t.Errorf("different snapshot replay: %v", err)
		}
	}
	for _, amount := range []int64{-1, maxMoneyFen + 1, 1<<63 - 1} {
		m := base
		m.OrderID = "invalid"
		m.TotalFen = amount
		if err := create(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid amount %d: %v", amount, err)
		}
	}
	if err := rcDo(ctx, p, func(tx pgx.Tx) error { return (Store{}).CancelSettlement(ctx, tx, "o") }); err != nil {
		t.Fatal(err)
	}
	if rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets`) != maxMoneyFen {
		t.Fatal("max safe amount refund")
	}
	// Frozen amounts explicitly permit zero; preserve that rule for free orders.
	if err := rcCreate(ctx, p, "zero", contract.PaymentMethodBalance, 0); err != nil {
		t.Fatal(err)
	}
	if err := rcCreate(ctx, p, "insufficient", contract.PaymentMethodBalance, maxMoneyFen); err != nil {
		t.Fatal(err)
	}
	if err := rcCreate(ctx, p, "no-fallback", contract.PaymentMethodBalance, 1); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("balance fallback: %v", err)
	}
	if rcScalar(t, p, ctx, `SELECT count(*) FROM settlements WHERE order_id='no-fallback'`) != 0 {
		t.Fatal("failed debit left settlement")
	}
}

func TestRecheckPortFaultsRollback(t *testing.T) {
	for _, point := range []string{"wallet-update", "entry-write", "receipt-write", "approval-state", "refund-state", "payout-write"} {
		t.Run(point, func(t *testing.T) {
			p, ctx := settlementFixture(t)
			s := Store{}
			var op func(pgx.Tx) error
			table, event, when := "wallets", "UPDATE", "true"
			var balance int64
			switch point {
			case "wallet-update", "entry-write":
				rcWallet(t, p, ctx, 100)
				balance = 100
				if point == "entry-write" {
					table, event = "wallet_entries", "INSERT"
				}
				op = func(tx pgx.Tx) error {
					return (Store{Policy: PaymentPolicy{BalanceEnabled: true}}).CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: "o", ActorID: "buyer", StationID: "s", PaymentMethod: contract.PaymentMethodBalance, TotalFen: 100})
				}
			case "receipt-write":
				if err := rcCreate(ctx, p, "o", contract.PaymentMethodCod, 100); err != nil {
					t.Fatal(err)
				}
				table, event = "cod_receipts", "INSERT"
				op = func(tx pgx.Tx) error { return s.RecordCODCollection(ctx, tx, "o", "rider") }
			case "approval-state", "refund-state":
				rcWallet(t, p, ctx, 100)
				if err := rcCreate(ctx, p, "o", contract.PaymentMethodBalance, 100); err != nil {
					t.Fatal(err)
				}
				a := rcRequest(t, p, ctx, "o")
				table, event = "refunds", "UPDATE"
				if point == "refund-state" {
					table, when = "settlements", "NEW.state='REFUNDED'"
				}
				op = func(tx pgx.Tx) error { return s.ApproveRefund(ctx, tx, a) }
			case "payout-write":
				rcCollect(t, p, ctx, "o", 100)
				a := rcRequest(t, p, ctx, "o")
				if err := rcDo(ctx, p, func(tx pgx.Tx) error { return s.ApproveRefund(ctx, tx, a) }); err != nil {
					t.Fatal(err)
				}
				table, event = "cod_refund_payouts", "INSERT"
				op = func(tx pgx.Tx) error {
					return s.RecordCODPayout(ctx, tx, contract.CODPayout{OrderID: "o", RefundID: a.RefundID, FinanceID: "finance", ReceiptReference: "proof"})
				}
			}
			before := map[string]int64{}
			for _, tb := range []string{"settlements", "wallet_entries", "cod_receipts", "refunds", "cod_refund_payouts"} {
				before[tb] = rcScalar(t, p, ctx, `SELECT count(*) FROM `+tb)
			}
			rcFault(t, p, ctx, table, event, when)
			rcInjected(t, rcDo(ctx, p, op))
			for tb, n := range before {
				if rcScalar(t, p, ctx, `SELECT count(*) FROM `+tb) != n {
					t.Fatalf("fault leaked %s", tb)
				}
			}
			if point == "wallet-update" || point == "entry-write" || point == "approval-state" || point == "refund-state" {
				if rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets`) != balance {
					t.Fatal("fault leaked wallet change")
				}
			}
			if point == "approval-state" || point == "refund-state" {
				if rcScalar(t, p, ctx, `SELECT count(*) FROM refunds WHERE finance_id IS NOT NULL`) != 0 {
					t.Fatal("approval partially committed")
				}
			}
			rcClearFault(t, p, ctx, table)
			if err := rcDo(ctx, p, op); err != nil {
				t.Fatalf("same-input retry: %v", err)
			}
		})
	}
}

func TestRecheckMigrationsAndDirectConstraints(t *testing.T) {
	p, ctx := settlementFixture(t)
	rcWallet(t, p, ctx, 100)
	rcCollect(t, p, ctx, "cod", 10)
	if err := rcDo(ctx, p, func(tx pgx.Tx) error {
		var localDate string
		if err := tx.QueryRow(ctx, `SELECT ((collected_at AT TIME ZONE 'Asia/Shanghai')::date)::text FROM cod_receipts WHERE order_id='cod'`).Scan(&localDate); err != nil {
			return err
		}
		batch, err := (Store{}).CreateCODBatch(ctx, tx, "s", "finance-a", "rider", localDate, []string{"cod"})
		if err != nil {
			return err
		}
		_, err = (Store{}).VerifyCODBatch(ctx, tx, "s", "finance-b", batch.ID, 9, "COUNTED")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := rcCreate(ctx, p, "balance", contract.PaymentMethodBalance, 10); err != nil {
		t.Fatal(err)
	}
	a := rcRequest(t, p, ctx, "balance")
	if err := rcDo(ctx, p, func(tx pgx.Tx) error { return (Store{}).ApproveRefund(ctx, tx, a) }); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	all, _ := migrations.SQL.ReadDir(".")
	versions := map[string]bool{}
	for _, f := range all {
		if !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		version := strings.Join(strings.Split(f.Name(), "_")[:2], "_")
		if versions[version] {
			t.Fatal("duplicate migration version")
		}
		versions[version] = true
		if strings.HasPrefix(f.Name(), "000_") || strings.HasPrefix(f.Name(), "040_") {
			data, _ := migrations.SQL.ReadFile(f.Name())
			files[f.Name()] = &fstest.MapFile{Data: data}
		}
	}
	fingerprint := func() string {
		var value string
		q := `SELECT jsonb_build_object('history',(SELECT jsonb_agg(to_jsonb(t) ORDER BY version)FROM schema_migrations t),'settlements',(SELECT jsonb_agg(to_jsonb(t) ORDER BY order_id)FROM settlements t),'wallets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY actor_id)FROM wallets t),'entries',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id)FROM wallet_entries t),'receipts',(SELECT jsonb_agg(to_jsonb(t) ORDER BY order_id)FROM cod_receipts t),'refunds',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id)FROM refunds t),'batches',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id)FROM cod_batches t),'members',(SELECT jsonb_agg(to_jsonb(t) ORDER BY batch_id,receipt_id)FROM cod_batch_receipts t),'verifications',(SELECT jsonb_agg(to_jsonb(t) ORDER BY batch_id)FROM cod_batch_verifications t))::text`
		if err := p.QueryRow(ctx, q).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := fingerprint()
	for range 2 {
		if err := platform.Migrate(ctx, p, files); err != nil {
			t.Fatal(err)
		}
	}
	if fingerprint() != before {
		t.Fatal("repeat migration changed history or money")
	}
	for _, q := range []string{
		`UPDATE wallets SET balance_fen=-1`, `UPDATE wallets SET balance_fen=9007199254740992`, `UPDATE wallets SET balance_fen=9223372036854775808`,
		`INSERT INTO wallet_entries(station_id,actor_id,order_id,kind,delta_fen)VALUES('s','buyer','balance','REFUND',10)`,
		`INSERT INTO wallet_entries(station_id,actor_id,order_id,kind,delta_fen)VALUES('s','other','balance','REFUND',10)`,
		`INSERT INTO cod_receipts(order_id,rider_id,amount_fen)VALUES('balance','r',10)`,
		`INSERT INTO cod_receipts(order_id,rider_id,amount_fen)VALUES('cod','r',11)`,
	} {
		if _, err := p.Exec(ctx, q); err == nil {
			t.Fatalf("constraint accepted %s", q)
		}
	}
	for _, table := range []string{"wallet_entries", "balance_import_batches", "cod_receipts", "cod_refund_payouts", "cod_batches", "cod_batch_receipts", "cod_batch_verifications", "audit_events"} {
		for _, verb := range []string{"DELETE FROM ", "TRUNCATE "} {
			if _, err := p.Exec(ctx, verb+table); err == nil {
				t.Fatalf("append-only allowed %s%s", verb, table)
			}
		}
	}
	for table, column := range map[string]string{"wallet_entries": "delta_fen", "balance_import_batches": "total_fen", "cod_receipts": "amount_fen", "cod_refund_payouts": "receipt_reference", "cod_batches": "expected_fen", "cod_batch_receipts": "batch_id", "cod_batch_verifications": "actual_fen", "audit_events": "action"} {
		if _, err := p.Exec(ctx, `UPDATE `+table+` SET `+column+`=`+column); err == nil {
			t.Fatalf("append-only allowed UPDATE %s", table)
		}
	}
	if rcScalar(t, p, ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('accounts','products','delivery_slots','orders')`) != 0 {
		t.Fatal("core fixture depends on other packages")
	}
}
