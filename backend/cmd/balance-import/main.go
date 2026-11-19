package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"campus.local/xiaoling/internal/settlement"
	"github.com/jackc/pgx/v5"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("balance-import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("manifest", "", "path to the approved JSON manifest")
	approvedDigest := flags.String("sha256", "", "SHA-256 from the independent approval record")
	approval := flags.String("approval", "", "independent approval identifier")
	approver := flags.String("approver", "", "approving finance identity")
	executor := flags.String("executor", "", "authorized execution identity")
	apply := flags.Bool("apply", false, "write the approved batch; omitted means read-only validation")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	digest, err := hex.DecodeString(*approvedDigest)
	if err != nil || len(digest) != 32 || *path == "" || *approval == "" || *approver == "" || *executor == "" || *approver == *executor || flags.NArg() != 0 {
		return errors.New("manifest, approved digest, approval ID, and distinct approver/executor are required")
	}
	dsn := getenv("BALANCE_IMPORT_DATABASE_URL")
	if dsn == "" || sameDatabaseRole(dsn, getenv("DATABASE_URL")) || sameDatabaseRole(dsn, getenv("MIGRATION_DATABASE_URL")) {
		return errors.New("configure a dedicated restricted BALANCE_IMPORT_DATABASE_URL")
	}
	f, err := os.Open(*path)
	if err != nil {
		return errors.New("unable to open manifest")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("manifest must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, settlement.MaxImportBytes+1))
	if err != nil || len(data) > settlement.MaxImportBytes {
		return errors.New("unable to read manifest within the size limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("unable to connect with the import database credential")
	}
	defer conn.Close(context.Background())
	mode := pgx.ReadOnly
	if *apply {
		mode = pgx.ReadWrite
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	if err != nil {
		return errors.New("unable to start import transaction")
	}
	defer tx.Rollback(context.Background())
	auth := settlement.ImportAuthorization{ApprovalID: *approval, ApproverID: *approver, ExecutorID: *executor, SHA256: [32]byte(digest)}
	if err := (settlement.Store{}).ImportBalance(ctx, tx, data, auth, !*apply); err != nil {
		switch {
		case errors.Is(err, settlement.ErrInvalid), errors.Is(err, settlement.ErrInvalidState), errors.Is(err, settlement.ErrNotFound):
			return err
		default:
			return errors.New("import transaction failed; check restricted database diagnostics")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("commit not confirmed; resolve outcome by replaying the exact approved batch")
	}
	if *apply {
		fmt.Fprintln(stdout, "Approved batch recorded or already present.")
	} else {
		fmt.Fprintln(stdout, "Dry run passed; no records written.")
	}
	return nil
}

// Changing connection options, password spelling, or the database within the
// same cluster does not turn an application/migration role into an import role.
func sameDatabaseRole(a, b string) bool {
	if b == "" {
		return false
	}
	if a == b {
		return true
	}
	x, err := pgx.ParseConfig(a)
	if err != nil {
		return false
	}
	y, err := pgx.ParseConfig(b)
	if err != nil || x.User != y.User {
		return false
	}
	host := func(s string) string {
		if strings.EqualFold(s, "localhost") {
			return "loopback"
		}
		if ip := net.ParseIP(s); ip != nil && ip.IsLoopback() {
			return "loopback"
		}
		return strings.ToLower(s)
	}
	endpoints := func(c *pgx.ConnConfig) map[string]bool {
		m := map[string]bool{fmt.Sprintf("%s:%d", host(c.Host), c.Port): true}
		for _, f := range c.Fallbacks {
			m[fmt.Sprintf("%s:%d", host(f.Host), f.Port)] = true
		}
		return m
	}
	left := endpoints(x)
	for e := range endpoints(y) {
		if left[e] {
			return true
		}
	}
	return false
}
