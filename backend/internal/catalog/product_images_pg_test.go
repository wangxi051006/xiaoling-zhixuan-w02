//go:build integration

package catalog

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductImageCatalogLifecycle(t *testing.T) {
	pool, _ := stockFixture(t)
	h := fixtureHandler(t, &HTTP{Pool: pool, Authorize: operatorFixture, ImageDir: t.TempDir()})
	initial := catalogCall(t, h, "GET", "/catalog/products/p", "", "", 200)
	if bytes.Contains(initial.Body.Bytes(), []byte(`"image_url"`)) {
		t.Fatal("existing product should have no image URL")
	}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest("POST", "/api/v1/admin/product-images", bytes.NewReader(imageBytes.Bytes()))
	upload.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, upload)
	if w.Code != http.StatusCreated {
		t.Fatalf("image upload returned %d: %s", w.Code, w.Body)
	}
	var result struct {
		URL string `json:"image_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.URL == "" {
		t.Fatalf("missing image URL: %s", w.Body)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", result.URL, nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), imageBytes.Bytes()) {
		t.Fatalf("public image did not match the upload: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	create := `{"name":"With photo","description":"","skus":[{"label":"One","units_per_pack":1,"unit_price_fen":100}],"on_sale":true,"reorder_point":0,"image_url":"` + result.URL + `"}`
	created := catalogCall(t, h, "POST", "/admin/products", "with-image", create, 201)
	var product struct {
		ID       string `json:"id"`
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &product); err != nil || product.ID == "" || product.ImageURL != result.URL {
		t.Fatalf("new product did not retain its image: %s", created.Body)
	}
	for _, path := range []string{"/admin/products", "/catalog/products", "/catalog/products/" + product.ID} {
		response := catalogCall(t, h, "GET", path, "", "", 200)
		if !bytes.Contains(response.Body.Bytes(), []byte(result.URL)) {
			t.Fatalf("%s did not return the image URL", path)
		}
	}
	catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":1,"image_url":"https://example.invalid/photo.png"}`, 400)
	catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":1,"image_url":"`+result.URL+`"}`, 200)
	preserved := catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":2,"name":"Renamed"}`, 200)
	if !bytes.Contains(preserved.Body.Bytes(), []byte(result.URL)) {
		t.Fatal("editing product information removed its image")
	}
	catalogCall(t, h, "PATCH", "/admin/products/p", "", `{"expected_version":3,"image_url":""}`, 200)
	cleared := catalogCall(t, h, "GET", "/catalog/products/p", "", "", 200)
	if bytes.Contains(cleared.Body.Bytes(), []byte(`"image_url"`)) {
		t.Fatal("removing an image left it on the product")
	}
}
