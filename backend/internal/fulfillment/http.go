package fulfillment

import (
	"bytes"
	"campus.local/xiaoling/internal/catalog"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/internal/schedule"
	"campus.local/xiaoling/internal/settlement"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"io"
	"mime"
	"net/http"
	"strings"
)

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := 500, "INTERNAL_ERROR"
	var large *http.MaxBytesError
	var pg *pgconn.PgError
	switch {
	case errors.As(err, &large):
		status, code = 413, "BODY_TOO_LARGE"
	case errors.Is(err, ErrInvalid), errors.Is(err, catalog.ErrInvalid), errors.Is(err, schedule.ErrInvalid), errors.Is(err, settlement.ErrInvalid):
		status, code = 400, "BAD_REQUEST"
	case errors.Is(err, ErrNotFound), errors.Is(err, identity.ErrObjectNotFound), errors.Is(err, settlement.ErrNotFound):
		status, code = 404, "NOT_FOUND"
	case errors.Is(err, ErrQuoteChanged), errors.Is(err, catalog.ErrPriceChanged), errors.Is(err, catalog.ErrUnavailable):
		status, code = 409, "QUOTE_CHANGED"
	case errors.Is(err, schedule.ErrSlotFull):
		status, code = 409, "SLOT_FULL"
	case errors.Is(err, schedule.ErrSlotExpired):
		status, code = 409, "SLOT_EXPIRED"
	case errors.Is(err, catalog.ErrStock):
		status, code = 409, "OUT_OF_STOCK"
	case errors.Is(err, settlement.ErrPaymentMethodDisabled):
		status, code = 409, "PAYMENT_METHOD_DISABLED"
	case errors.Is(err, settlement.ErrInsufficientBalance):
		status, code = 409, "INSUFFICIENT_BALANCE"
	case errors.Is(err, ErrCODActiveLimit):
		status, code = 409, "COD_ACTIVE_LIMIT"
	case errors.Is(err, ErrTaskClaimed):
		status, code = 409, "TASK_ALREADY_CLAIMED"
	case errors.Is(err, ErrIdempotency):
		status, code = 409, "IDEMPOTENCY_CONFLICT"
	case errors.Is(err, ErrInvalidState), errors.Is(err, settlement.ErrInvalidState), errors.Is(err, catalog.ErrStockState), errors.Is(err, schedule.ErrSlotConflict):
		status, code = 409, "INVALID_STATE"
	case errors.Is(err, ErrThrottled):
		status, code = 429, "RATE_LIMITED"
	case errors.Is(err, ErrNotReady):
		status, code = 503, "NOT_READY"
	case errors.As(err, &pg) && pg.Code == "23505":
		status, code = 409, "INVALID_STATE"
		if pg.ConstraintName == "orders_active_cod_guard" {
			code = "COD_ACTIVE_LIMIT"
		}
	}
	platform.WriteError(w, r, status, code, http.StatusText(status))
}

type commandResult struct {
	body             any
	objectID, reason string
}

func (h *HTTP) mutate(w http.ResponseWriter, r *http.Request, actor identity.Session, command string, status int, input any, noBody bool, apply func(pgx.Tx) (commandResult, error)) {
	keys := r.Header.Values("Idempotency-Key")
	if r.URL.RawQuery != "" || len(keys) != 1 || !validID(keys[0]) || strings.TrimSpace(keys[0]) != keys[0] {
		writeError(w, r, ErrInvalid)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if noBody {
		if len(bytes.TrimSpace(data)) != 0 {
			writeError(w, r, ErrInvalid)
			return
		}
	} else {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || exactJSON(data, input) != nil {
			writeError(w, r, ErrInvalid)
			return
		}
	}
	if q, ok := input.(*QuoteInput); ok {
		lines, e := normalizeLines(q.Lines)
		if e != nil {
			writeError(w, r, e)
			return
		}
		q.Lines = lines
	}
	canonical, err := json.Marshal([]any{actor.StationID, r.PathValue("id"), input})
	if err != nil {
		writeError(w, r, err)
		return
	}
	digest := sha256.Sum256(canonical)
	// Check committed replays before security admission, and release this pool
	// connection before opening either transaction. Never nest pool acquisition.
	if command == "createOrder" {
		var committed bool
		err = h.Service.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM idempotency_keys WHERE actor_id=$1 AND command=$2 AND key=$3 AND response_status IS NOT NULL)`, actor.AccountID, command, keys[0]).Scan(&committed)
		if err == nil && !committed {
			err = h.Service.admitOrderAttempt(r.Context(), actor.AccountID, r.RemoteAddr)
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
	}
	var response json.RawMessage
	err = platform.InTransaction(r.Context(), h.Service.Pool, func(tx pgx.Tx) error {
		result, e := tx.Exec(r.Context(), `INSERT INTO idempotency_keys(actor_id,command,key,request_digest) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, actor.AccountID, command, keys[0], digest[:])
		if e != nil {
			return e
		}
		if result.RowsAffected() == 0 {
			var prior []byte
			var saved *int
			if e = tx.QueryRow(r.Context(), `SELECT request_digest,response_status,response_body FROM idempotency_keys WHERE actor_id=$1 AND command=$2 AND key=$3`, actor.AccountID, command, keys[0]).Scan(&prior, &saved, &response); e != nil {
				return e
			}
			if !bytes.Equal(prior, digest[:]) || saved == nil || *saved != status || len(response) == 0 {
				return ErrIdempotency
			}
			if strings.HasPrefix(command, "rider:") {
				// Contact access expires with assignment/fulfillment. Replay the
				// committed command without resurrecting its historical PII view.
				if e = h.Service.Identity.AssertRider(r.Context(), tx, actor.AccountID, actor.StationID); e != nil {
					return e
				}
				current, err := h.Service.Task(r.Context(), tx, actor, r.PathValue("id"))
				if err != nil {
					return err
				}
				response, e = json.Marshal(current)
				return e
			}
			return nil
		}
		out, e := apply(tx)
		if e != nil {
			return e
		}
		response, e = json.Marshal(out.body)
		if e != nil {
			return e
		}
		storedResponse := response
		if strings.HasPrefix(command, "rider:") {
			safe, ok := out.body.(Task)
			if !ok {
				return ErrInvalidState
			}
			safe.Address = nil
			storedResponse, e = json.Marshal(safe)
			if e != nil {
				return e
			}
		}
		if _, e = tx.Exec(r.Context(), `INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id,reason_code) VALUES($1,$2,$3,$4,$5,NULLIF($6,''))`, actor.StationID, actor.AccountID, command, out.objectID, platform.RequestID(r.Context()), out.reason); e != nil {
			return e
		}
		_, e = tx.Exec(r.Context(), `UPDATE idempotency_keys SET response_status=$4,response_body=$5 WHERE actor_id=$1 AND command=$2 AND key=$3`, actor.AccountID, command, keys[0], status, storedResponse)
		return e
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	// PostgreSQL jsonb orders object keys differently. Canonicalize both fresh and
	// replayed envelopes so a committed retry returns the same wire response.
	var decoded any
	if err = json.Unmarshal(response, &decoded); err != nil {
		writeError(w, r, err)
		return
	}
	response, err = json.Marshal(decoded)
	if err != nil {
		writeError(w, r, err)
		return
	}
	respond(w, status, response)
}
func (h *HTTP) serve(w http.ResponseWriter, r *http.Request, actor identity.Session, route string) {
	s := h.Service
	if s == nil || s.Pool == nil || s.Clock == nil || s.Identity == nil || s.Catalog == nil || s.Schedule == nil || s.Settlement == nil || s.Clock.Now().IsZero() || !validID(actor.AccountID) || !validID(actor.StationID) {
		writeError(w, r, ErrNotReady)
		return
	}
	if r.Method == http.MethodGet {
		h.read(w, r, actor, route)
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	if id != "" && !validID(id) {
		writeError(w, r, ErrInvalid)
		return
	}
	switch route {
	case "POST /checkout/quote":
		var in QuoteInput
		h.mutate(w, r, actor, "createQuote", 201, &in, false, func(tx pgx.Tx) (commandResult, error) {
			q, e := s.Quote(ctx, tx, actor, in)
			return commandResult{body: q, objectID: q.ID}, e
		})
		return
	case "POST /orders":
		var in OrderInput
		h.mutate(w, r, actor, "createOrder", 201, &in, false, func(tx pgx.Tx) (commandResult, error) {
			o, e := s.Create(ctx, tx, actor, in)
			return commandResult{body: o, objectID: o.ID}, e
		})
		return
	case "PUT /rider/availability":
		h.availability(w, r, actor)
		return
	case "POST /rider/messages/read":
		var in struct {
			IDs []string `json:"ids"`
		}
		h.mutate(w, r, actor, "markMessagesRead", 200, &in, false, func(tx pgx.Tx) (commandResult, error) {
			if e := s.markRead(ctx, tx, actor, in.IDs); e != nil {
				return commandResult{}, e
			}
			return commandResult{body: map[string]bool{"accepted": true}, objectID: actor.AccountID}, nil
		})
		return
	case "POST /orders/{id}/urge":
		h.mutate(w, r, actor, "urgeOrder", 202, nil, true, func(tx pgx.Tx) (commandResult, error) {
			if e := s.urge(ctx, tx, actor, id); e != nil {
				return commandResult{}, e
			}
			return commandResult{body: map[string]bool{"accepted": true}, objectID: id}, nil
		})
		return
	}
	action := route[strings.LastIndex(route, "/")+1:]
	if strings.HasPrefix(route, "POST /rider/tasks/") {
		var in any
		noBody := true
		var cash DeliverInput
		var reason Reason
		if action == "deliver" {
			in = &cash
			noBody = false
		} else if action == "return" {
			in = &reason
			noBody = false
		}
		h.mutate(w, r, actor, "rider:"+action, 200, in, noBody, func(tx pgx.Tx) (commandResult, error) {
			if e := s.taskCommand(ctx, tx, actor, id, action, cash.Cash, reason.Code); e != nil {
				return commandResult{}, e
			}
			view, e := s.Task(ctx, tx, actor, id)
			return commandResult{body: view, objectID: id, reason: reason.Code}, e
		})
		return
	}
	var input any
	noBody := false
	var prep PrepareInput
	var reason Reason
	var restock RestockInput
	var inspect InspectInput
	switch action {
	case "prepare":
		input = &prep
	case "dispatch":
		noBody = true
	case "cancel", "reclaim", "refund":
		input = &reason
	case "restock":
		input = &restock
	case "return-inspect":
		input = &inspect
	default:
		writeError(w, r, ErrInvalid)
		return
	}
	customer := strings.HasPrefix(route, "POST /orders/")
	command := action
	if customer {
		command = "customer:" + action
	} else {
		command = "admin:" + action
	}
	status := 200
	if action == "refund" {
		status = 201
	}
	h.mutate(w, r, actor, command, status, input, noBody, func(tx pgx.Tx) (commandResult, error) {
		o, e := lockOrder(ctx, tx, actor.StationID, id)
		if e != nil {
			return commandResult{}, e
		}
		if customer && o.ActorID != actor.AccountID {
			return commandResult{}, ErrNotFound
		}
		switch action {
		case "prepare":
			e = s.prepare(ctx, tx, actor, o, prep)
		case "dispatch":
			e = s.dispatch(ctx, tx, actor, o)
		case "cancel":
			e = s.cancel(ctx, tx, actor, o, reason.Code, customer)
		case "reclaim":
			e = s.reclaim(ctx, tx, actor, o, reason.Code)
		case "restock":
			e = s.restock(ctx, tx, actor, o, restock)
		case "return-inspect":
			e = s.inspect(ctx, tx, actor, o, inspect)
		case "refund":
			e = s.requestRefund(ctx, tx, actor, o, reason.Code)
		}
		if e != nil {
			return commandResult{}, e
		}
		result := commandResult{objectID: id, reason: reason.Code}
		if action == "restock" {
			result.reason = restock.Code
		}
		if action == "return-inspect" {
			result.reason = inspect.Code
		}
		if action == "refund" {
			result.body, e = refundView(ctx, tx, id)
		} else if customer {
			result.body, e = s.Order(ctx, tx, actor, id, false)
		} else {
			result.body, e = s.adminOrder(ctx, tx, actor, id)
		}
		return result, e
	})
}
func inTxRead(ctx context.Context, s *Service, apply func(pgx.Tx) (any, error)) (any, error) {
	var out any
	err := platform.InTransaction(ctx, s.Pool, func(tx pgx.Tx) error { var err error; out, err = apply(tx); return err })
	return out, err
}
