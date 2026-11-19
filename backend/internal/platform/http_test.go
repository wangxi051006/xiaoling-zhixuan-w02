package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnknownRouteDoesNotSucceed(t *testing.T) {
	w := httptest.NewRecorder()
	NewHandler(nil, nil, slog.New(slog.DiscardHandler), 8).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/orders", nil))
	checkError(t, w, 404, "NOT_FOUND")
}

func TestPrivateReadiness(t *testing.T) {
	h := NewHandler(nil, func(context.Context) error { return errors.New("secret-dsn") }, slog.New(slog.DiscardHandler), 8)
	for _, tc := range []struct {
		path, host, remote string
		status             int
	}{
		{"/healthz", "localhost", "127.0.0.1:2020", 200},
		{"/readyz", "127.0.0.1:8080", "127.0.0.1:2020", 503},
		{"/readyz", "public.invalid", "127.0.0.1:2020", 404},
		{"/readyz", "localhost", "192.0.2.1:2020", 404},
		{"/api/v1/readyz", "localhost", "127.0.0.1:2020", 404},
	} {
		t.Run(tc.path+tc.host+tc.remote, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			r.Host, r.RemoteAddr = tc.host, tc.remote
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "secret-dsn") {
				t.Fatalf("got %d: %s; expected %d without DB detail", w.Code, w.Body, tc.status)
			}
		})
	}
}

func TestOversizeBodyRejectedBeforeHandler(t *testing.T) {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/example", func(http.ResponseWriter, *http.Request) { t.Error("oversize body reached handler") })
	for _, contentLength := range []int64{-1, 9} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/example", strings.NewReader("123456789"))
		r.ContentLength = contentLength
		NewHandler(api, nil, slog.New(slog.DiscardHandler), 8).ServeHTTP(w, r)
		checkError(t, w, 413, "BODY_TOO_LARGE")
	}
}

func TestPanicAndLogsDoNotExposeRequest(t *testing.T) {
	var logs bytes.Buffer
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/example/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Secret", "header-secret")
		_, _ = w.Write([]byte("partial-secret"))
		panic("panic-secret")
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/example/path-secret?token=query-secret", strings.NewReader("body-secret"))
	r.Header.Set("X-Request-ID", "request-secret")
	NewHandler(api, nil, slog.New(slog.NewJSONHandler(&logs, nil)), 32).ServeHTTP(w, r)
	checkError(t, w, 500, "INTERNAL_ERROR")
	if strings.Contains(logs.String()+w.Body.String(), "-secret") || w.Header().Get("X-Secret") != "" {
		t.Fatalf("secret or partial result escaped: %s %s", logs.String(), w.Body)
	}
}

func checkError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if w.Code != status || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != code || body.Message == "" || body.RequestID == "" || body.RequestID != w.Header().Get("X-Request-ID") {
		t.Fatalf("expected %d %s with request ID; got %d: %s", status, code, w.Code, w.Body)
	}
}
