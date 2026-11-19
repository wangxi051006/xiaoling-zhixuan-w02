//go:build integration

package integration

import (
	"campus.local/xiaoling/internal/identity"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (f *fixture) sourceRequest(actor, source, path, body, key string) *httptest.ResponseRecorder {
	c := f.credentials[actor]
	r := httptest.NewRequest("POST", c.Origin+"/api/v1"+path, strings.NewReader(body))
	r.RemoteAddr = source
	r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: c.Token})
	r.Header.Set("Origin", c.Origin)
	r.Header.Set("X-CSRF-Token", c.CSRF)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", f.seq+1))
	f.seq++
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	f.assertWire("POST", path, w.Code, w.Body.Bytes())
	return w
}
func TestP05NewOrderAccountRateCountsFailureSurvivesCancelAndReplay(t *testing.T) {
	f := setup(t)
	// A failed authenticated attempt consumes security quota; business rows roll back.
	q := f.quote("buyer", "ASAP", "BALANCE")
	f.exec(`UPDATE inventory SET on_hand=0`)
	w := f.sourceRequest("buyer", "192.0.2.1:4000", "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), "failed")
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	f.exec(`UPDATE inventory SET on_hand=100`)
	var originalBody, originalKey string
	for i := 0; i < 9; i++ {
		q = f.quote("buyer", "ASAP", "BALANCE")
		body := fmt.Sprintf(`{"quote_id":%q}`, q)
		key := fmt.Sprintf("rate-%d", i)
		w = f.sourceRequest("buyer", fmt.Sprintf("192.0.2.%d:4000", i+1), "/orders", body, key)
		if w.Code != 201 {
			t.Fatal(i, w.Code, w.Body)
		}
		if i == 0 {
			originalBody, originalKey = body, key
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		f.call("buyer", "POST", "/orders/"+out["id"].(string)+"/cancel", `{"reason_code":"CHANGE_MIND"}`, 200)
	}
	w = f.sourceRequest("buyer", "192.0.2.220:4000", "/orders", fmt.Sprintf(`{"quote_id":%q}`, f.quote("buyer", "ASAP", "BALANCE")), "limited")
	if w.Code != 429 {
		t.Fatal("account cancel loop not limited", w.Code, w.Body)
	}
	w = f.sourceRequest("buyer", "192.0.2.221:4000", "/orders", originalBody, originalKey)
	if w.Code != 201 {
		t.Fatal("replay consumed quota", w.Code, w.Body)
	}
	f.clock.Set(f.clock.Now().Add(time.Minute))
	f.order("buyer", "ASAP", "BALANCE")
}
func TestP05NewOrderSourceRateIgnoresForwardedHeadersAcrossAccounts(t *testing.T) {
	f := setup(t)
	for i := 0; i < 4; i++ {
		actor := fmt.Sprintf("rateactor%d", i)
		f.exec(`INSERT INTO accounts(id,station_id,phone)VALUES($1,'s',$2)`, actor, fmt.Sprintf("+1555000000%d", i))
		f.exec(`INSERT INTO role_bindings(account_id,role)VALUES($1,'CUSTOMER')`, actor)
		f.exec(`INSERT INTO addresses(id,account_id,building_id,room,contact_name,contact_phone)VALUES($1,$1,'b','synthetic-room','Synthetic','+15550000111')`, actor)
		f.exec(`INSERT INTO wallets(station_id,actor_id,opening_fen,balance_fen)VALUES('s',$1,10000,10000)`, actor)
		f.issue(actor)
	}
	for i := 0; i < 31; i++ {
		actor := fmt.Sprintf("rateactor%d", i/10)
		quoteBody := fmt.Sprintf(`{"lines":[{"sku_id":%q,"qty":1}],"address_id":%q,"delivery_mode":"ASAP","payment_method":"BALANCE"}`, f.sku, actor)
		q := f.call(actor, "POST", "/checkout/quote", quoteBody, 201)["quote_id"].(string)
		w := f.sourceRequest(actor, fmt.Sprintf("198.51.100.7:%d", 4000+i), "/orders", fmt.Sprintf(`{"quote_id":%q}`, q), fmt.Sprintf("source-%d", i))
		if i == 30 {
			if w.Code != 429 {
				t.Fatal("source cap bypassed with forwarding header or new account", w.Code, w.Body)
			}
			break
		}
		if w.Code != 201 {
			t.Fatal(i, w.Code, w.Body)
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		f.call(actor, "POST", "/orders/"+out["id"].(string)+"/cancel", `{"reason_code":"CHANGE_MIND"}`, 200)
	}
}
