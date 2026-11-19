package schedule

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
)

func TestRecheckStrictJSON(t *testing.T) {
	for _, body := range []string{
		`{"expected_version":1,"capacity":2,"accepting":true,"capacity":3}`,
		`{"Expected_Version":1,"capacity":2,"accepting":true}`,
		`{"expected_version":1,"CAPACITY":2,"accepting":true}`,
		`{"expected_version":1,"capacity":null,"accepting":true}`,
		`{"expected_version":1,"capacity":2}`,
		`null`, `[]`, `{}`, `{"expected_version":1,"capacity":2,"accepting":true,"used":0}`,
		`{"expected_version":1,"capacity":{"value":2},"accepting":true}`,
		`{"expected_version":1,"capacity":2.5,"accepting":true}`,
		`{"expected_version":1,"capacity":"2","accepting":true}`,
		`{"expected_version":1,"capacity":2,"accepting":true} {}`,
	} {
		r := httptest.NewRequest("PUT", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var in updateBody
		if err := decodeBody(httptest.NewRecorder(), r, &in); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid update shape %s: %v", body, err)
		}
	}
	for _, body := range []string{
		`{"local_date":"2026-11-19","opens_at":"08:00","closes_at":"09:00","lead_minutes":0,"capacity":1,"capacity":2}`,
		`{"local_date":"2026-11-19","Opens_At":"08:00","closes_at":"09:00","lead_minutes":0,"capacity":1}`,
		`{"local_date":"2026-11-19","opens_at":"08:00","closes_at":"09:00","lead_minutes":null,"capacity":1}`,
		`{"local_date":"2026-11-19","opens_at":"08:00","closes_at":"09:00","lead_minutes":0}`,
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var in batchBody
		if err := decodeBody(httptest.NewRecorder(), r, &in); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid batch shape: %v", err)
		}
	}
}

func TestRecheckWindowIntervalMustBeComplete(t *testing.T) {
	for _, tc := range []struct{ date, open, close string }{
		{"2026-11-18", "08:00", "08:20"},
		{"2026-11-18", "23:30", "00:31"},
		{"0000-01-01", "08:00", "09:00"},
		{"9999-12-31", "23:30", "00:30"},
	} {
		if _, err := generateWindows(tc.date, tc.open, tc.close, 0); err == nil {
			t.Errorf("accepted incomplete or unrepresentable interval: %+v", tc)
		}
	}
}

func TestRecheckErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   string
		status int
	}{
		{ErrSlotFull, "SLOT_FULL", 409}, {ErrSlotExpired, "SLOT_EXPIRED", 409},
		{ErrSlotConflict, "INVALID_STATE", 409}, {errReplayConflict, "IDEMPOTENCY_CONFLICT", 409},
		{ErrInvalid, "BAD_REQUEST", 400}, {ErrNotFound, "NOT_FOUND", 404},
		{errors.New("private SQL diagnostic"), "INTERNAL_ERROR", 500},
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/test", func(w http.ResponseWriter, r *http.Request) { slotError(w, r, tc.err) })
		w := httptest.NewRecorder()
		platform.NewHandler(mux, nil, nil, 0).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/test", nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(w.Body.String(), "private") {
			t.Errorf("error %v got %d %s", tc.err, w.Code, w.Body)
		}
	}
}

func TestRecheckCalendarLeadAndBookingEdges(t *testing.T) {
	for _, date := range []string{"2026-01-31", "2026-12-31", "2028-02-29"} {
		for _, lead := range []int{0, 120} {
			rows, err := generateWindows(date, "23:30", "00:30", lead)
			if err != nil || len(rows) != 4 {
				t.Fatalf("%s: %v", date, err)
			}
			for i, row := range rows {
				if row.EndsAt.Sub(row.StartsAt) != 15*time.Minute || row.StartsAt.Sub(row.DispatchAt) != time.Duration(lead)*time.Minute || (i > 0 && !rows[i-1].EndsAt.Equal(row.StartsAt)) {
					t.Fatal("broken window sequence")
				}
			}
		}
	}
	now := mustTime(t, "2026-12-30T16:00:00.000000123Z")
	for _, days := range []int{1, 3, 7} {
		for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			start := now.In(shanghai).AddDate(0, 0, days).Add(delta)
			slot := contract.SlotSnapshot{StartsAt: start, EndsAt: start.Add(15 * time.Minute), DispatchAt: start.Add(-15 * time.Minute)}
			want := contract.ErrorCode("")
			if delta > 0 {
				want = contract.ErrorCodeSlotExpired
			}
			if got := admissionWithin(slot, contract.DeliveryModeScheduled, now, true, 0, 1, days); got != want {
				t.Fatalf("days=%d delta=%v got=%s", days, delta, got)
			}
		}
	}
}
