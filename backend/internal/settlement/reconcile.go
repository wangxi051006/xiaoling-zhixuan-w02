package settlement

import (
	"context"
	"errors"
	"slices"
	"time"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

// DifferenceFen repeats the stored cod_batch_verifications.difference_fen, which
// the ledger generates as expected_fen - actual_fen (positive: short-paid,
// negative: over-paid, zero: balanced). The API returns that column instead of
// letting a client derive the sign. It stays null until an independent
// verification exists; an unverified batch has no difference, not a zero one.
type CODBatch struct {
	ID            string                    `json:"id"`
	RiderID       string                    `json:"rider_id"`
	LocalDate     string                    `json:"local_date"`
	ExpectedFen   int64                     `json:"expected_fen"`
	ActualFen     *int64                    `json:"actual_fen"`
	DifferenceFen *int64                    `json:"difference_fen"`
	Status        contract.RemittanceStatus `json:"status"`
}

// Totals are decimal strings: a station-wide sum can exceed int64 or JSON's safe integer range.
type WalletReconciliation struct {
	Accounts, BalanceMismatches, OrderMismatches           int64
	OpeningFen, ImportFen, DebitFen, RefundFen, ClosingFen string
}

func (s Store) ReconcileWallets(ctx context.Context, tx pgx.Tx, station string) (WalletReconciliation, error) {
	var r WalletReconciliation
	if !validID(station) {
		return r, ErrInvalid
	}
	err := tx.QueryRow(ctx, `WITH ledger AS (
		SELECT actor_id, sum(delta_fen) AS net,
			sum(amount_fen) FILTER (WHERE kind='IMPORT') AS imported,
			sum(amount_fen) FILTER (WHERE kind='DEBIT') AS debited,
			sum(amount_fen) FILTER (WHERE kind='REFUND') AS refunded
		FROM wallet_entries WHERE station_id=$1 GROUP BY actor_id
	), order_check AS (
		SELECT count(*) AS mismatches FROM settlements s
		LEFT JOIN wallet_entries d ON d.order_id=s.order_id AND d.kind='DEBIT'
		LEFT JOIN wallet_entries r ON r.order_id=s.order_id AND r.kind='REFUND'
		WHERE s.station_id=$1 AND s.method='BALANCE'
		AND (d.id IS NULL OR ((r.id IS NOT NULL) <> (s.state='REFUNDED')))
	)
	SELECT count(*), count(*) FILTER (WHERE w.opening_fen + coalesce(l.net,0) <> w.balance_fen),
		(SELECT mismatches FROM order_check), coalesce(sum(w.opening_fen),0)::text,
		coalesce(sum(l.imported),0)::text, coalesce(sum(l.debited),0)::text,
		coalesce(sum(l.refunded),0)::text, coalesce(sum(w.balance_fen),0)::text
	FROM wallets w LEFT JOIN ledger l ON l.actor_id=w.actor_id WHERE w.station_id=$1`, station).
		Scan(&r.Accounts, &r.BalanceMismatches, &r.OrderMismatches, &r.OpeningFen, &r.ImportFen, &r.DebitFen, &r.RefundFen, &r.ClosingFen)
	return r, err
}

type CODDayReconciliation struct {
	ReceivableFen, VoidedFen, CollectedFen, UnbatchedFen string
	BatchExpectedFen, PendingCountFen, CountedFen        string
	DifferenceFen, PayoutFen                             string
}

// Each stream uses its own Shanghai business date: creation, collection,
// batch collection date, or payout. These totals are not one order cohort.
// VoidedFen is the currently voided subset of that day's created receivables.
func (s Store) ReconcileCODDay(ctx context.Context, tx pgx.Tx, station, localDate string) (CODDayReconciliation, error) {
	var r CODDayReconciliation
	day, err := time.Parse(time.DateOnly, localDate)
	if !validID(station) || err != nil || day.Year() < 1 || day.Format(time.DateOnly) != localDate {
		return r, ErrInvalid
	}
	err = tx.QueryRow(ctx, `WITH bounds AS (
		SELECT $2::text::date::timestamp AT TIME ZONE 'Asia/Shanghai' AS starts_at,
			($2::text::date + 1)::timestamp AT TIME ZONE 'Asia/Shanghai' AS ends_at
	), receivables AS (
		SELECT sum(amount_fen) AS created, sum(amount_fen) FILTER (WHERE state='VOID') AS voided
		FROM settlements, bounds WHERE station_id=$1 AND method='COD' AND created_at >= starts_at AND created_at < ends_at
	), receipts AS (
		SELECT sum(r.amount_fen) AS collected, sum(r.amount_fen) FILTER (WHERE m.batch_id IS NULL) AS unbatched
		FROM cod_receipts r JOIN settlements s USING (order_id) LEFT JOIN cod_batch_receipts m ON m.receipt_id=r.order_id
		CROSS JOIN bounds WHERE s.station_id=$1 AND r.collected_at >= starts_at AND r.collected_at < ends_at
	), batches AS (
		SELECT sum(b.expected_fen) AS expected, sum(b.expected_fen) FILTER (WHERE v.batch_id IS NULL) AS pending,
			sum(v.actual_fen) AS counted, sum(v.difference_fen) AS difference
		FROM cod_batches b LEFT JOIN cod_batch_verifications v ON v.batch_id=b.id
		WHERE b.station_id=$1 AND b.local_date=$2::text::date
	), payouts AS (
		SELECT sum(s.amount_fen) AS paid FROM cod_refund_payouts p JOIN refunds r ON r.id=p.refund_id
		JOIN settlements s USING (order_id) CROSS JOIN bounds
		WHERE s.station_id=$1 AND p.recorded_at >= starts_at AND p.recorded_at < ends_at
	)
	SELECT coalesce(created,0)::text, coalesce(voided,0)::text, coalesce(collected,0)::text, coalesce(unbatched,0)::text,
		coalesce(expected,0)::text, coalesce(pending,0)::text, coalesce(counted,0)::text,
		coalesce(difference,0)::text, coalesce(paid,0)::text
	FROM receivables, receipts, batches, payouts`, station, localDate).
		Scan(&r.ReceivableFen, &r.VoidedFen, &r.CollectedFen, &r.UnbatchedFen,
			&r.BatchExpectedFen, &r.PendingCountFen, &r.CountedFen, &r.DifferenceFen, &r.PayoutFen)
	return r, err
}

func (s Store) CreateCODBatch(ctx context.Context, tx pgx.Tx, station, finance, rider, localDate string, receiptIDs []string) (CODBatch, error) {
	if !validID(station) || !validID(finance) || !validID(rider) || len(receiptIDs) == 0 || len(receiptIDs) > 100 {
		return CODBatch{}, ErrInvalid
	}
	day, err := time.Parse(time.DateOnly, localDate)
	if err != nil || day.Year() < 1 || day.Format(time.DateOnly) != localDate {
		return CODBatch{}, ErrInvalid
	}
	ids := slices.Clone(receiptIDs)
	slices.Sort(ids)
	for i, id := range ids {
		if !validID(id) || (i > 0 && id == ids[i-1]) {
			return CODBatch{}, ErrInvalid
		}
	}
	b := CODBatch{RiderID: rider, LocalDate: localDate, Status: contract.RemittanceStatusInBatch}
	// Lock in a common order, then check membership in a separate statement so a
	// transaction that waited on another batch sees that batch's committed membership.
	for _, id := range ids {
		var amount int64
		err := tx.QueryRow(ctx, `SELECT r.amount_fen FROM cod_receipts r JOIN settlements s USING (order_id)
			WHERE r.order_id=$1 AND s.station_id=$2 AND r.rider_id=$3
			AND s.state IN ('COD_COLLECTED','REFUND_PENDING','REFUNDED')
			AND (r.collected_at AT TIME ZONE 'Asia/Shanghai')::date=$4::text::date FOR UPDATE OF r`,
			id, station, rider, localDate).Scan(&amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return CODBatch{}, ErrInvalidState
		}
		if err != nil {
			return CODBatch{}, err
		}
		if amount > maxMoneyFen-b.ExpectedFen {
			return CODBatch{}, ErrInvalidState
		}
		b.ExpectedFen += amount
	}
	var assigned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cod_batch_receipts WHERE receipt_id=ANY($1::text[]))`, ids).Scan(&assigned); err != nil {
		return CODBatch{}, err
	}
	if assigned {
		return CODBatch{}, ErrInvalidState
	}
	if err := tx.QueryRow(ctx, `INSERT INTO cod_batches(station_id,rider_id,local_date,creator_id,expected_fen)
		VALUES ($1,$2,$3::text::date,$4,$5) RETURNING id`, station, rider, localDate, finance, b.ExpectedFen).Scan(&b.ID); err != nil {
		return CODBatch{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO cod_batch_receipts(batch_id,receipt_id) SELECT $1,unnest($2::text[])`, b.ID, ids)
	return b, err
}

func (s Store) VerifyCODBatch(ctx context.Context, tx pgx.Tx, station, finance, id string, actual int64, reason string) (CODBatch, error) {
	if !validID(station) || !validID(finance) || !validID(id) || actual < 0 || actual > maxMoneyFen || !reasonCode.MatchString(reason) {
		return CODBatch{}, ErrInvalid
	}
	b := CODBatch{ID: id}
	var creator string
	err := tx.QueryRow(ctx, `SELECT rider_id,to_char(local_date,'YYYY-MM-DD'),creator_id,expected_fen
		FROM cod_batches WHERE station_id=$1 AND id=$2 FOR UPDATE`, station, id).Scan(&b.RiderID, &b.LocalDate, &creator, &b.ExpectedFen)
	if errors.Is(err, pgx.ErrNoRows) {
		return CODBatch{}, ErrNotFound
	}
	if err != nil {
		return CODBatch{}, err
	}
	if creator == finance {
		return CODBatch{}, ErrInvalidState
	}
	var difference int64
	var previousAmount int64
	var previousActor, previousReason string
	err = tx.QueryRow(ctx, `SELECT verifier_id,actual_fen,reason_code,difference_fen FROM cod_batch_verifications WHERE batch_id=$1`, id).
		Scan(&previousActor, &previousAmount, &previousReason, &difference)
	switch {
	case err == nil:
		if previousActor != finance || previousAmount != actual || previousReason != reason {
			return CODBatch{}, ErrInvalidState
		}
	case errors.Is(err, pgx.ErrNoRows):
		// Read the generated column back rather than recomputing the sign here, so
		// the response and the ledger cannot disagree.
		if err := tx.QueryRow(ctx, `INSERT INTO cod_batch_verifications(batch_id,creator_id,verifier_id,reason_code,expected_fen,actual_fen)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING difference_fen`, id, creator, finance, reason, b.ExpectedFen, actual).Scan(&difference); err != nil {
			return CODBatch{}, err
		}
	default:
		return CODBatch{}, err
	}
	b.ActualFen = &actual
	b.DifferenceFen = &difference
	b.Status = contract.RemittanceStatusVerified
	if actual != b.ExpectedFen {
		b.Status = contract.RemittanceStatusDiscrepancy
	}
	return b, nil
}
