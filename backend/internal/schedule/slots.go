package schedule

import (
	"fmt"
	"time"
	_ "time/tzdata"

	"campus.local/xiaoling/internal/contract"
)

const (
	windowDuration     = 15 * time.Minute
	asapMinimumLeft    = 10 * time.Minute
	defaultBookingDays = 3
	localDateFormat    = "2006-01-02"
)

var shanghai = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return loc
}()

func generateWindows(localDate, opensAt, closesAt string, leadMinutes int) ([]contract.SlotSnapshot, error) {
	if leadMinutes < 0 || leadMinutes > 120 {
		return nil, fmt.Errorf("lead_minutes outside 0..120")
	}
	parse := func(clock string) (time.Time, error) {
		const layout = localDateFormat + " 15:04"
		value := localDate + " " + clock
		at, err := time.ParseInLocation(layout, value, shanghai)
		if err != nil || at.Format(layout) != value || at.Year() < 1 {
			return time.Time{}, fmt.Errorf("invalid local date or time")
		}
		return at, nil
	}
	start, err := parse(opensAt)
	if err != nil {
		return nil, err
	}
	end, err := parse(closesAt)
	if err != nil {
		return nil, err
	}
	if end.Equal(start) {
		return nil, fmt.Errorf("opening and closing times are equal")
	}
	if end.Before(start) {
		end = end.AddDate(0, 0, 1)
	}
	if end.Year() > 9999 || end.Sub(start)%windowDuration != 0 {
		return nil, fmt.Errorf("business interval must contain complete 15 minute windows")
	}
	start, end = start.UTC(), end.UTC()
	count := int(end.Sub(start) / windowDuration)
	if count == 0 {
		return nil, fmt.Errorf("business interval has no complete window")
	}
	rows := make([]contract.SlotSnapshot, 0, count)
	lead := time.Duration(leadMinutes) * time.Minute
	for range count {
		finish := start.Add(windowDuration)
		rows = append(rows, contract.SlotSnapshot{StartsAt: start, EndsAt: finish, DispatchAt: start.Add(-lead)})
		start = finish
	}
	return rows, nil
}

func admissionCode(slot contract.SlotSnapshot, mode contract.DeliveryMode, now time.Time, accepting bool, used, capacity int64) contract.ErrorCode {
	return admissionWithin(slot, mode, now, accepting, used, capacity, defaultBookingDays)
}

func admissionWithin(slot contract.SlotSnapshot, mode contract.DeliveryMode, now time.Time, accepting bool, used, capacity int64, bookingDays int) contract.ErrorCode {
	switch mode {
	case contract.DeliveryModeAsap:
		if slot.EndsAt.Sub(now) < asapMinimumLeft {
			return contract.ErrorCodeSlotExpired
		}
	case contract.DeliveryModeScheduled:
		if !now.Before(slot.DispatchAt) || slot.StartsAt.After(now.In(shanghai).AddDate(0, 0, bookingDays)) {
			return contract.ErrorCodeSlotExpired
		}
	default:
		return contract.ErrorCodeBadRequest
	}
	if !accepting {
		return contract.ErrorCodeSlotExpired
	}
	if used >= capacity {
		return contract.ErrorCodeSlotFull
	}
	return ""
}
