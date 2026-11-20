package bootstrap

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"campus.local/xiaoling/internal/identity"
)

type Config struct {
	Environment                          string
	Address                              string
	DatabaseURL                          string
	UserOrigin, RiderOrigin, AdminOrigin string
	CODEnabled, BalanceEnabled           bool
	OrdersEnabled, DispatchEnabled       bool
	AllowTestAuth                        bool
	EmailSignupEnabled                   bool
	EmailNotificationsEnabled            bool
	Emails                               *EmailSettings
	StaffOIDC                            *identity.StaffOIDCConfig
}

// EmailSettings holds the dedicated sender identity. Secrets come only from
// controlled server configuration and are never logged or returned.
type EmailSettings struct {
	CodeKey      string
	SMTPAddr     string
	SMTPFrom     string
	SMTPUsername string
	SMTPPassword string
	ImplicitTLS  bool
}

func SameDatabaseUser(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	return errA == nil && errB == nil && a.User != nil && b.User != nil && a.User.Username() == b.User.Username()
}

func LoadConfig(get func(string) string) (Config, error) {
	var cfg Config
	if get == nil {
		return cfg, errors.New("configuration source is required")
	}
	cfg.Environment = get("APP_ENV")
	if cfg.Environment != "development" && cfg.Environment != "production" && cfg.Environment != "test" {
		return Config{}, errors.New("APP_ENV must be development, production or test")
	}
	cfg.Address = get("HTTP_ADDR")
	if cfg.Address == "" && cfg.Environment != "production" {
		cfg.Address = "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(cfg.Address)
	p, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || portErr != nil || p == 0 || !net.ParseIP(host).IsLoopback() {
		return Config{}, errors.New("HTTP_ADDR must use a loopback IP and a valid port")
	}
	cfg.DatabaseURL = get("DATABASE_URL")
	db, err := url.Parse(cfg.DatabaseURL)
	if err != nil || (db.Scheme != "postgres" && db.Scheme != "postgresql") || db.Hostname() == "" || db.User == nil || db.User.Username() == "" || strings.TrimPrefix(db.Path, "/") == "" || db.Fragment != "" {
		return Config{}, errors.New("DATABASE_URL must explicitly identify a PostgreSQL host, user and database")
	}
	if cfg.Environment == "production" {
		query, err := url.ParseQuery(db.RawQuery)
		if err != nil || len(query["sslmode"]) != 1 || query.Get("sslmode") != "verify-full" {
			return Config{}, errors.New("production database requires sslmode=verify-full")
		}
		for key, values := range query {
			if len(values) != 1 || (key != "sslmode" && key != "sslrootcert" && key != "connect_timeout") {
				return Config{}, errors.New("unsupported production database URL parameter")
			}
		}
	}
	cfg.UserOrigin, cfg.RiderOrigin, cfg.AdminOrigin = get("USER_ORIGIN"), get("RIDER_ORIGIN"), get("ADMIN_ORIGIN")
	switch get("ALLOW_TEST_AUTH") {
	case "", "false":
	case "true":
		if cfg.Environment == "production" {
			return Config{}, errors.New("test authentication is forbidden in production")
		}
		if _, err := identity.NewLocalEntryPolicy(cfg.UserOrigin, cfg.RiderOrigin, cfg.AdminOrigin); err != nil {
			return Config{}, err
		}
		if !net.ParseIP(db.Hostname()).IsLoopback() {
			return Config{}, errors.New("test authentication requires a loopback database IP")
		}
		query, err := url.ParseQuery(db.RawQuery)
		if err != nil {
			return Config{}, errors.New("invalid local database URL parameters")
		}
		for key, values := range query {
			if len(values) != 1 || (key != "sslmode" && key != "connect_timeout") {
				return Config{}, errors.New("unsupported local test database URL parameter")
			}
		}
		cfg.AllowTestAuth = true
	default:
		return Config{}, errors.New("ALLOW_TEST_AUTH must be true or false")
	}
	for key, target := range map[string]*bool{"COD_ENABLED": &cfg.CODEnabled, "BALANCE_ENABLED": &cfg.BalanceEnabled, "ORDERS_ENABLED": &cfg.OrdersEnabled, "DISPATCH_ENABLED": &cfg.DispatchEnabled} {
		switch get(key) {
		case "", "false":
		case "true":
			*target = true
		default:
			return Config{}, errors.New("business switches require explicit true or false")
		}
	}
	if flag := get("WECHAT_ENABLED"); flag != "" && flag != "false" {
		return Config{}, errors.New("WECHAT is unavailable")
	}
	for key, target := range map[string]*bool{"EMAIL_SIGNUP_ENABLED": &cfg.EmailSignupEnabled, "EMAIL_NOTIFICATIONS_ENABLED": &cfg.EmailNotificationsEnabled} {
		switch get(key) {
		case "", "false":
		case "true":
			*target = true
		default:
			return Config{}, errors.New("email switches require explicit true or false")
		}
	}
	settings, err := loadEmailSettings(get)
	if err != nil {
		return Config{}, err
	}
	cfg.Emails = settings
	if cfg.Emails == nil && (cfg.EmailSignupEnabled || cfg.EmailNotificationsEnabled) {
		return Config{}, errors.New("email switches require EMAIL_CODE_KEY and EMAIL_SMTP_ADDR/EMAIL_SMTP_FROM")
	}
	staff := identity.StaffOIDCConfig{
		Issuer: get("STAFF_OIDC_ISSUER"), AuthURL: get("STAFF_OIDC_AUTH_URL"),
		TokenURL: get("STAFF_OIDC_TOKEN_URL"), JWKSURL: get("STAFF_OIDC_JWKS_URL"),
		ClientID: get("STAFF_OIDC_CLIENT_ID"), ClientSecret: get("STAFF_OIDC_CLIENT_SECRET"),
		MFAContext: get("STAFF_OIDC_MFA_CONTEXT"),
	}
	if staff != (identity.StaffOIDCConfig{}) {
		if err := staff.Validate(); err != nil {
			return Config{}, err
		}
		cfg.StaffOIDC = &staff
	}
	return cfg, nil
}

// loadEmailSettings fails closed: a partial sender configuration is an error and
// an absent one leaves every email capability switched off.
func loadEmailSettings(get func(string) string) (*EmailSettings, error) {
	key := strings.TrimSpace(get("EMAIL_CODE_KEY"))
	addr := strings.TrimSpace(get("EMAIL_SMTP_ADDR"))
	from := strings.TrimSpace(get("EMAIL_SMTP_FROM"))
	user := get("EMAIL_SMTP_USERNAME")
	pass := get("EMAIL_SMTP_PASSWORD")
	implicit := get("EMAIL_SMTP_IMPLICIT_TLS")
	if key == "" && addr == "" && from == "" && user == "" && pass == "" && implicit == "" {
		return nil, nil
	}
	if len(key) < 32 || strings.ContainsAny(key, " \t\r\n") {
		return nil, errors.New("EMAIL_CODE_KEY must be at least 32 bytes without whitespace")
	}
	if addr == "" || from == "" || (user == "") != (pass == "") {
		return nil, errors.New("EMAIL_SMTP_ADDR, EMAIL_SMTP_FROM and a username/password pair are required together")
	}
	settings := &EmailSettings{CodeKey: key, SMTPAddr: addr, SMTPFrom: from, SMTPUsername: user, SMTPPassword: pass}
	switch implicit {
	case "", "false":
	case "true":
		settings.ImplicitTLS = true
	default:
		return nil, errors.New("EMAIL_SMTP_IMPLICIT_TLS requires explicit true or false")
	}
	return settings, nil
}
