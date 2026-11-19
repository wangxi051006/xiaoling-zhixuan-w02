package identity

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
)

func TestEntrySeparationAndRecentMFA(t *testing.T) {
	now := time.Unix(1000, 0)
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	s := Session{AccountID: "a", StationID: "s", Entry: AdminEntry, Roles: []contract.Role{contract.RoleFinance}, ExpiresAt: now.Add(time.Hour), MFAAt: now.Add(-time.Minute), CSRFToken: "local-test-token"}
	for _, tc := range []struct {
		method, host, origin, csrf string
		entry                      Entry
		role                       contract.Role
		recent                     bool
		status                     int
	}{
		{"POST", "admin.example.test", "https://admin.example.test", s.CSRFToken, AdminEntry, contract.RoleFinance, true, 0},
		{"POST", "admin.example.test", "https://user.example.test", s.CSRFToken, AdminEntry, contract.RoleFinance, true, 403},
		{"POST", "admin.example.test", "https://admin.example.test", "wrong", AdminEntry, contract.RoleFinance, true, 403},
		{"GET", "rider.example.test", "", "", RiderEntry, contract.RoleRider, false, 401},
		{"GET", "admin.example.test", "", "", AdminEntry, contract.RoleOperator, false, 403},
		{"GET", "unexpected.example.test", "", "", AdminEntry, contract.RoleFinance, false, 404},
	} {
		r := httptest.NewRequest(tc.method, "https://"+tc.host+"/api/v1/me", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-CSRF-Token", tc.csrf)
		if err := policy.Authorize(r, tc.entry, s, now, tc.recent, tc.role); AccessStatus(err) != tc.status {
			t.Errorf("%s %s role %s: got %v, want %d", tc.method, tc.host, tc.role, err, tc.status)
		}
	}
	s.MFAAt = now.Add(-16 * time.Minute)
	r := httptest.NewRequest("GET", "https://admin.example.test/api/v1/me", nil)
	if AccessStatus(policy.Authorize(r, AdminEntry, s, now, true, contract.RoleFinance)) != 403 {
		t.Fatal("stale MFA accepted")
	}
	s.ExpiresAt = now
	if AccessStatus(policy.Authorize(r, AdminEntry, s, now, false, contract.RoleFinance)) != 401 {
		t.Fatal("expired session accepted")
	}
}

func TestSessionCookieIsHostBound(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "fixture", time.Unix(1000, 0))
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != SessionCookieName || c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("incorrect session isolation flags: %v", c)
	}
}
