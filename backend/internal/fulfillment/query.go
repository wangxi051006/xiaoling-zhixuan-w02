package fulfillment

import (
	"campus.local/xiaoling/internal/identity"
	"context"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type page[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next_cursor"`
}
type pageInput struct {
	Cursor, Date string
	Limit        int
}

func readPage(raw string, dateAllowed, list bool) (p pageInput, err error) {
	p.Limit = 20
	v, e := url.ParseQuery(raw)
	if e != nil {
		return p, ErrInvalid
	}
	for k, a := range v {
		if len(a) != 1 || a[0] == "" {
			return p, ErrInvalid
		}
		switch k {
		case "cursor":
			if !list || !validID(a[0]) {
				return p, ErrInvalid
			}
			p.Cursor = a[0]
		case "limit":
			n, e := strconv.Atoi(a[0])
			if !list || e != nil || n < 1 || n > 100 {
				return p, ErrInvalid
			}
			p.Limit = n
		case "date":
			d, e := time.Parse(time.DateOnly, a[0])
			if !dateAllowed || e != nil || d.Year() < 1 || d.Format(time.DateOnly) != a[0] {
				return p, ErrInvalid
			}
			p.Date = a[0]
		default:
			return p, ErrInvalid
		}
	}
	return p, nil
}
func nextPage[T any](items []T, p pageInput, id func(T) string) page[T] {
	if len(items) <= p.Limit {
		return page[T]{Items: items}
	}
	cursor := id(items[p.Limit-1])
	return page[T]{Items: items[:p.Limit], Next: &cursor}
}
func (h *HTTP) read(w http.ResponseWriter, r *http.Request, actor identity.Session, route string) {
	dateAllowed := route == "GET /admin/dashboard" || route == "GET /rider/earnings"
	detail := strings.Contains(route, "{id}")
	list := !detail && route != "GET /admin/dashboard"
	p, err := readPage(r.URL.RawQuery, dateAllowed, list)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if detail && r.URL.RawQuery != "" {
		writeError(w, r, ErrInvalid)
		return
	}
	if dateAllowed && p.Date == "" {
		z, e := time.LoadLocation("Asia/Shanghai")
		if e != nil {
			writeError(w, r, ErrNotReady)
			return
		}
		p.Date = h.Service.Clock.Now().In(z).Format(time.DateOnly)
	}
	out, err := inTxRead(r.Context(), h.Service, func(tx pgx.Tx) (any, error) {
		if strings.Contains(route, "/rider/") {
			if e := h.Service.Identity.AssertRider(r.Context(), tx, actor.AccountID, actor.StationID); e != nil {
				return nil, e
			}
		}
		switch route {
		case "GET /orders/{id}":
			return h.Service.Order(r.Context(), tx, actor, r.PathValue("id"), false)
		case "GET /admin/orders/{id}":
			return h.Service.adminOrder(r.Context(), tx, actor, r.PathValue("id"))
		case "GET /orders", "GET /admin/orders":
			return h.Service.orderList(r.Context(), tx, actor, p, route == "GET /admin/orders")
		case "GET /rider/tasks/{id}":
			return h.Service.Task(r.Context(), tx, actor, r.PathValue("id"))
		case "GET /rider/tasks", "GET /rider/history":
			return h.Service.taskList(r.Context(), tx, actor, p, route == "GET /rider/history")
		case "GET /rider/earnings":
			return earnings(r.Context(), tx, actor, p)
		case "GET /rider/messages":
			return messages(r.Context(), tx, actor, p)
		case "GET /admin/dashboard":
			return dashboard(r.Context(), tx, actor, p.Date)
		}
		return nil, ErrInvalid
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Service) orderList(ctx context.Context, tx pgx.Tx, actor identity.Session, p pageInput, admin bool) (any, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM orders WHERE station_id=$1 AND ($2::bool OR actor_id=$3) AND id>$4 ORDER BY id LIMIT $5`, actor.StationID, admin, actor.AccountID, p.Cursor, p.Limit+1)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if admin {
		items := make([]AdminOrder, 0, len(ids))
		for _, id := range ids {
			o, e := s.adminOrder(ctx, tx, actor, id)
			if e != nil {
				return nil, e
			}
			items = append(items, o)
		}
		return nextPage(items, p, func(o AdminOrder) string { return o.ID }), nil
	}
	items := make([]Order, 0, len(ids))
	for _, id := range ids {
		o, e := s.Order(ctx, tx, actor, id, false)
		if e != nil {
			return nil, e
		}
		items = append(items, o)
	}
	return nextPage(items, p, func(o Order) string { return o.ID }), nil
}
func (s *Service) taskList(ctx context.Context, tx pgx.Tx, actor identity.Session, p pageInput, history bool) (any, error) {
	clause := `(t.state='OPEN' OR (t.rider_id=$2 AND t.state IN ('ASSIGNED','PICKED_UP','RETURNING')))`
	if history {
		clause = `t.rider_id=$2 AND t.state IN ('DELIVERED','CLOSED')`
	}
	rows, err := tx.Query(ctx, `SELECT t.id FROM delivery_tasks t JOIN orders o ON o.id=t.order_id WHERE o.station_id=$1 AND `+clause+` AND t.id>$3 ORDER BY t.id LIMIT $4`, actor.StationID, actor.AccountID, p.Cursor, p.Limit+1)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	items := make([]LobbyTask, 0, len(ids))
	for _, id := range ids {
		v, e := s.Task(ctx, tx, actor, id)
		if e != nil {
			return nil, e
		}
		items = append(items, lobby(v))
	}
	return nextPage(items, p, func(t LobbyTask) string { return t.ID }), nil
}

type Earning struct {
	ID     string    `json:"id"`
	TaskID string    `json:"task_id"`
	Amount int64     `json:"amount_fen"`
	At     time.Time `json:"recorded_at"`
}

func earnings(ctx context.Context, tx pgx.Tx, actor identity.Session, p pageInput) (any, error) {
	rows, err := tx.Query(ctx, `SELECT e.id,e.task_id,e.amount_fen,e.recorded_at FROM rider_earnings e JOIN delivery_tasks t ON t.id=e.task_id JOIN orders o ON o.id=t.order_id WHERE o.station_id=$1 AND e.rider_id=$2 AND e.id>$3 AND (e.recorded_at AT TIME ZONE 'Asia/Shanghai')::date=$4::text::date ORDER BY e.id LIMIT $5`, actor.StationID, actor.AccountID, p.Cursor, p.Date, p.Limit+1)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Earning, error) {
		var v Earning
		e := r.Scan(&v.ID, &v.TaskID, &v.Amount, &v.At)
		v.At = v.At.UTC()
		return v, e
	})
	if items == nil {
		items = []Earning{}
	}
	return nextPage(items, p, func(v Earning) string { return v.ID }), err
}

type Message struct {
	ID     string     `json:"id"`
	Kind   string     `json:"kind"`
	TaskID string     `json:"task_id"`
	At     time.Time  `json:"created_at"`
	ReadAt *time.Time `json:"read_at,omitempty"`
}

func messages(ctx context.Context, tx pgx.Tx, actor identity.Session, p pageInput) (any, error) {
	rows, err := tx.Query(ctx, `SELECT id,kind,task_id,created_at,read_at FROM notifications WHERE station_id=$1 AND recipient_id=$2 AND id>$3 ORDER BY id LIMIT $4`, actor.StationID, actor.AccountID, p.Cursor, p.Limit+1)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var v Message
		e := r.Scan(&v.ID, &v.Kind, &v.TaskID, &v.At, &v.ReadAt)
		v.At = v.At.UTC()
		if v.ReadAt != nil {
			at := v.ReadAt.UTC()
			v.ReadAt = &at
		}
		return v, e
	})
	if items == nil {
		items = []Message{}
	}
	return nextPage(items, p, func(v Message) string { return v.ID }), err
}

type Dashboard struct {
	Date      string `json:"local_date"`
	Orders    int64  `json:"order_count"`
	Delivered int64  `json:"delivered_count"`
	Net       int64  `json:"net_collected_fen"`
	Overdue   int64  `json:"overdue_count"`
}

func dashboard(ctx context.Context, tx pgx.Tx, actor identity.Session, date string) (Dashboard, error) {
	out := Dashboard{Date: date}
	err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='DELIVERED'),count(*) FILTER(WHERE overdue) FROM orders WHERE station_id=$1 AND (created_at AT TIME ZONE 'Asia/Shanghai')::date=$2::text::date`, actor.StationID, date).Scan(&out.Orders, &out.Delivered, &out.Overdue)
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT (COALESCE((SELECT sum(c.amount_fen) FROM cod_receipts c JOIN settlements s USING(order_id) WHERE s.station_id=$1 AND (c.collected_at AT TIME ZONE 'Asia/Shanghai')::date=$2::text::date),0) - COALESCE((SELECT sum(w.delta_fen) FROM wallet_entries w WHERE w.station_id=$1 AND w.kind IN ('DEBIT','REFUND') AND (w.recorded_at AT TIME ZONE 'Asia/Shanghai')::date=$2::text::date),0) - COALESCE((SELECT sum(s.amount_fen) FROM cod_refund_payouts p JOIN refunds f ON f.id=p.refund_id JOIN settlements s ON s.order_id=f.order_id WHERE s.station_id=$1 AND (p.recorded_at AT TIME ZONE 'Asia/Shanghai')::date=$2::text::date),0))::bigint`, actor.StationID, date).Scan(&out.Net)
	if out.Net > maxFen || out.Net < -maxFen {
		return out, ErrInvalidState
	}
	return out, err
}

type Refund struct {
	ID      string    `json:"id"`
	OrderID string    `json:"order_id"`
	Amount  int64     `json:"amount_fen"`
	Method  string    `json:"payment_method"`
	Status  string    `json:"status"`
	At      time.Time `json:"requested_at"`
}

func refundView(ctx context.Context, tx pgx.Tx, id string) (Refund, error) {
	var out Refund
	err := tx.QueryRow(ctx, `SELECT f.id,f.order_id,s.amount_fen,s.method,s.state,f.requested_at FROM refunds f JOIN settlements s USING(order_id) WHERE f.order_id=$1`, id).Scan(&out.ID, &out.OrderID, &out.Amount, &out.Method, &out.Status, &out.At)
	out.At = out.At.UTC()
	return out, err
}
