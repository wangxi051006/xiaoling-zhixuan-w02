package catalog

import (
	"context"
	"net/http"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

// CreateMerchantProduct 创建属于指定店铺的商品，校验规则与平台后台完全一致，
// 只是额外把 merchant_id 固定为调用者自己的店铺，避免商家跨店写数据。
func (s Store) CreateMerchantProduct(ctx context.Context, tx pgx.Tx, station, merchant string, in ProductInput) (AdminProduct, error) {
	if !validText(merchant, 1, 128) {
		return AdminProduct{}, ErrInvalid
	}
	if !validText(in.Name, 1, 200) || !validText(in.Description, 0, 2000) || !validAmount(in.ReorderPoint) || len(in.SKUs) < 1 || len(in.SKUs) > 8 {
		return AdminProduct{}, ErrInvalid
	}
	for _, sku := range in.SKUs {
		if !validSKU(sku.Label, sku.UnitsPerPack, sku.UnitPriceFen) {
			return AdminProduct{}, ErrInvalid
		}
	}
	id := randomID()
	if _, err := tx.Exec(ctx, `INSERT INTO products(id,station_id,name,description,on_sale,image_url,merchant_id) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		id, station, in.Name, in.Description, in.OnSale, in.ImageURL, merchant); err != nil {
		return AdminProduct{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO inventory(id,product_id,reorder_point) VALUES($1,$2,$3)`, randomID(), id, in.ReorderPoint); err != nil {
		return AdminProduct{}, err
	}
	for _, sku := range in.SKUs {
		if _, err := tx.Exec(ctx, `INSERT INTO skus(id,product_id,label,units_per_pack,unit_price_fen) VALUES($1,$2,$3,$4,$5)`,
			randomID(), id, sku.Label, sku.UnitsPerPack, sku.UnitPriceFen); err != nil {
			return AdminProduct{}, err
		}
	}
	return s.GetProduct(ctx, tx, station, id, false)
}

// AdjustForMerchant 只允许调整本店商品对应的库存；别人的库存一律按不存在处理。
func (s Store) AdjustForMerchant(ctx context.Context, tx pgx.Tx, station, actor, merchant string, in Adjustment) (Inventory, error) {
	if !validText(merchant, 1, 128) {
		return Inventory{}, ErrInvalid
	}
	var owned bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inventory i JOIN products p ON p.id=i.product_id
		WHERE i.id=$1 AND p.station_id=$2 AND p.merchant_id=$3)`, in.InventoryID, station, merchant).Scan(&owned)
	if err != nil {
		return Inventory{}, err
	}
	if !owned {
		return Inventory{}, ErrNotFound
	}
	return s.Adjust(ctx, tx, station, actor, in)
}

// UpdateMerchantProduct 只允许修改本店商品，校验、版本冲突与审计和平台后台一致。
func (s Store) UpdateMerchantProduct(ctx context.Context, tx pgx.Tx, station, merchant, id string, in ProductUpdate) (AdminProduct, error) {
	if !validText(merchant, 1, 128) || !validText(id, 1, 128) {
		return AdminProduct{}, ErrInvalid
	}
	var owned bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE id=$1 AND station_id=$2 AND merchant_id=$3)`,
		id, station, merchant).Scan(&owned)
	if err != nil {
		return AdminProduct{}, err
	}
	if !owned {
		return AdminProduct{}, ErrNotFound
	}
	return s.UpdateProduct(ctx, tx, station, id, in)
}

// merchant 是商家入口的鉴权包装：必须来自 merchant 入口且具备 MERCHANT 角色。
func (h *HTTP) merchant(next func(http.ResponseWriter, *http.Request, identity.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Authorize == nil || h.Pool == nil {
			platform.WriteError(w, r, 503, "NOT_READY", "Service not ready")
			return
		}
		if s, ok := h.Authorize(w, r, identity.MerchantEntry, false, contract.RoleMerchant); ok {
			next(w, r, s)
		}
	}
}

// createMerchant 对应 POST /api/v1/merchant/products。
func (h *HTTP) createMerchant(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		ImageURL    string  `json:"image_url"`
		OnSale      *bool   `json:"on_sale"`
		Reorder     *int64  `json:"reorder_point"`
		SKUs        []struct {
			Label *string `json:"label"`
			Units *int32  `json:"units_per_pack"`
			Price *int64  `json:"unit_price_fen"`
		} `json:"skus"`
	}
	if decodeCatalog(r, &body) != nil || body.Name == nil || body.Description == nil || body.OnSale == nil || body.Reorder == nil || body.SKUs == nil {
		catalogError(w, r, ErrInvalid)
		return
	}
	in := ProductInput{Name: *body.Name, Description: *body.Description, ImageURL: body.ImageURL, OnSale: *body.OnSale, ReorderPoint: *body.Reorder, SKUs: make([]SKUInput, 0, len(body.SKUs))}
	for _, item := range body.SKUs {
		if item.Label == nil || item.Units == nil || item.Price == nil {
			catalogError(w, r, ErrInvalid)
			return
		}
		in.SKUs = append(in.SKUs, SKUInput{*item.Label, *item.Units, *item.Price})
	}
	h.keyed(w, r, s, "createMerchantProduct", in, func(ctx context.Context, tx pgx.Tx) (any, error) {
		if err := h.checkProductImage(s.StationID, in.ImageURL); err != nil {
			return nil, err
		}
		merchant, err := identity.MerchantID(ctx, tx, s)
		if err != nil {
			return nil, err
		}
		return (Store{}).CreateMerchantProduct(ctx, tx, s.StationID, merchant, in)
	})
}

// adjustMerchant 对应 POST /api/v1/merchant/inventory/adjustments。
func (h *HTTP) adjustMerchant(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var in Adjustment
	if decodeCatalog(r, &in) != nil {
		catalogError(w, r, ErrInvalid)
		return
	}
	h.keyed(w, r, s, "adjustMerchantInventory", in, func(ctx context.Context, tx pgx.Tx) (any, error) {
		merchant, err := identity.MerchantID(ctx, tx, s)
		if err != nil {
			return nil, err
		}
		return (Store{}).AdjustForMerchant(ctx, tx, s.StationID, s.AccountID, merchant, in)
	})
}

// updateMerchant 对应 PATCH /api/v1/merchant/products/{id}：改名、改描述、上下架、
// 改补货点、改本店 SKU 价格。图片走平台后台的上传接口，这里强制忽略 image_url。
func (h *HTTP) updateMerchant(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var in ProductUpdate
	id := r.PathValue("id")
	if !validText(id, 1, 128) || decodeCatalog(r, &in) != nil {
		catalogError(w, r, ErrInvalid)
		return
	}
	in.ImageURL = nil
	h.writeProduct(w, r, s, "updateMerchantProduct", func(tx pgx.Tx) (AdminProduct, error) {
		merchant, err := identity.MerchantID(r.Context(), tx, s)
		if err != nil {
			return AdminProduct{}, err
		}
		return (Store{}).UpdateMerchantProduct(r.Context(), tx, s.StationID, merchant, id, in)
	})
}
