package bootstrap

import (
	"campus.local/xiaoling/internal/catalog"
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/fulfillment"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/internal/schedule"
	"campus.local/xiaoling/internal/settlement"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
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
	if o.Pool == nil || o.Clock == nil || o.Clock.Now().IsZero() {
		return nil, errors.New("application requires database and clock")
	}
	policy, err := identity.NewEntryPolicy(o.UserOrigin, o.RiderOrigin, o.AdminOrigin)
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
	return platform.NewHandler(mux, o.Pool.Ping, o.Logger, platform.DefaultBodyLimit), nil
}
