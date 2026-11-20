package bootstrap

import "testing"

func TestStaffOIDCRequiresCompleteHTTPSConfiguration(t *testing.T) {
	env := map[string]string{"APP_ENV": "test", "DATABASE_URL": "postgres://fixture@127.0.0.1/test"}
	load := func() error {
		_, err := LoadConfig(func(key string) string { return env[key] })
		return err
	}
	t.Run("absent", func(t *testing.T) {
		if err := load(); err != nil {
			t.Fatal(err)
		}
	})
	env["STAFF_OIDC_ISSUER"] = "https://idp.example.test"
	t.Run("partial", func(t *testing.T) {
		if err := load(); err == nil {
			t.Fatal("partial staff OIDC configuration was accepted")
		}
	})
	for key, value := range map[string]string{
		"STAFF_OIDC_AUTH_URL": "http://idp.example.test/authorize", "STAFF_OIDC_TOKEN_URL": "https://idp.example.test/token",
		"STAFF_OIDC_JWKS_URL": "https://idp.example.test/jwks", "STAFF_OIDC_CLIENT_ID": "fixture",
		"STAFF_OIDC_CLIENT_SECRET": "fixture", "STAFF_OIDC_MFA_CONTEXT": "fixture-mfa",
	} {
		env[key] = value
	}
	t.Run("plaintext", func(t *testing.T) {
		if err := load(); err == nil {
			t.Fatal("plaintext staff OIDC endpoint was accepted")
		}
	})
	env["STAFF_OIDC_AUTH_URL"] = "https://idp.example.test/authorize"
	t.Run("complete", func(t *testing.T) {
		if err := load(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOrderAndDispatchSwitchesAreIndependentAndDefaultOff(t *testing.T) {
	for _, flags := range []struct {
		orders, dispatch         string
		wantOrders, wantDispatch bool
	}{
		{"", "", false, false}, {"true", "false", true, false}, {"false", "true", false, true}, {"true", "true", true, true},
	} {
		env := map[string]string{"APP_ENV": "test", "DATABASE_URL": "postgres://fixture@127.0.0.1/test", "ORDERS_ENABLED": flags.orders, "DISPATCH_ENABLED": flags.dispatch}
		cfg, err := LoadConfig(func(k string) string { return env[k] })
		if err != nil || cfg.OrdersEnabled != flags.wantOrders || cfg.DispatchEnabled != flags.wantDispatch {
			t.Fatalf("independent switches: %+v %v", cfg, err)
		}
	}
}

func TestTradingFlagsRejectImplicitEnablement(t *testing.T) {
	for _, key := range []string{"COD_ENABLED", "BALANCE_ENABLED", "WECHAT_ENABLED", "ORDERS_ENABLED", "DISPATCH_ENABLED"} {
		env := map[string]string{"APP_ENV": "test", "DATABASE_URL": "postgres://fixture@127.0.0.1/test", "COD_ENABLED": "false", "BALANCE_ENABLED": "false"}
		env[key] = "yes"
		if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("unsafe %s accepted", key)
		}
	}
}

func TestConfigFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"missing-db", map[string]string{"APP_ENV": "production"}},
		{"insecure-db", map[string]string{"APP_ENV": "production", "HTTP_ADDR": "127.0.0.1:8080", "DATABASE_URL": "postgres://api@db.invalid/db?sslmode=disable"}},
		{"wildcard-listen", map[string]string{"APP_ENV": "development", "HTTP_ADDR": "0.0.0.0:8080", "DATABASE_URL": "postgres://api@localhost/db?sslmode=disable"}},
		{"fake-auth", map[string]string{"APP_ENV": "production", "HTTP_ADDR": "127.0.0.1:8080", "ALLOW_TEST_AUTH": "true", "DATABASE_URL": "postgres://api@db.invalid/db?sslmode=verify-full"}},
		{"unknown-environment", map[string]string{"APP_ENV": "prodution", "DATABASE_URL": "postgres://api@localhost/db?sslmode=disable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadConfig(func(key string) string { return tc.env[key] }); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestProductionRequiresExplicitSecureConfig(t *testing.T) {
	env := map[string]string{"APP_ENV": "production", "HTTP_ADDR": "127.0.0.1:8080", "DATABASE_URL": "postgres://api@db.invalid/db?sslmode=verify-full"}
	c, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil || c.Address != "127.0.0.1:8080" {
		t.Fatalf("secure config rejected: %v", err)
	}
}

func TestLocalTestAuthRequiresLocalOriginsAndDatabase(t *testing.T) {
	env := map[string]string{
		"APP_ENV": "development", "DATABASE_URL": "postgres://fixture@127.0.0.1/test",
		"USER_ORIGIN": "http://user.localhost:5174", "RIDER_ORIGIN": "http://rider.localhost:5174",
		"ADMIN_ORIGIN": "http://admin.localhost:5174",
	}
	load := func() error { _, err := LoadConfig(func(k string) string { return env[k] }); return err }
	env["ALLOW_TEST_AUTH"] = "true"
	if err := load(); err != nil {
		t.Fatalf("local-only test setup rejected: %v", err)
	}
	env["ADMIN_ORIGIN"] = "https://admin.example.test"
	if err := load(); err == nil {
		t.Fatal("test credentials allowed on a nonlocal origin")
	}
	env["ADMIN_ORIGIN"] = "http://admin.localhost:5174"
	env["DATABASE_URL"] = "postgres://fixture@192.0.2.10/test"
	if err := load(); err == nil {
		t.Fatal("test credentials allowed against a nonlocal database")
	}
	env["DATABASE_URL"] = "postgres://fixture@127.0.0.1/test?host=192.0.2.10"
	if err := load(); err == nil {
		t.Fatal("database host override allowed in test mode")
	}
	env["DATABASE_URL"] = "postgres://fixture@127.0.0.1/test"
	env["ALLOW_TEST_AUTH"] = "yes"
	if err := load(); err == nil {
		t.Fatal("implicit test auth flag accepted")
	}
}
