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
	call := func(t *testing.T, app http.Handler, method, base, path, body, origin, peer string, cookie *http.Cookie, want int, csrf ...string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if len(csrf) > 0 {
			r.Header.Set("X-CSRF-Token", csrf[0])
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
	call(t, disabled, "POST", o.AdminOrigin, login, credentials, o.AdminOrigin, local, nil, 404)
	call(t, enabled, "POST", admin, login, credentials, admin, "192.0.2.44:40200", nil, 404)
	call(t, enabled, "POST", admin, login, credentials, "http://user.localhost:5174", local, nil, 403)
	ids := map[string]bool{}
	for _, tc := range []struct {
		entry string
		roles []string
	}{
		{"user", []string{"CUSTOMER"}}, {"rider", []string{"RIDER"}}, {"admin", []string{"FINANCE", "OPERATOR", "SECURITY_ADMIN"}},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			origin := "http://" + tc.entry + ".localhost:5174"
			call(t, enabled, "GET", origin, login, "", origin, local, nil, 200)
			w := call(t, enabled, "POST", origin, login, `{"account":"123456","password":"123457"}`, origin, local, nil, 401)
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("wrong password issued a cookie")
			}
			w = call(t, enabled, "POST", origin, login, credentials, origin, local, nil, 204)
			cookies := w.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" {
				t.Fatal("missing host-bound secure session cookie")
			}
			w = call(t, enabled, "GET", origin, "/api/v1/me", "", origin, local, cookies[0], 200)
			var me struct {
				ID    string
				Roles []string
				CSRF  string `json:"csrf_token"`
			}
			if json.Unmarshal(w.Body.Bytes(), &me) != nil || !slices.Equal(me.Roles, tc.roles) || me.ID == "" || ids[me.ID] {
				t.Fatal("entry did not receive its own account and roles")
			}
			ids[me.ID] = true
			call(t, enabled, "GET", origin, "/api/v1/me", "", origin, "192.0.2.44:40200", cookies[0], 401)
			other := "http://user.localhost:5174"
			if tc.entry == "user" {
				other = admin
			}
			call(t, enabled, "GET", other, "/api/v1/me", "", other, local, cookies[0], 401)
			formal := "https://" + tc.entry + ".example.test"
			call(t, disabled, "GET", formal, "/api/v1/me", "", formal, local, cookies[0], 401)

			const buildings = "/api/v1/me/service-buildings"
			if tc.entry != "user" {
				call(t, enabled, "GET", origin, buildings, "", origin, local, cookies[0], 404)
				return
			}
			call(t, enabled, "GET", origin, buildings, "", origin, local, nil, 401)
			if _, err := pool.Exec(ctx, `INSERT INTO service_buildings (id,station_id,name,enabled) VALUES
				('a-disabled','xiaoling-local-test','已停用楼栋',false),
				('z-enabled','xiaoling-local-test','另一座楼栋',true)`); err != nil {
				t.Fatal(err)
			}
			type buildingPage struct {
				Items []struct{ ID, Name string }
				Next  *string `json:"next_cursor"`
			}
			var first, second buildingPage
			w = call(t, enabled, "GET", origin, buildings+"?limit=1", "", origin, local, cookies[0], 200)
			if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || len(first.Items) != 1 ||
				first.Items[0].Name != "测试一号楼" || first.Next == nil || *first.Next != first.Items[0].ID {
				t.Fatalf("expected the local test building on the first page: %s", w.Body.String())
			}
			w = call(t, enabled, "GET", origin, buildings+"?limit=1&cursor="+*first.Next, "", origin, local, cookies[0], 200)
			if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil || len(second.Items) != 1 ||
				second.Items[0].ID != "z-enabled" || second.Next != nil {
				t.Fatalf("expected only the other enabled building on the last page: %s", w.Body.String())
			}
			input, err := json.Marshal(map[string]string{
				"building_id": first.Items[0].ID, "room": "测试", "contact_name": "验收测试", "contact_phone": "+8613800000000",
			})
			if err != nil {
				t.Fatal(err)
			}
			w = call(t, enabled, "POST", origin, "/api/v1/me/addresses", string(input), origin, local, cookies[0], 201, me.CSRF)
			var address struct {
				BuildingName string `json:"building_name"`
				Room         string `json:"room"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &address); err != nil || address.BuildingName != first.Items[0].Name || address.Room != "测试" {
				t.Fatalf("saved address does not match the selected building and room: %s", w.Body.String())
			}
		})
	}
}
