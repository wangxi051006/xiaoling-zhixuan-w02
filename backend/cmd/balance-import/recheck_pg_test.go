//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecheckActualCLIRestrictedRole(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("isolated TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer admin.Close(context.Background())
	var random [8]byte
	_, _ = rand.Read(random[:])
	schema := "p04_cli_" + hex.EncodeToString(random[:])
	role := "p04_import_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	roleSQL := pgx.Identifier{role}.Sanitize()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+quoted+`;CREATE ROLE `+roleSQL+` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(c, `DROP SCHEMA `+quoted+` CASCADE;DROP ROLE `+roleSQL); err != nil {
			t.Error("CLI resource cleanup failed")
		}
	}()
	// The migration fixture uses the same project migrator, in a disposable schema.
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	files := fstest.MapFS{}
	entries, _ := migrations.SQL.ReadDir(".")
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "000_") || strings.HasPrefix(e.Name(), "040_") {
			data, _ := migrations.SQL.ReadFile(e.Name())
			files[e.Name()] = &fstest.MapFile{Data: data}
		}
	}
	if err := platform.Migrate(ctx, pool, files); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name)VALUES('s','Synthetic');GRANT USAGE ON SCHEMA `+quoted+` TO `+roleSQL+`;GRANT SELECT ON stations,wallets,wallet_entries,balance_import_batches TO `+roleSQL+`;GRANT INSERT ON wallets,wallet_entries,balance_import_batches TO `+roleSQL+`;GRANT UPDATE(balance_fen) ON wallets TO `+roleSQL+`;GRANT USAGE ON SEQUENCE wallet_entries_id_seq TO `+roleSQL); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "postgres" {
		t.Fatal("URL-form isolated test DSN required")
	}
	u.User = url.User(role)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	importDSN := u.String()
	restricted, err := pgx.Connect(ctx, importDSN)
	if err != nil {
		t.Fatal("restricted role unavailable")
	}
	defer restricted.Close(context.Background())
	var unsafe bool
	if err := restricted.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=oid) FROM pg_roles WHERE rolname=current_user`).Scan(&unsafe); err != nil || unsafe {
		t.Fatal("import role is privileged")
	}
	for _, table := range []string{"stations", "settlements", "refunds", "audit_events", "cod_receipts"} {
		var allowed bool
		if err := restricted.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'INSERT,UPDATE,DELETE')`, schema+"."+table).Scan(&allowed); err != nil || allowed {
			t.Fatalf("role has unrelated write privilege on %s", table)
		}
	}
	if _, err := restricted.Exec(ctx, `UPDATE stations SET name='unauthorized'`); err == nil {
		t.Fatal("restricted role could change station")
	}
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "balance-import.exe")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("CLI build failed: %s", out)
	}
	var seq int
	call := func(label, payload, approval, digest string, apply bool, wantOK bool) {
		t.Helper()
		seq++
		manifest := filepath.Join(tmp, fmt.Sprintf("manifest-%d.json", seq))
		if err := os.WriteFile(manifest, []byte(payload), 0600); err != nil {
			t.Fatal(err)
		}
		if digest == "" {
			h := sha256.Sum256([]byte(payload))
			digest = hex.EncodeToString(h[:])
		}
		args := []string{"--manifest", manifest, "--sha256", digest, "--approval", approval, "--approver", "finance-reviewer", "--executor", "restricted-executor"}
		if apply {
			args = append(args, "--apply")
		}
		cmd := exec.CommandContext(ctx, binary, args...)
		for _, e := range os.Environ() {
			key, _, _ := strings.Cut(e, "=")
			if key != "DATABASE_URL" && key != "MIGRATION_DATABASE_URL" && key != "BALANCE_IMPORT_DATABASE_URL" {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, "BALANCE_IMPORT_DATABASE_URL="+importDSN)
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("CLI %s success=%v want=%v, sanitized output=%s", label, err == nil, wantOK, out)
		}
		for _, private := range []string{importDSN, manifest, role, "synthetic-buyer", "external-one"} {
			if strings.Contains(string(out), private) {
				t.Fatal("CLI leaked private input")
			}
		}
		t.Logf("actual CLI %s success=%v (restricted role)", label, err == nil)
	}
	scalar := func(query string) int64 {
		t.Helper()
		var n int64
		if err := pool.QueryRow(ctx, query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	first := `{"station_id":"s","total_fen":300,"entries":[{"actor_id":"synthetic-buyer","external_reference":"external-one","amount_fen":100},{"actor_id":"other","external_reference":"external-two","amount_fen":200}]}`
	call("dry-run", first, "APPROVAL_ONE", "", false, true)
	for _, tb := range []string{"wallets", "wallet_entries", "balance_import_batches", "idempotency_keys"} {
		if scalar(`SELECT count(*)FROM `+tb) != 0 {
			t.Fatal("dry-run wrote " + tb)
		}
	}
	call("apply", first, "APPROVAL_ONE", "", true, true)
	call("exact replay", first, "APPROVAL_ONE", "", true, true)
	if scalar(`SELECT sum(balance_fen)FROM wallets`) != 300 || scalar(`SELECT count(*)FROM wallet_entries`) != 2 || scalar(`SELECT count(*)FROM balance_import_batches`) != 1 {
		t.Fatal("CLI replay duplicated money")
	}
	changed := strings.Replace(strings.Replace(first, `"total_fen":300`, `"total_fen":400`, 1), `"amount_fen":100`, `"amount_fen":200`, 1)
	call("valid changed batch", changed, "APPROVAL_ONE", "", true, false)
	call("missing approval", first, "", "", true, false)
	if scalar(`SELECT sum(balance_fen)FROM wallets`) != 300 || scalar(`SELECT count(*)FROM wallet_entries`) != 2 || scalar(`SELECT count(*)FROM balance_import_batches`) != 1 {
		t.Fatal("rejected approval changed the ledger")
	}
	call("approval moved", first, "APPROVAL_TWO", "", true, false)
	call("tampered digest", first, "APPROVAL_THREE", strings.Repeat("00", 32), true, false)
	for label, payload := range map[string]string{
		"duplicate reference": `{"station_id":"s","total_fen":1,"entries":[{"actor_id":"synthetic-buyer","external_reference":"external-one","amount_fen":1}]}`,
		"duplicate entries":   `{"station_id":"s","total_fen":2,"entries":[{"actor_id":"a","external_reference":"r","amount_fen":1},{"actor_id":"a","external_reference":"r","amount_fen":1}]}`,
		"wrong station":       `{"station_id":"elsewhere","total_fen":1,"entries":[{"actor_id":"a","external_reference":"r","amount_fen":1}]}`,
		"total mismatch":      `{"station_id":"s","total_fen":2,"entries":[{"actor_id":"a","external_reference":"r","amount_fen":1}]}`,
		"negative":            `{"station_id":"s","total_fen":-1,"entries":[{"actor_id":"a","external_reference":"r","amount_fen":-1}]}`,
		"overflow":            `{"station_id":"s","total_fen":9007199254740992,"entries":[{"actor_id":"a","external_reference":"r","amount_fen":9007199254740992}]}`,
		"oversized":           strings.Repeat(" ", 1<<20+1),
	} {
		call(label, payload, "BAD_BATCH", "", true, false)
	}
	fault := `{"station_id":"s","total_fen":300,"entries":[{"actor_id":"synthetic-buyer","external_reference":"fault-one","amount_fen":100},{"actor_id":"other","external_reference":"fault-two","amount_fen":200}]}`
	if _, err := pool.Exec(ctx, `CREATE FUNCTION cli_fault()RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.external_reference='fault-two' THEN RAISE EXCEPTION 'synthetic CLI fault' USING ERRCODE='P0001';END IF;RETURN NEW;END$$;CREATE TRIGGER cli_fault AFTER INSERT ON wallet_entries FOR EACH ROW EXECUTE FUNCTION cli_fault()`); err != nil {
		t.Fatal(err)
	}
	call("mid-batch rollback", fault, "APPROVAL_FAULT", "", true, false)
	if scalar(`SELECT sum(balance_fen)FROM wallets`) != 300 || scalar(`SELECT count(*)FROM wallet_entries`) != 2 || scalar(`SELECT count(*)FROM balance_import_batches`) != 1 {
		t.Fatal("CLI mid-batch failure leaked writes")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER cli_fault ON wallet_entries;DROP FUNCTION cli_fault()`); err != nil {
		t.Fatal(err)
	}
	call("fault retry", fault, "APPROVAL_FAULT", "", true, true)
	if scalar(`SELECT sum(balance_fen)FROM wallets`) != 600 || scalar(`SELECT count(*)FROM wallet_entries`) != 4 || scalar(`SELECT count(*)FROM balance_import_batches`) != 2 {
		t.Fatal("CLI retry drift")
	}
	t.Log("restricted role: no superuser, role management, station/settlement/refund/audit/COD writes; dry-run zero writes; final import 600 fen, four entries, two batches")
}
