//go:build integration

package identity

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"campus.local/xiaoling/internal/platform"
	"campus.local/xiaoling/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// emailPool builds an isolated schema with only the base, identity and email
// identity segments so a failure points at this feature, not at other domains.
func emailPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || dsn == os.Getenv("DATABASE_URL") || dsn == os.Getenv("MIGRATION_DATABASE_URL") {
		t.Fatal("an isolated TEST_DATABASE_URL is required")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	t.Cleanup(admin.Close)
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	schema := pgx.Identifier{"a01_email_" + hex.EncodeToString(nonce[:])}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	files := fstest.MapFS{}
	names, err := fs.Glob(migrations.SQL, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "000_") && !strings.HasPrefix(name, "010_") && !strings.HasPrefix(name, "060_") {
			continue
		}
		raw, err := fs.ReadFile(migrations.SQL, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: raw}
	}
	if err := platform.Migrate(t.Context(), pool, files); err != nil {
		t.Fatal(err)
	}
	return pool
}

type fixtureEmailSender struct {
	mu    sync.Mutex
	codes map[string]string
	fail  bool
}

func (f *fixtureEmailSender) SendVerification(_ context.Context, to, code, purpose string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("mail provider unavailable")
	}
	if f.codes == nil {
		f.codes = map[string]string{}
	}
	f.codes[purpose+"|"+to] = code
	return nil
}

func (f *fixtureEmailSender) code(t *testing.T, purpose, to string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	code := f.codes[purpose+"|"+to]
	if code == "" {
		t.Fatalf("no %s code was delivered to %s", purpose, to)
	}
	return code
}

func (f *fixtureEmailSender) sent(purpose, to string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.codes[purpose+"|"+to]
	return ok
}

type emailFixture struct {
	pool     *pgxpool.Pool
	clock    *fakeClock
	sender   *fixtureEmailSender
	codes    *EmailCodes
	sessions *Sessions
}

func newEmailFixture(t *testing.T) *emailFixture {
	t.Helper()
	pool := emailPool(t)
	clock := &fakeClock{time.Unix(1780000000, 0)}
	sender := &fixtureEmailSender{}
	codes, err := NewEmailCodes(pool, sender, clock, []byte("one-of-your-local-test-fixture-keys"))
	if err != nil {
		t.Fatal(err)
	}
	codes.AllowRegistration = true
	if _, err := pool.Exec(t.Context(), `INSERT INTO stations(id,name) VALUES ('s','Synthetic')`); err != nil {
		t.Fatal(err)
	}
	return &emailFixture{pool: pool, clock: clock, sender: sender, codes: codes, sessions: NewSessions(pool, clock)}
}

func (f *emailFixture) register(t *testing.T, email string) (string, Session) {
	t.Helper()
	ctx := t.Context()
	receipt, err := f.codes.Request(ctx, EmailPurposeRegister, email, "", "127.0.0.1")
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	var token string
	var session Session
	if _, err := f.codes.Complete(ctx, receipt.ID, f.sender.code(t, "REGISTER", strings.ToLower(strings.TrimSpace(email))), func(ctx context.Context, tx pgx.Tx, v VerifiedEmail) error {
		var err error
		token, session, err = f.sessions.RegisterEmail(ctx, tx, v)
		return err
	}); err != nil {
		t.Fatalf("register complete: %v", err)
	}
	return token, session
}

func TestEmailRegistrationCreatesOneVerifiedCustomerAndOneSession(t *testing.T) {
	f := newEmailFixture(t)
	ctx := t.Context()
	// Mixed case and a '+' tag must survive normalisation as one mailbox.
	token, session := f.register(t, "New.Student+e2e@Example.test")
	if token == "" || session.Entry != UserEntry || len(session.Roles) != 1 || session.Roles[0] != "CUSTOMER" {
		t.Fatalf("unexpected session: %+v", session)
	}
	var accountType, email string
	var verified bool
	if err := f.pool.QueryRow(ctx, `SELECT account_type,email,email_verified_at IS NOT NULL FROM accounts WHERE id=$1`, session.AccountID).
		Scan(&accountType, &email, &verified); err != nil {
		t.Fatal(err)
	}
	if accountType != "CUSTOMER" || email != "new.student+e2e@example.test" || !verified {
		t.Fatalf("unexpected account row: %s %s %v", accountType, email, verified)
	}
	var accounts, bindings int
	if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts), (SELECT count(*) FROM role_bindings WHERE account_id=$1)`, session.AccountID).
		Scan(&accounts, &bindings); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || bindings != 1 {
		t.Fatalf("registration duplicated rows: accounts=%d bindings=%d", accounts, bindings)
	}
	if found, err := f.sessions.Lookup(ctx, token, UserEntry); err != nil || found.AccountID != session.AccountID {
		t.Fatalf("issued session is unusable: %v", err)
	}
}

func TestEmailRegistrationSwitchAlsoClosesOutstandingRegistrationChallenges(t *testing.T) {
	f := newEmailFixture(t)
	const mailbox = "synthetic@example.test"
	ctx := t.Context()
	challenge, err := f.codes.Request(ctx, EmailPurposeRegister, mailbox, "", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	f.codes.AllowRegistration = false
	_, err = f.codes.Complete(ctx, challenge.ID, f.sender.code(t, "REGISTER", mailbox), func(ctx context.Context, tx pgx.Tx, v VerifiedEmail) error {
		_, _, err := f.sessions.RegisterEmail(ctx, tx, v)
		return err
	})
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("outstanding registration completed after signup closed: %v", err)
	}
	var exists bool
	if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE email=$1)`, mailbox).Scan(&exists); err != nil || exists {
		t.Fatalf("closed registration changed accounts: exists=%v err=%v", exists, err)
	}
}

func TestEmailBindHTTPDistinguishesInvalidCodeFromExpiredSession(t *testing.T) {
	f := newEmailFixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `INSERT INTO accounts(id,station_id,phone) VALUES ('c','s','+15550000001');
		INSERT INTO role_bindings(account_id,role) VALUES ('c','CUSTOMER')`); err != nil {
		t.Fatal(err)
	}
	token, session, err := f.sessions.issueCustomer(ctx, "+15550000001")
	if err != nil {
		t.Fatal(err)
	}
	const address = "owner@example.test"
	receipt, err := f.codes.Request(ctx, EmailPurposeBind, address, session.AccountID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewEntryPolicy("https://user.example.test", "https://rider.example.test", "https://admin.example.test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewIdentityHTTP(policy, f.sessions, NewDirectory(f.pool), nil, nil, nil).WithEmailCodes(f.codes).Register(mux)
	post := func(cookie, code string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://user.example.test/api/v1/me/email/challenges/"+receipt.ID+"/verify", strings.NewReader(`{"code":"`+code+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://user.example.test")
		r.Header.Set("X-CSRF-Token", session.CSRFToken)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookie})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := post(secretToken(), "xxxxxx"); w.Code != 401 {
		t.Fatalf("unknown session: got %d", w.Code)
	}
	if w := post(token, "xxxxxx"); w.Code != 400 || !bytes.Contains(w.Body.Bytes(), []byte("验证码无效")) {
		t.Fatalf("invalid code must not be an authentication failure: got %d", w.Code)
	}
	if w := post(token, f.sender.code(t, "BIND", address)); w.Code != 200 {
		t.Fatalf("valid code after correction: got %d", w.Code)
	}
}

func TestEmailCodeIsOneTimeAndAResendRetiresTheOldCode(t *testing.T) {
	f := newEmailFixture(t)
	ctx := t.Context()
	f.register(t, "student@example.test")
	f.sender.mu.Lock()
	f.sender.codes = map[string]string{}
	f.sender.mu.Unlock()
	// A fresh register attempt for an owned address is accepted identically but
	// must not deliver anything.
	f.clock.now = f.clock.now.Add(2 * emailResendInterval)
	if _, err := f.codes.Request(ctx, EmailPurposeRegister, "student@example.test", "", "127.0.0.2"); err != nil {
		t.Fatalf("owned address should stay silent, not fail: %v", err)
	}
	if f.sender.sent("REGISTER", "student@example.test") {
		t.Fatal("a registration code was delivered to an owned address")
	}
	f.clock.now = f.clock.now.Add(2 * emailResendInterval)
	if _, err := f.codes.Request(ctx, EmailPurposeRegister, "student@example.test", "", "127.0.0.2"); err != nil {
		t.Fatalf("owned address should stay silent, not fail: %v", err)
	}
	if f.sender.sent("REGISTER", "student@example.test") {
		t.Fatal("a registration code was delivered to an owned address")
	}

	// Resend invalidates the previous code for the same purpose.
	f.clock.now = f.clock.now.Add(2 * emailResendInterval)
	first, err := f.codes.Request(ctx, EmailPurposeLogin, "student@example.test", "", "127.0.0.3")
	if err != nil {
		t.Fatal(err)
	}
	oldCode := f.sender.code(t, "LOGIN", "student@example.test")
	f.clock.now = f.clock.now.Add(2 * emailResendInterval)
	second, err := f.codes.Request(ctx, EmailPurposeLogin, "student@example.test", "", "127.0.0.3")
	if err != nil {
		t.Fatal(err)
	}
	newCode := f.sender.code(t, "LOGIN", "student@example.test")
	if oldCode == newCode {
		t.Fatal("resend reused the same code")
	}
	if _, err := f.codes.Complete(ctx, first.ID, oldCode, func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
		t.Fatalf("superseded code accepted: %v", err)
	}
	var token string
	var session Session
	if _, err := f.codes.Complete(ctx, second.ID, newCode, func(ctx context.Context, tx pgx.Tx, v VerifiedEmail) error {
		var err error
		token, session, err = f.sessions.LoginEmail(ctx, tx, v)
		return err
	}); err != nil {
		t.Fatalf("login with resent code failed: %v", err)
	}
	if token == "" || session.Entry != UserEntry {
		t.Fatal("login did not issue a customer session")
	}
	// The consumed code cannot be replayed.
	if _, err := f.codes.Complete(ctx, second.ID, newCode, func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
		t.Fatalf("replayed code accepted: %v", err)
	}
}

func TestEmailWrongGuessLimitExpiryAndSendFailure(t *testing.T) {
	f := newEmailFixture(t)
	ctx := t.Context()
	receipt, err := f.codes.Request(ctx, EmailPurposeRegister, "guess@example.test", "", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < emailMaxAttempts; i++ {
		if _, err := f.codes.Complete(ctx, receipt.ID, "000000", func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
			t.Fatalf("wrong guess %d was not rejected: %v", i, err)
		}
	}
	if _, err := f.codes.Complete(ctx, receipt.ID, f.sender.code(t, "REGISTER", "guess@example.test"), func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
		t.Fatalf("code accepted after the attempt limit: %v", err)
	}

	// Expiry: the code is useless once the TTL passed.
	f.clock.now = f.clock.now.Add(2 * emailResendInterval)
	expiring, err := f.codes.Request(ctx, EmailPurposeRegister, "late@example.test", "", "127.0.0.4")
	if err != nil {
		t.Fatal(err)
	}
	lateCode := f.sender.code(t, "REGISTER", "late@example.test")
	f.clock.now = f.clock.now.Add(emailTTL + time.Second)
	if _, err := f.codes.Complete(ctx, expiring.ID, lateCode, func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
		t.Fatalf("expired code accepted: %v", err)
	}

	// A send failure never becomes a passing verification.
	f.clock.now = f.clock.now.Add(2 * emailResendInterval)
	f.sender.fail = true
	failed, err := f.codes.Request(ctx, EmailPurposeRegister, "broken@example.test", "", "127.0.0.5")
	if !errors.Is(err, ErrEmailSend) {
		t.Fatalf("send failure did not fail closed: %v", err)
	}
	f.sender.fail = false
	if _, err := f.codes.Complete(ctx, failed.ID, "123456", func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
		t.Fatalf("undelivered challenge verified: %v", err)
	}
}

func TestEmailRateLimitsPerMailboxAndSource(t *testing.T) {
	f := newEmailFixture(t)
	ctx := t.Context()
	if _, err := f.codes.Request(ctx, EmailPurposeRegister, "first@example.test", "", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.codes.Request(ctx, EmailPurposeRegister, "first@example.test", "", "127.0.0.2"); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("resend inside the window accepted: %v", err)
	}
	// The source window counts every address, deliverable or not.
	for i := 0; i < emailSourcePerHour; i++ {
		address := "source-" + string(rune('a'+i)) + "@example.test"
		if _, err := f.codes.Request(ctx, EmailPurposeRegister, address, "", "127.0.0.9"); err != nil {
			t.Fatalf("source request %d failed early: %v", i, err)
		}
	}
	if _, err := f.codes.Request(ctx, EmailPurposeRegister, "one-more@example.test", "", "127.0.0.9"); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("source limit not enforced: %v", err)
	}
}

func TestEmailBindAttachesOnlyToTheSignedInAccount(t *testing.T) {
	f := newEmailFixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `INSERT INTO accounts(id,station_id,phone) VALUES ('c','s','+15550000001'),('d','s','+15550000002');
		INSERT INTO role_bindings(account_id,role) VALUES ('c','CUSTOMER'),('d','CUSTOMER')`); err != nil {
		t.Fatal(err)
	}
	token, session, err := f.sessions.issue(ctx, "", "", "", "+15550000001", UserEntry, time.Time{})
	if err != nil || token == "" {
		t.Fatalf("seed session: %v", err)
	}
	receipt, err := f.codes.Request(ctx, EmailPurposeBind, "owner@example.test", session.AccountID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	bindCode := f.sender.code(t, "BIND", "owner@example.test")
	// Another account cannot redeem this bind challenge.
	other := Session{AccountID: "d", Entry: UserEntry, StationID: "s"}
	if _, err := f.codes.Complete(ctx, receipt.ID, "111111", func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
		t.Fatalf("wrong bind code accepted: %v", err)
	}
	if _, err := f.codes.Complete(ctx, receipt.ID, bindCode, func(ctx context.Context, tx pgx.Tx, v VerifiedEmail) error {
		return f.sessions.BindEmail(ctx, tx, other, v)
	}); !errors.Is(err, ErrEmailInput) {
		t.Fatalf("a different account redeemed the bind challenge: %v", err)
	}
	if _, err := f.codes.Complete(ctx, receipt.ID, bindCode, func(ctx context.Context, tx pgx.Tx, v VerifiedEmail) error {
		return f.sessions.BindEmail(ctx, tx, session, v)
	}); err != nil {
		t.Fatalf("owner could not bind: %v", err)
	}
	var email string
	if err := f.pool.QueryRow(ctx, `SELECT email FROM accounts WHERE id='c'`).Scan(&email); err != nil || email != "owner@example.test" {
		t.Fatalf("bind did not land on the account: %q %v", email, err)
	}
	// A second account cannot take the same mailbox.
	f.clock.now = f.clock.now.Add(2 * emailResendInterval)
	second, err := f.codes.Request(ctx, EmailPurposeBind, "owner@example.test", "d", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.codes.Complete(ctx, second.ID, "111111", func(context.Context, pgx.Tx, VerifiedEmail) error { return nil }); !errors.Is(err, ErrInvalidEmailCode) {
		t.Fatalf("challenge for an owned address verified: %v", err)
	}
}

func TestEmailConcurrentRegistrationLeavesOneAccount(t *testing.T) {
	f := newEmailFixture(t)
	ctx := t.Context()
	verified := VerifiedEmail{Purpose: EmailPurposeRegister, Email: "race@example.test"}
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = platform.InTransaction(ctx, f.pool, func(tx pgx.Tx) error {
				_, _, err := f.sessions.RegisterEmail(ctx, tx, verified)
				return err
			})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrEmailAlreadyOwned) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE email=$1`, verified.Email).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || count != 1 {
		t.Fatalf("concurrent registration created %d accounts with %d successes", count, ok)
	}
}
