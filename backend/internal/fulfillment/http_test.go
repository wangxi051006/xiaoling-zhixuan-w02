package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAll27FrozenRoutesAuthorizeExactRoleAndMFA(t *testing.T) {
	routes := []struct {
		method, path, roles string
		mfa                 bool
	}{
		{"POST", "/checkout/quote", "CUSTOMER", false}, {"POST", "/orders", "CUSTOMER", false}, {"GET", "/orders", "CUSTOMER", false}, {"GET", "/orders/id", "CUSTOMER", false}, {"POST", "/orders/id/cancel", "CUSTOMER", false}, {"POST", "/orders/id/urge", "CUSTOMER", false},
		{"GET", "/admin/orders", "OPERATOR", false}, {"GET", "/admin/orders/id", "OPERATOR", false}, {"POST", "/admin/orders/id/prepare", "OPERATOR", false}, {"POST", "/admin/orders/id/dispatch", "OPERATOR", false}, {"POST", "/admin/orders/id/reclaim", "OPERATOR", false}, {"POST", "/admin/orders/id/cancel", "OPERATOR", false}, {"POST", "/admin/orders/id/restock", "OPERATOR", false}, {"POST", "/admin/orders/id/return-inspect", "OPERATOR", false}, {"POST", "/admin/orders/id/refund", "OPERATOR", true}, {"GET", "/admin/dashboard", "OPERATOR FINANCE", false},
		{"PUT", "/rider/availability", "RIDER", false}, {"GET", "/rider/tasks", "RIDER", false}, {"GET", "/rider/tasks/id", "RIDER", false}, {"POST", "/rider/tasks/id/claim", "RIDER", false}, {"POST", "/rider/tasks/id/pickup", "RIDER", false}, {"POST", "/rider/tasks/id/deliver", "RIDER", false}, {"POST", "/rider/tasks/id/return", "RIDER", false}, {"GET", "/rider/history", "RIDER", false}, {"GET", "/rider/earnings", "RIDER", false}, {"GET", "/rider/messages", "RIDER", false}, {"POST", "/rider/messages/read", "RIDER", false},
	}
	if len(routes) != 27 {
		t.Fatal(len(routes))
	}
	for _, tc := range routes {
		t.Run(tc.path+tc.method, func(t *testing.T) {
			called := false
			h := HTTP{Authorize: func(w http.ResponseWriter, r *http.Request, entry identity.Entry, mfa bool, roles ...contract.Role) (identity.Session, bool) {
				called = true
				wantEntry := identity.UserEntry
				if strings.HasPrefix(tc.path, "/admin/") {
					wantEntry = identity.AdminEntry
				} else if strings.HasPrefix(tc.path, "/rider/") {
					wantEntry = identity.RiderEntry
				}
				ss := []string{}
				for _, role := range roles {
					ss = append(ss, string(role))
				}
				if entry != wantEntry || mfa != tc.mfa || strings.Join(ss, " ") != tc.roles {
					t.Errorf("entry=%s mfa=%v roles=%v", entry, mfa, roles)
				}
				w.WriteHeader(403)
				return identity.Session{}, false
			}}
			mux := http.NewServeMux()
			h.Register(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/v1"+tc.path, nil))
			if !called || w.Code != 403 {
				t.Errorf("authorization called=%v status=%d", called, w.Code)
			}
		})
	}
}

func TestMissingAuthorizerUsesFrozenErrorEnvelope(t *testing.T) {
	h := HTTP{}
	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/orders", nil))
	var out map[string]any
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || out["code"] != "NOT_READY" || len(out) != 3 || w.Code != 503 {
		t.Fatalf("unfrozen unavailable response: %d %s", w.Code, w.Body)
	}
}
