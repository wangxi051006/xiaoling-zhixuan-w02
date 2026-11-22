package fulfillment

import (
	"context"
	"errors"
	"math"

	"campus.local/xiaoling/internal/identity"
	"github.com/jackc/pgx/v5"
)

var ErrOrderLimit = errors.New("order acceptance limit reached")
var ErrBatchCOD = errors.New("cash on delivery requires individual confirmation")
var ErrCODClaimExclusive = errors.New("cash on delivery requires an exclusive delivery assignment")

type orderLimits struct {
	MerchantBatch int   `json:"merchant_batch_limit"`
	RiderActive   int   `json:"rider_active_limit"`
	Version       int64 `json:"version"`
}

type orderLimitsUpdate struct {
	MerchantBatch   int   `json:"merchant_batch_limit"`
	RiderActive     int   `json:"rider_active_limit"`
	ExpectedVersion int64 `json:"expected_version"`
}

type availability struct {
	Available bool `json:"available"`
}

func readOrderLimits(ctx context.Context, tx pgx.Tx, station string, lock bool) (limits orderLimits, err error) {
	query := `SELECT merchant_batch_limit,rider_active_limit,order_limits_version FROM stations WHERE id=$1`
	if lock {
		query += " FOR SHARE"
	}
	err = tx.QueryRow(ctx, query, station).Scan(&limits.MerchantBatch, &limits.RiderActive, &limits.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return limits, err
}

func updateOrderLimits(ctx context.Context, tx pgx.Tx, station string, input orderLimitsUpdate) (limits orderLimits, err error) {
	if input.MerchantBatch < 1 || input.MerchantBatch > math.MaxInt32 ||
		input.RiderActive < 1 || input.RiderActive > math.MaxInt32 ||
		input.ExpectedVersion < 1 || input.ExpectedVersion >= maxFen {
		return limits, ErrInvalid
	}
	err = tx.QueryRow(ctx, `UPDATE stations SET merchant_batch_limit=$2,rider_active_limit=$3,order_limits_version=order_limits_version+1
		WHERE id=$1 AND order_limits_version=$4 RETURNING merchant_batch_limit,rider_active_limit,order_limits_version`,
		station, input.MerchantBatch, input.RiderActive, input.ExpectedVersion).Scan(&limits.MerchantBatch, &limits.RiderActive, &limits.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrInvalidState
	}
	return limits, err
}

type riderCapacity struct {
	Active    int  `json:"active"`
	Limit     int  `json:"limit"`
	Remaining int  `json:"remaining"`
	CODActive bool `json:"cod_active"`
}

func readRiderCapacity(ctx context.Context, tx pgx.Tx, station, rider string) (capacity riderCapacity, err error) {
	err = tx.QueryRow(ctx, `SELECT rider_active_limit, active.count, active.has_cod
		FROM stations CROSS JOIN (
			SELECT count(*), COALESCE(bool_or(o.method='COD'),false) AS has_cod
			FROM delivery_tasks t JOIN orders o ON o.id=t.order_id
			WHERE t.rider_id=$2 AND t.state IN ('ASSIGNED','PICKED_UP','RETURNING')
		) active WHERE stations.id=$1`, station, rider).Scan(&capacity.Limit, &capacity.Active, &capacity.CODActive)
	if !capacity.CODActive {
		capacity.Remaining = max(0, capacity.Limit-capacity.Active)
	}
	return capacity, err
}

// readAvailability exposes the same column the PUT handler writes, so the
// rider screen shows a server-confirmed state instead of guessing.
func readAvailability(ctx context.Context, port RiderStatePort, tx pgx.Tx, actor identity.Session) (availability, error) {
	if port == nil {
		return availability{}, ErrNotReady
	}
	available, err := port.RiderAvailability(ctx, tx, actor.AccountID, actor.StationID)
	if err != nil {
		return availability{}, err
	}
	return availability{Available: available}, nil
}
