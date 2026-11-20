package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

type Slot struct {
	ID         string    `json:"id"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	DispatchAt time.Time `json:"dispatch_at"`
	Accepting  bool      `json:"accepting"`
}

func slotView(s contract.SlotSnapshot) Slot {
	return Slot{s.ID, s.StartsAt.UTC(), s.EndsAt.UTC(), s.DispatchAt.UTC(), true}
}

type Address struct {
	BuildingID   string `json:"building_id"`
	Room         string `json:"room"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	BuildingName string `json:"building_name"`
}

func addressView(a contract.AddressSnapshot) Address {
	return Address{a.BuildingID, a.Room, a.ContactName, a.ContactPhone, a.BuildingName}
}

type Order struct {
	ID             string                    `json:"id"`
	Mode           contract.DeliveryMode     `json:"delivery_mode"`
	Slot           Slot                      `json:"slot"`
	Status         contract.OrderStatus      `json:"status"`
	Stock          contract.StockStatus      `json:"stock_status"`
	Settlement     contract.SettlementStatus `json:"settlement_status"`
	Method         contract.PaymentMethod    `json:"payment_method"`
	SubtotalFen    int64                     `json:"subtotal_fen"`
	DeliveryFeeFen int64                     `json:"delivery_fee_fen"`
	TotalFen       int64                     `json:"total_fen"`
	Lines          []PriceLine               `json:"lines"`
	CreatedAt      time.Time                 `json:"created_at"`
}
type AdminOrder struct {
	Order
	Address          Address `json:"address"`
	RiderID          *string `json:"rider_id,omitempty"`
	Overdue          bool    `json:"overdue"`
	MerchantAccepted bool    `json:"merchant_accepted"`
}
type orderRow struct {
	ID, ActorID, StationID string
	Mode                   contract.DeliveryMode
	Method                 contract.PaymentMethod
	Status                 contract.OrderStatus
	Stock                  contract.StockStatus
	MerchantAccepted       bool
	TotalFen, FeeFen       int64
	CODCommissionBPS       int64
	ItemCount              int32
	Address                contract.AddressSnapshot
	Slot                   contract.SlotSnapshot
	CreatedAt              time.Time
	Overdue                bool
	LastUrge               *time.Time
}

func lockOrder(ctx context.Context, tx pgx.Tx, station, id string) (o orderRow, err error) {
	return readOrder(ctx, tx, station, id, true)
}
func readOrder(ctx context.Context, tx pgx.Tx, station, id string, lock bool) (o orderRow, err error) {
	if !validID(id) {
		return o, ErrInvalid
	}
	suffix := " FOR SHARE"
	if lock {
		suffix = " FOR UPDATE"
	}
	var a, sl []byte
	err = tx.QueryRow(ctx, `SELECT id,actor_id,station_id,mode,method,status,stock_status,total_fen,fee_fen,item_count,address_snapshot,slot_snapshot,created_at,overdue,last_urge_at,
		(merchant_accepted_at IS NOT NULL OR stock_status='SORTED'),cod_commission_bps FROM orders WHERE station_id=$1 AND id=$2`+suffix, station, id).Scan(&o.ID, &o.ActorID, &o.StationID, &o.Mode, &o.Method, &o.Status, &o.Stock, &o.TotalFen, &o.FeeFen, &o.ItemCount, &a, &sl, &o.CreatedAt, &o.Overdue, &o.LastUrge, &o.MerchantAccepted, &o.CODCommissionBPS)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	if err != nil {
		return o, err
	}
	if err = json.Unmarshal(a, &o.Address); err != nil {
		return o, err
	}
	err = json.Unmarshal(sl, &o.Slot)
	return o, err
}
func (s *Service) Order(ctx context.Context, tx pgx.Tx, actor identity.Session, id string, admin bool) (Order, error) {
	o, err := readOrder(ctx, tx, actor.StationID, id, false)
	if err != nil {
		return Order{}, err
	}
	if !admin && o.ActorID != actor.AccountID {
		return Order{}, ErrNotFound
	}
	out := Order{
		ID: o.ID, Mode: o.Mode, Slot: slotView(o.Slot), Status: o.Status, Stock: o.Stock, Method: o.Method,
		SubtotalFen: o.TotalFen - o.FeeFen, DeliveryFeeFen: o.FeeFen, TotalFen: o.TotalFen,
		CreatedAt: o.CreatedAt.UTC(), Lines: []PriceLine{},
	}
	err = tx.QueryRow(ctx, `SELECT state FROM settlements WHERE order_id=$1`, id).Scan(&out.Settlement)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT sku_id,qty,name,spec,unit_price_fen,line_total_fen FROM order_items WHERE order_id=$1 ORDER BY sku_id`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var l PriceLine
		if err = rows.Scan(&l.SKUID, &l.Qty, &l.Name, &l.Spec, &l.UnitPriceFen, &l.LineTotalFen); err != nil {
			return out, err
		}
		out.Lines = append(out.Lines, l)
	}
	return out, rows.Err()
}
func (s *Service) adminOrder(ctx context.Context, tx pgx.Tx, actor identity.Session, id string) (AdminOrder, error) {
	o, err := s.Order(ctx, tx, actor, id, true)
	if err != nil {
		return AdminOrder{}, err
	}
	row, err := readOrder(ctx, tx, actor.StationID, id, false)
	if err != nil {
		return AdminOrder{}, err
	}
	out := AdminOrder{Order: o, Address: addressView(row.Address), Overdue: row.Overdue, MerchantAccepted: row.MerchantAccepted}
	err = tx.QueryRow(ctx, `SELECT rider_id FROM delivery_tasks WHERE order_id=$1`, id).Scan(&out.RiderID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return out, err
}
func (s *Service) event(ctx context.Context, tx pgx.Tx, actor identity.Session, id, kind, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,actor_id,kind,reason_code,recorded_at) VALUES($1,$2,$3,NULLIF($4,''),$5)`, id, actor.AccountID, kind, reason, s.Clock.Now().UTC())
	return err
}

// notify only records an outbox row; it never calls a mail server here.
func (s *Service) notify(ctx context.Context, tx pgx.Tx, kind string, o orderRow) error {
	if s.EmailNotifier == nil {
		return nil
	}
	return s.EmailNotifier.NotifyOrder(ctx, tx, kind, o.StationID, o.ID, o.ActorID)
}

func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, station, id, kind string, at time.Time) error {
	p, _ := json.Marshal(map[string]string{"order_id": id})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_jobs(station_id,kind,dedupe_key,payload,available_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(station_id,kind,dedupe_key) DO NOTHING`, station, kind, id, p, at)
	return err
}
