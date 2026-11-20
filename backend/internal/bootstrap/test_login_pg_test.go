//go:build integration

package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLocalPasswordLoginIsEntryBoundAndDisabledByDefault(t *testing.T) {
	pool, ctx := g1Pool(t)
	o := ApplicationOptions{Pool: pool, Clock: &g1Clock{time.Now()},
		UserOrigin: "http://user.localhost:5174", RiderOrigin: "http://rider.localhost:5174", AdminOrigin: "http://admin.localhost:5174"}
	enabled, err := NewLocalTestApplication(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocalTestApplication(ctx, o); err != nil {
		t.Fatalf("repeat initialization: %v", err)
	}
	o.UserOrigin, o.RiderOrigin, o.AdminOrigin = "https://user.example.test", "https://rider.example.test", "https://admin.example.test"
	disabled, err := NewApplication(o)
	if err != nil {
		t.Fatal(err)
	}
	call := func(app http.Handler, method, base, path, body, origin, peer string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s%s: status %d, want %d", method, base, path, w.Code, want)
		}
		return w
	}
	const login = "/api/v1/auth/test/login"
	const credentials = `{"account":"123456","password":"123456"}`
	const local = "127.0.0.1:40200"
	const admin = "http://admin.localhost:5174"
	call(disabled, "POST", o.AdminOrigin, login, credentials, o.AdminOrigin, local, nil, 404)
	call(enabled, "POST", admin, login, credentials, admin, "192.0.2.44:40200", nil, 404)
	call(enabled, "POST", admin, login, credentials, "http://user.localhost:5174", local, nil, 403)
	ids := map[string]bool{}
	for _, tc := range []struct {
		entry string
		roles []string
	}{
		{"user", []string{"CUSTOMER"}}, {"rider", []string{"RIDER"}}, {"admin", []string{"FINANCE", "OPERATOR", "SECURITY_ADMIN"}},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			origin := "http://" + tc.entry + ".localhost:5174"
			call(enabled, "GET", origin, login, "", origin, local, nil, 200)
			w := call(enabled, "POST", origin, login, `{"account":"123456","password":"123457"}`, origin, local, nil, 401)
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("wrong password issued a cookie")
			}
			w = call(enabled, "POST", origin, login, credentials, origin, local, nil, 204)
			cookies := w.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" {
				t.Fatal("missing host-bound secure session cookie")
			}
			w = call(enabled, "GET", origin, "/api/v1/me", "", origin, local, cookies[0], 200)
			var me struct {
				ID    string
				Roles []string
			}
			if json.Unmarshal(w.Body.Bytes(), &me) != nil || !slices.Equal(me.Roles, tc.roles) || me.ID == "" || ids[me.ID] {
				t.Fatal("entry did not receive its own account and roles")
			}
			ids[me.ID] = true
			call(enabled, "GET", origin, "/api/v1/me", "", origin, "192.0.2.44:40200", cookies[0], 401)
			other := "http://user.localhost:5174"
			if tc.entry == "user" {
				other = admin
			}
			call(enabled, "GET", other, "/api/v1/me", "", other, local, cookies[0], 401)
			formal := "https://" + tc.entry + ".example.test"
			call(disabled, "GET", formal, "/api/v1/me", "", formal, local, cookies[0], 401)
		})
	}
}
