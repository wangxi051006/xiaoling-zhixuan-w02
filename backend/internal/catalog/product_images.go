package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
)

const (
	ImageUploadPattern = "POST /api/v1/admin/product-images"
	MaxImageBytes      = 5 << 20
	imageURLPrefix     = "/api/v1/catalog/product-images/"
	maxImageDimension  = 4096
)

var imageFileName = regexp.MustCompile(`^[a-f0-9]{32}-[a-f0-9]{64}\.(jpg|png)$`)

type productImage struct {
	URL string `json:"image_url"`
}

func imageStationPrefix(station string) string {
	sum := sha256.Sum256([]byte(station))
	return hex.EncodeToString(sum[:16]) + "-"
}

func (h *HTTP) checkProductImage(station, url string) error {
	if url == "" {
		return nil
	}
	name, ok := strings.CutPrefix(url, imageURLPrefix)
	if !ok || !imageFileName.MatchString(name) || !strings.HasPrefix(name, imageStationPrefix(station)) || h.ImageDir == "" {
		return ErrInvalid
	}
	info, err := os.Stat(filepath.Join(h.ImageDir, name))
	if errors.Is(err, os.ErrNotExist) || err == nil && !info.Mode().IsRegular() {
		return ErrInvalid
	}
	return err
}

func (h *HTTP) uploadProductImage(w http.ResponseWriter, r *http.Request, s identity.Session) {
	if h.ImageDir == "" {
		platform.WriteError(w, r, http.StatusServiceUnavailable, "NOT_READY", "Product image storage is unavailable")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (media != "image/jpeg" && media != "image/png") {
		catalogError(w, r, ErrInvalid)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxImageBytes))
	if err != nil {
		catalogError(w, r, err)
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxImageDimension || config.Height > maxImageDimension || media != "image/"+format {
		catalogError(w, r, ErrInvalid)
		return
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		catalogError(w, r, ErrInvalid)
		return
	}
	extension := ".png"
	if format == "jpeg" {
		extension = ".jpg"
	}
	digest := sha256.Sum256(data)
	name := imageStationPrefix(s.StationID) + hex.EncodeToString(digest[:]) + extension
	if err := saveProductImage(h.ImageDir, name, data); err != nil {
		catalogError(w, r, err)
		return
	}
	catalogJSON(w, http.StatusCreated, productImage{URL: imageURLPrefix + name})
}

func saveProductImage(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	dest := filepath.Join(dir, name)
	if info, err := os.Stat(dest); err == nil && info.Mode().IsRegular() {
		return nil
	}
	file, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), dest); err != nil {
		// A concurrent request may have published the same image.
		if info, statErr := os.Stat(dest); statErr != nil || !info.Mode().IsRegular() {
			return err
		}
	}
	return nil
}

func (h *HTTP) getProductImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("id")
	if h.ImageDir == "" || !imageFileName.MatchString(name) {
		catalogError(w, r, ErrNotFound)
		return
	}
	file, err := os.Open(filepath.Join(h.ImageDir, name))
	if errors.Is(err, os.ErrNotExist) {
		catalogError(w, r, ErrNotFound)
		return
	}
	if err != nil {
		catalogError(w, r, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		catalogError(w, r, ErrNotFound)
		return
	}
	media := "image/png"
	if strings.HasSuffix(name, ".jpg") {
		media = "image/jpeg"
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("ETag", `"`+name+`"`)
	http.ServeContent(w, r, name, info.ModTime(), file)
}
