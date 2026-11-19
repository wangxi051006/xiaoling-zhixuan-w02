//go:build integration

package settlement

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Start both real transactions before releasing the goroutines. Hold a database
// row lock until pg_blocking_pids proves both exact contender PIDs are waiting.
// A contender can wait on another contender's unique-key transaction lock too.
func rcRace(t *testing.T, p *pgxpool.Pool, ctx context.Context, lock string, ops ...func(pgx.Tx) error) []error {
	t.Helper()
	gate, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, lock); err != nil {
		t.Fatal(err)
	}
	txs := make([]pgx.Tx, len(ops))
	pids := make([]uint32, len(ops))
	results := make(chan struct {
		i   int
		err error
	}, len(ops))
	start := make(chan struct{})
	for i := range ops {
		txs[i], err = p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		pids[i] = txs[i].Conn().PgConn().PID()
		defer txs[i].Rollback(context.Background())
		go func(i int) {
			<-start
			err := ops[i](txs[i])
			if err == nil {
				err = txs[i].Commit(ctx)
			} else {
				_ = txs[i].Rollback(context.Background())
			}
			results <- struct {
				i   int
				err error
			}{i, err}
		}(i)
	}
	close(start)
	for {
		all := true
		for _, pid := range pids {
			if rcScalar(t, p, ctx, `SELECT cardinality(pg_blocking_pids($1))`, pid) == 0 {
				all = false
			}
		}
		if all {
			break
		}
		select {
		case result := <-results:
			t.Fatalf("contender %d escaped transaction barrier: %v", result.i, result.err)
		case <-ctx.Done():
			t.Fatal("transaction barrier timed out")
		default:
			runtime.Gosched()
		}
	}
	t.Logf("observed exact blocked contender PIDs %v", pids)
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	out := make([]error, len(ops))
	for range ops {
		r := <-results
		out[r.i] = r.err
	}
	return out
}
func rcOutcomes(t *testing.T, out []error, success int, conflict error) {
	t.Helper()
	n := 0
	for _, err := range out {
		if err == nil {
			n++
		} else if !errors.Is(err, conflict) {
			t.Fatalf("unexpected race error: %v", err)
		}
	}
	if n != success {
		t.Fatalf("race successes=%d want=%d results=%v", n, success, out)
	}
}
func rcWallet(t *testing.T, p *pgxpool.Pool, ctx context.Context, balance int64) {
	t.Helper()
	if _, err := p.Exec(ctx, `INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)VALUES('s','buyer',$1,$1)`, balance); err != nil {
		t.Fatal(err)
	}
}
func rcCollect(t *testing.T, p *pgxpool.Pool, ctx context.Context, id string, amount int64) {
	t.Helper()
	if err := rcCreate(ctx, p, id, contract.PaymentMethodCod, amount); err != nil {
		t.Fatal(err)
	}
	if err := rcDo(ctx, p, func(tx pgx.Tx) error { return (Store{}).RecordCODCollection(ctx, tx, id, "rider") }); err != nil {
		t.Fatal(err)
	}
}
func rcRequest(t *testing.T, p *pgxpool.Pool, ctx context.Context, id string) contract.RefundApproval {
	t.Helper()
	if err := rcDo(ctx, p, func(tx pgx.Tx) error {
		return (Store{}).RequestRefund(ctx, tx, contract.RefundRequest{OrderID: id, OperatorID: "ops", ReasonCode: "SERVICE"})
	}); err != nil {
		t.Fatal(err)
	}
	a := contract.RefundApproval{OrderID: id, FinanceID: "finance", ReasonCode: "APPROVED"}
	if err := p.QueryRow(ctx, `SELECT id FROM refunds WHERE order_id=$1`, id).Scan(&a.RefundID); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRecheckLastBalanceAndSameOrderBarriers(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			p, ctx := settlementFixture(t)
			rcWallet(t, p, ctx, 100)
			ops := []func(pgx.Tx) error{}
			for i := range 2 {
				id := fmt.Sprintf("o%d", i)
				if same {
					id = "o"
				}
				ops = append(ops, func(tx pgx.Tx) error {
					return (Store{Policy: PaymentPolicy{BalanceEnabled: true}}).CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: id, StationID: "s", ActorID: "buyer", PaymentMethod: contract.PaymentMethodBalance, TotalFen: 100})
				})
			}
			want := 1
			if same {
				want = 2
			}
			rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM wallets FOR UPDATE`, ops...), want, ErrInsufficientBalance)
			if rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets`) != 0 || rcScalar(t, p, ctx, `SELECT count(*) FROM settlements`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM wallet_entries WHERE kind='DEBIT'`) != 1 {
				t.Fatal("balance or debit uniqueness violated")
			}
		})
	}
}

func TestRecheckRefundCrossCommandBarrier(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			p, ctx := settlementFixture(t)
			rcWallet(t, p, ctx, 100)
			if err := rcCreate(ctx, p, "o", contract.PaymentMethodBalance, 100); err != nil {
				t.Fatal(err)
			}
			ops := []func(pgx.Tx) error{func(tx pgx.Tx) error { return (Store{}).CancelSettlement(ctx, tx, "o") }, func(tx pgx.Tx) error { return (Store{}).FinalizeReturnSettlement(ctx, tx, "o") }}
			want := 2
			if manual {
				approval := rcRequest(t, p, ctx, "o")
				ops = append(ops, func(tx pgx.Tx) error { return (Store{}).ApproveRefund(ctx, tx, approval) })
				want = 1
			}
			rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM settlements WHERE order_id='o' FOR UPDATE`, ops...), want, ErrInvalidState)
			if rcScalar(t, p, ctx, `SELECT balance_fen FROM wallets`) != 100 || rcScalar(t, p, ctx, `SELECT count(*) FROM refunds`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM wallet_entries WHERE kind='REFUND'`) != 1 {
				t.Fatal("cross-command double refund")
			}
		})
	}
}

func TestRecheckCODCollectionBatchVerifyBarriers(t *testing.T) {
	p, ctx := settlementFixture(t)
	if err := rcCreate(ctx, p, "o", contract.PaymentMethodCod, 100); err != nil {
		t.Fatal(err)
	}
	collect := func(tx pgx.Tx) error { return (Store{}).RecordCODCollection(ctx, tx, "o", "rider") }
	rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM settlements WHERE order_id='o' FOR UPDATE`, collect, collect), 2, ErrInvalidState)
	if rcScalar(t, p, ctx, `SELECT count(*) FROM cod_receipts`) != 1 {
		t.Fatal("duplicate collection")
	}
	var date string
	if err := p.QueryRow(ctx, `SELECT (collected_at AT TIME ZONE 'Asia/Shanghai')::date::text FROM cod_receipts`).Scan(&date); err != nil {
		t.Fatal(err)
	}
	batch := func(tx pgx.Tx) error {
		_, err := (Store{}).CreateCODBatch(ctx, tx, "s", "fin-a", "rider", date, []string{"o"})
		return err
	}
	rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM cod_receipts FOR UPDATE`, batch, batch), 1, ErrInvalidState)
	var id string
	if err := p.QueryRow(ctx, `SELECT id FROM cod_batches`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	verify := func(tx pgx.Tx) error {
		b, err := (Store{}).VerifyCODBatch(ctx, tx, "s", "fin-b", id, 90, "COUNTED")
		if err == nil && (b.Status != contract.RemittanceStatusDiscrepancy || *b.ActualFen != 90) {
			return fmt.Errorf("wrong discrepancy")
		}
		return err
	}
	rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM cod_batches FOR UPDATE`, verify, verify), 2, ErrInvalidState)
	if rcScalar(t, p, ctx, `SELECT count(*) FROM cod_batch_receipts`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM cod_batch_verifications`) != 1 || rcScalar(t, p, ctx, `SELECT difference_fen FROM cod_batch_verifications`) != 10 {
		t.Fatal("batch or verification duplicate")
	}
}

func TestRecheckApprovalPayoutBarriers(t *testing.T) {
	p, ctx := settlementFixture(t)
	rcCollect(t, p, ctx, "o", 100)
	a := rcRequest(t, p, ctx, "o")
	approve := func(tx pgx.Tx) error { return (Store{}).ApproveRefund(ctx, tx, a) }
	rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM settlements FOR UPDATE`, approve, approve), 2, ErrInvalidState)
	if rcScalar(t, p, ctx, `SELECT count(*) FROM cod_refund_payouts`) != 0 {
		t.Fatal("approval pretended payout")
	}
	payout := func(tx pgx.Tx) error {
		return (Store{}).RecordCODPayout(ctx, tx, contract.CODPayout{OrderID: "o", RefundID: a.RefundID, FinanceID: "finance", ReceiptReference: "proof"})
	}
	rcOutcomes(t, rcRace(t, p, ctx, `SELECT 1 FROM settlements FOR UPDATE`, payout, payout), 2, ErrInvalidState)
	if rcScalar(t, p, ctx, `SELECT count(*) FROM cod_refund_payouts`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM settlements WHERE state='REFUNDED'`) != 1 {
		t.Fatal("duplicate payout")
	}
}

func TestRecheckPayoutReferenceContention(t *testing.T) {
	p, ctx := settlementFixture(t)
	approvals := []contract.RefundApproval{}
	for _, id := range []string{"a", "b"} {
		rcCollect(t, p, ctx, id, 100)
		a := rcRequest(t, p, ctx, id)
		if err := rcDo(ctx, p, func(tx pgx.Tx) error { return (Store{}).ApproveRefund(ctx, tx, a) }); err != nil {
			t.Fatal(err)
		}
		approvals = append(approvals, a)
	}
	first, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(context.Background())
	a := approvals[0]
	if err := (Store{}).RecordCODPayout(ctx, first, contract.CODPayout{OrderID: a.OrderID, RefundID: a.RefundID, FinanceID: "finance", ReceiptReference: "shared-proof"}); err != nil {
		t.Fatal(err)
	}
	second, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback(context.Background())
	pid := second.Conn().PgConn().PID()
	out := make(chan error, 1)
	go func() {
		a := approvals[1]
		err := (Store{}).RecordCODPayout(ctx, second, contract.CODPayout{OrderID: a.OrderID, RefundID: a.RefundID, FinanceID: "finance", ReceiptReference: "shared-proof"})
		_ = second.Rollback(context.Background())
		out <- err
	}()
	for rcScalar(t, p, ctx, `SELECT cardinality(pg_blocking_pids($1))`, pid) == 0 {
		select {
		case err := <-out:
			t.Fatalf("payout escaped unique barrier: %v", err)
		case <-ctx.Done():
			t.Fatal("unique barrier timed out")
		default:
			runtime.Gosched()
		}
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-out; !errors.Is(err, ErrInvalidState) {
		t.Fatalf("reference reuse: %v", err)
	}
	if rcScalar(t, p, ctx, `SELECT count(*) FROM cod_refund_payouts`) != 1 || rcScalar(t, p, ctx, `SELECT count(*) FROM settlements WHERE state='REFUND_PENDING'`) != 1 {
		t.Fatal("shared proof used twice")
	}
}
