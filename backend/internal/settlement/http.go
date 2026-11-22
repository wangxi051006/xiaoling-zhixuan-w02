package settlement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RefundCommands is supplied by fulfillment. Implementations validate and lock the
// order before calling SettlementPort; they must use the transaction supplied here.
type RefundCommands interface {
	ApproveRefund(context.Context, pgx.Tx, identity.Session, string, string) error
	RecordCODPayout(context.Context, pgx.Tx, identity.Session, string, string) error
}

type HTTP struct {
	Pool      *pgxpool.Pool
	Clock     contract.Clock
	Store     Store
	Refunds   RefundCommands
	Authorize func(http.ResponseWriter, *http.Request, identity.Entry, bool, ...contract.Role) (identity.Session, bool)
}

type handler func(http.ResponseWriter, *http.Request, identity.Session)

func (h *HTTP) Register(mux *http.ServeMux) {
	user := func(fn handler) http.HandlerFunc {
		return h.require(fn, identity.UserEntry, false, contract.RoleCustomer)
	}
	finance := func(fn handler, mfa bool) http.HandlerFunc {
		return h.require(fn, identity.AdminEntry, mfa, contract.RoleFinance)
	}
	mux.HandleFunc("GET /api/v1/payment-methods", user(h.methods))
	mux.HandleFunc("GET /api/v1/me/wallet", user(h.wallet))
	mux.HandleFunc("GET /api/v1/admin/cod/receipts", finance(h.receipts, false))
	mux.HandleFunc("POST /api/v1/admin/cod/batches", finance(h.createBatch, true))
	mux.HandleFunc("POST /api/v1/admin/cod/batches/{id}/verify", finance(h.verifyBatch, true))
	mux.HandleFunc("GET /api/v1/admin/cod/merchant-settlements", finance(h.merchantSettlements, false))
	mux.HandleFunc("GET /api/v1/merchant/settlements", h.require(h.merchantSettlements, identity.MerchantEntry, false, contract.RoleMerchant))
	mux.HandleFunc("POST /api/v1/admin/cod/merchant-settlements/{id}/record-payout", finance(h.recordMerchantPayout, true))
	mux.HandleFunc("GET /api/v1/admin/refunds", h.require(h.refunds, identity.AdminEntry, false, contract.RoleOperator, contract.RoleFinance))
	mux.HandleFunc("POST /api/v1/admin/refunds/{id}/approve", finance(h.approve, true))
	mux.HandleFunc("POST /api/v1/admin/refunds/{id}/record-cod-payout", finance(h.payout, true))
}

func (h *HTTP) require(fn handler, entry identity.Entry, mfa bool, roles ...contract.Role) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Authorize == nil {
			writeSettlementError(w, r, errNotReady)
			return
		}
		s, ok := h.Authorize(w, r, entry, mfa, roles...)
		if !ok {
			return
		}
		if h.Pool == nil || h.Clock == nil || h.Clock.Now().IsZero() || !validID(s.AccountID) || !validID(s.StationID) {
			writeSettlementError(w, r, errNotReady)
			return
		}
		fn(w, r, s)
	}
}

var (
	errNotReady = errors.New("settlement service not ready")
	errReplay   = errors.New("settlement idempotency conflict")
)

func writeSettlementError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := 500, "INTERNAL_ERROR"
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		status, code = 413, "BODY_TOO_LARGE"
	case errors.Is(err, ErrInvalid):
		status, code = 400, "BAD_REQUEST"
	case errors.Is(err, ErrNotFound), errors.Is(err, identity.ErrObjectNotFound):
		status, code = 404, "NOT_FOUND"
	case errors.Is(err, ErrInvalidState):
		status, code = 409, "INVALID_STATE"
	case errors.Is(err, ErrPaymentMethodDisabled):
		status, code = 409, "PAYMENT_METHOD_DISABLED"
	case errors.Is(err, ErrInsufficientBalance):
		status, code = 409, "INSUFFICIENT_BALANCE"
	case errors.Is(err, errReplay):
		status, code = 409, "IDEMPOTENCY_CONFLICT"
	case errors.Is(err, errNotReady):
		status, code = 503, "NOT_READY"
	}
	platform.WriteError(w, r, status, code, http.StatusText(status))
}

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (h *HTTP) methods(w http.ResponseWriter, r *http.Request, _ identity.Session) {
	if r.URL.RawQuery != "" {
		writeSettlementError(w, r, ErrInvalid)
		return
	}
	respond(w, 200, map[string]any{"methods": h.Store.Policy.Methods()})
}

func (h *HTTP) wallet(w http.ResponseWriter, r *http.Request, s identity.Session) {
	if r.URL.RawQuery != "" {
		writeSettlementError(w, r, ErrInvalid)
		return
	}
	var fen int64
	err := h.Pool.QueryRow(r.Context(), `SELECT balance_fen FROM wallets WHERE station_id=$1 AND actor_id=$2`, s.StationID, s.AccountID).Scan(&fen)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeSettlementError(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"balance_fen": fen, "currency": "CNY"})
}

type page[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next_cursor"`
}

type pageQuery struct {
	date, cursor string
	limit        int
}

func readPage(raw string, dateAllowed bool) (p pageQuery, err error) {
	p.limit = 20
	values, err := url.ParseQuery(raw)
	if err != nil {
		return p, ErrInvalid
	}
	for key, v := range values {
		if len(v) != 1 || v[0] == "" {
			return p, ErrInvalid
		}
		switch key {
		case "limit":
			p.limit, err = strconv.Atoi(v[0])
			if err != nil || p.limit < 1 || p.limit > 100 {
				return p, ErrInvalid
			}
		case "cursor":
			if !validID(v[0]) {
				return p, ErrInvalid
			}
			p.cursor = v[0]
		case "date":
			d, err := time.Parse(time.DateOnly, v[0])
			if !dateAllowed || err != nil || d.Year() < 1 || d.Format(time.DateOnly) != v[0] {
				return p, ErrInvalid
			}
			p.date = v[0]
		default:
			return p, ErrInvalid
		}
	}
	return p, nil
}

func paginate[T any](items []T, limit int, id func(T) string) page[T] {
	if len(items) <= limit {
		return page[T]{Items: items}
	}
	cursor := id(items[limit-1])
	return page[T]{Items: items[:limit], Next: &cursor}
}

type codReceipt struct {
	ID          string                    `json:"id"`
	OrderID     string                    `json:"order_id"`
	RiderID     string                    `json:"rider_id"`
	AmountFen   int64                     `json:"amount_fen"`
	CollectedAt time.Time                 `json:"collected_at"`
	Remittance  contract.RemittanceStatus `json:"remittance_status"`
}

func (h *HTTP) receipts(w http.ResponseWriter, r *http.Request, s identity.Session) {
	p, err := readPage(r.URL.RawQuery, true)
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	if p.date == "" {
		zone, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			writeSettlementError(w, r, errNotReady)
			return
		}
		p.date = h.Clock.Now().In(zone).Format(time.DateOnly)
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT c.order_id,c.rider_id,c.amount_fen,c.collected_at,
		CASE WHEN v.actual_fen=v.expected_fen THEN 'VERIFIED' WHEN v.batch_id IS NOT NULL THEN 'DISCREPANCY'
		WHEN br.batch_id IS NOT NULL THEN 'IN_BATCH' ELSE 'NOT_REMITTED' END
		FROM cod_receipts c JOIN settlements s USING (order_id)
		LEFT JOIN cod_batch_receipts br ON br.receipt_id=c.order_id LEFT JOIN cod_batch_verifications v USING (batch_id)
		WHERE s.station_id=$1 AND c.order_id>$2 AND (c.collected_at AT TIME ZONE 'Asia/Shanghai')::date =
		$3::text::date ORDER BY c.order_id LIMIT $4`, s.StationID, p.cursor, p.date, p.limit+1)
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]codReceipt, 0, p.limit+1)
	for rows.Next() {
		var item codReceipt
		if err := rows.Scan(&item.ID, &item.RiderID, &item.AmountFen, &item.CollectedAt, &item.Remittance); err != nil {
			writeSettlementError(w, r, err)
			return
		}
		item.OrderID, item.CollectedAt = item.ID, item.CollectedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeSettlementError(w, r, err)
		return
	}
	respond(w, 200, paginate(items, p.limit, func(i codReceipt) string { return i.ID }))
}

type refundView struct {
	ID          string                    `json:"id"`
	OrderID     string                    `json:"order_id"`
	AmountFen   int64                     `json:"amount_fen"`
	Method      contract.PaymentMethod    `json:"payment_method"`
	Status      contract.SettlementStatus `json:"status"`
	RequestedAt time.Time                 `json:"requested_at"`
}

const refundSelect = `SELECT f.id,f.order_id,s.amount_fen,s.method,
	CASE WHEN f.source='MANUAL' AND f.approved_at IS NULL THEN 'REFUND_PENDING' ELSE s.state END,f.requested_at
	FROM refunds f JOIN settlements s USING(order_id) WHERE s.station_id=$1`

func scanRefund(row interface{ Scan(...any) error }) (f refundView, err error) {
	err = row.Scan(&f.ID, &f.OrderID, &f.AmountFen, &f.Method, &f.Status, &f.RequestedAt)
	f.RequestedAt = f.RequestedAt.UTC()
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (h *HTTP) refunds(w http.ResponseWriter, r *http.Request, s identity.Session) {
	p, err := readPage(r.URL.RawQuery, false)
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	rows, err := h.Pool.Query(r.Context(), refundSelect+` AND f.id>$2 ORDER BY f.id LIMIT $3`, s.StationID, p.cursor, p.limit+1)
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]refundView, 0, p.limit+1)
	for rows.Next() {
		f, err := scanRefund(rows)
		if err != nil {
			writeSettlementError(w, r, err)
			return
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		writeSettlementError(w, r, err)
		return
	}
	respond(w, 200, paginate(items, p.limit, func(f refundView) string { return f.ID }))
}

type commandResult struct {
	body     any
	objectID string
	reason   string
}

func (h *HTTP) mutate(w http.ResponseWriter, r *http.Request, s identity.Session, command string, status int, input any, apply func(pgx.Tx) (commandResult, error)) {
	keys := r.Header.Values("Idempotency-Key")
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || len(keys) != 1 || !validID(keys[0]) || strings.TrimSpace(keys[0]) != keys[0] || err != nil || media != "application/json" {
		writeSettlementError(w, r, ErrInvalid)
		return
	}
	key := keys[0]
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	if err := decodeExactJSON(data, input); err != nil {
		writeSettlementError(w, r, ErrInvalid)
		return
	}
	encoded, err := json.Marshal([]any{s.StationID, r.PathValue("id"), input})
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	digest := sha256.Sum256(encoded)
	ctx := r.Context()
	var response json.RawMessage
	err = platform.InTransaction(ctx, h.Pool, func(tx pgx.Tx) error {
		insert, err := tx.Exec(ctx, `INSERT INTO idempotency_keys(actor_id,command,key,request_digest) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.AccountID, command, key, digest[:])
		if err != nil {
			return err
		}
		if insert.RowsAffected() == 0 {
			var prior []byte
			var savedStatus *int
			if err := tx.QueryRow(ctx, `SELECT request_digest,response_status,response_body FROM idempotency_keys WHERE actor_id=$1 AND command=$2 AND key=$3`, s.AccountID, command, key).Scan(&prior, &savedStatus, &response); err != nil {
				return err
			}
			if !bytes.Equal(prior, digest[:]) || savedStatus == nil || *savedStatus != status || response == nil {
				return errReplay
			}
			return nil
		}
		result, err := apply(tx)
		if err != nil {
			return err
		}
		response, err = json.Marshal(result.body)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id,reason_code)
			VALUES($1,$2,$3,$4,$5,NULLIF($6,''))`, s.StationID, s.AccountID, command, result.objectID, platform.RequestID(ctx), result.reason); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE idempotency_keys SET response_status=$4,response_body=$5 WHERE actor_id=$1 AND command=$2 AND key=$3`, s.AccountID, command, key, status, response)
		return err
	})
	if err != nil {
		writeSettlementError(w, r, err)
		return
	}
	respond(w, status, completeCODReplay(command, response))
}

// completeCODReplay fills difference_fen into a cached COD batch response that
// was stored before the field existed. The cached bytes, the audit trail and the
// verification record are left untouched and the command is not re-executed.
func completeCODReplay(command string, body json.RawMessage) json.RawMessage {
	if (command != "createCODBatch" && command != "verifyCODBatch") || len(body) == 0 {
		return body
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(body, &stored); err != nil {
		return body
	}
	if _, ok := stored["difference_fen"]; ok {
		return body
	}
	var batch CODBatch
	if err := json.Unmarshal(body, &batch); err != nil {
		return body
	}
	if batch.ActualFen != nil {
		difference := batch.ExpectedFen - *batch.ActualFen
		batch.DifferenceFen = &difference
	}
	completed, err := json.Marshal(batch)
	if err != nil {
		return body
	}
	return completed
}

func (h *HTTP) createBatch(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var in struct {
		RiderID string   `json:"rider_id"`
		Date    string   `json:"local_date"`
		IDs     []string `json:"receipt_ids"`
	}
	h.mutate(w, r, s, "createCODBatch", 201, &in, func(tx pgx.Tx) (commandResult, error) {
		b, err := h.Store.CreateCODBatch(r.Context(), tx, s.StationID, s.AccountID, in.RiderID, in.Date, in.IDs)
		return commandResult{body: b, objectID: b.ID}, err
	})
}

func (h *HTTP) verifyBatch(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var in struct {
		Amount *int64 `json:"actual_fen"`
		Reason string `json:"reason_code"`
	}
	h.mutate(w, r, s, "verifyCODBatch", 200, &in, func(tx pgx.Tx) (commandResult, error) {
		if in.Amount == nil {
			return commandResult{}, ErrInvalid
		}
		b, err := h.Store.VerifyCODBatch(r.Context(), tx, s.StationID, s.AccountID, r.PathValue("id"), *in.Amount, in.Reason)
		return commandResult{b, b.ID, in.Reason}, err
	})
}

func (h *HTTP) approve(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var in struct {
		Reason string `json:"reason_code"`
	}
	h.refundMutation(w, r, s, "approveRefund", &in, func(tx pgx.Tx, id string) (string, error) {
		if !reasonCode.MatchString(in.Reason) {
			return "", ErrInvalid
		}
		return in.Reason, h.Refunds.ApproveRefund(r.Context(), tx, s, id, in.Reason)
	})
}

func (h *HTTP) payout(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var in struct {
		Reference string `json:"receipt_reference"`
	}
	h.refundMutation(w, r, s, "recordCODPayout", &in, func(tx pgx.Tx, id string) (string, error) {
		if !validID(in.Reference) || strings.TrimSpace(in.Reference) != in.Reference {
			return "", ErrInvalid
		}
		return "", h.Refunds.RecordCODPayout(r.Context(), tx, s, id, in.Reference)
	})
}

func (h *HTTP) refundMutation(w http.ResponseWriter, r *http.Request, s identity.Session, command string, input any, apply func(pgx.Tx, string) (string, error)) {
	if h.Refunds == nil {
		writeSettlementError(w, r, errNotReady)
		return
	}
	id := r.PathValue("id")
	if !validID(id) {
		writeSettlementError(w, r, ErrInvalid)
		return
	}
	h.mutate(w, r, s, command, 200, input, func(tx pgx.Tx) (commandResult, error) {
		ctx := r.Context()
		if _, err := scanRefund(tx.QueryRow(ctx, refundSelect+` AND f.id=$2 AND f.source='MANUAL'`, s.StationID, id)); err != nil {
			return commandResult{}, err
		}
		reason, err := apply(tx, id)
		if err != nil {
			return commandResult{}, err
		}
		f, err := scanRefund(tx.QueryRow(ctx, refundSelect+` AND f.id=$2`, s.StationID, id))
		return commandResult{f, id, reason}, err
	})
}
