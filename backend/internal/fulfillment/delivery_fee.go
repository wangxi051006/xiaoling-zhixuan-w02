package fulfillment

import (
	"context"
	"errors"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

type deliveryFee struct {
	AmountFen        int64 `json:"delivery_fee_fen"`
	Version          int64 `json:"version"`
	CODCommissionBPS int   `json:"cod_commission_bps"`
}

type deliveryFeeUpdate struct {
	AmountFen        *int64 `json:"delivery_fee_fen"`
	ExpectedVersion  int64  `json:"expected_version"`
	CODCommissionBPS *int   `json:"cod_commission_bps,omitempty"`
}

func readDeliveryFee(ctx context.Context, tx pgx.Tx, station string) (fee deliveryFee, err error) {
	err = tx.QueryRow(ctx, `SELECT delivery_fee_fen,delivery_fee_version,cod_commission_bps FROM stations WHERE id=$1`, station).Scan(&fee.AmountFen, &fee.Version, &fee.CODCommissionBPS)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return fee, err
}

func updateDeliveryFee(ctx context.Context, tx pgx.Tx, station string, input deliveryFeeUpdate) (fee deliveryFee, err error) {
	if input.AmountFen == nil || *input.AmountFen < 0 || *input.AmountFen > maxFen || input.ExpectedVersion < 1 || input.ExpectedVersion >= maxFen {
		return fee, ErrInvalid
	}
	if input.CODCommissionBPS != nil && (*input.CODCommissionBPS < 0 || *input.CODCommissionBPS > 10000) {
		return fee, ErrInvalid
	}
	err = tx.QueryRow(ctx, `UPDATE stations SET delivery_fee_fen=$2, delivery_fee_version=delivery_fee_version+1,
		cod_commission_bps=COALESCE($4,cod_commission_bps)
		WHERE id=$1 AND delivery_fee_version=$3 RETURNING delivery_fee_fen,delivery_fee_version,cod_commission_bps`,
		station, *input.AmountFen, input.ExpectedVersion, input.CODCommissionBPS).Scan(&fee.AmountFen, &fee.Version, &fee.CODCommissionBPS)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrInvalidState
	}
	return fee, err
}

func withDeliveryFee(ctx context.Context, tx pgx.Tx, station string, items contract.PricedLines) (contract.PricedLines, error) {
	fee, err := readDeliveryFee(ctx, tx, station)
	if err != nil {
		return contract.PricedLines{}, err
	}
	items.DeliveryFeeFen = fee.AmountFen
	if items.SubtotalFen < 0 || items.SubtotalFen > maxFen-fee.AmountFen {
		return contract.PricedLines{}, ErrInvalid
	}
	items.TotalFen = items.SubtotalFen + fee.AmountFen
	return items, validatePrices(items)
}
