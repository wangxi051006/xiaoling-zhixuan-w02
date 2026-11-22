package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"slices"

	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

const (
	localTestLoginPath = "/api/v1/auth/test/login"
	localTestEmail     = "xiaoling-user@example.invalid"
	localTestIssuer    = "urn:xiaoling:local-test"
)

// This digest represents a public fixture, not a reusable password store.
var localTestCredential, _ = hex.DecodeString("8d969eef6ecad3c29a3a629280e686cf0c3f5d5a86aff3ca12020c923adc6c92")

func localTestAccountID(entry Entry) string { return "xiaoling-local-test-" + string(entry) }

func isLocalTestAccount(id string) bool {
	return id == localTestAccountID(UserEntry) || id == localTestAccountID(RiderEntry) ||
		id == localTestAccountID(AdminEntry) || id == localTestAccountID(MerchantEntry)
}

func loopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback()
}

func (h *IdentityHTTP) RegisterLocalTestLogin(ctx context.Context, mux *http.ServeMux) error {
	if !h.policy.local {
		return errors.New("test login requires local entry policy")
	}
	if err := h.seedLocalTestAccounts(ctx); err != nil {
		return errors.New("could not initialize local test accounts; use an isolated, migrated database")
	}
	mux.HandleFunc("GET "+localTestLoginPath, h.testLoginAvailability)
	mux.HandleFunc("POST "+localTestLoginPath, h.testLogin)
	return nil
}

func (h *IdentityHTTP) seedLocalTestAccounts(ctx context.Context) error {
	return platform.InTransaction(ctx, h.sessions.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO stations(id,name) VALUES('xiaoling-local-test','Local test station') ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		var station string
		if err := tx.QueryRow(ctx, `SELECT id FROM stations`).Scan(&station); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_buildings(id,station_id,name)
			VALUES('xiaoling-local-test-building',$1,'测试一号楼') ON CONFLICT DO NOTHING`, station); err != nil {
			return err
		}
		for _, fixture := range []struct {
			entry Entry
			name  string
			roles []string
		}{
			{UserEntry, "Test customer", []string{"CUSTOMER"}},
			{RiderEntry, "Test rider", []string{"RIDER"}},
			{AdminEntry, "Test administrator", []string{"FINANCE", "OPERATOR", "SECURITY_ADMIN"}},
			{MerchantEntry, "Test merchant", []string{"MERCHANT"}},
		} {
			if _, configured := h.policy.origins[fixture.entry]; !configured {
				continue
			}
			id := localTestAccountID(fixture.entry)
			var result pgx.Rows
			var err error
			if fixture.entry == UserEntry {
				result, err = tx.Query(ctx, `INSERT INTO accounts(id,station_id,email,email_verified_at,display_name)
					VALUES($1,$2,$3,now(),$4) ON CONFLICT(id) DO NOTHING RETURNING id`, id, station, localTestEmail, fixture.name)
			} else {
				result, err = tx.Query(ctx, `INSERT INTO accounts(id,station_id,issuer,subject,display_name)
					VALUES($1,$2,$3,$1,$4) ON CONFLICT(id) DO NOTHING RETURNING id`, id, station, localTestIssuer, fixture.name)
			}
			if err != nil {
				return err
			}
			inserted := result.Next()
			result.Close()
			if err := result.Err(); err != nil {
				return err
			}
			if !inserted {
				// Never elevate a pre-existing account that happens to use a reserved ID.
				var matches bool
				var roles []string
				err := tx.QueryRow(ctx, `SELECT COALESCE(station_id=$2 AND
					(CASE WHEN $3='user' THEN email=$4 AND phone IS NULL ELSE issuer=$5 AND subject=a.id END),false),
					ARRAY(SELECT role FROM role_bindings WHERE account_id=a.id ORDER BY role)
					FROM accounts a WHERE id=$1`, id, station, fixture.entry, localTestEmail, localTestIssuer).Scan(&matches, &roles)
				if err != nil || !matches || !slices.Equal(roles, fixture.roles) {
					return errors.New("reserved test account conflicts with existing data")
				}
				continue
			}
			for _, role := range fixture.roles {
				if _, err := tx.Exec(ctx, `INSERT INTO role_bindings(account_id,role) VALUES($1,$2)`, id, role); err != nil {
					return err
				}
			}
			if fixture.entry == RiderEntry {
				if _, err := tx.Exec(ctx, `INSERT INTO rider_profiles(account_id) VALUES($1)`, id); err != nil {
					return err
				}
			}
			if fixture.entry == MerchantEntry {
				if _, err := tx.Exec(ctx, `INSERT INTO merchants(id,station_id,name) VALUES('xiaoling-local-test-shop',$1,'测试店铺')`, station); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO merchant_staff(account_id,station_id,merchant_id) VALUES($1,$2,'xiaoling-local-test-shop')`, id, station); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (h *IdentityHTTP) testLoginEntry(w http.ResponseWriter, r *http.Request) (Entry, bool) {
	if !loopbackPeer(r) {
		identityStatus(w, r, http.StatusNotFound)
		return "", false
	}
	return h.entry(w, r, UserEntry, RiderEntry, AdminEntry, MerchantEntry)
}

func (h *IdentityHTTP) testLoginAvailability(w http.ResponseWriter, r *http.Request) {
	if entry, ok := h.testLoginEntry(w, r); ok {
		writeIdentityJSON(w, http.StatusOK, map[string]Entry{"entry": entry})
	}
}

func (h *IdentityHTTP) testLogin(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.testLoginEntry(w, r)
	if !ok || !h.sameOrigin(w, r, entry) {
		return
	}
	var body struct {
		Account  string `json:"account"`
		Password string `json:"password"`
	}
	if !decodeIdentity(w, r, &body) {
		return
	}
	account, password := sha256.Sum256([]byte(body.Account)), sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(account[:], localTestCredential)&subtle.ConstantTimeCompare(password[:], localTestCredential) != 1 {
		identityStatus(w, r, http.StatusUnauthorized)
		return
	}
	var token string
	var session Session
	err := platform.InTransaction(r.Context(), h.sessions.pool, func(tx pgx.Tx) error {
		var err error
		// The simulated MFA timestamp applies only to the reserved fixtures.
		token, session, err = h.sessions.issueAtMode(r.Context(), tx, localTestAccountID(entry), "", "", "", entry, h.sessions.clock.Now(), true)
		return err
	})
	if err != nil {
		identityError(w, r, err)
		return
	}
	SetSessionCookie(w, token, session.ExpiresAt)
	h.logAction(r, "local_test_login", session.AccountID, "accepted")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
