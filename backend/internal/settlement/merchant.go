package settlement

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"campus.local/xiaoling/internal/identity"
	"github.com/jackc/pgx/v5"
)

type merchantSettlement struct {
	OrderID     string  `json:"order_id"`
	Collected   int64   `json:"collected_fen"`
	DeliveryFee int64   `json:"delivery_fee_fen"`
	Commission  int64   `json:"commission_fen"`
	Amount      int64   `json:"amount_fen"`
	Status      string  `json:"status"`
	Reference   *string `json:"receipt_reference,omitempty"`
}

const merchantSettlementSelect = `SELECT c.order_id,c.amount_fen,o.fee_fen,e.commission_fen,c.amount_fen-e.amount_fen,
	CASE WHEN p.order_id IS NOT NULL THEN 'PAID'
	WHEN s.state <> 'COD_COLLECTED' OR f.id IS NOT NULL THEN 'REFUND'
	WHEN c.amount_fen=e.amount_fen THEN 'NO_PAYOUT'
	WHEN v.actual_fen=v.expected_fen THEN 'PAYABLE'
	ELSE 'WAITING_REMITTANCE' END, p.receipt_reference
	FROM cod_receipts c JOIN settlements s USING(order_id)
	JOIN orders o ON o.id=c.order_id JOIN delivery_tasks t ON t.order_id=c.order_id
	JOIN rider_earnings e ON e.task_id=t.id
	LEFT JOIN cod_batch_receipts b ON b.receipt_id=c.order_id
	LEFT JOIN cod_batch_verifications v USING(batch_id)
	LEFT JOIN refunds f ON f.order_id=c.order_id
	LEFT JOIN cod_merchant_payouts p ON p.order_id=c.order_id
	WHERE s.station_id=$1 AND e.commission_fen IS NOT NULL`

func scanMerchantSettlement(r pgx.Row) (m merchantSettlement, err error) {
	err = r.Scan(&m.OrderID, &m.Collected, &m.DeliveryFee, &m.Commission, &m.Amount, &m.Status, &m.Reference)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (h *HTTP) merchantSettlements(w http.ResponseWriter, r *http.Request, actor identity.Session) {
	p, err := readPage(r.URL.RawQuery, true)
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	rows, err := h.Pool.Query(r.Context(), merchantSettlementSelect+`
		AND c.order_id>$2 AND ($3='' OR (c.collected_at AT TIME ZONE 'Asia/Shanghai')::date=NULLIF($3,'')::date)
		ORDER BY c.order_id LIMIT $4`, actor.StationID, p.cursor, p.date, p.limit+1)
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (merchantSettlement, error) {
		return scanMerchantSettlement(row)
	})
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	if items == nil {
		items = []merchantSettlement{}
	}
	respond(w, 200, paginate(items, p.limit, func(m merchantSettlement) string { return m.OrderID }))
}

func (h *HTTP) recordMerchantPayout(w http.ResponseWriter, r *http.Request, actor identity.Session) {
	var in struct {
		Reference string `json:"receipt_reference"`
	}
	id := r.PathValue("id")
	h.mutate(w, r, actor, "recordMerchantPayout", 200, &in, func(tx pgx.Tx) (commandResult, error) {
		if !validID(id) || !validID(in.Reference) || strings.TrimSpace(in.Reference) != in.Reference {
			return commandResult{}, ErrInvalid
		}
		m, err := recordMerchantPayout(r.Context(), tx, actor, id, in.Reference)
		return commandResult{body: m, objectID: id}, err
	})
}

func recordMerchantPayout(ctx context.Context, tx pgx.Tx, actor identity.Session, orderID, reference string) (merchantSettlement, error) {
	// Refund commands take the same settlement lock before changing eligibility.
	var locked string
	err := tx.QueryRow(ctx, `SELECT order_id FROM settlements WHERE order_id=$1 AND station_id=$2 AND method='COD' FOR UPDATE`,
		orderID, actor.StationID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return merchantSettlement{}, ErrNotFound
	}
	if err != nil {
		return merchantSettlement{}, err
	}
	m, err := scanMerchantSettlement(tx.QueryRow(ctx, merchantSettlementSelect+` AND c.order_id=$2`, actor.StationID, orderID))
	if err != nil {
		return merchantSettlement{}, err
	}
	if m.Status != "PAYABLE" {
		return merchantSettlement{}, ErrInvalidState
	}
	if _, err = tx.Exec(ctx, `INSERT INTO cod_merchant_payouts(order_id,finance_id,amount_fen,receipt_reference)
		VALUES($1,$2,$3,$4)`, orderID, actor.AccountID, m.Amount, reference); err != nil {
		return merchantSettlement{}, err
	}
	m.Status, m.Reference = "PAID", &reference
	return m, nil
}
