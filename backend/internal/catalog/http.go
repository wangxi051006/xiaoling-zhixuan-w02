package catalog

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
	"reflect"
	"strconv"
	"strings"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HTTP struct {
	Pool      *pgxpool.Pool
	ImageDir  string
	Authorize func(http.ResponseWriter, *http.Request, identity.Entry, bool, ...contract.Role) (identity.Session, bool)
}

func (h *HTTP) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/catalog/products", h.available(h.listPublic))
	mux.HandleFunc("GET /api/v1/catalog/products/{id}", h.available(h.getPublic))
	mux.HandleFunc("GET "+imageURLPrefix+"{id}", h.getProductImage)
	mux.HandleFunc(ImageUploadPattern, h.operator(h.uploadProductImage))
	mux.HandleFunc("GET /api/v1/admin/products", h.operator(h.listAdmin))
	mux.HandleFunc("POST /api/v1/admin/products", h.operator(h.create))
	mux.HandleFunc("PATCH /api/v1/admin/products/{id}", h.operator(h.update))
	mux.HandleFunc("PUT /api/v1/admin/products/{id}/promotion", h.operator(h.promotion))
	mux.HandleFunc("POST /api/v1/admin/inventory/adjustments", h.operator(h.adjust))
}

func (h *HTTP) available(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Pool == nil {
			platform.WriteError(w, r, 503, "NOT_READY", "Service not ready")
			return
		}
		next(w, r)
	}
}

func (h *HTTP) operator(next func(http.ResponseWriter, *http.Request, identity.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Authorize == nil || h.Pool == nil {
			platform.WriteError(w, r, 503, "NOT_READY", "Service not ready")
			return
		}
		if s, ok := h.Authorize(w, r, identity.AdminEntry, false, contract.RoleOperator); ok {
			next(w, r, s)
		}
	}
}

var errIdempotency = errors.New("catalog idempotency conflict")

func catalogError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := 500, "INTERNAL_ERROR"
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		status, code = 413, "BODY_TOO_LARGE"
	case errors.Is(err, ErrInvalid):
		status, code = 400, "BAD_REQUEST"
	case errors.Is(err, ErrNotFound):
		status, code = 404, "NOT_FOUND"
	case errors.Is(err, ErrVersion):
		status, code = 409, "INVALID_STATE"
	case errors.Is(err, ErrStock):
		status, code = 409, "OUT_OF_STOCK"
	case errors.Is(err, errIdempotency):
		status, code = 409, "IDEMPOTENCY_CONFLICT"
	}
	platform.WriteError(w, r, status, code, http.StatusText(status))
}

func catalogJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type catalogPage[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next_cursor"`
}

func listQuery(raw string, public bool) (cursor, q, category string, limit int, err error) {
	params, e := url.ParseQuery(raw)
	if e != nil {
		return "", "", "", 0, ErrInvalid
	}
	limit = 20
	for k, values := range params {
		if len(values) != 1 || !validText(values[0], 1, 200) {
			return "", "", "", 0, ErrInvalid
		}
		switch k {
		case "cursor":
			if !validText(values[0], 1, 128) {
				return "", "", "", 0, ErrInvalid
			}
			cursor = values[0]
		case "limit":
			limit, e = strconv.Atoi(values[0])
			if e != nil || limit < 1 || limit > 100 {
				return "", "", "", 0, ErrInvalid
			}
		case "q":
			if !public || !validText(values[0], 1, 80) {
				return "", "", "", 0, ErrInvalid
			}
			q = values[0]
		case "category":
			if !public || !validText(values[0], 1, 80) {
				return "", "", "", 0, ErrInvalid
			}
			category = values[0]
		default:
			return "", "", "", 0, ErrInvalid
		}
	}
	return
}

func (h *HTTP) listPublic(w http.ResponseWriter, r *http.Request) { h.list(w, r, "", true) }
func (h *HTTP) listAdmin(w http.ResponseWriter, r *http.Request, s identity.Session) {
	h.list(w, r, s.StationID, false)
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request, station string, public bool) {
	cursor, q, category, limit, err := listQuery(r.URL.RawQuery, public)
	if err != nil {
		catalogError(w, r, err)
		return
	}
	items, err := (Store{}).ListProducts(r.Context(), h.Pool, station, cursor, q, category, limit+1, public)
	if err != nil {
		catalogError(w, r, err)
		return
	}
	var next *string
	if len(items) > limit {
		id := items[limit-1].ID
		next = &id
		items = items[:limit]
	}
	if public {
		products := make([]Product, 0, len(items))
		for _, p := range items {
			products = append(products, p.Product)
		}
		catalogJSON(w, 200, catalogPage[Product]{products, next})
		return
	}
	if items == nil {
		items = []AdminProduct{}
	}
	catalogJSON(w, 200, catalogPage[AdminProduct]{items, next})
}

func (h *HTTP) getPublic(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validText(id, 1, 128) {
		catalogError(w, r, ErrInvalid)
		return
	}
	p, err := (Store{}).GetProduct(r.Context(), h.Pool, "", id, true)
	if err != nil {
		catalogError(w, r, err)
		return
	}
	catalogJSON(w, 200, p.Product)
}

func decodeCatalog(r *http.Request, v any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return ErrInvalid
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return ErrInvalid
	}
	shape := json.NewDecoder(bytes.NewReader(data))
	if err := catalogShape(shape, reflect.TypeOf(v).Elem(), false); err != nil {
		return ErrInvalid
	}
	if _, err := shape.Token(); err != io.EOF {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

// Validate exact JSON member names, duplicate keys and nullability before Go's
// decoder can normalize case or treat null as an omitted optional field.
func catalogShape(d *json.Decoder, t reflect.Type, nullable bool) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if nullable {
			return nil
		}
		return ErrInvalid
	}
	if t == reflect.TypeFor[time.Time]() {
		if _, ok := token.(string); !ok {
			return ErrInvalid
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			return ErrInvalid
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = f.Type
			}
		}
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return e
			}
			name, ok := key.(string)
			field, found := fields[name]
			if !ok || !found || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			if field == reflect.TypeFor[json.RawMessage]() && name == "promotion" {
				field = reflect.TypeFor[Promotion]()
			}
			if e = catalogShape(d, field, name == "promotion"); e != nil {
				return e
			}
		}
		_, err = d.Token()
		return err
	case reflect.Slice:
		if token != json.Delim('[') {
			return ErrInvalid
		}
		for d.More() {
			if err = catalogShape(d, t.Elem(), false); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	default:
		if _, ok := token.(json.Delim); ok {
			return ErrInvalid
		}
		return nil
	}
}

// Pointers distinguish required fields from present zero values.
type skuBody struct {
	ID    *string `json:"id,omitempty"`
	Label *string `json:"label"`
	Units *int32  `json:"units_per_pack"`
	Price *int64  `json:"unit_price_fen"`
}

func (s skuBody) values() (SKUInput, bool) {
	if s.Label == nil || s.Units == nil || s.Price == nil {
		return SKUInput{}, false
	}
	return SKUInput{*s.Label, *s.Units, *s.Price}, true
}

func (h *HTTP) create(w http.ResponseWriter, r *http.Request, s identity.Session) {
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
	h.keyed(w, r, s, "createProduct", in, func(ctx context.Context, tx pgx.Tx) (any, error) {
		if err := h.checkProductImage(s.StationID, in.ImageURL); err != nil {
			return nil, err
		}
		return (Store{}).CreateProduct(ctx, tx, s.StationID, in)
	})
}

func (h *HTTP) update(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var body struct {
		Name        *string    `json:"name"`
		Description *string    `json:"description"`
		ImageURL    *string    `json:"image_url"`
		OnSale      *bool      `json:"on_sale"`
		Reorder     *int64     `json:"reorder_point"`
		Expected    int64      `json:"expected_version"`
		SKUs        *[]skuBody `json:"skus"`
	}
	id := r.PathValue("id")
	if !validText(id, 1, 128) || decodeCatalog(r, &body) != nil {
		catalogError(w, r, ErrInvalid)
		return
	}
	in := ProductUpdate{Name: body.Name, Description: body.Description, ImageURL: body.ImageURL, OnSale: body.OnSale, ReorderPoint: body.Reorder, ExpectedVersion: body.Expected}
	if body.SKUs != nil {
		items := make([]SKU, 0, len(*body.SKUs))
		for _, item := range *body.SKUs {
			v, ok := item.values()
			if !ok || item.ID == nil {
				catalogError(w, r, ErrInvalid)
				return
			}
			items = append(items, SKU{*item.ID, v.Label, v.UnitsPerPack, v.UnitPriceFen})
		}
		in.SKUs = &items
	}
	if in.ImageURL != nil {
		if err := h.checkProductImage(s.StationID, *in.ImageURL); err != nil {
			catalogError(w, r, err)
			return
		}
	}
	h.writeProduct(w, r, s, "updateProduct", func(tx pgx.Tx) (AdminProduct, error) {
		return (Store{}).UpdateProduct(r.Context(), tx, s.StationID, id, in)
	})
}

func (h *HTTP) promotion(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var body struct {
		Expected  int64           `json:"expected_version"`
		Promotion json.RawMessage `json:"promotion"`
	}
	id := r.PathValue("id")
	if !validText(id, 1, 128) || decodeCatalog(r, &body) != nil || body.Promotion == nil {
		catalogError(w, r, ErrInvalid)
		return
	}
	var p *Promotion
	if !bytes.Equal(body.Promotion, []byte("null")) {
		var b struct {
			Start time.Time `json:"starts_at"`
			End   time.Time `json:"ends_at"`
			Tiers []struct {
				MinQty   *int32 `json:"min_qty"`
				Discount *int32 `json:"discount_bps"`
			} `json:"tiers"`
		}
		d := json.NewDecoder(bytes.NewReader(body.Promotion))
		d.DisallowUnknownFields()
		if d.Decode(&b) != nil || b.Tiers == nil {
			catalogError(w, r, ErrInvalid)
			return
		}
		p = &Promotion{StartsAt: b.Start, EndsAt: b.End, Tiers: make([]Tier, 0, len(b.Tiers))}
		for _, tier := range b.Tiers {
			if tier.MinQty == nil || tier.Discount == nil {
				catalogError(w, r, ErrInvalid)
				return
			}
			p.Tiers = append(p.Tiers, Tier{*tier.MinQty, *tier.Discount})
		}
	}
	h.writeProduct(w, r, s, "updatePromotion", func(tx pgx.Tx) (AdminProduct, error) {
		return (Store{}).UpdatePromotion(r.Context(), tx, s.StationID, id, body.Expected, p)
	})
}

func (h *HTTP) writeProduct(w http.ResponseWriter, r *http.Request, s identity.Session, action string, fn func(pgx.Tx) (AdminProduct, error)) {
	var item AdminProduct
	err := platform.InTransaction(r.Context(), h.Pool, func(tx pgx.Tx) (err error) {
		item, err = fn(tx)
		if err != nil {
			return err
		}
		return catalogAudit(r, tx, s, action, item.ID, nil)
	})
	if err != nil {
		catalogError(w, r, err)
		return
	}
	catalogJSON(w, 200, item)
}

func (h *HTTP) adjust(w http.ResponseWriter, r *http.Request, s identity.Session) {
	var in Adjustment
	if decodeCatalog(r, &in) != nil {
		catalogError(w, r, ErrInvalid)
		return
	}
	h.keyed(w, r, s, "adjustInventory", in, func(ctx context.Context, tx pgx.Tx) (any, error) {
		return (Store{}).Adjust(ctx, tx, s.StationID, s.AccountID, in)
	})
}

func (h *HTTP) keyed(w http.ResponseWriter, r *http.Request, s identity.Session, command string, input any, fn func(context.Context, pgx.Tx) (any, error)) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !validText(keys[0], 1, 128) {
		catalogError(w, r, ErrInvalid)
		return
	}
	body, err := json.Marshal(input)
	if err != nil {
		catalogError(w, r, err)
		return
	}
	digest := sha256.Sum256(append(append([]byte(s.StationID), 0), body...))
	ctx := r.Context()
	key := keys[0]
	var response json.RawMessage
	status := 201
	err = platform.InTransaction(ctx, h.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO idempotency_keys(actor_id,command,key,request_digest) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, s.AccountID, command, key, digest[:])
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var prior []byte
			var savedStatus *int
			if err := tx.QueryRow(ctx, `SELECT request_digest,response_status,response_body FROM idempotency_keys WHERE actor_id=$1 AND command=$2 AND key=$3`, s.AccountID, command, key).Scan(&prior, &savedStatus, &response); err != nil {
				return err
			}
			if !bytes.Equal(digest[:], prior) || savedStatus == nil || response == nil {
				return errIdempotency
			}
			status = *savedStatus
			return nil
		}
		v, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		var id string
		var reason *string
		switch item := v.(type) {
		case AdminProduct:
			id = item.ID
		case Inventory:
			id = item.ID
			value := input.(Adjustment).ReasonCode
			reason = &value
		}
		if err = catalogAudit(r, tx, s, command, id, reason); err != nil {
			return err
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `UPDATE idempotency_keys SET response_status=$4,response_body=$5 WHERE actor_id=$1 AND command=$2 AND key=$3 RETURNING response_body`, s.AccountID, command, key, status, encoded).Scan(&response)
	})
	if err != nil {
		catalogError(w, r, err)
		return
	}
	catalogJSON(w, status, response)
}

func catalogAudit(r *http.Request, tx pgx.Tx, s identity.Session, action, id string, reason *string) error {
	_, err := tx.Exec(r.Context(), `INSERT INTO audit_events(station_id,actor_id,action,object_id,request_id,reason_code) VALUES($1,$2,$3,$4,$5,$6)`, s.StationID, s.AccountID, action, id, platform.RequestID(r.Context()), reason)
	return err
}
