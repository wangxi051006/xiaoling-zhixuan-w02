//go:build integration

package identity

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"github.com/jackc/pgx/v5"
)

// staffSession seeds a real admin session row so ProvisionRider sees the same
// database state a browser would produce after staff login and MFA.
func staffSession(t *testing.T, f *emailFixture, account, station string, mfa *time.Time) Session {
	t.Helper()
	token := secretToken()
	csrf := secretToken()
	digest := sha256.Sum256([]byte(token))
	expires := f.clock.now.Add(time.Hour)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO sessions(token_digest,account_id,entry,csrf_token,expires_at,mfa_at)
		VALUES ($1,$2,'admin',$3,$4,$5)
		ON CONFLICT (account_id,entry) DO UPDATE SET token_digest=EXCLUDED.token_digest, csrf_token=EXCLUDED.csrf_token,
			expires_at=EXCLUDED.expires_at, mfa_at=EXCLUDED.mfa_at`, digest[:], account, csrf, expires, mfa); err != nil {
		t.Fatal(err)
	}
	session := Session{AccountID: account, StationID: station, Entry: AdminEntry, CSRFToken: csrf, ExpiresAt: expires}
	if mfa != nil {
		session.MFAAt = *mfa
	}
	return session
}

func seedStaff(t *testing.T, f *emailFixture) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `DROP INDEX stations_singleton;
		INSERT INTO stations(id,name) VALUES ('other','Other');
		INSERT INTO accounts(id,station_id,issuer,subject,display_name) VALUES
			('sec','s','https://idp.test','sec','Security'),
			('op','s','https://idp.test','op','Operator'),
			('fin','s','https://idp.test','fin','Finance'),
			('foreign','other','https://idp.test','foreign','Foreign');
		INSERT INTO role_bindings(account_id,role) VALUES
			('sec','SECURITY_ADMIN'),('op','OPERATOR'),('fin','FINANCE'),('foreign','SECURITY_ADMIN')`); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionRiderRequiresSecurityAdminWithRecentMFA(t *testing.T) {
	f := newEmailFixture(t)
	seedStaff(t, f)
	ctx := t.Context()
	fresh := f.clock.now.Add(-2 * time.Minute)
	stale := f.clock.now.Add(-recentMFAWindow - time.Minute)
	request := RiderProvision{Issuer: "https://idp.test", Subject: "rider-1", DisplayName: "Rider One", ReasonCode: "ONBOARDING"}

	cases := []struct {
		name    string
		session Session
	}{
		{"operator", staffSession(t, f, "op", "s", &fresh)},
		{"finance", staffSession(t, f, "fin", "s", &fresh)},
		// A staff session always carries an MFA timestamp, so the zero case is
		// exercised with a constructed session that never reached the database.
		{"no MFA", Session{AccountID: "sec", StationID: "s", Entry: AdminEntry}},
		{"stale MFA", staffSession(t, f, "sec", "s", &stale)},
	}
	for _, tc := range cases {
		if _, err := f.sessions.ProvisionRider(ctx, tc.session, request, "request-"+tc.name); AccessStatus(err) != http.StatusForbidden {
			t.Fatalf("%s was allowed to provision: %v", tc.name, err)
		}
	}
	var accounts int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE subject = 'rider-1'`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if accounts != 0 {
		t.Fatal("a rejected request created an account")
	}

	admin := staffSession(t, f, "sec", "s", &fresh)
	rider, err := f.sessions.ProvisionRider(ctx, admin, request, "request-ok")
	if err != nil {
		t.Fatalf("security admin could not provision: %v", err)
	}
	if rider.Enabled || rider.Available {
		t.Fatalf("a new rider must not be able to take work: %+v", rider)
	}
	var accountType, station string
	if err := f.pool.QueryRow(ctx, `SELECT account_type, station_id FROM accounts WHERE id = $1`, rider.ID).Scan(&accountType, &station); err != nil {
		t.Fatal(err)
	}
	if accountType != "STAFF" || station != "s" {
		t.Fatalf("unexpected account shape: %s %s", accountType, station)
	}
	rows, err := f.pool.Query(ctx, `SELECT role FROM role_bindings WHERE account_id = $1 ORDER BY role`, rider.ID)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0] != "RIDER" {
		t.Fatalf("rider was granted more than RIDER: %v", roles)
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'rider.provision' AND object_id = $1 AND actor_id = 'sec'`, rider.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("provisioning must be audited exactly once, saw %d", audits)
	}

	// Repeating the same trusted identity is a conflict, not a second account.
	if _, err := f.sessions.ProvisionRider(ctx, admin, request, "request-again"); !errors.Is(err, ErrStaffIdentityBound) {
		t.Fatalf("duplicate trusted identity was accepted: %v", err)
	}
}

func TestProvisionedRiderCannotSignInUntilEnabledAndLosesAccessWhenDisabled(t *testing.T) {
	f := newEmailFixture(t)
	seedStaff(t, f)
	ctx := t.Context()
	fresh := f.clock.now.Add(-time.Minute)
	admin := staffSession(t, f, "sec", "s", &fresh)
	rider, err := f.sessions.ProvisionRider(ctx, admin, RiderProvision{Issuer: "https://idp.test", Subject: "rider-2", DisplayName: "Rider Two", ReasonCode: "ONBOARDING"}, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	mfa := f.clock.now.Add(-time.Minute)
	if _, _, err := f.sessions.issue(ctx, "", "https://idp.test", "rider-2", "", RiderEntry, mfa); !errors.Is(err, ErrSession) {
		t.Fatalf("a disabled rider signed in: %v", err)
	}
	freshOp := staffSession(t, f, "op", "s", &fresh)
	directory := NewDirectory(f.pool)
	enabledRider, err := directory.SetRiderEnabled(ctx, freshOp.AccountID, "s", rider.ID, true, rider.Version, "APPROVED", "request-enable")
	if err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	token, session, err := f.sessions.issue(ctx, "", "https://idp.test", "rider-2", "", RiderEntry, mfa)
	if err != nil || token == "" || session.Entry != RiderEntry || session.Roles[0] != contract.RoleRider {
		t.Fatalf("enabled rider could not sign in: %v", err)
	}
	if _, err := directory.SetRiderEnabled(ctx, freshOp.AccountID, "s", rider.ID, false, enabledRider.Version, "SUSPENDED", "request-disable"); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if _, err := f.sessions.Lookup(ctx, token, RiderEntry); !errors.Is(err, ErrSession) {
		t.Fatalf("a disabled rider kept a live session: %v", err)
	}
}

func TestProvisionedRiderStaysInsideTheActorStation(t *testing.T) {
	f := newEmailFixture(t)
	seedStaff(t, f)
	ctx := t.Context()
	fresh := f.clock.now.Add(-time.Minute)
	foreign := staffSession(t, f, "foreign", "other", &fresh)
	rider, err := f.sessions.ProvisionRider(ctx, foreign, RiderProvision{Issuer: "https://idp.test", Subject: "rider-3", DisplayName: "Rider Three", ReasonCode: "ONBOARDING"}, "request-foreign")
	if err != nil {
		t.Fatalf("security admin of another station failed: %v", err)
	}
	var station string
	if err := f.pool.QueryRow(ctx, `SELECT station_id FROM accounts WHERE id = $1`, rider.ID).Scan(&station); err != nil {
		t.Fatal(err)
	}
	if station != "other" {
		t.Fatalf("rider landed in the wrong station: %s", station)
	}
}
