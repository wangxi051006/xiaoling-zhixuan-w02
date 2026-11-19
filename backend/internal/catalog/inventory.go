package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

// Clock is shared with the caller's pricing clock. Nil uses the system clock.
// Port errors require rolling back the caller's entire transaction.
type Store struct{ Clock contract.Clock }

var _ contract.CatalogPort = Store{}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (Store) Price(ctx context.Context, tx pgx.Tx, lines []contract.OrderLineInput, at time.Time) (contract.PricedLines, error) {
	if len(lines) == 0 || len(lines) > 50 {
		return contract.PricedLines{}, ErrInvalid
	}
	ids := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		if seen[line.SKUID] || line.SKUID == "" || line.Qty < 1 || line.Qty > 999 {
			return contract.PricedLines{}, ErrInvalid
		}
		seen[line.SKUID] = true
		ids = append(ids, line.SKUID)
	}
	rows, err := tx.Query(ctx, `SELECT s.id, p.id, i.id, p.name, s.label, p.version, s.unit_price_fen,
		s.units_per_pack, (s.active AND p.on_sale), p.promotion_id, p.promotion_starts_at,
		p.promotion_ends_at, p.promotion_tiers
		FROM skus s JOIN products p ON p.id=s.product_id JOIN inventory i ON i.product_id=p.id
		WHERE s.id=ANY($1)`, ids)
	if err != nil {
		return contract.PricedLines{}, err
	}
	defer rows.Close()
	byID := make(map[string]pricingSKU, len(lines))
	for rows.Next() {
		var id string
		var promoID *string
		var starts, ends *time.Time
		var tiers []byte
		var sku pricingSKU
		if err := rows.Scan(&id, &sku.productID, &sku.inventoryID, &sku.name, &sku.label, &sku.version,
			&sku.price, &sku.units, &sku.onSale, &promoID, &starts, &ends, &tiers); err != nil {
			return contract.PricedLines{}, err
		}
		if promoID != nil {
			p := &Promotion{ID: *promoID, StartsAt: *starts, EndsAt: *ends}
			if err := json.Unmarshal(tiers, &p.Tiers); err != nil {
				return contract.PricedLines{}, err
			}
			sku.promotion = p
		}
		byID[id] = sku
	}
	if err := rows.Err(); err != nil {
		return contract.PricedLines{}, err
	}
	q := contract.PricedLines{Lines: make([]contract.PricedLine, 0, len(lines))}
	for _, line := range lines {
		priced, err := priceLine(line, byID[line.SKUID], at)
		if err != nil {
			return contract.PricedLines{}, err
		}
		if priced.LineTotalFen > maxFen-q.SubtotalFen {
			return contract.PricedLines{}, ErrInvalid
		}
		q.SubtotalFen += priced.LineTotalFen
		q.Lines = append(q.Lines, priced)
	}
	q.TotalFen = q.SubtotalFen
	return q, nil
}

type reservation struct {
	id, state string
	units     int32
	final     *int32
	digest    []byte
}

// The caller already owns the order transaction; serialize catalog operations for the same order.
func lockOrderStock(ctx context.Context, tx pgx.Tx, orderID string) ([]reservation, error) {
	if orderID == "" || len(orderID) > 128 {
		return nil, ErrInvalid
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('xiaoling:stock:' || current_schema() || ':' || $1, 0))`, orderID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT inventory_id, state, units, final_units, request_digest FROM stock_reservations WHERE order_id=$1`, orderID)
	if err != nil {
		return nil, err
	}
	res, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (reservation, error) {
		var r reservation
		err := row.Scan(&r.id, &r.state, &r.units, &r.final, &r.digest)
		return r, err
	})
	slices.SortFunc(res, func(a, b reservation) int {
		if a.id < b.id {
			return -1
		}
		if a.id > b.id {
			return 1
		}
		return 0
	})
	return res, err
}

func lockInventory(ctx context.Context, tx pgx.Tx, ids []string) error {
	// Use the same bytewise ordering in every multi-item command.
	slices.Sort(ids)
	for _, id := range ids {
		var found string
		if err := tx.QueryRow(ctx, `SELECT id FROM inventory WHERE id=$1 FOR UPDATE`, id).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrUnavailable
			}
			return err
		}
	}
	return nil
}

func (s Store) Reserve(ctx context.Context, tx pgx.Tx, orderID string, quote contract.PricedLines) error {
	if len(quote.Lines) == 0 || len(quote.Lines) > 50 {
		return ErrInvalid
	}
	units := map[string]int32{}
	seen := map[string]bool{}
	ids := make([]string, 0, len(quote.Lines))
	for _, line := range quote.Lines {
		if line.SKUID == "" || line.InventoryID == "" || seen[line.SKUID] || line.Qty < 1 || line.Qty > 999 ||
			(line.UnitsPerPack != 1 && line.UnitsPerPack != 3) {
			return ErrInvalid
		}
		seen[line.SKUID] = true
		if units[line.InventoryID] == 0 {
			ids = append(ids, line.InventoryID)
		}
		units[line.InventoryID] += line.Qty * line.UnitsPerPack
	}
	canonical := quote
	canonical.Lines = slices.Clone(quote.Lines)
	slices.SortFunc(canonical.Lines, func(a, b contract.PricedLine) int { return strings.Compare(a.SKUID, b.SKUID) })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ErrInvalid
	}
	digest := sha256.Sum256(encoded)
	prior, err := lockOrderStock(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if len(prior) != 0 {
		if len(prior) != len(units) {
			return ErrStockState
		}
		for _, r := range prior {
			if r.state != "RESERVED" || units[r.id] != r.units || !bytes.Equal(r.digest, digest[:]) {
				return ErrStockState
			}
		}
		return nil
	}
	if err := lockInventory(ctx, tx, ids); err != nil {
		return err
	}
	input := make([]contract.OrderLineInput, len(quote.Lines))
	for i, line := range quote.Lines {
		input[i] = line.OrderLineInput
	}
	clock := s.Clock
	if clock == nil {
		clock = contract.SystemClock{}
	}
	current, err := s.Price(ctx, tx, input, clock.Now())
	if err != nil {
		return err
	}
	if current.SubtotalFen != quote.SubtotalFen || current.TotalFen != quote.TotalFen || current.DeliveryFeeFen != quote.DeliveryFeeFen || !slices.Equal(current.Lines, quote.Lines) {
		return ErrPriceChanged
	}
	for _, id := range ids {
		var available int64
		if err := tx.QueryRow(ctx, `SELECT on_hand-reserved FROM inventory WHERE id=$1`, id).Scan(&available); err != nil {
			return err
		}
		if available < int64(units[id]) {
			return ErrStock
		}
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO stock_reservations(order_id,inventory_id,units,request_digest) VALUES($1,$2,$3,$4)`, orderID, id, units[id], digest[:]); err != nil {
			return err
		}
		if err := moveStock(ctx, tx, orderID, id, "RESERVED", 0, units[id]); err != nil {
			return err
		}
	}
	return nil
}

func moveStock(ctx context.Context, tx pgx.Tx, orderID, id, kind string, hand, reserved int32) error {
	var version int64
	err := tx.QueryRow(ctx, `UPDATE inventory SET on_hand=on_hand+$2,reserved=reserved+$3,version=version+1
		WHERE id=$1 AND on_hand+$2 BETWEEN 0 AND 9007199254740991 AND reserved+$3 BETWEEN 0 AND on_hand+$2
		RETURNING version`, id, hand, reserved).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStock
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO inventory_movements(inventory_id,order_id,kind,hand_delta,reserved_delta,inventory_version)
		VALUES($1,$2,$3,$4,$5,$6)`, id, orderID, kind, hand, reserved, version)
	return err
}

func (Store) Consume(ctx context.Context, tx pgx.Tx, orderID string) error {
	return finalizeStock(ctx, tx, orderID, "RESERVED", "CONSUMED", nil)
}

func (Store) Release(ctx context.Context, tx pgx.Tx, orderID string) error {
	return finalizeStock(ctx, tx, orderID, "RESERVED", "RELEASED", nil)
}

func (Store) Restock(ctx context.Context, tx pgx.Tx, orderID string, lines []contract.RestockLine) error {
	units := make(map[string]int32, len(lines))
	for _, line := range lines {
		if line.InventoryID == "" || line.SellableUnits <= 0 || units[line.InventoryID] != 0 {
			return ErrInvalid
		}
		units[line.InventoryID] = line.SellableUnits
	}
	return finalizeStock(ctx, tx, orderID, "CONSUMED", "RETURNED", units)
}

func (Store) RestockCanceled(ctx context.Context, tx pgx.Tx, orderID string, lines []contract.CanceledStockLine) error {
	units := make(map[string]int32, len(lines))
	for _, line := range lines {
		if line.InventoryID == "" || line.DamagedUnits <= 0 || units[line.InventoryID] != 0 {
			return ErrInvalid
		}
		units[line.InventoryID] = line.DamagedUnits
	}
	return finalizeStock(ctx, tx, orderID, "RESERVED", "CANCELED_RESTOCKED", units)
}

func finalizeStock(ctx context.Context, tx pgx.Tx, orderID, from, to string, units map[string]int32) error {
	rows, err := lockOrderStock(ctx, tx, orderID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrStockState
	}
	ids := make([]string, 0, len(rows))
	matched := 0
	for _, r := range rows {
		n, specified := units[r.id]
		if specified {
			matched++
		}
		if n > r.units {
			return ErrStockState
		}
		switch r.state {
		case to:
			if units != nil && (r.final == nil || *r.final != n) {
				return ErrStockState
			}
		case from:
			ids = append(ids, r.id)
		default:
			return ErrStockState
		}
	}
	if matched != len(units) || (len(ids) != 0 && len(ids) != len(rows)) {
		return ErrStockState
	}
	if len(ids) == 0 {
		return nil
	}
	if err := lockInventory(ctx, tx, ids); err != nil {
		return err
	}
	for _, r := range rows {
		var hand, reserved int32
		var final *int32
		n := units[r.id]
		switch to {
		case "CONSUMED":
			hand, reserved = -r.units, -r.units
		case "RELEASED":
			reserved = -r.units
		case "RETURNED":
			hand, final = n, &n
		case "CANCELED_RESTOCKED":
			hand, reserved, final = -n, -r.units, &n
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_reservations SET state=$3,final_units=$4 WHERE order_id=$1 AND inventory_id=$2`, orderID, r.id, to, final); err != nil {
			return err
		}
		if err := moveStock(ctx, tx, orderID, r.id, to, hand, reserved); err != nil {
			return err
		}
	}
	return nil
}
