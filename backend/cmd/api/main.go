package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"campus.local/xiaoling/internal/bootstrap"
	"campus.local/xiaoling/internal/catalog"
	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/fulfillment"
	"campus.local/xiaoling/internal/identity"
	"campus.local/xiaoling/internal/notify"
	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/internal/schedule"
	"campus.local/xiaoling/internal/settlement"
	"campus.local/xiaoling/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger, os.Args[1:]); err != nil {
		logger.Error("startup_failed", "reason", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "migrate") {
		return errors.New("usage: api [migrate]")
	}
	migrate := len(args) == 1
	cfg, err := bootstrap.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	dsn := cfg.DatabaseURL
	if migrate {
		get := func(key string) string {
			if key == "DATABASE_URL" {
				return os.Getenv("MIGRATION_DATABASE_URL")
			}
			return os.Getenv(key)
		}
		migrationConfig, err := bootstrap.LoadConfig(get)
		if err != nil {
			return errors.New("invalid or absent MIGRATION_DATABASE_URL")
		}
		dsn = migrationConfig.DatabaseURL
		if cfg.Environment == "production" && bootstrap.SameDatabaseUser(cfg.DatabaseURL, dsn) {
			return errors.New("production requires distinct application and migration database users")
		}
	}
	pool, err := platform.NewPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if migrate {
		migrationCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := platform.Migrate(migrationCtx, pool, migrations.SQL); err != nil {
			return errors.New("database migration failed; inspect restricted database diagnostics")
		}
		logger.Info("migration_complete")
		return nil
	}
	clock := contract.SystemClock{}
	var staff *identity.StaffOIDC
	if cfg.StaffOIDC != nil {
		entryPolicy := identity.NewEntryPolicy
		if cfg.AllowTestAuth {
			entryPolicy = identity.NewLocalEntryPolicy
		}
		policy, err := entryPolicy(cfg.UserOrigin, cfg.RiderOrigin, cfg.AdminOrigin)
		if err != nil {
			return errors.New("invalid identity entry configuration")
		}
		staff, err = identity.NewStaffOIDC(*cfg.StaffOIDC, policy, clock)
		if err != nil {
			return err
		}
	}
	payments := settlement.PaymentPolicy{CODEnabled: cfg.CODEnabled, BalanceEnabled: cfg.BalanceEnabled}
	directory := identity.NewDirectory(pool)
	var emails *identity.EmailCodes
	var outbox *notify.Outbox
	if cfg.Emails != nil {
		sender, err := notify.NewSMTPSender(notify.SMTPConfig{Addr: cfg.Emails.SMTPAddr, From: cfg.Emails.SMTPFrom, Username: cfg.Emails.SMTPUsername, Password: cfg.Emails.SMTPPassword, ImplicitTLS: cfg.Emails.ImplicitTLS})
		if err != nil {
			return errors.New("email sender configuration is invalid")
		}
		codes, err := identity.NewEmailCodes(pool, &notify.VerificationGateway{Sender: sender, From: cfg.Emails.SMTPFrom}, clock, []byte(cfg.Emails.CodeKey))
		if err != nil {
			return errors.New("email verification configuration is invalid")
		}
		// Self-service sign-up stays switchable; email login and phone login do not depend on it.
		codes.AllowRegistration = cfg.EmailSignupEnabled
		emails = codes
		outbox = &notify.Outbox{Pool: pool, Clock: clock, Sender: sender, Enabled: cfg.EmailNotificationsEnabled}
	}
	var notifier fulfillment.EmailNotifier
	if outbox != nil {
		notifier = outbox
	}
	orders := &fulfillment.Service{Pool: pool, Clock: clock, Identity: directory, Catalog: catalog.Store{Clock: clock}, Schedule: schedule.Store{}, Settlement: settlement.Store{Policy: payments}, RiderState: directory, AlertSink: newOrderAlertLog(logger), EmailNotifier: notifier, Policy: payments, AcceptNewOrders: cfg.OrdersEnabled, AllowDispatch: cfg.DispatchEnabled}
	options := bootstrap.ApplicationOptions{Pool: pool, Clock: clock, UserOrigin: cfg.UserOrigin, RiderOrigin: cfg.RiderOrigin, AdminOrigin: cfg.AdminOrigin, Payments: payments, Emails: emails, Staff: staff, Logger: logger, Orders: orders, ProductImageDir: cfg.ProductImageDir}
	var handler http.Handler
	if cfg.AllowTestAuth {
		setupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		handler, err = bootstrap.NewLocalTestApplication(setupCtx, options)
	} else {
		handler, err = bootstrap.NewApplication(options)
	}
	if err != nil {
		if cfg.AllowTestAuth {
			return err
		}
		return errors.New("business application configuration is incomplete")
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	ticks := time.NewTicker(time.Second)
	go func() {
		defer close(workerDone)
		_ = bootstrap.RunOrderJobs(workerCtx, orders.RunDue, ticks.C, logger)
	}()
	defer func() { stopWorker(); ticks.Stop(); <-workerDone }()
	var emailTicks *time.Ticker
	if outbox != nil && outbox.Enabled {
		emailTicks = time.NewTicker(5 * time.Second)
		emailDone := make(chan struct{})
		go func() {
			defer close(emailDone)
			_ = bootstrap.RunEmailJobs(workerCtx, outbox.RunDue, emailTicks.C, logger)
		}()
		defer func() { stopWorker(); emailTicks.Stop(); <-emailDone }()
	}
	server := &http.Server{
		Addr: cfg.Address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
		ErrorLog: slog.NewLogLogger(slog.DiscardHandler, slog.LevelError),
	}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP listener failed")
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return errors.New("HTTP graceful shutdown timed out")
		}
	}
	return nil
}
