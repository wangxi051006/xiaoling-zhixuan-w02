package settlement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

const MaxImportBytes = 1 << 20

type ImportAuthorization struct {
	ApprovalID, ApproverID, ExecutorID string
	SHA256                             [32]byte
}

type balanceManifest struct {
	StationID string         `json:"station_id"`
	TotalFen  int64          `json:"total_fen"`
	Entries   []balanceEntry `json:"entries"`
}

type balanceEntry struct {
	ActorID   string `json:"actor_id"`
	Reference string `json:"external_reference"`
	Amount    int64  `json:"amount_fen"`
}

func parseBalanceManifest(data []byte, auth ImportAuthorization) (balanceManifest, error) {
	var m balanceManifest
	if len(data) > MaxImportBytes || sha256.Sum256(data) != auth.SHA256 || !reasonCode.MatchString(auth.ApprovalID) ||
		!validID(auth.ApproverID) || !validID(auth.ExecutorID) || auth.ApproverID == auth.ExecutorID {
		return m, ErrInvalid
	}
	if err := decodeExactJSON(data, &m); err != nil || !validID(m.StationID) || len(m.Entries) == 0 || len(m.Entries) > 1000 {
		return m, ErrInvalid
	}
	seen := make(map[string]bool, len(m.Entries))
	var total int64
	for _, e := range m.Entries {
		if !validID(e.ActorID) || !validID(e.Reference) || strings.TrimSpace(e.Reference) != e.Reference || seen[e.Reference] || e.Amount <= 0 || e.Amount > maxMoneyFen-total {
			return m, ErrInvalid
		}
		seen[e.Reference] = true
		total += e.Amount
	}
	if total != m.TotalFen {
		return m, ErrInvalid
	}
	return m, nil
}

// ImportBalance is for the offline CLI and its restricted database role, not the API.
// The approved digest and identities must come from the independently reviewed approval.
func (s Store) ImportBalance(ctx context.Context, tx pgx.Tx, data []byte, auth ImportAuthorization, dryRun bool) error {
	if tx == nil {
		return errNotReady
	}
	m, err := parseBalanceManifest(data, auth)
	if err != nil {
		return err
	}
	// Serialize approved batches without granting the import role UPDATE on
	// shared station metadata (SELECT FOR NO KEY UPDATE requires that grant).
	if !dryRun {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('balance-import:' || current_schema() || ':' || $1,0))`, m.StationID); err != nil {
			return err
		}
	}
	var station string
	if err := tx.QueryRow(ctx, `SELECT id FROM stations WHERE id=$1`, m.StationID).Scan(&station); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var approval, priorStation, approver, executor string
	var digest []byte
	err = tx.QueryRow(ctx, `SELECT approval_id,station_id,approver_id,executor_id,manifest_sha256 FROM balance_import_batches
		WHERE approval_id=$1 OR manifest_sha256=$2 ORDER BY (approval_id=$1) DESC LIMIT 1`, auth.ApprovalID, auth.SHA256[:]).
		Scan(&approval, &priorStation, &approver, &executor, &digest)
	if err == nil {
		if approval == auth.ApprovalID && priorStation == station && approver == auth.ApproverID && executor == auth.ExecutorID && bytes.Equal(digest, auth.SHA256[:]) {
			return nil
		}
		return ErrInvalidState
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	credits := map[string]int64{}
	actors, refs := []string{}, []string{}
	for _, entry := range m.Entries {
		if credits[entry.ActorID] == 0 {
			actors = append(actors, entry.ActorID)
		}
		credits[entry.ActorID] += entry.Amount
		refs = append(refs, entry.Reference)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM wallet_entries WHERE external_reference=ANY($1::text[]))`, refs).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrInvalidState
	}
	slices.Sort(actors)
	for _, actor := range actors {
		q := `SELECT balance_fen FROM wallets WHERE station_id=$1 AND actor_id=$2`
		if !dryRun {
			if _, err := tx.Exec(ctx, `INSERT INTO wallets(station_id,actor_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, station, actor); err != nil {
				return err
			}
			q += ` FOR UPDATE`
		}
		var balance int64
		if err := tx.QueryRow(ctx, q, station, actor).Scan(&balance); err != nil && !(dryRun && errors.Is(err, pgx.ErrNoRows)) {
			return err
		}
		if credits[actor] > maxMoneyFen-balance {
			return ErrInvalidState
		}
	}
	if dryRun {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO balance_import_batches(approval_id,station_id,approver_id,executor_id,manifest_sha256,total_fen,entry_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, auth.ApprovalID, station, auth.ApproverID, auth.ExecutorID, auth.SHA256[:], m.TotalFen, len(m.Entries)); err != nil {
		return err
	}
	for _, entry := range m.Entries {
		if _, err := tx.Exec(ctx, `INSERT INTO wallet_entries(station_id,actor_id,approval_id,external_reference,kind,delta_fen)
			VALUES ($1,$2,$3,$4,'IMPORT',$5)`, station, entry.ActorID, auth.ApprovalID, entry.Reference, entry.Amount); err != nil {
			return err
		}
	}
	for _, actor := range actors {
		if _, err := tx.Exec(ctx, `UPDATE wallets SET balance_fen=balance_fen+$3 WHERE station_id=$1 AND actor_id=$2`, station, actor, credits[actor]); err != nil {
			return err
		}
	}
	return nil
}
