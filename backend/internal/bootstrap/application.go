package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"campus.local/xiaoling/internal/catalog"
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/fulfillment"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/internal/schedule"
	"campus.local/xiaoling/internal/settlement"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ApplicationOptions struct {
	Pool                                 *pgxpool.Pool
	Clock                                contract.Clock
	UserOrigin, RiderOrigin, AdminOrigin string
	Payments                             settlement.PaymentPolicy
	OTP                                  *identity.OTP
	Emails                               *identity.EmailCodes
	Staff                                *identity.StaffOIDC
	Logger                               *slog.Logger
	Orders                               *fulfillment.Service
}

func NewApplication(o ApplicationOptions) (http.Handler, error) {
	return newApplication(context.Background(), o, false)
}

// NewLocalTestApplication is only for explicitly opted-in local development.
func NewLocalTestApplication(ctx context.Context, o ApplicationOptions) (http.Handler, error) {
	return newApplication(ctx, o, true)
}

func newApplication(ctx context.Context, o ApplicationOptions, localTest bool) (http.Handler, error) {
	if o.Pool == nil || o.Clock == nil || o.Clock.Now().IsZero() {
		return nil, errors.New("application requires database and clock")
	}
	entryPolicy := identity.NewEntryPolicy
	if localTest {
		cfg := o.Pool.Config().ConnConfig
		if !net.ParseIP(cfg.Host).IsLoopback() {
			return nil, errors.New("test authentication requires a loopback database")
		}
		for _, fallback := range cfg.Fallbacks {
			if !net.ParseIP(fallback.Host).IsLoopback() {
				return nil, errors.New("test authentication requires loopback database fallbacks")
			}
		}
		entryPolicy = identity.NewLocalEntryPolicy
	}
	policy, err := entryPolicy(o.UserOrigin, o.RiderOrigin, o.AdminOrigin)
	if err != nil {
		return nil, err
	}
	sessions := identity.NewSessions(o.Pool, o.Clock)
	auth := identity.NewIdentityHTTP(policy, sessions, identity.NewDirectory(o.Pool), o.OTP, o.Staff, o.Logger).WithEmailCodes(o.Emails)
	mux := http.NewServeMux()
	auth.Register(mux)
	(&catalog.HTTP{Pool: o.Pool, Authorize: auth.Authenticated}).Register(mux)
	(&schedule.HTTP{Pool: o.Pool, Clock: o.Clock, Store: schedule.Store{}, Authorize: auth.Authenticated}).Register(mux)
	var refunds settlement.RefundCommands
	if o.Orders != nil {
		refunds = o.Orders
	}
	(&settlement.HTTP{Pool: o.Pool, Clock: o.Clock, Store: settlement.Store{Policy: o.Payments}, Refunds: refunds, Authorize: auth.Authenticated}).Register(mux)
	if o.Orders != nil {
		if o.Orders.Pool != o.Pool || o.Orders.Clock == nil || o.Orders.Identity == nil || o.Orders.Catalog == nil || o.Orders.Schedule == nil || o.Orders.Settlement == nil || o.Orders.RiderState == nil {
			return nil, errors.New("orders require the application database, clock and owning-domain ports")
		}
		(&fulfillment.HTTP{Service: o.Orders, Authorize: auth.Authenticated}).Register(mux)
	}
	if localTest {
		if err := auth.RegisterLocalTestLogin(ctx, mux); err != nil {
			return nil, err
		}
	}
	return platform.NewHandler(mux, o.Pool.Ping, o.Logger, platform.DefaultBodyLimit), nil
}
