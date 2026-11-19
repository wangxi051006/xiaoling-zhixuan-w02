//go:build integration

package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

func TestIdentityMigrationsStandaloneReplaysExactlyOnce(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations WHERE version NOT LIKE '000_%' AND version NOT LIKE '010_%' AND version NOT LIKE '060_%'`)
	if err != nil {
		t.Fatal(err)
	}
	other, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatal("identity test schema depends on other W1 migration segments")
	}
	const history = `SELECT json_agg(h)::text FROM (SELECT version,name,encode(digest,'hex'),applied_at FROM schema_migrations ORDER BY version) h`
	var initialHistory, replayedHistory string
	if err := pool.QueryRow(ctx, history).Scan(&initialHistory); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('replay-marker','Fixture')`); err != nil {
		t.Fatal(err)
	}
	if err := platform.Migrate(ctx, pool, identityMigrations(t)); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&after); err != nil || after != before {
		t.Fatal("replay changed migration history")
	}
	if err := pool.QueryRow(ctx, history).Scan(&replayedHistory); err != nil || replayedHistory != initialHistory {
		t.Fatal("replay changed migration digest or timestamp")
	}
	var markers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stations WHERE id='replay-marker'`).Scan(&markers); err != nil || markers != 1 {
		t.Fatal("replay changed existing data")
	}
}

// Generated synthetic contacts avoid putting contact values in evidence output.
func syntheticPhone(index int) string { return "+1" + fmt.Sprintf("%010d", index) }

func TestPostgresOTPExpiryAttemptsDeliveryAndDigest(t *testing.T) {
	for _, scenario := range []string{"expiry", "attempts", "delivery"} {
		t.Run(scenario, func(t *testing.T) {
			pool := identityPool(t)
			clock := &fakeClock{time.Unix(1780000000, 0)}
			sender := &fixtureOTP{failSend: scenario == "delivery"}
			svc, err := NewOTP(NewPostgresOTPStore(pool), sender, clock, bytes.Repeat([]byte{1}, 32))
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := svc.Request(t.Context(), syntheticPhone(1), "127.0.0.1")
			if scenario == "delivery" {
				if !errors.Is(err, ErrOTPSend) {
					t.Fatal("delivery did not fail closed")
				}
				err = pool.QueryRow(t.Context(), `SELECT id FROM otp_challenges`).Scan(&receipt.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			var digest []byte
			var expires time.Time
			var serialized string
			if err := pool.QueryRow(t.Context(), `SELECT code_digest,expires_at,row_to_json(c)::text FROM otp_challenges c WHERE id=$1`, receipt.ID).Scan(&digest, &expires, &serialized); err != nil {
				t.Fatal(err)
			}
			if len(digest) != 32 || bytes.Equal(digest, []byte(sender.lastCode)) || !expires.Equal(clock.Now().Add(5*time.Minute)) || strings.Contains(serialized, `"`+sender.lastCode+`"`) {
				t.Fatal("OTP storage or expiry violated")
			}
			if scenario == "expiry" {
				clock.now = expires
			}
			if scenario == "attempts" {
				for range 5 {
					if _, err := svc.Verify(t.Context(), receipt.ID, "invalid-form"); !errors.Is(err, ErrInvalidOTP) {
						t.Fatal("bad attempt accepted")
					}
				}
			}
			if _, err := svc.Verify(t.Context(), receipt.ID, sender.lastCode); !errors.Is(err, ErrInvalidOTP) {
				t.Fatal("invalid challenge authenticated")
			}
			var attempts int
			if err := pool.QueryRow(t.Context(), `SELECT attempts FROM otp_challenges WHERE id=$1`, receipt.ID).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if scenario == "attempts" && attempts != 5 {
				t.Fatal("attempt budget not persisted")
			}
		})
	}
}

func TestPostgresOTPPhoneSourceLimitsAndConcurrentRequest(t *testing.T) {
	for _, scenario := range []string{"phone hour", "source hour", "concurrent phone", "ipv6 prefix"} {
		t.Run(scenario, func(t *testing.T) {
			pool := identityPool(t)
			clock := &fakeClock{time.Unix(1780000000, 0)}
			svc, err := NewOTP(NewPostgresOTPStore(pool), &fixtureOTP{}, clock, bytes.Repeat([]byte{2}, 32))
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "concurrent phone" {
				// Two independent adapters avoid a test sender data race.
				other, err := NewOTP(NewPostgresOTPStore(pool), &fixtureOTP{}, clock, bytes.Repeat([]byte{2}, 32))
				if err != nil {
					t.Fatal(err)
				}
				start := make(chan struct{})
				results := make(chan error, 2)
				var wg sync.WaitGroup
				for _, service := range []*OTP{svc, other} {
					wg.Go(func() {
						<-start
						_, err := service.Request(t.Context(), syntheticPhone(1), "127.0.0.1")
						results <- err
					})
				}
				close(start)
				wg.Wait()
				close(results)
				ok, limited := 0, 0
				for err := range results {
					if err == nil {
						ok++
					} else if errors.Is(err, ErrRateLimit) {
						limited++
					} else {
						t.Fatal(err)
					}
				}
				if ok != 1 || limited != 1 {
					t.Fatal("concurrent cooldown was not atomic")
				}
				return
			}
			count := 5
			if scenario != "phone hour" {
				count = 20
			}
			for i := range count {
				phone, source := syntheticPhone(1), "127.0.0.1"
				if scenario != "phone hour" {
					phone = syntheticPhone(i + 1)
				}
				if scenario == "ipv6 prefix" {
					source = fmt.Sprintf("2001:db8:1:2::%x", i+1)
				}
				if _, err := svc.Request(t.Context(), phone, source); err != nil {
					t.Fatal(err)
				}
				if scenario == "phone hour" {
					clock.now = clock.now.Add(time.Minute)
				}
			}
			phone, source := syntheticPhone(1), "127.0.0.1"
			if scenario != "phone hour" {
				phone = syntheticPhone(50)
			}
			if scenario == "ipv6 prefix" {
				source = "2001:db8:1:2::ff"
			}
			if _, err := svc.Request(t.Context(), phone, source); !errors.Is(err, ErrRateLimit) {
				t.Fatal("hourly limit bypassed")
			}
		})
	}
}

func TestIdentityPortsReadCallerTransactionAndRollback(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Fixture')`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO accounts(id,station_id,phone) VALUES ('customer','s',$1)`, syntheticPhone(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO accounts(id,station_id,issuer,subject) VALUES ('rider','s','https://idp.example.test','rider');
		INSERT INTO role_bindings(account_id,role) VALUES ('customer','CUSTOMER'),('rider','RIDER');
		INSERT INTO rider_profiles(account_id) VALUES ('rider');
		INSERT INTO service_buildings(id,station_id,name) VALUES ('building','s','Fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO addresses(id,account_id,building_id,room,contact_name,contact_phone) VALUES ('address','customer','building',$1,'Fixture',$2)`, strings.Repeat("R", 3), syntheticPhone(1)); err != nil {
		t.Fatal(err)
	}
	var port contract.IdentityPort = NewDirectory(pool)
	if _, err := port.ResolveAddress(ctx, tx, "customer", "address"); err != nil {
		t.Fatal("port did not read caller's pending address")
	}
	if err := port.AssertRider(ctx, tx, "rider", "s"); err != nil {
		t.Fatal("port did not read caller's pending rider")
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='rider'`); err != nil {
		t.Fatal(err)
	}
	if err := port.AssertRider(ctx, tx, "rider", "s"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatal("port ignored pending disable")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("caller rollback did not remove pending identity state")
	}
}

func TestAddressValidationAndSiteBoundary(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `DROP INDEX stations_singleton;
		INSERT INTO stations(id,name) VALUES ('s','Fixture'),('outside','Other fixture');
		INSERT INTO service_buildings(id,station_id,name) VALUES ('local','s','Fixture'),('foreign','outside','Other fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts(id,station_id,phone) VALUES ('a','s',$1),('b','outside',$2)`, syntheticPhone(1), syntheticPhone(2)); err != nil {
		t.Fatal(err)
	}
	directory := NewDirectory(pool)
	input := AddressInput{BuildingID: "local", Room: strings.Repeat("R", 3), ContactName: "Fixture", ContactPhone: syntheticPhone(1)}
	for _, scenario := range []string{"empty room", "long room", "control room", "bad phone", "foreign building"} {
		t.Run(scenario, func(t *testing.T) {
			bad := input
			switch scenario {
			case "empty room":
				bad.Room = " "
			case "long room":
				bad.Room = strings.Repeat("R", 41)
			case "control room":
				bad.Room = "R\x00R"
			case "bad phone":
				bad.ContactPhone = "invalid"
			case "foreign building":
				bad.BuildingID = "foreign"
			}
			if _, err := directory.CreateAddress(ctx, "a", "s", bad); !errors.Is(err, ErrAddressInput) {
				t.Fatal("invalid address input accepted")
			}
		})
	}
	created, err := directory.CreateAddress(ctx, "a", "s", input)
	if err != nil {
		t.Fatal(err)
	}
	room := strings.Repeat("S", 3)
	if _, err := directory.UpdateAddress(ctx, "a", "outside", created.AddressID, 1, AddressPatch{Room: &room}); !errors.Is(err, ErrObjectNotFound) {
		t.Fatal("cross-site update accepted")
	}
	if err := directory.DeleteAddress(ctx, "a", "outside", created.AddressID, 1); !errors.Is(err, ErrObjectNotFound) {
		t.Fatal("cross-site delete accepted")
	}
	foreign := "foreign"
	if _, err := directory.UpdateAddress(ctx, "a", "s", created.AddressID, 1, AddressPatch{BuildingID: &foreign}); !errors.Is(err, ErrAddressInput) {
		t.Fatal("cross-site building update accepted")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE addresses SET building_id='foreign' WHERE id=$1`, created.AddressID); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.ResolveAddress(ctx, tx, "a", created.AddressID); !errors.Is(err, ErrObjectNotFound) {
		t.Fatal("cross-site stored address resolved")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := directory.DeleteAddress(ctx, "a", "s", created.AddressID, 1); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL AND version=2 FROM addresses WHERE id=$1`, created.AddressID).Scan(&deleted); err != nil || !deleted {
		t.Fatal("address was not soft deleted")
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := directory.ResolveAddress(ctx, tx, "a", created.AddressID); !errors.Is(err, ErrObjectNotFound) {
		t.Fatal("deleted address resolved")
	}
}

func TestIdentitySecurityMutationsRollbackWithAuditFailure(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Fixture');
		INSERT INTO accounts(id,station_id,issuer,subject) VALUES
		('sec','s','https://idp.example.test','sec'),('op','s','https://idp.example.test','op'),('target','s','https://idp.example.test','target');
		INSERT INTO role_bindings(account_id,role) VALUES ('sec','SECURITY_ADMIN'),('op','OPERATOR'),('target','FINANCE'),('target','RIDER');
		INSERT INTO rider_profiles(account_id,available) VALUES ('target',true)`); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{time.Unix(1780000000, 0)}
	sessions := NewSessions(pool, clock)
	_, security, err := sessions.Issue(ctx, "sec", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := sessions.Issue(ctx, "target", AdminEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	riderToken, _, err := sessions.Issue(ctx, "target", RiderEntry, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDirectory(pool).SetRiderEnabled(ctx, "sec", "s", "target", false, 1, "POLICY", "fixture-request"); AccessStatus(err) != 403 {
		t.Fatal("security administrator changed rider operational state")
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
		CREATE TRIGGER fail_test_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_test_audit()`); err != nil {
		t.Fatal(err)
	}
	if err := sessions.ChangeStaffRole(ctx, security, "target", contract.RoleFinance, false, "POLICY", "fixture-request"); err == nil {
		t.Fatal("role mutation ignored audit failure")
	}
	if _, err := NewDirectory(pool).SetRiderEnabled(ctx, "op", "s", "target", false, 1, "POLICY", "fixture-request"); err == nil {
		t.Fatal("rider mutation ignored audit failure")
	}
	for entry, token := range map[Entry]string{AdminEntry: adminToken, RiderEntry: riderToken} {
		if _, err := sessions.Lookup(ctx, token, entry); err != nil {
			t.Fatal("audit failure did not roll back session revocation")
		}
	}
	var roles, audits int
	var enabled, available bool
	var version int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM role_bindings WHERE account_id='target'`).Scan(&roles); err != nil || roles != 2 {
		t.Fatal("role rollback failed")
	}
	if err := pool.QueryRow(ctx, `SELECT enabled,available,version FROM rider_profiles WHERE account_id='target'`).Scan(&enabled, &available, &version); err != nil || !enabled || !available || version != 1 {
		t.Fatal("rider rollback failed")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("partial audit persisted")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_test_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDirectory(pool).SetRiderEnabled(ctx, "op", "s", "target", false, 1, "POLICY", "fixture-request"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDirectory(pool).SetRiderEnabled(ctx, "op", "s", "target", true, 2, "POLICY", "fixture-request"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Lookup(ctx, riderToken, RiderEntry); !errors.Is(err, ErrSession) {
		t.Fatal("rider re-enable resurrected old session")
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET enabled=false WHERE id='target'; UPDATE accounts SET enabled=true WHERE id='target'`); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Lookup(ctx, adminToken, AdminEntry); !errors.Is(err, ErrSession) {
		t.Fatal("account re-enable resurrected old session")
	}
}

func TestIdentityHTTPRegistrationPrivacyUnavailableAndForgedSource(t *testing.T) {
	pool := identityPool(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO stations(id,name) VALUES ('s','Fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts(id,station_id,phone) VALUES ('customer','s',$1);`, syntheticPhone(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_bindings(account_id,role) VALUES ('customer','CUSTOMER')`); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{time.Unix(1780000000, 0)}
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	sender := &fixtureOTP{}
	otp, err := NewOTP(NewPostgresOTPStore(pool), sender, clock, bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewSessions(pool, clock)
	request := func(h *IdentityHTTP, method, origin, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		mux := http.NewServeMux()
		h.Register(mux)
		r := httptest.NewRequest(method, origin+"/api/v1"+path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		platform.NewHandler(mux, nil, nil, 4096).ServeHTTP(w, r)
		return w
	}
	h := NewIdentityHTTP(policy, sessions, NewDirectory(pool), otp, nil, nil)
	for _, bad := range []map[string]string{
		{"challenge_id": strings.Repeat("a", 32), "code": "invalid-form"},
		{"challenge_id": "", "code": strings.Repeat("0", 6)},
	} {
		body, _ := json.Marshal(bad)
		w := request(h, "POST", policy.origins[UserEntry].origin, "/auth/otp/verify", string(body), nil)
		if w.Code != 400 {
			t.Fatal("malformed OTP payload did not return BAD_REQUEST")
		}
	}
	for i := 1; i <= 2; i++ {
		body, _ := json.Marshal(map[string]string{"phone": syntheticPhone(i)})
		w := request(h, "POST", policy.origins[UserEntry].origin, "/auth/otp/request", string(body), nil)
		var receipt map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || w.Code != 202 || len(receipt) != 2 || receipt["challenge_id"] == nil || receipt["expires_at"] == nil {
			t.Fatal("registration status changed OTP request response")
		}
	}
	body, _ := json.Marshal(map[string]string{"phone": syntheticPhone(1)})
	w := request(h, "POST", policy.origins[UserEntry].origin, "/auth/otp/request", string(body), map[string]string{"X-Forwarded-For": "192.0.2.2", "X-Forwarded-Host": "admin.example.test", "X-Forwarded-Proto": "https"})
	if w.Code != 429 {
		t.Fatal("forwarded source bypassed phone cooldown")
	}
	for _, tc := range []struct{ method, origin, path string }{
		{"POST", policy.origins[UserEntry].origin, "/auth/otp/request"},
		{"GET", policy.origins[AdminEntry].origin, "/auth/staff/start"},
	} {
		w := request(NewIdentityHTTP(policy, sessions, NewDirectory(pool), nil, nil, nil), tc.method, tc.origin, tc.path, "", nil)
		if w.Code != 503 {
			t.Fatal("missing adapter did not return unavailable")
		}
	}
	token, session, err := sessions.Issue(ctx, "customer", UserEntry, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"Cookie": SessionCookieName + "=" + token, "X-CSRF-Token": session.CSRFToken, "Origin": "https://evil.example.test", "Referer": policy.origins[UserEntry].origin, "X-Forwarded-Host": "user.example.test"}
	w = request(h, "POST", policy.origins[UserEntry].origin, "/auth/logout", "", base)
	if w.Code != 403 {
		t.Fatal("forged source allowed write")
	}
	base["Origin"] = policy.origins[UserEntry].origin
	w = request(h, "POST", policy.origins[UserEntry].origin, "/auth/logout", "", base)
	if w.Code != 204 {
		t.Fatal("valid logout failed")
	}
	if _, err := sessions.Lookup(ctx, token, UserEntry); !errors.Is(err, ErrSession) {
		t.Fatal("logout session remains usable")
	}
	// Lookup never treats a stored digest as a bearer token.
	digest := sha256.Sum256([]byte(token))
	if _, err := sessions.Lookup(ctx, fmt.Sprintf("%x", digest), UserEntry); !errors.Is(err, ErrSession) {
		t.Fatal("stored digest accepted as token")
	}
}
