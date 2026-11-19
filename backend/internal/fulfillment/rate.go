package fulfillment

import (
	"campus.local/xiaoling/internal/platform"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net"
	"net/netip"
	"time"
)

// Count authenticated new-command attempts, including subsequent business
// failures. Commit this security state before business locks so all customers
// behind one source do not serialize their inventory/settlement transactions.
// Successful idempotency replays return before reaching this method.
func (s *Service) admitOrderAttempt(ctx context.Context, accountID, remote string) error {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ErrInvalid
	}
	hash := sha256.Sum256([]byte(ip.Unmap().String()))
	buckets := []struct {
		key   string
		limit int
	}{{"account:" + accountID, 10}, {fmt.Sprintf("source:%x", hash[:]), 30}}
	at := s.Clock.Now().UTC().Truncate(time.Minute)
	return platform.InTransaction(ctx, s.Pool, func(tx pgx.Tx) error {
		for _, b := range buckets {
			_, e := tx.Exec(ctx, `INSERT INTO order_rate_buckets(bucket_key,window_at,attempts) VALUES($1,$2,0) ON CONFLICT DO NOTHING`, b.key, at)
			if e != nil {
				return e
			}
			var window time.Time
			var attempts int
			e = tx.QueryRow(ctx, `SELECT window_at,attempts FROM order_rate_buckets WHERE bucket_key=$1 FOR UPDATE`, b.key).Scan(&window, &attempts)
			if e != nil {
				return e
			}
			if window.Equal(at) && attempts >= b.limit {
				return ErrThrottled
			}
			if !window.Equal(at) {
				attempts = 0
			}
			if _, e = tx.Exec(ctx, `UPDATE order_rate_buckets SET window_at=$2,attempts=$3 WHERE bucket_key=$1`, b.key, at, attempts+1); e != nil {
				return e
			}
		}
		return nil
	})
}
