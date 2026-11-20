package fulfillment

import (
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/settlement"
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"strings"
)

type RiderStatePort interface {
	RiderAvailability(context.Context, pgx.Tx, string, string) (bool, error)
	SetRiderAvailability(context.Context, pgx.Tx, string, string, bool) error
}

// Alert contains operational identifiers only. Receivers must deduplicate the
// stable IdempotencyKey because a crash after SendAlert can cause a retry.
type Alert struct {
	IdempotencyKey, Kind, StationID, OrderID string
	RecipientIDs                             []string
}
type AlertSink interface {
	SendAlert(context.Context, Alert) error
}

// EmailNotifier records a durable transactional email inside the order
// transaction. Delivery happens later, so a mail outage never blocks an order.
type EmailNotifier interface {
	NotifyOrder(ctx context.Context, tx pgx.Tx, kind, stationID, orderID, accountID string) error
}
type Service struct {
	Pool            *pgxpool.Pool
	Clock           contract.Clock
	Identity        contract.IdentityPort
	Catalog         contract.CatalogPort
	Schedule        contract.SchedulePort
	Settlement      contract.SettlementPort
	RiderState      RiderStatePort
	AlertSink       AlertSink
	EmailNotifier   EmailNotifier
	Policy          settlement.PaymentPolicy
	AcceptNewOrders bool
	AllowDispatch   bool
}
type HTTP struct {
	Service   *Service
	Authorize func(http.ResponseWriter, *http.Request, identity.Entry, bool, ...contract.Role) (identity.Session, bool)
}

func (h *HTTP) Register(mux *http.ServeMux) {
	user := []string{"POST /checkout/quote", "POST /orders", "GET /orders", "GET /orders/{id}", "POST /orders/{id}/cancel", "POST /orders/{id}/urge"}
	admin := []string{"GET /admin/delivery-fee", "PUT /admin/delivery-fee", "GET /admin/orders", "GET /admin/orders/{id}", "POST /admin/orders/{id}/prepare", "POST /admin/orders/{id}/dispatch", "POST /admin/orders/{id}/reclaim", "POST /admin/orders/{id}/cancel", "POST /admin/orders/{id}/restock", "POST /admin/orders/{id}/return-inspect", "POST /admin/orders/{id}/refund", "GET /admin/dashboard"}
	rider := []string{"PUT /rider/availability", "GET /rider/tasks", "GET /rider/tasks/{id}", "POST /rider/tasks/{id}/claim", "POST /rider/tasks/{id}/pickup", "POST /rider/tasks/{id}/deliver", "POST /rider/tasks/{id}/return", "GET /rider/history", "GET /rider/earnings", "GET /rider/messages", "POST /rider/messages/read"}
	add := func(route string, entry identity.Entry, mfa bool, roles ...contract.Role) {
		parts := strings.SplitN(route, " ", 2)
		mux.HandleFunc(parts[0]+" /api/v1"+parts[1], func(w http.ResponseWriter, r *http.Request) {
			if h.Authorize == nil {
				writeError(w, r, ErrNotReady)
				return
			}
			s, ok := h.Authorize(w, r, entry, mfa, roles...)
			if !ok {
				return
			}
			h.serve(w, r, s, route)
		})
	}
	for _, r := range user {
		add(r, identity.UserEntry, false, contract.RoleCustomer)
	}
	for _, r := range admin {
		roles := []contract.Role{contract.RoleOperator}
		if r == "GET /admin/dashboard" {
			roles = append(roles, contract.RoleFinance)
		}
		add(r, identity.AdminEntry, r == "POST /admin/orders/{id}/refund", roles...)
	}
	for _, r := range []string{
		"GET /admin/order-limits", "PUT /admin/order-limits",
		"POST /admin/order-batches/accept", "POST /admin/order-batches/ready",
	} {
		add(r, identity.AdminEntry, false, contract.RoleOperator)
	}
	for _, r := range rider {
		add(r, identity.RiderEntry, false, contract.RoleRider)
	}
	for _, r := range []string{"GET /rider/capacity", "POST /rider/task-batches/claim", "POST /rider/task-batches/deliver"} {
		add(r, identity.RiderEntry, false, contract.RoleRider)
	}
}
