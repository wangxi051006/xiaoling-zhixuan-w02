package identity

import (
	"context"
	"errors"
	"net"
	"net/http"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
	"github.com/jackc/pgx/v5"
)

type emailCodeInput struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
}

type emailVerifyInput struct {
	Code string `json:"code"`
}

// emailSource derives the client address from the connection only; forwarding
// headers are never trusted for rate limiting.
func (h *IdentityHTTP) emailSource(w http.ResponseWriter, r *http.Request) (string, bool) {
	source, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		identityStatus(w, r, 400)
		return "", false
	}
	return source, true
}

// requestEmailCode starts a register or login challenge. Only the user entry may
// call it and no role or station parameter is accepted.
func (h *IdentityHTTP) requestEmailCode(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, UserEntry)
	if !ok || !h.sameOrigin(w, r, entry) {
		return
	}
	if h.emails == nil {
		identityStatus(w, r, 503)
		return
	}
	var input emailCodeInput
	if !decodeIdentity(w, r, &input) {
		return
	}
	purpose := EmailPurpose(input.Purpose)
	if purpose != EmailPurposeRegister && purpose != EmailPurposeLogin {
		identityStatus(w, r, 400)
		return
	}
	source, ok := h.emailSource(w, r)
	if !ok {
		return
	}
	receipt, err := h.emails.Request(r.Context(), purpose, input.Email, "", source)
	if err != nil {
		h.logAction(r, "email_request", "", "rejected")
		identityError(w, r, err)
		return
	}
	h.logAction(r, "email_request", "", "accepted")
	writeIdentityJSON(w, 202, receipt)
}

// verifyEmailCode redeems a register or login code. Account creation and code
// consumption share one transaction inside EmailCodes.Complete.
func (h *IdentityHTTP) verifyEmailCode(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, UserEntry)
	if !ok || !h.sameOrigin(w, r, entry) {
		return
	}
	if h.emails == nil {
		identityStatus(w, r, 503)
		return
	}
	var input emailVerifyInput
	if !decodeIdentity(w, r, &input) {
		return
	}
	var token string
	var session Session
	_, err := h.emails.Complete(r.Context(), r.PathValue("id"), input.Code, func(ctx context.Context, tx pgx.Tx, v VerifiedEmail) error {
		var err error
		switch v.Purpose {
		case EmailPurposeRegister:
			token, session, err = h.sessions.RegisterEmail(ctx, tx, v)
		case EmailPurposeLogin:
			token, session, err = h.sessions.LoginEmail(ctx, tx, v)
		default:
			return ErrEmailInput
		}
		return err
	})
	if err != nil {
		h.logAction(r, "email_verify", "", "rejected")
		identityError(w, r, err)
		return
	}
	SetSessionCookie(w, token, session.ExpiresAt)
	h.logAction(r, "email_verify", session.AccountID, "accepted")
	h.respondMeWithEmail(w, r, session)
}

// requestEmailBind starts a challenge for the signed-in customer's own account.
func (h *IdentityHTTP) requestEmailBind(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, UserEntry, false, contract.RoleCustomer)
	if !ok {
		return
	}
	if h.emails == nil {
		identityStatus(w, r, 503)
		return
	}
	var input emailCodeInput
	if !decodeIdentity(w, r, &input) {
		return
	}
	if input.Purpose != "" && EmailPurpose(input.Purpose) != EmailPurposeBind {
		identityStatus(w, r, 400)
		return
	}
	source, ok := h.emailSource(w, r)
	if !ok {
		return
	}
	receipt, err := h.emails.Request(r.Context(), EmailPurposeBind, input.Email, s.AccountID, source)
	if err != nil {
		h.logAction(r, "email_bind_request", s.AccountID, "rejected")
		identityError(w, r, err)
		return
	}
	h.logAction(r, "email_bind_request", s.AccountID, "accepted")
	writeIdentityJSON(w, 202, receipt)
}

// verifyEmailBind attaches the verified address to the caller's own account.
func (h *IdentityHTTP) verifyEmailBind(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, UserEntry, false, contract.RoleCustomer)
	if !ok {
		return
	}
	if h.emails == nil {
		identityStatus(w, r, 503)
		return
	}
	var input emailVerifyInput
	if !decodeIdentity(w, r, &input) {
		return
	}
	_, err := h.emails.Complete(r.Context(), r.PathValue("id"), input.Code, func(ctx context.Context, tx pgx.Tx, v VerifiedEmail) error {
		return h.sessions.BindEmail(ctx, tx, s, v)
	})
	if err != nil {
		h.logAction(r, "email_bind_verify", s.AccountID, "rejected")
		if errors.Is(err, ErrInvalidEmailCode) {
			platform.WriteError(w, r, http.StatusBadRequest, "BAD_REQUEST", "验证码无效、已过期或错误次数已达上限。请检查验证码或重新获取。")
			return
		}
		identityError(w, r, err)
		return
	}
	h.logAction(r, "email_bind_verify", s.AccountID, "accepted")
	h.respondMeWithEmail(w, r, s)
}

// provisionRider opens a rider account from the admin entry only. The server,
// not the client, decides the station and the single granted role.
func (h *IdentityHTTP) provisionRider(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, AdminEntry, true, contract.RoleSecurityAdmin)
	if !ok {
		return
	}
	var input RiderProvision
	if !decodeIdentity(w, r, &input) {
		return
	}
	if h.staff == nil {
		identityStatus(w, r, http.StatusServiceUnavailable)
		return
	}
	if input.Issuer != h.staff.cfg.Issuer {
		identityError(w, r, ErrProvisionInput)
		return
	}
	rider, err := h.sessions.ProvisionRider(r.Context(), s, input, platform.RequestID(r.Context()))
	if err != nil {
		h.logAction(r, "rider_provision", s.AccountID, "rejected")
		identityError(w, r, err)
		return
	}
	h.logAction(r, "rider_provision", s.AccountID, "accepted")
	writeIdentityJSON(w, 201, rider)
}
