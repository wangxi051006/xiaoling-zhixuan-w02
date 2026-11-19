package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io"
	"mime"
	"net/http"
	"time"
)

type Task struct {
	ID           string               `json:"id"`
	BuildingName string               `json:"building_name"`
	ItemCount    int32                `json:"item_count"`
	FeeFen       int64                `json:"fee_fen"`
	Slot         Slot                 `json:"slot"`
	Status       contract.OrderStatus `json:"order_status"`
	Address      *Address             `json:"address,omitempty"`
}
type LobbyTask struct {
	ID           string               `json:"id"`
	BuildingName string               `json:"building_name"`
	ItemCount    int32                `json:"item_count"`
	FeeFen       int64                `json:"fee_fen"`
	Slot         Slot                 `json:"slot"`
	Status       contract.OrderStatus `json:"order_status"`
}

func lobby(t Task) LobbyTask {
	return LobbyTask{t.ID, t.BuildingName, t.ItemCount, t.FeeFen, t.Slot, t.Status}
}
func (s *Service) Task(ctx context.Context, tx pgx.Tx, actor identity.Session, id string) (Task, error) {
	task, err := taskIdentity(ctx, tx, actor.StationID, id)
	if err != nil {
		return Task{}, err
	}
	o, err := readOrder(ctx, tx, actor.StationID, task.OrderID, false)
	if err != nil {
		return Task{}, err
	}
	// Re-read assignment after acquiring the order's shared lock; a concurrent
	// reclaim or completion must not leave a stale contact projection.
	task, err = taskIdentity(ctx, tx, actor.StationID, id)
	if err != nil {
		return Task{}, err
	}
	if task.State != "OPEN" && (task.RiderID == nil || *task.RiderID != actor.AccountID) {
		return Task{}, ErrNotFound
	}
	out := Task{ID: id, BuildingName: o.Address.BuildingName, ItemCount: o.ItemCount, FeeFen: o.FeeFen, Slot: slotView(o.Slot), Status: o.Status}
	if task.RiderID != nil && *task.RiderID == actor.AccountID && (task.State == "ASSIGNED" || task.State == "PICKED_UP" || task.State == "RETURNING") {
		a := addressView(o.Address)
		out.Address = &a
	}
	return out, nil
}
func (h *HTTP) availability(w http.ResponseWriter, r *http.Request, actor identity.Session) {
	var in struct {
		Available *bool `json:"available"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		writeError(w, r, err)
		return
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || r.URL.RawQuery != "" || exactJSON(data, &in) != nil || in.Available == nil {
		writeError(w, r, ErrInvalid)
		return
	}
	if h.Service.RiderState == nil {
		writeError(w, r, ErrNotReady)
		return
	}
	err = platform.InTransaction(r.Context(), h.Service.Pool, func(tx pgx.Tx) error {
		if e := h.Service.RiderState.SetRiderAvailability(r.Context(), tx, actor.AccountID, actor.StationID, *in.Available); e != nil {
			return e
		}
		_, e := tx.Exec(r.Context(), `INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id) VALUES($1,$2,'setAvailability',$2,$3)`, actor.StationID, actor.AccountID, platform.RequestID(r.Context()))
		return e
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	respond(w, 200, map[string]bool{"available": *in.Available})
}
func (s *Service) urge(ctx context.Context, tx pgx.Tx, actor identity.Session, id string) error {
	o, err := lockOrder(ctx, tx, actor.StationID, id)
	if err != nil {
		return err
	}
	if o.ActorID != actor.AccountID {
		return ErrNotFound
	}
	switch o.Status {
	case contract.OrderStatusDelivered, contract.OrderStatusCanceled, contract.OrderStatusReturned:
		return ErrInvalidState
	}
	now := s.Clock.Now().UTC()
	if o.LastUrge != nil && now.Sub(*o.LastUrge) < time.Minute {
		return ErrThrottled
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET last_urge_at=$2 WHERE id=$1`, id, now); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"order_id": id})
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_jobs(station_id,kind,dedupe_key,payload,available_at) VALUES($1,'ORDER_URGE',$2,$3,$4)`, actor.StationID, fmt.Sprintf("%s:%d", id, now.UnixNano()), payload, now); err != nil {
		return err
	}
	return s.event(ctx, tx, actor, id, "URGED", "")
}
func (s *Service) markRead(ctx context.Context, tx pgx.Tx, actor identity.Session, ids []string) error {
	if len(ids) < 1 || len(ids) > 100 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id) || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	if err := s.Identity.AssertRider(ctx, tx, actor.AccountID, actor.StationID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM notifications WHERE station_id=$1 AND recipient_id=$2 AND id=ANY($3) ORDER BY id FOR UPDATE`, actor.StationID, actor.AccountID, ids)
	if err != nil {
		return err
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if len(found) != len(ids) {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,$4) WHERE station_id=$1 AND recipient_id=$2 AND id=ANY($3)`, actor.StationID, actor.AccountID, ids, s.Clock.Now().UTC())
	return err
}
