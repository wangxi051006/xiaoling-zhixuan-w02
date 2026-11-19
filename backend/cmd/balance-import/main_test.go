package main

import (
	"io"
	"strings"
	"testing"
)

func TestRunRejectsUnsafeImportInputsBeforeReadingManifest(t *testing.T) {
	for _, tc := range []struct {
		name, digest, approver, executor, importDSN, apiDSN, migrateDSN, want string
	}{
		{name: "missing approval digest", approver: "finance", executor: "executor", want: "approved digest"},
		{name: "same approver and executor", digest: strings.Repeat("ab", 32), approver: "finance", executor: "finance", want: "distinct approver/executor"},
		{name: "missing dedicated credential", digest: strings.Repeat("ab", 32), approver: "finance", executor: "executor", want: "dedicated restricted"},
		{name: "reused API credential", digest: strings.Repeat("ab", 32), approver: "finance", executor: "executor", importDSN: "fixture-api", apiDSN: "fixture-api", want: "dedicated restricted"},
		{name: "reused migration credential", digest: strings.Repeat("ab", 32), approver: "finance", executor: "executor", importDSN: "fixture-migrate", migrateDSN: "fixture-migrate", want: "dedicated restricted"},
		{name: "API credential with changed options", digest: strings.Repeat("ab", 32), approver: "finance", executor: "executor", importDSN: "postgres://app-role@127.0.0.1:55494/postgres?sslmode=disable&application_name=import", apiDSN: "postgres://app-role@127.0.0.1:55494/postgres?sslmode=disable", want: "dedicated restricted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"BALANCE_IMPORT_DATABASE_URL": tc.importDSN, "DATABASE_URL": tc.apiDSN, "MIGRATION_DATABASE_URL": tc.migrateDSN}
			args := []string{"-manifest", "not-opened.json", "-sha256", tc.digest, "-approval", "APPROVED_1", "-approver", tc.approver, "-executor", tc.executor, "-apply"}
			err := run(t.Context(), args, io.Discard, io.Discard, func(key string) string { return env[key] })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run=%v; want rejection containing %q", err, tc.want)
			}
		})
	}
}
