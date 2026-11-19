package catalog

import (
	"campus.local/xiaoling/internal/contract"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/platform"
)

func TestCatalogMissingInfrastructureIsUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	(&HTTP{}).Register(mux)
	h := platform.NewHandler(mux, nil, nil, 0)
	for _, path := range []string{"/catalog/products", "/catalog/products/p", "/admin/products"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1"+path, nil))
		if w.Code != 503 {
			t.Errorf("%s: want unavailable, got %d", path, w.Code)
		}
	}
}

func TestCatalogRejectsNullOrMisnamedOptionalFields(t *testing.T) {
	for _, body := range []string{`{"expected_version":1,"name":null}`, `{"expected_version":1,"skus":null}`, `{"expected_version":1,"Name":"x"}`, `{"expected_version":1,"on_sale":true,"on_sale":false}`} {
		var v ProductUpdate
		r := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if !errors.Is(decodeCatalog(r, &v), ErrInvalid) {
			t.Errorf("invalid field shape accepted: %s", body)
		}
	}
}

func TestCatalogRejectsCursorBeyondFrozenLimit(t *testing.T) {
	if _, _, _, _, err := listQuery("cursor="+strings.Repeat("x", 129), true); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted cursor beyond 128 characters")
	}
}

func TestCatalogErrorsMatchFrozenContractAndRedactDiagnostics(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Enum []string `json:"enum"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err = json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, c := range spec.Components.Schemas["ErrorCode"].Enum {
		allowed[c] = true
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{ErrInvalid, 400, "BAD_REQUEST"}, {ErrNotFound, 404, "NOT_FOUND"}, {ErrVersion, 409, "INVALID_STATE"}, {ErrStock, 409, "OUT_OF_STOCK"}, {errIdempotency, 409, "IDEMPOTENCY_CONFLICT"}, {errors.New("SELECT private_diagnostic"), 500, "INTERNAL_ERROR"}} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/check", func(w http.ResponseWriter, r *http.Request) { catalogError(w, r, tc.err) })
		w := httptest.NewRecorder()
		platform.NewHandler(mux, nil, nil, 0).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/check", nil))
		var v map[string]string
		if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || v["code"] != tc.code || !allowed[v["code"]] || v["request_id"] == "" || len(v) != 3 || strings.Contains(w.Body.String(), "private_diagnostic") {
			t.Fatalf("invalid error envelope: %s", w.Body.String())
		}
	}
}

func TestPricingRejectsAbnormalDiscounts(t *testing.T) {
	at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for _, bps := range []int32{-1, 10001} {
		_, err := priceLine(contract.OrderLineInput{SKUID: "s", Qty: 1}, pricingSKU{units: 1, price: 100, onSale: true, promotion: &Promotion{ID: "p", StartsAt: at, EndsAt: at.Add(time.Hour), Tiers: []Tier{{1, bps}}}}, at)
		if !errors.Is(err, ErrInvalid) {
			t.Fatal("abnormal discount accepted")
		}
	}
}
