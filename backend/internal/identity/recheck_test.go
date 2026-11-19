package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
)

// Catch responses which look valid to Go clients but violate the frozen API.
func TestIdentityErrorEnvelopeMatchesFrozenContract(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal("cannot read frozen contract")
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct{ Enum []string }
		}
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal("cannot decode frozen contract")
	}
	codes := map[string]bool{}
	for _, code := range spec.Components.Schemas["ErrorCode"].Enum {
		codes[code] = true
	}
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"bad input", ErrOTPInput, 400},
		{"authentication", ErrInvalidOTP, 401},
		{"permission", AccessError(403), 403},
		{"object", ErrObjectNotFound, 404},
		{"version", ErrVersionConflict, 409},
		{"rate", ErrRateLimit, 429},
		{"provider", ErrOTPSend, 503},
		{"internal", errors.New("private adapter diagnostic"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/check", func(w http.ResponseWriter, r *http.Request) { identityError(w, r, tc.err) })
			w := httptest.NewRecorder()
			platform.NewHandler(mux, nil, nil, 4096).ServeHTTP(w, httptest.NewRequest("GET", "https://user.example.test/api/v1/check", nil))
			var got map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal("error is not JSON")
			}
			if w.Code != tc.status || !codes[got["code"]] || got["message"] == "" || got["request_id"] == "" || len(got) != 3 {
				t.Fatalf("invalid envelope: status=%d code=%s", w.Code, got["code"])
			}
			if got["message"] == tc.err.Error() && tc.status == 500 {
				t.Fatal("internal diagnostic exposed")
			}
		})
	}
}

func TestIdentityMissingDependenciesUnavailable(t *testing.T) {
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, method, origin, path string }{
		{"session store", "GET", "https://user.example.test", "/me"},
		{"otp sender", "POST", "https://user.example.test", "/auth/otp/request"},
		{"otp verifier", "POST", "https://user.example.test", "/auth/otp/verify"},
		{"idp", "GET", "https://admin.example.test", "/auth/staff/start"},
		{"idp callback", "GET", "https://rider.example.test", "/auth/staff/callback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			NewIdentityHTTP(policy, nil, nil, nil, nil, nil).Register(mux)
			r := httptest.NewRequest(tc.method, tc.origin+"/api/v1"+tc.path, nil)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			platform.NewHandler(mux, nil, nil, 4096).ServeHTTP(w, r)
			if w.Code != 503 {
				t.Fatalf("missing dependency returned %d, want 503", w.Code)
			}
		})
	}
}

func TestEntryPolicyRejectsAmbiguousSources(t *testing.T) {
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1780000000, 0)
	session := Session{AccountID: "customer", StationID: "s", Entry: UserEntry, Roles: []contract.Role{contract.RoleCustomer}, ExpiresAt: now.Add(time.Hour), CSRFToken: secretToken()}
	for _, scenario := range []string{"absent origin", "null origin", "duplicate origin", "duplicate csrf", "forwarded origin"} {
		t.Run(scenario, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://user.example.test/api/v1/auth/logout", nil)
			r.Header.Set("Origin", "https://user.example.test")
			r.Header.Set("X-CSRF-Token", session.CSRFToken)
			switch scenario {
			case "absent origin":
				r.Header.Del("Origin")
			case "null origin":
				r.Header.Set("Origin", "null")
			case "duplicate origin":
				r.Header.Add("Origin", "https://user.example.test")
			case "duplicate csrf":
				r.Header.Add("X-CSRF-Token", session.CSRFToken)
			case "forwarded origin":
				r.Header.Del("Origin")
				r.Header.Set("X-Forwarded-Origin", "https://user.example.test")
			}
			if AccessStatus(policy.Authorize(r, UserEntry, session, now, false, contract.RoleCustomer)) != 403 {
				t.Fatal("ambiguous source authorized")
			}
		})
	}
}
