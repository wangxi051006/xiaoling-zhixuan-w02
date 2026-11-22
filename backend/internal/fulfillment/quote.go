package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"reflect"
	"time"
)

type QuoteInput struct {
	Lines     []contract.OrderLineInput `json:"lines"`
	AddressID string                    `json:"address_id"`
	Mode      contract.DeliveryMode     `json:"delivery_mode"`
	SlotID    *string                   `json:"slot_id,omitempty"`
	Method    contract.PaymentMethod    `json:"payment_method"`
}
type PriceLine struct {
	SKUID        string `json:"sku_id"`
	Qty          int32  `json:"qty"`
	Name         string `json:"name"`
	Spec         string `json:"spec"`
	UnitPriceFen int64  `json:"unit_price_fen"`
	LineTotalFen int64  `json:"line_total_fen"`
}

func priceViews(lines []contract.PricedLine) []PriceLine {
	out := make([]PriceLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, PriceLine{l.SKUID, l.Qty, l.Name, l.Spec, l.UnitPriceFen, l.LineTotalFen})
	}
	return out
}

type Quote struct {
	ID             string                 `json:"quote_id"`
	ValidUntil     time.Time              `json:"valid_until"`
	SlotID         string                 `json:"slot_id"`
	Lines          []PriceLine            `json:"lines"`
	SubtotalFen    int64                  `json:"subtotal_fen"`
	DeliveryFeeFen int64                  `json:"delivery_fee_fen"`
	TotalFen       int64                  `json:"total_fen"`
	Method         contract.PaymentMethod `json:"payment_method"`
}
type quoteRow struct {
	ID, ActorID, StationID string
	MerchantID             string
	Mode                   contract.DeliveryMode
	Method                 contract.PaymentMethod
	Address                contract.AddressSnapshot
	Slot                   contract.SlotSnapshot
	Prices                 contract.PricedLines
	ValidUntil             time.Time
}

func (s *Service) Quote(ctx context.Context, tx pgx.Tx, actor identity.Session, in QuoteInput) (Quote, error) {
	if !s.AcceptNewOrders {
		return Quote{}, ErrNotReady
	}
	if in.Method != contract.PaymentMethodCod && in.Method != contract.PaymentMethodBalance && in.Method != contract.PaymentMethodWechat {
		return Quote{}, ErrInvalid
	}
	lines, err := normalizeLines(in.Lines)
	if err != nil || !validID(in.AddressID) {
		return Quote{}, ErrInvalid
	}
	slotID := ""
	if in.Mode == contract.DeliveryModeScheduled {
		if in.SlotID == nil || !validID(*in.SlotID) {
			return Quote{}, ErrInvalid
		}
		slotID = *in.SlotID
	} else if in.Mode != contract.DeliveryModeAsap || in.SlotID != nil {
		return Quote{}, ErrInvalid
	}
	if err := s.Policy.Check(in.Method); err != nil {
		return Quote{}, err
	}
	address, err := s.Identity.ResolveAddress(ctx, tx, actor.AccountID, in.AddressID)
	if err != nil {
		return Quote{}, err
	}
	if address.StationID != actor.StationID {
		return Quote{}, ErrNotFound
	}
	now := s.Clock.Now().UTC()
	slot, err := s.Schedule.SelectSlot(ctx, tx, actor.StationID, in.Mode, slotID, now)
	if err != nil {
		return Quote{}, err
	}
	items, err := s.Catalog.Price(ctx, tx, lines, now)
	if err != nil {
		return Quote{}, err
	}
	merchant, err := checkoutMerchant(ctx, tx, actor.StationID, items.Lines)
	if err != nil {
		return Quote{}, err
	}
	prices, err := withDeliveryFee(ctx, tx, actor.StationID, items)
	if err != nil {
		return Quote{}, err
	}
	a, _ := json.Marshal(address)
	sl, _ := json.Marshal(slot)
	p, _ := json.Marshal(prices)
	q := Quote{
		ValidUntil: now.Add(5 * time.Minute), SlotID: slot.ID, Lines: priceViews(prices.Lines), Method: in.Method,
		SubtotalFen: prices.SubtotalFen, DeliveryFeeFen: prices.DeliveryFeeFen, TotalFen: prices.TotalFen,
	}
	err = tx.QueryRow(ctx, `INSERT INTO quotes(actor_id,station_id,mode,method,slot_id,address_snapshot,slot_snapshot,priced_snapshot,valid_until,created_at,merchant_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')) RETURNING id`,
		actor.AccountID, actor.StationID, in.Mode, in.Method, slot.ID, a, sl, p, q.ValidUntil, now, merchant).Scan(&q.ID)
	return q, err
}
func validatePrices(p contract.PricedLines) error {
	if len(p.Lines) == 0 || len(p.Lines) > 50 || p.SubtotalFen < 0 || p.TotalFen < 0 || p.TotalFen > maxFen || p.DeliveryFeeFen < 0 || p.SubtotalFen > maxFen-p.DeliveryFeeFen || p.TotalFen != p.SubtotalFen+p.DeliveryFeeFen {
		return ErrInvalid
	}
	var items int64
	var sum int64
	for _, l := range p.Lines {
		items += int64(l.Qty)
		if l.Qty < 1 || l.Qty > 999 || items > 999 || l.UnitPriceFen < 0 || l.UnitPriceFen > maxFen/int64(l.Qty) || l.LineTotalFen != l.UnitPriceFen*int64(l.Qty) || l.LineTotalFen > maxFen-sum {
			return ErrInvalid
		}
		sum += l.LineTotalFen
	}
	if sum != p.SubtotalFen {
		return ErrInvalid
	}
	return nil
}
func loadQuote(ctx context.Context, tx pgx.Tx, actor identity.Session, id string) (q quoteRow, err error) {
	var a, slot, p []byte
	q.ID = id
	err = tx.QueryRow(ctx, `SELECT actor_id,station_id,mode,method,address_snapshot,slot_snapshot,priced_snapshot,valid_until,COALESCE(merchant_id,'')
		FROM quotes WHERE id=$1 AND actor_id=$2 AND station_id=$3 FOR SHARE`, id, actor.AccountID, actor.StationID).
		Scan(&q.ActorID, &q.StationID, &q.Mode, &q.Method, &a, &slot, &p, &q.ValidUntil, &q.MerchantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, ErrNotFound
	}
	if err != nil {
		return q, err
	}
	for _, x := range []struct {
		data []byte
		dst  any
	}{{a, &q.Address}, {slot, &q.Slot}, {p, &q.Prices}} {
		if err = json.Unmarshal(x.data, x.dst); err != nil {
			return q, err
		}
	}
	return q, nil
}

const maxFen int64 = 1<<53 - 1

type OrderInput struct {
	QuoteID string  `json:"quote_id"`
	Note    *string `json:"note,omitempty"`
}

func (s *Service) Create(ctx context.Context, tx pgx.Tx, actor identity.Session, in OrderInput) (Order, error) {
	if !validID(in.QuoteID) || in.Note != nil && !validText(*in.Note, 200) {
		return Order{}, ErrInvalid
	}
	if !s.AcceptNewOrders {
		return Order{}, ErrNotReady
	}
	q, err := loadQuote(ctx, tx, actor, in.QuoteID)
	if err != nil {
		return Order{}, err
	}
	if err = s.Policy.Check(q.Method); err != nil {
		return Order{}, err
	}
	now := s.Clock.Now().UTC()
	if !now.Before(q.ValidUntil) {
		return Order{}, ErrQuoteChanged
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT id FROM orders WHERE quote_id=$1`, q.ID).Scan(&existing)
	if err == nil {
		return Order{}, ErrInvalidState
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Order{}, err
	}
	address, err := s.Identity.ResolveAddress(ctx, tx, actor.AccountID, q.Address.AddressID)
	if err != nil {
		return Order{}, ErrQuoteChanged
	}
	if address != q.Address {
		return Order{}, ErrQuoteChanged
	}
	inputs := make([]contract.OrderLineInput, len(q.Prices.Lines))
	var items int32
	for i, l := range q.Prices.Lines {
		inputs[i] = l.OrderLineInput
		items += l.Qty
	}
	merchandise, err := s.Catalog.Price(ctx, tx, inputs, now)
	if err != nil {
		return Order{}, err
	}
	merchant, err := checkoutMerchant(ctx, tx, actor.StationID, merchandise.Lines)
	if errors.Is(err, ErrNotFound) || errors.Is(err, errMixedMerchants) {
		return Order{}, ErrQuoteChanged
	}
	if err != nil {
		return Order{}, err
	}
	if merchant != q.MerchantID {
		return Order{}, ErrQuoteChanged
	}
	current, err := withDeliveryFee(ctx, tx, actor.StationID, merchandise)
	if errors.Is(err, ErrInvalid) {
		return Order{}, ErrQuoteChanged
	}
	if err != nil {
		return Order{}, err
	}
	if !reflect.DeepEqual(current, q.Prices) {
		return Order{}, ErrQuoteChanged
	}
	// An order exists before any foreign-key-bearing port write. Admission below uses only the original slot.
	a, _ := json.Marshal(q.Address)
	slot, _ := json.Marshal(q.Slot)
	status := contract.OrderStatusPreparing
	if q.Mode == contract.DeliveryModeScheduled {
		status = contract.OrderStatusScheduled
	}
	note := ""
	if in.Note != nil {
		note = *in.Note
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO orders(quote_id,actor_id,station_id,mode,method,slot_id,address_snapshot,slot_snapshot,total_fen,fee_fen,item_count,status,note,created_at,cod_commission_bps,merchant_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,(SELECT cod_commission_bps FROM stations WHERE id=$3),NULLIF($15,'')) RETURNING id`,
		q.ID, actor.AccountID, actor.StationID, q.Mode, q.Method, q.Slot.ID, a, slot, current.TotalFen, current.DeliveryFeeFen, items, status, note, now, merchant).Scan(&id)
	if err != nil {
		return Order{}, err
	}
	if err = s.Schedule.ReserveSlot(ctx, tx, q.Slot.ID, id, q.Mode, now); err != nil {
		return Order{}, err
	}
	if err = s.Catalog.Reserve(ctx, tx, id, merchandise); err != nil {
		return Order{}, err
	}
	for _, l := range current.Lines {
		_, err = tx.Exec(ctx, `INSERT INTO order_items(order_id,sku_id,inventory_id,qty,name,spec,units_per_pack,unit_price_fen,line_total_fen,price_version,promotion_id,discount_bps) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, l.SKUID, l.InventoryID, l.Qty, l.Name, l.Spec, l.UnitsPerPack, l.UnitPriceFen, l.LineTotalFen, l.PriceVersion, l.PromotionID, l.DiscountBPS)
		if err != nil {
			return Order{}, err
		}
	}
	if err = s.Settlement.CreateSettlement(ctx, tx, contract.OrderMoney{OrderID: id, ActorID: actor.AccountID, StationID: actor.StationID, PaymentMethod: q.Method, TotalFen: current.TotalFen}); err != nil {
		return Order{}, err
	}
	if err = s.event(ctx, tx, actor, id, "CREATED", ""); err != nil {
		return Order{}, err
	}
	if err = s.notify(ctx, tx, "ORDER_PLACED", orderRow{ID: id, ActorID: actor.AccountID, StationID: actor.StationID}); err != nil {
		return Order{}, err
	}
	if q.Mode == contract.DeliveryModeScheduled {
		if err = s.enqueue(ctx, tx, actor.StationID, id, "SCHEDULE_WAKE", q.Slot.DispatchAt); err != nil {
			return Order{}, err
		}
	}
	if err = s.enqueue(ctx, tx, actor.StationID, id, "ORDER_OVERDUE", q.Slot.EndsAt); err != nil {
		return Order{}, err
	}
	return s.Order(ctx, tx, actor, id, false)
}
