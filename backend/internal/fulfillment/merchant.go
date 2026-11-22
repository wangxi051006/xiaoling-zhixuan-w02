package fulfillment

import (
	"context"
	"errors"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"github.com/jackc/pgx/v5"
)

var errMixedMerchants = errors.New("an order must belong to one merchant")

func checkoutMerchant(ctx context.Context, tx pgx.Tx, station string, lines []contract.PricedLine) (string, error) {
	products := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		products[line.ProductID] = struct{}{}
	}
	ids := make([]string, 0, len(products))
	for id := range products {
		ids = append(ids, id)
	}
	rows, err := tx.Query(ctx, `SELECT COALESCE(merchant_id, '') FROM products
		WHERE station_id=$1 AND id=ANY($2) ORDER BY id FOR SHARE`, station, ids)
	if err != nil {
		return "", err
	}
	owners, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	if len(owners) != len(products) || len(owners) == 0 {
		return "", ErrNotFound
	}
	for _, owner := range owners[1:] {
		if owner != owners[0] {
			return "", errMixedMerchants
		}
	}
	return owners[0], nil
}

type merchantOrder struct {
	Order
	Accepted bool `json:"merchant_accepted"`
}

func (s *Service) merchantOrders(ctx context.Context, tx pgx.Tx, actor identity.Session, p pageInput, id string) (any, error) {
	merchantID, err := identity.MerchantID(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	if id != "" && !validID(id) {
		return nil, ErrInvalid
	}
	rows, err := tx.Query(ctx, `SELECT id, (merchant_accepted_at IS NOT NULL OR stock_status='SORTED')
		FROM orders WHERE station_id=$1 AND merchant_id=$2 AND id>$3 AND ($4='' OR id=$4)
		ORDER BY id LIMIT $5 FOR SHARE`, actor.StationID, merchantID, p.Cursor, id, p.Limit+1)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item merchantOrder, err error) {
		err = row.Scan(&item.ID, &item.Accepted)
		return
	})
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Order, err = s.Order(ctx, tx, actor, items[i].ID, true)
		if err != nil {
			return nil, err
		}
	}
	if id != "" {
		if len(items) == 0 {
			return nil, ErrNotFound
		}
		return items[0], nil
	}
	return nextPage(items, p, func(item merchantOrder) string { return item.ID }), nil
}
