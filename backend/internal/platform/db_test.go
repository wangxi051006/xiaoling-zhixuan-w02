package platform

import (
	"fmt"
	"log/slog"
	"net"
	"net/http/httptest"
	"testing"
)

func TestReadinessFailsWithUnavailablePostgres(t *testing.T) {
	// Keep the port reserved, but never speak PostgreSQL on it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dsn := fmt.Sprintf("postgres://fixture:fixture@%s/fixture?sslmode=disable&connect_timeout=1", listener.Addr())
	pool, err := NewPool(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	req := httptest.NewRequest("GET", "http://localhost/readyz", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	NewHandler(nil, pool.Ping, slog.New(slog.DiscardHandler), DefaultBodyLimit).ServeHTTP(w, req)
	checkError(t, w, 503, "NOT_READY")
}
