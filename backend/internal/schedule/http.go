package schedule

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HTTP struct {
	Pool      *pgxpool.Pool
	Clock     contract.Clock
	Store     Store
	Authorize func(http.ResponseWriter, *http.Request, identity.Entry, bool, ...contract.Role) (identity.Session, bool)
}

func (h *HTTP) Register(mux *http.ServeMux) {
	user := func(fn handler) http.HandlerFunc { return h.require(identity.UserEntry, contract.RoleCustomer, fn) }
	admin := func(fn handler) http.HandlerFunc { return h.require(identity.AdminEntry, contract.RoleOperator, fn) }
	mux.HandleFunc("GET /api/v1/delivery/slots", user(h.listCustomer))
	mux.HandleFunc("GET /api/v1/admin/slots", admin(h.listAdmin))
	mux.HandleFunc("GET /api/v1/admin/slots/{id}", admin(h.getAdmin))
	mux.HandleFunc("PUT /api/v1/admin/slots/{id}", admin(h.updateAdmin))
	mux.HandleFunc("POST /api/v1/admin/slots/batches", admin(h.createBatch))
}

type handler func(http.ResponseWriter, *http.Request, identity.Session)

func (h *HTTP) require(entry identity.Entry, role contract.Role, next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Authorize == nil || h.Pool == nil || h.Clock == nil {
			platform.WriteError(w, r, 503, "NOT_READY", "Service not ready")
			return
		}
		if s, ok := h.Authorize(w, r, entry, false, role); ok {
			next(w, r, s)
		}
	}
}

var errReplayConflict = errors.New("slot batch idempotency conflict")

func slotError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := 500, "INTERNAL_ERROR"
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		status, code = 413, "BODY_TOO_LARGE"
	case errors.Is(err, ErrInvalid):
		status, code = 400, "BAD_REQUEST"
	case errors.Is(err, ErrNotFound):
		status, code = 404, "NOT_FOUND"
	case errors.Is(err, ErrSlotFull):
		status, code = 409, "SLOT_FULL"
	case errors.Is(err, ErrSlotExpired):
		status, code = 409, "SLOT_EXPIRED"
	case errors.Is(err, ErrSlotConflict):
		status, code = 409, "INVALID_STATE"
	case errors.Is(err, errReplayConflict):
		status, code = 409, "IDEMPOTENCY_CONFLICT"
	}
	platform.WriteError(w, r, status, code, http.StatusText(status))
}

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type slotView struct {
	ID         string    `json:"id"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	DispatchAt time.Time `json:"dispatch_at"`
	Accepting  bool      `json:"accepting"`
}

type adminSlotView struct {
	slotView
	Capacity  int64     `json:"capacity"`
	Used      int64     `json:"used"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

func publicView(s slotRow) slotView {
	return slotView{s.ID, s.StartsAt, s.EndsAt, s.DispatchAt, s.Accepting}
}

func adminView(s slotRow) adminSlotView {
	return adminSlotView{publicView(s), s.Capacity, s.Used, s.Version, s.UpdatedAt}
}

type page[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next_cursor"`
}

func parseList(raw string, now time.Time) (day, after time.Time, limit int, err error) {
	v, err := url.ParseQuery(raw)
	if err != nil {
		return day, after, 0, ErrInvalid
	}
	for key, values := range v {
		if len(values) != 1 || values[0] == "" || (key != "date" && key != "limit" && key != "cursor") {
			return day, after, 0, ErrInvalid
		}
	}
	date := v.Get("date")
	if date == "" {
		date = now.In(shanghai).Format(localDateFormat)
	}
	day, err = time.ParseInLocation(localDateFormat, date, shanghai)
	if err != nil || day.Format(localDateFormat) != date || day.Year() < 1 {
		return day, after, 0, ErrInvalid
	}
	limit = 20
	if n := v.Get("limit"); n != "" {
		limit, err = strconv.Atoi(n)
		if err != nil || limit < 1 || limit > 100 {
			return day, after, 0, ErrInvalid
		}
	}
	if cursor := v.Get("cursor"); cursor != "" {
		if len(cursor) > 128 {
			return day, after, 0, ErrInvalid
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return day, after, 0, ErrInvalid
		}
		after, err = time.Parse(time.RFC3339Nano, string(raw))
		if err != nil || after.Before(day) || !after.Before(day.AddDate(0, 0, 1)) {
			return day, after, 0, ErrInvalid
		}
	}
	return day, after, limit, nil
}

func (h *HTTP) list(r *http.Request, station string, public bool) ([]slotRow, *string, error) {
	now := h.Clock.Now()
	day, after, limit, err := parseList(r.URL.RawQuery, now)
	if err != nil {
		return nil, nil, err
	}
	var cursor any
	if !after.IsZero() {
		cursor = after
	}
	filter := ""
	args := []any{station, day, day.AddDate(0, 0, 1), cursor, limit + 1}
	if public {
		filter = ` AND accepting AND used<capacity AND dispatch_at>$6 AND starts_at<=$7`
		args = append(args, now, now.In(shanghai).AddDate(0, 0, h.Store.days()))
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT `+slotColumns+` FROM delivery_slots
		WHERE station_id=$1 AND starts_at >= $2 AND starts_at < $3 AND ($4::timestamptz IS NULL OR starts_at > $4)`+filter+` ORDER BY starts_at LIMIT $5`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := make([]slotRow, 0, limit+1)
	for rows.Next() {
		row, err := scanSlot(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	next := base64.RawURLEncoding.EncodeToString([]byte(items[limit-1].StartsAt.Format(time.RFC3339Nano)))
	return items[:limit], &next, nil
}

func (h *HTTP) listCustomer(w http.ResponseWriter, r *http.Request, session identity.Session) {
	rows, cursor, err := h.list(r, session.StationID, true)
	if err != nil {
		slotError(w, r, err)
		return
	}
	items := make([]slotView, 0, len(rows))
	for _, row := range rows {
		items = append(items, publicView(row))
	}
	respond(w, 200, page[slotView]{items, cursor})
}

func (h *HTTP) listAdmin(w http.ResponseWriter, r *http.Request, session identity.Session) {
	rows, cursor, err := h.list(r, session.StationID, false)
	if err != nil {
		slotError(w, r, err)
		return
	}
	items := make([]adminSlotView, 0, len(rows))
	for _, row := range rows {
		items = append(items, adminView(row))
	}
	respond(w, 200, page[adminSlotView]{items, cursor})
}

func validText(s string, max int) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func (h *HTTP) getAdmin(w http.ResponseWriter, r *http.Request, session identity.Session) {
	id := r.PathValue("id")
	if !validText(id, 128) {
		slotError(w, r, ErrInvalid)
		return
	}
	row, err := scanSlot(h.Pool.QueryRow(r.Context(), `SELECT `+slotColumns+` FROM delivery_slots WHERE id=$1 AND station_id=$2`, id, session.StationID))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		slotError(w, r, err)
		return
	}
	respond(w, 200, adminView(row))
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return ErrInvalid
	}
	body := http.MaxBytesReader(w, r.Body, 4096)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	var fields []string
	switch v.(type) {
	case *updateBody:
		fields = []string{"expected_version", "capacity", "accepting"}
	case *batchBody:
		fields = []string{"local_date", "opens_at", "closes_at", "lead_minutes", "capacity"}
	default:
		return ErrInvalid
	}
	allowed := make(map[string]bool, len(fields))
	for _, key := range fields {
		allowed[key] = true
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(fields))
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || seen[key] {
			return ErrInvalid
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	if err := json.Unmarshal(data, v); err != nil {
		return ErrInvalid
	}
	return nil
}

const maxSafeInteger = 9007199254740991

type updateBody struct {
	Version   *int64 `json:"expected_version"`
	Capacity  *int64 `json:"capacity"`
	Accepting *bool  `json:"accepting"`
}

func (h *HTTP) updateAdmin(w http.ResponseWriter, r *http.Request, session identity.Session) {
	var in updateBody
	if err := decodeBody(w, r, &in); err != nil {
		slotError(w, r, err)
		return
	}
	id := r.PathValue("id")
	if !validText(id, 128) || in.Version == nil || *in.Version < 1 || *in.Version > maxSafeInteger ||
		in.Capacity == nil || *in.Capacity < 0 || *in.Capacity > maxSafeInteger || in.Accepting == nil {
		slotError(w, r, ErrInvalid)
		return
	}
	ctx := r.Context()
	var result slotRow
	err := platform.InTransaction(ctx, h.Pool, func(tx pgx.Tx) error {
		row, err := scanSlot(tx.QueryRow(ctx, `SELECT `+slotColumns+` FROM delivery_slots WHERE id=$1 AND station_id=$2 FOR UPDATE`, id, session.StationID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if row.Version != *in.Version || row.Version >= maxSafeInteger || row.Used > *in.Capacity {
			return ErrSlotConflict
		}
		result, err = scanSlot(tx.QueryRow(ctx, `UPDATE delivery_slots SET capacity=$2, accepting=$3, version=version+1, updated_at=$4 WHERE id=$1 RETURNING `+slotColumns,
			id, *in.Capacity, *in.Accepting, h.Clock.Now()))
		if err != nil {
			return err
		}
		return slotAudit(ctx, tx, r, session, "updateAdminSlot", id)
	})
	if err != nil {
		slotError(w, r, err)
		return
	}
	respond(w, 200, adminView(result))
}

type batchBody struct {
	Date     string `json:"local_date"`
	Opens    string `json:"opens_at"`
	Closes   string `json:"closes_at"`
	Lead     *int   `json:"lead_minutes"`
	Capacity int64  `json:"capacity"`
}

func (h *HTTP) createBatch(w http.ResponseWriter, r *http.Request, s identity.Session) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !validText(keys[0], 128) {
		slotError(w, r, ErrInvalid)
		return
	}
	var in batchBody
	if err := decodeBody(w, r, &in); err != nil {
		slotError(w, r, err)
		return
	}
	if in.Lead == nil || in.Capacity < 1 || in.Capacity > 9999 {
		slotError(w, r, ErrInvalid)
		return
	}
	windows, err := generateWindows(in.Date, in.Opens, in.Closes, *in.Lead)
	if err != nil {
		slotError(w, r, ErrInvalid)
		return
	}
	digestBody, err := json.Marshal(struct {
		Station string
		Batch   batchBody
	}{s.StationID, in})
	if err != nil {
		slotError(w, r, err)
		return
	}
	digest := sha256.Sum256(digestBody)
	ctx := r.Context()
	const command = "createSlotBatch"
	key := keys[0]
	var response json.RawMessage
	err = platform.InTransaction(ctx, h.Pool, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `INSERT INTO idempotency_keys(actor_id,command,key,request_digest) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.AccountID, command, key, digest[:])
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			var prior []byte
			var savedStatus *int
			if err := tx.QueryRow(ctx, `SELECT request_digest,response_status,response_body FROM idempotency_keys WHERE actor_id=$1 AND command=$2 AND key=$3`, s.AccountID, command, key).Scan(&prior, &savedStatus, &response); err != nil {
				return err
			}
			if !bytes.Equal(prior, digest[:]) || savedStatus == nil || *savedStatus != 201 || response == nil {
				return errReplayConflict
			}
			return nil
		}
		items, err := insertBatch(ctx, tx, s.StationID, h.Clock.Now(), windows, in.Capacity)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := slotAudit(ctx, tx, r, s, command, item.ID); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(page[adminSlotView]{items, nil})
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `UPDATE idempotency_keys SET response_status=201,response_body=$4 WHERE actor_id=$1 AND command=$2 AND key=$3 RETURNING response_body`, s.AccountID, command, key, encoded).Scan(&response)
	})
	if err != nil {
		slotError(w, r, err)
		return
	}
	respond(w, 201, response)
}

func slotAudit(ctx context.Context, tx pgx.Tx, r *http.Request, s identity.Session, action, objectID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id) VALUES($1,$2,$3,$4,$5)`, s.StationID, s.AccountID, action, objectID, platform.RequestID(r.Context()))
	return err
}

func insertBatch(ctx context.Context, tx pgx.Tx, station string, now time.Time, windows []contract.SlotSnapshot, capacity int64) ([]adminSlotView, error) {
	// Serialize batches per station, including offsets that would otherwise overlap without identical keys.
	var stationID string
	if err := tx.QueryRow(ctx, `SELECT id FROM stations WHERE id=$1 FOR NO KEY UPDATE`, station).Scan(&stationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	items := make([]adminSlotView, 0, len(windows))
	for _, slot := range windows {
		row, err := scanSlot(tx.QueryRow(ctx, `SELECT `+slotColumns+` FROM delivery_slots
			WHERE station_id=$1 AND starts_at<$3 AND ends_at>$2 ORDER BY starts_at LIMIT 1 FOR UPDATE`, station, slot.StartsAt, slot.EndsAt))
		switch {
		case err == nil:
			if !row.StartsAt.Equal(slot.StartsAt) || !row.EndsAt.Equal(slot.EndsAt) || !row.DispatchAt.Equal(slot.DispatchAt) || row.Capacity != capacity {
				return nil, ErrSlotConflict
			}
		case errors.Is(err, pgx.ErrNoRows):
			var id [16]byte
			rand.Read(id[:])
			row, err = scanSlot(tx.QueryRow(ctx, `INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity,updated_at)
				VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+slotColumns, hex.EncodeToString(id[:]), station, slot.StartsAt, slot.EndsAt, slot.DispatchAt, capacity, now))
			if err != nil {
				return nil, err
			}
		default:
			return nil, err
		}
		items = append(items, adminView(row))
	}
	return items, nil
}
