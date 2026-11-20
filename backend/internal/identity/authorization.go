package identity

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"campus.local/xiaoling/internal/contract"
)

type Entry string

const (
	UserEntry       Entry = "user"
	RiderEntry      Entry = "rider"
	AdminEntry      Entry = "admin"
	recentMFAWindow       = 15 * time.Minute
)

type entryOrigin struct{ origin, host string }

type EntryPolicy struct {
	origins map[Entry]entryOrigin
	local   bool
}

func NewEntryPolicy(user, rider, admin string) (*EntryPolicy, error) {
	return newEntryPolicy(user, rider, admin, false)
}

// NewLocalEntryPolicy keeps entry hosts distinct without requiring local TLS.
func NewLocalEntryPolicy(user, rider, admin string) (*EntryPolicy, error) {
	return newEntryPolicy(user, rider, admin, true)
}

func newEntryPolicy(user, rider, admin string, local bool) (*EntryPolicy, error) {
	policy := &EntryPolicy{origins: make(map[Entry]entryOrigin, 3), local: local}
	hosts := make(map[string]bool, 3)
	for entry, raw := range map[Entry]string{UserEntry: user, RiderEntry: rider, AdminEntry: admin} {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "https" && !(local && u.Scheme == "http")) || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return nil, errors.New("each entry requires a distinct canonical HTTPS origin")
		}
		if local && u.Hostname() != string(entry)+".localhost" {
			return nil, errors.New("test authentication requires user.localhost, rider.localhost and admin.localhost")
		}
		host := strings.ToLower(u.Host)
		if raw != u.Scheme+"://"+host || strings.HasSuffix(u.Hostname(), ".") || (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
			return nil, errors.New("entry origin is not canonical")
		}
		name := strings.ToLower(u.Hostname())
		if hosts[name] {
			return nil, errors.New("entry hosts must be distinct")
		}
		hosts[name] = true
		policy.origins[entry] = entryOrigin{raw, host}
	}
	return policy, nil
}

type AccessError int

func (e AccessError) Error() string { return http.StatusText(int(e)) }

func AccessStatus(err error) int {
	if err == nil {
		return 0
	}
	var access AccessError
	if errors.As(err, &access) {
		return int(access)
	}
	return http.StatusInternalServerError
}

func roleForEntry(entry Entry, role contract.Role) bool {
	switch entry {
	case UserEntry:
		return role == contract.RoleCustomer
	case RiderEntry:
		return role == contract.RoleRider
	case AdminEntry:
		return role == contract.RoleOperator || role == contract.RoleFinance || role == contract.RoleSecurityAdmin
	default:
		return false
	}
}

func (p *EntryPolicy) Authorize(r *http.Request, entry Entry, session Session, now time.Time, requireMFA bool, allowed ...contract.Role) error {
	config, ok := p.origins[entry]
	if !ok || !strings.EqualFold(r.Host, config.host) {
		return AccessError(http.StatusNotFound)
	}
	if session.AccountID == "" || session.StationID == "" || session.Entry != entry || !now.Before(session.ExpiresAt) {
		return AccessError(http.StatusUnauthorized)
	}
	if !slices.ContainsFunc(allowed, func(role contract.Role) bool {
		return roleForEntry(entry, role) && slices.Contains(session.Roles, role)
	}) {
		return AccessError(http.StatusForbidden)
	}
	if requireMFA && (session.MFAAt.IsZero() || session.MFAAt.After(now) || now.Sub(session.MFAAt) >= recentMFAWindow) {
		return AccessError(http.StatusForbidden)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		origins, tokens := r.Header.Values("Origin"), r.Header.Values("X-CSRF-Token")
		if len(origins) != 1 || origins[0] != config.origin || len(tokens) != 1 || session.CSRFToken == "" || subtle.ConstantTimeCompare([]byte(tokens[0]), []byte(session.CSRFToken)) != 1 {
			return AccessError(http.StatusForbidden)
		}
	}
	return nil
}
