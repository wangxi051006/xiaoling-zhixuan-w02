package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

const DefaultBodyLimit int64 = 64 << 10

type requestIDKey struct{}

type httpError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, httpError{Code: code, Message: message, RequestID: RequestID(r.Context())})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func NewHandler(api *http.ServeMux, ready func(context.Context) error, logger *slog.Logger, maxBody int64) http.Handler {
	if maxBody <= 0 {
		maxBody = DefaultBodyLimit
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw [16]byte
		_, _ = rand.Read(raw[:])
		id := hex.EncodeToString(raw[:])
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
		res := &bufferedResponse{header: make(http.Header)}
		func() {
			defer func() {
				if recover() != nil {
					res = &bufferedResponse{header: make(http.Header)}
					WriteError(res, r, 500, "INTERNAL_ERROR", "Internal server error")
				}
			}()
			serve(res, r, api, ready, maxBody)
		}()
		for k, values := range res.header {
			w.Header()[k] = values
		}
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if res.status == 0 {
			res.status = http.StatusOK
		}
		w.WriteHeader(res.status)
		if r.Method != http.MethodHead {
			_, _ = res.body.WriteTo(w)
		}
		logger.InfoContext(r.Context(), "http_request", "request_id", id, "status", res.status)
	})
}

func serve(w http.ResponseWriter, r *http.Request, api *http.ServeMux, ready func(context.Context) error, maxBody int64) {
	if (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") && r.Method == http.MethodGet && localRequest(r) {
		if r.URL.Path == "/readyz" {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if ready == nil || ready(ctx) != nil {
				WriteError(w, r, 503, "NOT_READY", "Service not ready")
				return
			}
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	if api == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		WriteError(w, r, 404, "NOT_FOUND", "Not found")
		return
	}
	_, pattern := api.Handler(r)
	if pattern == "" {
		WriteError(w, r, 404, "NOT_FOUND", "Not found")
		return
	}
	if r.ContentLength > maxBody {
		WriteError(w, r, 413, "BODY_TOO_LARGE", "Request body too large")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxBody)
	body, err := io.ReadAll(limited)
	_ = limited.Close()
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, r, 413, "BODY_TOO_LARGE", "Request body too large")
		} else {
			WriteError(w, r, 400, "BAD_REQUEST", "Invalid request body")
		}
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	api.ServeHTTP(w, r)
}

func localRequest(r *http.Request) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.Trim(r.Host, "[]")
	}
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

// Buffer small JSON responses so a panic cannot expose a partially written result.
type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *bufferedResponse) Header() http.Header { return w.header }
func (w *bufferedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *bufferedResponse) Write(body []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(body)
}
