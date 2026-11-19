package schedule

import (
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestWindowsUseShanghaiBusinessDateAndCrossMidnight(t *testing.T) {
	for _, tc := range []struct {
		opens, closes, first, lastEnd string
	}{
		{"00:00", "01:00", "2026-11-17T16:00:00Z", "2026-11-17T17:00:00Z"},
		{"23:30", "00:30", "2026-11-18T15:30:00Z", "2026-11-18T16:30:00Z"},
	} {
		t.Run(tc.opens, func(t *testing.T) {
			rows, err := generateWindows("2026-11-18", tc.opens, tc.closes, 15)
			if err != nil || len(rows) != 4 {
				t.Fatalf("windows=%v err=%v", rows, err)
			}
			first := mustTime(t, tc.first)
			for i, row := range rows {
				start := first.Add(time.Duration(i) * 15 * time.Minute)
				if row.StartsAt != start || row.EndsAt != start.Add(15*time.Minute) || row.DispatchAt != start.Add(-15*time.Minute) {
					t.Fatalf("unexpected window %d: %+v", i, row)
				}
			}
			if rows[3].EndsAt != mustTime(t, tc.lastEnd) {
				t.Fatalf("last end = %v", rows[3].EndsAt)
			}
		})
	}
}

func TestGenerateWindowsValidatesLocalTimesAndLead(t *testing.T) {
	for _, tc := range []struct {
		date, opens, closes string
		lead                int
	}{
		{"2026-02-30", "08:00", "09:00", 0},
		{"2026-11-18", "8:00", "09:00", 0},
		{"2026-11-18", "08:00", "08:00", 0},
		{"2026-11-18", "08:00", "08:14", 0},
		{"2026-11-18", "08:00", "09:00", -1},
		{"2026-11-18", "08:00", "09:00", 121},
	} {
		if _, err := generateWindows(tc.date, tc.opens, tc.closes, tc.lead); err == nil {
			t.Errorf("accepted invalid batch: %+v", tc)
		}
	}
	rows, err := generateWindows("2026-11-18", "08:00", "08:30", 20)
	if err != nil || len(rows) != 2 || rows[0].DispatchAt != mustTime(t, "2026-11-17T23:40:00Z") {
		t.Fatalf("configurable lead and complete windows: %v %v", rows, err)
	}
}

func TestSlotCutoffsAndSharedCapacity(t *testing.T) {
	slot := contract.SlotSnapshot{
		StartsAt:   mustTime(t, "2026-11-18T03:00:00Z"),
		EndsAt:     mustTime(t, "2026-11-18T03:15:00Z"),
		DispatchAt: mustTime(t, "2026-11-18T02:45:00Z"),
	}
	const asap = contract.DeliveryModeAsap
	const scheduled = contract.DeliveryModeScheduled
	const expired = contract.ErrorCodeSlotExpired
	const full = contract.ErrorCodeSlotFull
	for _, tc := range []struct {
		name, now string
		mode      contract.DeliveryMode
		accepting bool
		used      int64
		want      contract.ErrorCode
	}{
		{"same window for ASAP", "2026-11-18T02:44:59Z", asap, true, 0, ""},
		{"same window for reservation", "2026-11-18T02:44:59Z", scheduled, true, 0, ""},
		{"full pool for ASAP", "2026-11-18T02:44:59Z", asap, true, 1, full},
		{"full pool for reservation", "2026-11-18T02:44:59Z", scheduled, true, 1, full},
		{"at dispatch", "2026-11-18T02:45:00Z", scheduled, true, 0, expired},
		{"at ten minutes", "2026-11-18T03:05:00Z", asap, true, 0, ""},
		{"under ten minutes", "2026-11-18T03:05:01Z", asap, true, 0, expired},
		{"at end", "2026-11-18T03:15:00Z", asap, true, 0, expired},
		{"inside three days", "2026-11-15T03:00:01Z", scheduled, true, 0, ""},
		{"outside three days", "2026-11-15T02:59:59Z", scheduled, true, 0, expired},
		{"closed", "2026-11-18T02:44:59Z", asap, false, 0, expired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := admissionCode(slot, tc.mode, mustTime(t, tc.now), tc.accepting, tc.used, 1); got != tc.want {
				t.Fatalf("admission=%q want=%q", got, tc.want)
			}
		})
	}
}
