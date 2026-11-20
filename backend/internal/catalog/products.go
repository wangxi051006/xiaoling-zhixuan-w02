package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound = errors.New("catalog item not found")
	ErrVersion  = errors.New("catalog version conflict")
	reasonCode  = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)
)

type SKUInput struct {
	Label        string `json:"label"`
	UnitsPerPack int32  `json:"units_per_pack"`
	UnitPriceFen int64  `json:"unit_price_fen"`
}

type SKU struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	UnitsPerPack int32  `json:"units_per_pack"`
	UnitPriceFen int64  `json:"unit_price_fen"`
}

type Product struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url,omitempty"`
	SKUs        []SKU  `json:"skus"`
}

type Inventory struct {
	ID           string `json:"id"`
	OnHand       int64  `json:"on_hand"`
	Reserved     int64  `json:"reserved"`
	ReorderPoint int64  `json:"reorder_point"`
	Version      int64  `json:"version"`
}

func (i Inventory) LowStock() bool { return i.OnHand-i.Reserved <= i.ReorderPoint }

type AdminProduct struct {
	Product
	OnSale    bool       `json:"on_sale"`
	Version   int64      `json:"version"`
	Inventory Inventory  `json:"inventory"`
	Promotion *Promotion `json:"promotion"`
}

type ProductInput struct {
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	ImageURL     string     `json:"image_url,omitempty"`
	SKUs         []SKUInput `json:"skus"`
	OnSale       bool       `json:"on_sale"`
	ReorderPoint int64      `json:"reorder_point"`
}

type ProductUpdate struct {
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
	ImageURL        *string `json:"image_url,omitempty"`
	OnSale          *bool   `json:"on_sale,omitempty"`
	ReorderPoint    *int64  `json:"reorder_point,omitempty"`
	ExpectedVersion int64   `json:"expected_version"`
	SKUs            *[]SKU  `json:"skus,omitempty"`
}

type Adjustment struct {
	InventoryID     string `json:"inventory_id"`
	Delta           int64  `json:"delta"`
	ExpectedVersion int64  `json:"expected_version"`
	ReasonCode      string `json:"reason_code"`
}

type reader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

const productSelect = `SELECT p.id,p.name,p.description,p.image_url,p.on_sale,p.version,
	i.id,i.on_hand,i.reserved,i.reorder_point,i.version,
	CASE WHEN p.promotion_id IS NOT NULL THEN jsonb_build_object('starts_at',p.promotion_starts_at,
	'ends_at',p.promotion_ends_at,'tiers',p.promotion_tiers) END,
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id',s.id,'label',s.label,'units_per_pack',s.units_per_pack,
	'unit_price_fen',s.unit_price_fen) ORDER BY s.units_per_pack,s.id) FROM skus s WHERE s.product_id=p.id AND s.active),'[]'::jsonb)
	FROM products p JOIN inventory i ON i.product_id=p.id`

func scanProduct(row pgx.Row) (AdminProduct, error) {
	var p AdminProduct
	i := &p.Inventory
	var promotion, skus []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.ImageURL, &p.OnSale, &p.Version, &i.ID, &i.OnHand, &i.Reserved, &i.ReorderPoint, &i.Version, &promotion, &skus)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(skus, &p.SKUs); err != nil {
		return p, err
	}
	if promotion != nil {
		err = json.Unmarshal(promotion, &p.Promotion)
	}
	return p, err
}

func (Store) ListProducts(ctx context.Context, db reader, station, cursor, query, category string, limit int, public bool) ([]AdminProduct, error) {
	rows, err := db.Query(ctx, productSelect+` WHERE (p.station_id=$1 OR ($5::boolean AND $1='')) AND p.id COLLATE "C">$2
	AND (NOT $5::boolean OR p.on_sale)
	AND ($3='' OR strpos(lower(p.name),lower($3))>0 OR strpos(lower(p.description),lower($3))>0)
	AND ($6='' OR p.category=$6) ORDER BY p.id COLLATE "C" LIMIT $4`, station, cursor, query, limit, public, category)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AdminProduct, error) { return scanProduct(row) })
}

func (Store) GetProduct(ctx context.Context, db reader, station, id string, public bool) (AdminProduct, error) {
	return scanProduct(db.QueryRow(ctx, productSelect+` WHERE (p.station_id=$1 OR ($3::boolean AND $1='')) AND p.id=$2 AND (NOT $3::boolean OR p.on_sale)`, station, id, public))
}

func validText(v string, min, max int) bool {
	n := utf8.RuneCountInString(v)
	return utf8.ValidString(v) && n >= min && n <= max && !strings.ContainsRune(v, 0) && (min == 0 || strings.TrimSpace(v) != "")
}

func validAmount(v int64) bool  { return v >= 0 && v <= maxFen }
func validVersion(v int64) bool { return v >= 1 && v <= maxFen }

func validSKU(label string, units int32, price int64) bool {
	return validText(label, 1, 60) && (units == 1 || units == 3) && validAmount(price)
}

func (s Store) CreateProduct(ctx context.Context, tx pgx.Tx, station string, in ProductInput) (AdminProduct, error) {
	if !validText(in.Name, 1, 200) || !validText(in.Description, 0, 2000) || !validAmount(in.ReorderPoint) || len(in.SKUs) < 1 || len(in.SKUs) > 8 {
		return AdminProduct{}, ErrInvalid
	}
	for _, sku := range in.SKUs {
		if !validSKU(sku.Label, sku.UnitsPerPack, sku.UnitPriceFen) {
			return AdminProduct{}, ErrInvalid
		}
	}
	id := randomID()
	if _, err := tx.Exec(ctx, `INSERT INTO products(id,station_id,name,description,on_sale,image_url) VALUES($1,$2,$3,$4,$5,$6)`, id, station, in.Name, in.Description, in.OnSale, in.ImageURL); err != nil {
		return AdminProduct{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO inventory(id,product_id,reorder_point) VALUES($1,$2,$3)`, randomID(), id, in.ReorderPoint); err != nil {
		return AdminProduct{}, err
	}
	for _, sku := range in.SKUs {
		if _, err := tx.Exec(ctx, `INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen) VALUES($1,$2,$3,$4,$5)`, randomID(), id, sku.Label, sku.UnitsPerPack, sku.UnitPriceFen); err != nil {
			return AdminProduct{}, err
		}
	}
	return s.GetProduct(ctx, tx, station, id, false)
}

// Catalog mutations and reservations serialize on the physical inventory row.
func lockProduct(ctx context.Context, tx pgx.Tx, station, id string) (string, int64, error) {
	var inventoryID string
	var version int64
	err := tx.QueryRow(ctx, `SELECT i.id FROM inventory i JOIN products p ON p.id=i.product_id WHERE p.id=$1 AND p.station_id=$2 FOR UPDATE OF i`, id, station).Scan(&inventoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, err
	}
	// Read the version in a fresh statement after waiting for the inventory lock.
	err = tx.QueryRow(ctx, `SELECT version FROM products WHERE id=$1`, id).Scan(&version)
	return inventoryID, version, err
}

func (s Store) UpdateProduct(ctx context.Context, tx pgx.Tx, station, id string, in ProductUpdate) (AdminProduct, error) {
	if !validVersion(in.ExpectedVersion) || in.Name != nil && !validText(*in.Name, 1, 200) || in.Description != nil && !validText(*in.Description, 0, 2000) || in.ReorderPoint != nil && !validAmount(*in.ReorderPoint) {
		return AdminProduct{}, ErrInvalid
	}
	var ids []string
	if in.SKUs != nil {
		if len(*in.SKUs) > 8 {
			return AdminProduct{}, ErrInvalid
		}
		ids = make([]string, 0, len(*in.SKUs))
		seen := map[string]bool{}
		for _, sku := range *in.SKUs {
			if !validSKU(sku.Label, sku.UnitsPerPack, sku.UnitPriceFen) || !validText(sku.ID, 1, 128) || seen[sku.ID] {
				return AdminProduct{}, ErrInvalid
			}
			ids = append(ids, sku.ID)
			seen[sku.ID] = true
		}
	}
	inventoryID, version, err := lockProduct(ctx, tx, station, id)
	if err != nil {
		return AdminProduct{}, err
	}
	if version != in.ExpectedVersion || version == maxFen {
		return AdminProduct{}, ErrVersion
	}
	if in.SKUs != nil {
		var own int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM skus WHERE product_id=$1 AND id=ANY($2)`, id, ids).Scan(&own); err != nil {
			return AdminProduct{}, err
		}
		if own != len(ids) {
			return AdminProduct{}, ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE skus SET active=false WHERE product_id=$1 AND active AND NOT (id=ANY($2))`, id, ids); err != nil {
			return AdminProduct{}, err
		}
		for _, sku := range *in.SKUs {
			if _, err := tx.Exec(ctx, `UPDATE skus SET label=$3,units_per_pack=$4,unit_price_fen=$5,active=true WHERE product_id=$1 AND id=$2`, id, sku.ID, sku.Label, sku.UnitsPerPack, sku.UnitPriceFen); err != nil {
				return AdminProduct{}, err
			}
		}
	}
	if in.ReorderPoint != nil {
		if _, err := tx.Exec(ctx, `UPDATE inventory SET reorder_point=$2,version=version+1 WHERE id=$1 AND reorder_point<>$2`, inventoryID, *in.ReorderPoint); err != nil {
			return AdminProduct{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET name=COALESCE($2,name),description=COALESCE($3,description),on_sale=COALESCE($4,on_sale),image_url=COALESCE($5,image_url),version=version+1 WHERE id=$1`, id, in.Name, in.Description, in.OnSale, in.ImageURL); err != nil {
		return AdminProduct{}, err
	}
	return s.GetProduct(ctx, tx, station, id, false)
}

func (s Store) UpdatePromotion(ctx context.Context, tx pgx.Tx, station, id string, expected int64, promotion *Promotion) (AdminProduct, error) {
	if !validVersion(expected) {
		return AdminProduct{}, ErrInvalid
	}
	if promotion != nil {
		if promotion.StartsAt.IsZero() || !promotion.EndsAt.Truncate(time.Microsecond).After(promotion.StartsAt.Truncate(time.Microsecond)) || len(promotion.Tiers) > 20 || promotion.Tiers == nil {
			return AdminProduct{}, ErrInvalid
		}
		seen := map[int32]bool{}
		for _, tier := range promotion.Tiers {
			if tier.MinQty < 1 || tier.MinQty > 999 || tier.DiscountBPS < 0 || tier.DiscountBPS > 10000 || seen[tier.MinQty] {
				return AdminProduct{}, ErrInvalid
			}
			seen[tier.MinQty] = true
		}
	}
	_, version, err := lockProduct(ctx, tx, station, id)
	if err != nil {
		return AdminProduct{}, err
	}
	if version != expected || version == maxFen {
		return AdminProduct{}, ErrVersion
	}
	if promotion == nil {
		_, err = tx.Exec(ctx, `UPDATE products SET promotion_id=NULL,promotion_starts_at=NULL,promotion_ends_at=NULL,promotion_tiers=NULL,version=version+1 WHERE id=$1`, id)
	} else {
		_, err = tx.Exec(ctx, `UPDATE products SET promotion_id=$2,promotion_starts_at=$3,promotion_ends_at=$4,promotion_tiers=$5,version=version+1 WHERE id=$1`, id, randomID(), promotion.StartsAt, promotion.EndsAt, promotion.Tiers)
	}
	if err != nil {
		return AdminProduct{}, err
	}
	return s.GetProduct(ctx, tx, station, id, false)
}

func (Store) Adjust(ctx context.Context, tx pgx.Tx, station, actor string, in Adjustment) (Inventory, error) {
	var i Inventory
	if !validText(in.InventoryID, 1, 128) || !validText(actor, 1, 128) || !validVersion(in.ExpectedVersion) || in.Delta == 0 || in.Delta < -999999 || in.Delta > 999999 || !reasonCode.MatchString(in.ReasonCode) {
		return i, ErrInvalid
	}
	err := tx.QueryRow(ctx, `SELECT i.id,i.on_hand,i.reserved,i.reorder_point,i.version FROM inventory i JOIN products p ON p.id=i.product_id WHERE i.id=$1 AND p.station_id=$2 FOR UPDATE OF i`, in.InventoryID, station).Scan(&i.ID, &i.OnHand, &i.Reserved, &i.ReorderPoint, &i.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	if i.Version != in.ExpectedVersion || i.Version == maxFen {
		return i, ErrVersion
	}
	if i.OnHand+in.Delta < i.Reserved {
		return i, ErrStock
	}
	if !validAmount(i.OnHand + in.Delta) {
		return i, ErrInvalid
	}
	i.OnHand += in.Delta
	i.Version++
	if _, err := tx.Exec(ctx, `UPDATE inventory SET on_hand=$2,version=$3 WHERE id=$1`, i.ID, i.OnHand, i.Version); err != nil {
		return Inventory{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO inventory_movements(inventory_id,kind,hand_delta,reserved_delta,inventory_version,actor_id,reason_code) VALUES($1,'ADJUSTED',$2,0,$3,$4,$5)`, i.ID, in.Delta, i.Version, actor, in.ReasonCode)
	return i, err
}
