package schedule

import (
	"context"
	"errors"
	"time"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

var (
	ErrSlotFull     = errors.New("slot is full")
	ErrSlotExpired  = errors.New("slot is outside the booking window or closed")
	ErrSlotConflict = errors.New("slot state conflict")
	ErrInvalid      = errors.New("invalid slot request")
	ErrNotFound     = errors.New("slot not found")
	ErrUnavailable  = errors.New("schedule transaction unavailable")
)

type Store struct {
	// BookingDays overrides the pilot default when positive.
	BookingDays int
}

var _ contract.SchedulePort = Store{}

type slotRow struct {
	contract.SlotSnapshot
	Capacity, Used int64
	Accepting      bool
	UpdatedAt      time.Time
}

const slotColumns = `id,station_id,starts_at,ends_at,dispatch_at,version,capacity,used,accepting,updated_at`

type scanner interface{ Scan(...any) error }

func scanSlot(row scanner) (s slotRow, err error) {
	err = row.Scan(&s.ID, &s.StationID, &s.StartsAt, &s.EndsAt, &s.DispatchAt, &s.Version,
		&s.Capacity, &s.Used, &s.Accepting, &s.UpdatedAt)
	s.StartsAt, s.EndsAt, s.DispatchAt, s.UpdatedAt = s.StartsAt.UTC(), s.EndsAt.UTC(), s.DispatchAt.UTC(), s.UpdatedAt.UTC()
	return
}

func (s Store) days() int {
	if s.BookingDays > 0 {
		return s.BookingDays
	}
	return defaultBookingDays
}

func (s Store) admission(row slotRow, mode contract.DeliveryMode, now time.Time) error {
	switch admissionWithin(row.SlotSnapshot, mode, now, row.Accepting, row.Used, row.Capacity, s.days()) {
	case "":
		return nil
	case contract.ErrorCodeSlotExpired:
		return ErrSlotExpired
	case contract.ErrorCodeSlotFull:
		return ErrSlotFull
	default:
		return ErrInvalid
	}
}

func (s Store) SelectSlot(ctx context.Context, tx pgx.Tx, stationID string, mode contract.DeliveryMode, requestedSlotID string, now time.Time) (contract.SlotSnapshot, error) {
	if tx == nil {
		return contract.SlotSnapshot{}, ErrUnavailable
	}
	if !validNow(now) || !validText(stationID, 128) || (requestedSlotID != "" && !validText(requestedSlotID, 128)) {
		return contract.SlotSnapshot{}, ErrInvalid
	}
	var query string
	var argument any
	switch {
	case mode == contract.DeliveryModeScheduled && requestedSlotID != "":
		query, argument = ` AND id=$2`, requestedSlotID
	case mode == contract.DeliveryModeAsap && requestedSlotID == "":
		threshold := now.Add(asapMinimumLeft)
		// PostgreSQL stores microseconds; round the lower bound up, not down.
		argument = threshold.Add(time.Microsecond - time.Nanosecond).Truncate(time.Microsecond)
		query = ` AND accepting AND ends_at >= $2 ORDER BY (used < capacity) DESC, starts_at LIMIT 1`
	default:
		return contract.SlotSnapshot{}, ErrInvalid
	}
	row, err := scanSlot(tx.QueryRow(ctx, `SELECT `+slotColumns+` FROM delivery_slots WHERE station_id=$1`+query, stationID, argument))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrSlotExpired
	}
	if err != nil {
		return contract.SlotSnapshot{}, err
	}
	if err := s.admission(row, mode, now); err != nil {
		return contract.SlotSnapshot{}, err
	}
	return row.SlotSnapshot, nil
}

func lockSlot(ctx context.Context, tx pgx.Tx, id string) (slotRow, error) {
	return scanSlot(tx.QueryRow(ctx, `SELECT `+slotColumns+` FROM delivery_slots WHERE id=$1 FOR UPDATE`, id))
}

type reservation struct {
	slotID     string
	mode       contract.DeliveryMode
	reservedAt time.Time
	releasedAt *time.Time
}

func findReservation(ctx context.Context, tx pgx.Tx, orderID string) (r reservation, err error) {
	err = tx.QueryRow(ctx, `SELECT slot_id,mode,reserved_at,released_at FROM slot_reservations WHERE order_id=$1`, orderID).
		Scan(&r.slotID, &r.mode, &r.reservedAt, &r.releasedAt)
	return
}

func (r reservation) replay(slotID string, mode contract.DeliveryMode) error {
	if r.slotID != slotID || r.mode != mode || r.releasedAt != nil {
		return ErrSlotConflict
	}
	return nil
}

func (s Store) ReserveSlot(ctx context.Context, tx pgx.Tx, slotID, orderID string, mode contract.DeliveryMode, now time.Time) error {
	if tx == nil {
		return ErrUnavailable
	}
	if !validNow(now) || !validText(slotID, 128) || !validText(orderID, 128) || (mode != contract.DeliveryModeAsap && mode != contract.DeliveryModeScheduled) {
		return ErrInvalid
	}
	row, err := lockSlot(ctx, tx, slotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSlotExpired
	}
	if err != nil {
		return err
	}
	r, err := findReservation(ctx, tx, orderID)
	if err == nil {
		return r.replay(slotID, mode)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err := s.admission(row, mode, now); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `INSERT INTO slot_reservations(order_id,slot_id,mode,reserved_at)
		VALUES($1,$2,$3,$4) ON CONFLICT(order_id) DO NOTHING`, orderID, slotID, mode, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		r, err := findReservation(ctx, tx, orderID)
		if err != nil {
			return err
		}
		return r.replay(slotID, mode)
	}
	result, err = tx.Exec(ctx, `UPDATE delivery_slots SET used=used+1 WHERE id=$1 AND accepting AND used<capacity`, slotID)
	if err == nil && result.RowsAffected() != 1 {
		return ErrSlotConflict
	}
	return err
}

// ReleaseSlot is only for an order that the caller has locked and found eligible for release.
func (Store) ReleaseSlot(ctx context.Context, tx pgx.Tx, orderID string, now time.Time) error {
	if tx == nil {
		return ErrUnavailable
	}
	if !validNow(now) || !validText(orderID, 128) {
		return ErrInvalid
	}
	r, err := findReservation(ctx, tx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSlotConflict
	}
	if err != nil {
		return err
	}
	// Acquire the slot before changing any reservation, as ReserveSlot does.
	row, err := lockSlot(ctx, tx, r.slotID)
	if err != nil {
		return err
	}
	r, err = findReservation(ctx, tx, orderID)
	if err != nil || r.releasedAt != nil {
		return err
	}
	if !now.Before(row.EndsAt) {
		return ErrSlotExpired
	}
	if now.Before(r.reservedAt) || row.Used == 0 {
		return ErrSlotConflict
	}
	result, err := tx.Exec(ctx, `UPDATE slot_reservations SET released_at=$2 WHERE order_id=$1 AND released_at IS NULL`, orderID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrSlotConflict
	}
	result, err = tx.Exec(ctx, `UPDATE delivery_slots SET used=used-1 WHERE id=$1 AND used>0`, row.ID)
	if err == nil && result.RowsAffected() != 1 {
		return ErrSlotConflict
	}
	return err
}

func validNow(now time.Time) bool {
	return !now.IsZero() && now.Year() >= 1 && now.Year() <= 9999
}
