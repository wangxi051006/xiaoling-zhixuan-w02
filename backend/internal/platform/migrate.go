package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var migrationName = regexp.MustCompile(`^([0-9]{3}_[0-9]{3})_[a-z][a-z0-9_]*\.sql$`)

func Migrate(ctx context.Context, pool *pgxpool.Pool, source fs.FS) error {
	entries, err := fs.Glob(source, "*.sql")
	if err != nil || len(entries) == 0 {
		return errors.New("no PostgreSQL migrations found")
	}
	versions := make(map[string]bool, len(entries))
	for _, name := range entries {
		parts := migrationName.FindStringSubmatch(name)
		if parts == nil || versions[parts[1]] {
			return fmt.Errorf("invalid or duplicate migration version: %s", name)
		}
		versions[parts[1]] = true
	}
	return InTransaction(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('xiaoling:migration:' || current_schema(), 0))`); err != nil {
			return fmt.Errorf("lock migration: %w", err)
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY, name text NOT NULL,
			digest bytea NOT NULL CHECK (octet_length(digest) = 32),
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
			return fmt.Errorf("create migration history: %w", err)
		}
		rows, err := tx.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
		if err != nil {
			return err
		}
		applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for i, version := range applied {
			if i >= len(entries) || migrationName.FindStringSubmatch(entries[i])[1] != version {
				return fmt.Errorf("migration history is not a prefix of the build manifest at %s", version)
			}
		}
		for i, name := range entries {
			version := migrationName.FindStringSubmatch(name)[1]
			sql, err := fs.ReadFile(source, name)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(sql)
			if i < len(applied) {
				var appliedName string
				var previous []byte
				if err := tx.QueryRow(ctx, `SELECT name, digest FROM schema_migrations WHERE version = $1`, version).Scan(&appliedName, &previous); err != nil {
					return err
				}
				if appliedName != name || !bytes.Equal(previous, digest[:]) {
					return fmt.Errorf("applied migration differs from build: %s", name)
				}
				continue
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("execute migration %s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, digest) VALUES ($1, $2, $3)`, version, name, digest[:]); err != nil {
				return fmt.Errorf("register migration %s: %w", name, err)
			}
		}
		return nil
	})
}
