package settlement

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/identity"
)

func TestSettlementRoutesUseFrozenRoleAndMFAPolicy(t *testing.T) {
	for _, tc := range []struct {
		method, path, roles string
		mfa                 bool
	}{
		{"GET", "/payment-methods", "CUSTOMER", false},
		{"GET", "/me/wallet", "CUSTOMER", false},
		{"GET", "/admin/cod/receipts", "FINANCE", false},
		{"POST", "/admin/cod/batches", "FINANCE", true},
		{"POST", "/admin/cod/batches/id/verify", "FINANCE", true},
		{"GET", "/admin/refunds", "OPERATOR FINANCE", false},
		{"POST", "/admin/refunds/id/approve", "FINANCE", true},
		{"POST", "/admin/refunds/id/record-cod-payout", "FINANCE", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			called := false
			h := HTTP{Authorize: func(w http.ResponseWriter, _ *http.Request, entry identity.Entry, mfa bool, roles ...contract.Role) (identity.Session, bool) {
				called = true
				wantEntry := identity.UserEntry
				if strings.HasPrefix(tc.path, "/admin/") {
					wantEntry = identity.AdminEntry
				}
				var got []string
				for _, role := range roles {
					got = append(got, string(role))
				}
				if entry != wantEntry || mfa != tc.mfa || strings.Join(got, " ") != tc.roles {
					t.Errorf("entry=%v, mfa=%v, roles=%v", entry, mfa, roles)
				}
				w.WriteHeader(403)
				return identity.Session{}, false
			}}
			mux := http.NewServeMux()
			h.Register(mux)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/v1"+tc.path, nil))
			if !called || w.Code != 403 {
				t.Fatalf("authorize=%v status=%d", called, w.Code)
			}
		})
	}
}

func TestImportManifestRejectsUnapprovedOrInvalidCredits(t *testing.T) {
	valid := `{"station_id":"s","total_fen":100,"entries":[{"actor_id":"buyer","external_reference":"ref-1","amount_fen":100}]}`
	auth := ImportAuthorization{ApprovalID: "A1", ApproverID: "approver", ExecutorID: "executor", SHA256: sha256.Sum256([]byte(valid))}
	if _, err := parseBalanceManifest([]byte(valid), auth); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ImportAuthorization){
		func(a *ImportAuthorization) { a.ApprovalID = "" },
		func(a *ImportAuthorization) { a.SHA256 = [32]byte{} },
		func(a *ImportAuthorization) { a.ExecutorID = a.ApproverID },
	} {
		bad := auth
		mutate(&bad)
		if _, err := parseBalanceManifest([]byte(valid), bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid approval: %v", err)
		}
	}
	for _, payload := range []string{
		`{"station_id":"s","total_fen":200,"entries":[{"actor_id":"buyer","external_reference":"ref-1","amount_fen":100}]}`,
		`{"station_id":"s","total_fen":-100,"entries":[{"actor_id":"buyer","external_reference":"ref-1","amount_fen":-100}]}`,
		`{"station_id":"s","total_fen":200,"entries":[{"actor_id":"buyer","external_reference":"r","amount_fen":100},{"actor_id":"other","external_reference":"r","amount_fen":100}]}`,
	} {
		bad := auth
		bad.SHA256 = sha256.Sum256([]byte(payload))
		if _, err := parseBalanceManifest([]byte(payload), bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid manifest: %v", err)
		}
	}
}

func TestPaymentPolicyDefaultsToDisabled(t *testing.T) {
	for _, method := range []contract.PaymentMethod{contract.PaymentMethodCod, contract.PaymentMethodBalance, contract.PaymentMethodWechat, "OTHER"} {
		if err := (PaymentPolicy{}).Check(method); !errors.Is(err, ErrPaymentMethodDisabled) {
			t.Errorf("default allowed %q: %v", method, err)
		}
	}
}

func TestPaymentPolicyUsesIndependentSwitches(t *testing.T) {
	for _, tc := range []struct {
		policy       PaymentPolicy
		cod, balance bool
	}{
		{PaymentPolicy{CODEnabled: true}, true, false},
		{PaymentPolicy{BalanceEnabled: true}, false, true},
		{PaymentPolicy{CODEnabled: true, BalanceEnabled: true}, true, true},
	} {
		got := tc.policy.Methods()
		if len(got) != 3 || got[0].Code != contract.PaymentMethodCod || got[0].Enabled != tc.cod ||
			got[1].Code != contract.PaymentMethodBalance || got[1].Enabled != tc.balance ||
			got[2].Code != contract.PaymentMethodWechat || got[2].Enabled || got[2].Label != "微信支付（待接入）" {
			t.Fatalf("unexpected channel list: %+v", got)
		}
		for _, option := range got {
			err := tc.policy.Check(option.Code)
			if option.Enabled && err != nil || !option.Enabled && !errors.Is(err, ErrPaymentMethodDisabled) {
				t.Errorf("policy and channel list disagree on %q: %v", option.Code, err)
			}
		}
	}
}
