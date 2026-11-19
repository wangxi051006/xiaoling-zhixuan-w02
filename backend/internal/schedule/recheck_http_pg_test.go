//go:build integration

package schedule

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecheckHTTPPaginationAndInputLimits(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	h := recheckHandler(pool, now, "s")
	body := `{"local_date":"2026-11-19","opens_at":"00:00","closes_at":"23:45","lead_minutes":0,"capacity":9999}`
	created := slotCall(t, h, "POST", "/admin/slots/batches", body, strings.Repeat("k", 128), 201)
	var batch page[adminSlotView]
	if err := json.Unmarshal(created.Body.Bytes(), &batch); err != nil || len(batch.Items) != 95 || batch.Next != nil {
		t.Fatal("invalid generated batch shape")
	}
	for i, item := range batch.Items {
		if item.StartsAt.In(shanghai).Format(localDateFormat) != "2026-11-19" || item.StartsAt.Sub(item.DispatchAt) != 0 || item.Capacity != 9999 || item.Used != 0 || item.Version != 1 || !item.UpdatedAt.Equal(now) || (i > 0 && !batch.Items[i-1].EndsAt.Equal(item.StartsAt)) {
			t.Fatal("batch did not preserve frozen request")
		}
	}
	for _, prefix := range []string{"/admin/slots", "/delivery/slots"} {
		first := slotCall(t, h, "GET", prefix+"?date=2026-11-19", "", "", 200)
		var p struct {
			Items []map[string]json.RawMessage `json:"items"`
			Next  *string                      `json:"next_cursor"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &p); err != nil || len(p.Items) != 20 || p.Next == nil {
			t.Fatal("wrong default page size")
		}
		seen := map[string]bool{}
		cursor := ""
		for {
			w := slotCall(t, h, "GET", prefix+"?date=2026-11-19&limit=17"+cursor, "", "", 200)
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			for _, item := range p.Items {
				var id string
				_ = json.Unmarshal(item["id"], &id)
				if id == "" || seen[id] {
					t.Fatal("page duplicated item")
				}
				seen[id] = true
				if prefix == "/delivery/slots" {
					if len(item) != 5 || item["capacity"] != nil || item["used"] != nil || item["version"] != nil || item["updated_at"] != nil {
						t.Fatal("customer response leaked private fields")
					}
				} else if len(item) != 9 || item["version"] == nil || item["updated_at"] == nil {
					t.Fatal("admin response missing real configuration fields")
				}
			}
			if p.Next == nil {
				break
			}
			cursor = "&cursor=" + url.QueryEscape(*p.Next)
		}
		if len(seen) != 95 {
			t.Fatalf("pagination missed slots: %d", len(seen))
		}
		max := slotCall(t, h, "GET", prefix+"?date=2026-11-19&limit=100", "", "", 200)
		if err := json.Unmarshal(max.Body.Bytes(), &p); err != nil || len(p.Items) != 95 || p.Next != nil {
			t.Fatal("maximum page wrong")
		}
		empty := slotCall(t, h, "GET", prefix+"?date=2026-11-22", "", "", 200)
		if strings.TrimSpace(empty.Body.String()) != `{"items":[],"next_cursor":null}` {
			t.Fatalf("empty page wrong: %s", empty.Body)
		}
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=1.5", "limit=-1", "limit=", "limit=1&limit=2", "date=2026-02-29", "date=0000-01-01", "date=2026-1-01", "date=2026-11-19&date=2026-11-20", "cursor=", "cursor=bad!", "cursor=" + strings.Repeat("x", 129), "role=OPERATOR", "station_id=other", "%xy=1"} {
		for _, prefix := range []string{"/admin/slots", "/delivery/slots"} {
			slotCall(t, h, "GET", prefix+"?"+query, "", "", 400)
		}
	}
	for _, id := range []string{strings.Repeat("x", 129), "has%20space", "%09"} {
		slotCall(t, h, "GET", "/admin/slots/"+id, "", "", 400)
	}
	for _, invalid := range []string{
		`{"expected_version":0,"capacity":1,"accepting":true}`,
		`{"expected_version":9007199254740992,"capacity":1,"accepting":true}`,
		`{"expected_version":1,"capacity":-1,"accepting":true}`,
		`{"expected_version":1,"capacity":9007199254740992,"accepting":true}`,
		`{"expected_version":1,"capacity":1,"accepting":true,"dispatch_at":"2026-11-18T00:00:00Z"}`,
		`{"expected_version":1,"capacity":1,"accepting":true,"ends_at":"2026-11-18T00:00:00Z"}`,
		`{"expected_version":1,"capacity":1,"accepting":true,"lead_minutes":1}`,
		`{"expected_version":1,"capacity":1,"accepting":true,"slot_reservations":[]}`,
		`{"expected_version":1,"capacity":1,"accepting":true,"version":2}`,
	} {
		slotCall(t, h, "PUT", "/admin/slots/early", invalid, "", 400)
	}
	for _, change := range []struct{ from, to string }{{"9999", "0"}, {"9999", "10000"}, {"9999", "-1"}, {`"lead_minutes":0`, `"lead_minutes":-1`}, {`"lead_minutes":0`, `"lead_minutes":121`}, {"2026-11-19", "2026-02-30"}, {"00:00", "24:00"}, {"23:45", "00:00"}} {
		slotCall(t, h, "POST", "/admin/slots/batches", strings.Replace(body, change.from, change.to, 1), "invalid", 400)
	}
	for _, key := range []string{"", strings.Repeat("k", 129), "has space"} {
		slotCall(t, h, "POST", "/admin/slots/batches", body, key, 400)
	}
	slotCall(t, h, "POST", "/admin/slots/batches", body+strings.Repeat(" ", 4096), "large", 413)
	slotCall(t, h, "PUT", "/admin/slots/early", `{"expected_version":1,"capacity":9007199254740991,"accepting":true}`, "", 200)
	if scalar(t, pool, ctx, `SELECT capacity FROM delivery_slots WHERE id='early'`) != maxSafeInteger {
		t.Fatal("maximum allowed capacity not retained")
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_slots SET version=9007199254740991 WHERE id='early'`); err != nil {
		t.Fatal(err)
	}
	slotCall(t, h, "PUT", "/admin/slots/early", `{"expected_version":9007199254740991,"capacity":1,"accepting":true}`, "", 409)
}

func TestRecheckHTTPBatchConcurrentKeys(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(map[bool]string{true: "same-key", false: "different-key"}[sameKey], func(t *testing.T) {
			pool, ctx, now := slotFixture(t)
			h := recheckHandler(pool, now, "s")
			body := `{"local_date":"2026-11-19","opens_at":"23:30","closes_at":"00:30","lead_minutes":15,"capacity":2}`
			start := make(chan struct{})
			results := make(chan *httptest.ResponseRecorder, 2)
			for i := range 2 {
				go func() {
					key := "same"
					if !sameKey && i == 1 {
						key = "other"
					}
					r := httptest.NewRequest("POST", "https://admin.example.test/api/v1/admin/slots/batches", strings.NewReader(body))
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("Idempotency-Key", key)
					w := httptest.NewRecorder()
					<-start
					h.ServeHTTP(w, r)
					results <- w
				}()
			}
			close(start)
			a, b := <-results, <-results
			if a.Code != 201 || b.Code != 201 || !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
				t.Fatalf("concurrent batch replay failed: %d %d", a.Code, b.Code)
			}
			if scalar(t, pool, ctx, `SELECT count(*) FROM delivery_slots`) != 6 {
				t.Fatal("batch duplicate creation")
			}
			keys := int64(1)
			if !sameKey {
				keys = 2
			}
			if scalar(t, pool, ctx, `SELECT count(*) FROM idempotency_keys`) != keys {
				t.Fatal("wrong idempotency record count")
			}
			slotCall(t, h, "POST", "/admin/slots/batches", strings.Replace(body, `"capacity":2`, `"capacity":3`, 1), "same", 409)
			slotCall(t, h, "POST", "/admin/slots/batches", strings.Replace(body, `"capacity":2`, `"capacity":3`, 1), "third", 409)
			if scalar(t, pool, ctx, `SELECT count(*) FROM idempotency_keys`) != keys {
				t.Fatal("conflict cached a key")
			}
		})
	}
}

func TestRecheckHTTPAndPortBookingRangeAgree(t *testing.T) {
	pool, ctx, now := slotFixture(t)
	now = now.Truncate(time.Microsecond)
	for _, days := range []int{1, 3, 7} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		bound := now.In(shanghai).AddDate(0, 0, days)
		for _, delta := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
			start := bound.Add(delta)
			id := "before"
			if delta == 0 {
				id = "at"
			}
			if delta > 0 {
				id = "after"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity) VALUES($1,'s',$2,$3,$4,1)`, id, start, start.Add(15*time.Minute), start.Add(-15*time.Minute)); err != nil {
				t.Fatal(err)
			}
			_, selErr := (Store{BookingDays: days}).SelectSlot(ctx, tx, "s", contract.DeliveryModeScheduled, id, now)
			resErr := (Store{BookingDays: days}).ReserveSlot(ctx, tx, id, id, contract.DeliveryModeScheduled, now)
			if delta <= 0 {
				if selErr != nil || resErr != nil {
					t.Fatal("inside/inclusive limit rejected")
				}
			} else if selErr != ErrSlotExpired || resErr != ErrSlotExpired {
				t.Fatal("beyond limit accepted")
			}
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		// Persist these boundary windows for the customer HTTP query separately.
		for _, delta := range []time.Duration{-time.Microsecond, 0, time.Microsecond} {
			id := "before"
			if delta == 0 {
				id = "at"
			}
			if delta > 0 {
				id = "after"
			}
			start := bound.Add(delta)
			if _, err := pool.Exec(ctx, `INSERT INTO delivery_slots(id,station_id,starts_at,ends_at,dispatch_at,capacity) VALUES($1,'s',$2,$3,$4,1)`, id, start, start.Add(15*time.Minute), start.Add(-15*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		h := HTTP{Pool: pool, Clock: fixedClock{now}, Store: Store{BookingDays: days}, Authorize: func(http.ResponseWriter, *http.Request, identity.Entry, bool, ...contract.Role) (identity.Session, bool) {
			return identity.Session{AccountID: "customer", StationID: "s"}, true
		}}
		mux := http.NewServeMux()
		h.Register(mux)
		w := slotCall(t, platform.NewHandler(mux, nil, nil, 0), "GET", "/delivery/slots?date="+bound.In(shanghai).Format(localDateFormat), "", "", 200)
		var p page[slotView]
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || len(p.Items) != 2 || p.Items[0].ID != "before" || p.Items[1].ID != "at" {
			t.Fatal("HTTP range differs from port range")
		}
		if _, err := pool.Exec(ctx, `DELETE FROM delivery_slots WHERE id IN ('before','at','after')`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecheckMissingHTTPInfrastructure(t *testing.T) {
	for _, missing := range []string{"pool", "clock", "authorization"} {
		h := HTTP{Pool: &pgxpool.Pool{}, Clock: fixedClock{mustTime(t, "2026-11-18T02:40:00Z")}, Authorize: func(http.ResponseWriter, *http.Request, identity.Entry, bool, ...contract.Role) (identity.Session, bool) {
			t.Fatal("authorization called with missing infrastructure")
			return identity.Session{}, false
		}}
		switch missing {
		case "pool":
			h.Pool = nil
		case "clock":
			h.Clock = nil
		case "authorization":
			h.Authorize = nil
		}
		mux := http.NewServeMux()
		h.Register(mux)
		handler := platform.NewHandler(mux, nil, nil, 0)
		for _, path := range []string{"/delivery/slots", "/admin/slots", "/admin/slots/early"} {
			slotCall(t, handler, "GET", path, "", "", 503)
		}
		slotCall(t, handler, "PUT", "/admin/slots/early", `{}`, "", 503)
		slotCall(t, handler, "POST", "/admin/slots/batches", `{}`, "key", 503)
	}
}
