package identity

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"campus.local/xiaoling/internal/contract"
	"campus.local/xiaoling/internal/platform"
)

const staffBrowserCookie = "__Host-xiaoling-staff-flow"

type IdentityHTTP struct {
	policy    *EntryPolicy
	sessions  *Sessions
	directory *Directory
	otp       *OTP
	emails    *EmailCodes
	staff     *StaffOIDC
	logger    *slog.Logger
}

func NewIdentityHTTP(policy *EntryPolicy, sessions *Sessions, directory *Directory, otp *OTP, staff *StaffOIDC, logger *slog.Logger) *IdentityHTTP {
	return &IdentityHTTP{policy: policy, sessions: sessions, directory: directory, otp: otp, staff: staff, logger: logger}
}

// WithEmailCodes enables the email verification endpoints. Without it they keep
// answering 503, so a deployment that has no sender cannot half-enable the flow.
func (h *IdentityHTTP) WithEmailCodes(codes *EmailCodes) *IdentityHTTP {
	h.emails = codes
	return h
}

func (h *IdentityHTTP) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/otp/request", h.requestOTP)
	mux.HandleFunc("POST /api/v1/auth/otp/verify", h.verifyOTP)
	mux.HandleFunc("POST /api/v1/auth/email/challenges", h.requestEmailCode)
	mux.HandleFunc("POST /api/v1/auth/email/challenges/{id}/verify", h.verifyEmailCode)
	mux.HandleFunc("POST /api/v1/me/email/challenges", h.requestEmailBind)
	mux.HandleFunc("POST /api/v1/me/email/challenges/{id}/verify", h.verifyEmailBind)
	mux.HandleFunc("POST /api/v1/admin/riders/provision", h.provisionRider)
	mux.HandleFunc("GET /api/v1/auth/staff/start", h.startStaff)
	mux.HandleFunc("GET /api/v1/auth/staff/callback", h.completeStaff)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/me", h.me)
	mux.HandleFunc("GET /api/v1/me/service-buildings", h.listServiceBuildings)
	mux.HandleFunc("GET /api/v1/me/addresses", h.listAddresses)
	mux.HandleFunc("POST /api/v1/me/addresses", h.createAddress)
	mux.HandleFunc("PATCH /api/v1/me/addresses/{id}", h.updateAddress)
	mux.HandleFunc("DELETE /api/v1/me/addresses/{id}", h.deleteAddress)
	mux.HandleFunc("GET /api/v1/admin/riders", h.listRiders)
	mux.HandleFunc("GET /api/v1/admin/riders/{id}", h.getRider)
	mux.HandleFunc("PATCH /api/v1/admin/riders/{id}", h.updateRider)
}

func writeIdentityJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func identityStatus(w http.ResponseWriter, r *http.Request, status int) {
	codes := map[int]string{400: "BAD_REQUEST", 401: "UNAUTHENTICATED", 403: "FORBIDDEN", 404: "NOT_FOUND", 409: "INVALID_STATE", 413: "BODY_TOO_LARGE", 429: "RATE_LIMITED", 500: "INTERNAL_ERROR", 503: "NOT_READY"}
	platform.WriteError(w, r, status, codes[status], http.StatusText(status))
}

func identityError(w http.ResponseWriter, r *http.Request, err error) {
	status := AccessStatus(err)
	switch {
	case errors.Is(err, ErrOTPInput), errors.Is(err, ErrAddressInput), errors.Is(err, ErrRiderInput),
		errors.Is(err, ErrEmailInput), errors.Is(err, ErrProvisionInput):
		status = 400
	case errors.Is(err, ErrInvalidOTP), errors.Is(err, ErrStaffLogin), errors.Is(err, ErrSession),
		errors.Is(err, ErrInvalidEmailCode):
		status = 401
	case errors.Is(err, ErrObjectNotFound):
		status = 404
	case errors.Is(err, ErrVersionConflict), errors.Is(err, ErrEmailAlreadyOwned), errors.Is(err, ErrStaffIdentityBound):
		status = 409
	case errors.Is(err, ErrRegistrationClosed):
		status = 403
	case errors.Is(err, ErrRateLimit):
		status = 429
	case errors.Is(err, ErrOTPSend), errors.Is(err, ErrEmailSend):
		status = 503
	}
	identityStatus(w, r, status)
}

func decodeIdentity(w http.ResponseWriter, r *http.Request, out any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		identityStatus(w, r, 400)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	err = dec.Decode(out)
	if err == nil {
		err = dec.Decode(new(any))
		if err == io.EOF {
			return true
		}
	}
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		identityStatus(w, r, 413)
	} else {
		identityStatus(w, r, 400)
	}
	return false
}

func (h *IdentityHTTP) entry(w http.ResponseWriter, r *http.Request, entries ...Entry) (Entry, bool) {
	if h.policy == nil || h.sessions == nil || h.sessions.pool == nil || h.sessions.clock == nil {
		identityStatus(w, r, 503)
		return "", false
	}
	for _, entry := range entries {
		if strings.EqualFold(r.Host, h.policy.origins[entry].host) {
			return entry, true
		}
	}
	identityStatus(w, r, 404)
	return "", false
}

func (h *IdentityHTTP) sameOrigin(w http.ResponseWriter, r *http.Request, entry Entry) bool {
	values := r.Header.Values("Origin")
	if len(values) == 1 && values[0] == h.policy.origins[entry].origin {
		return true
	}
	identityStatus(w, r, 403)
	return false
}

func cookieValue(r *http.Request, name string) string {
	cookies := r.CookiesNamed(name)
	if len(cookies) != 1 {
		return ""
	}
	return cookies[0].Value
}

var allRoles = []contract.Role{contract.RoleCustomer, contract.RoleRider, contract.RoleOperator, contract.RoleFinance, contract.RoleSecurityAdmin}

func (h *IdentityHTTP) authorized(w http.ResponseWriter, r *http.Request, entry Entry, mfa bool, roles ...contract.Role) (Session, bool) {
	session, err := h.sessions.lookup(r.Context(), cookieValue(r, SessionCookieName), entry, h.policy.local && loopbackPeer(r))
	if err == nil {
		err = h.policy.Authorize(r, entry, session, h.sessions.clock.Now(), mfa, roles...)
	}
	if err != nil {
		identityError(w, r, err)
		return Session{}, false
	}
	return session, true
}

func (h *IdentityHTTP) Authenticated(w http.ResponseWriter, r *http.Request, entry Entry, mfa bool, roles ...contract.Role) (Session, bool) {
	if _, ok := h.entry(w, r, entry); !ok {
		return Session{}, false
	}
	return h.authorized(w, r, entry, mfa, roles...)
}

func (h *IdentityHTTP) logAction(r *http.Request, action, actor, outcome string) {
	if h.logger != nil {
		h.logger.InfoContext(r.Context(), "identity_action", "action", action, "account_id", actor, "request_id", platform.RequestID(r.Context()), "result", outcome)
	}
}

type meResponse struct {
	ID        string          `json:"id"`
	StationID string          `json:"station_id"`
	Roles     []contract.Role `json:"roles"`
	CSRFToken string          `json:"csrf_token"`
	Email     *string         `json:"email"`
}

func respondMe(w http.ResponseWriter, s Session) {
	writeIdentityJSON(w, 200, meResponse{s.AccountID, s.StationID, s.Roles, s.CSRFToken, nil})
}

// respondMeWithEmail also reports the verified mailbox of the signed-in customer.
func (h *IdentityHTTP) respondMeWithEmail(w http.ResponseWriter, r *http.Request, s Session) {
	email, err := h.directory.AccountEmail(r.Context(), s.AccountID)
	if err != nil {
		identityError(w, r, err)
		return
	}
	writeIdentityJSON(w, 200, meResponse{s.AccountID, s.StationID, s.Roles, s.CSRFToken, email})
}

func (h *IdentityHTTP) requestOTP(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, UserEntry)
	if !ok || !h.sameOrigin(w, r, entry) {
		return
	}
	if h.otp == nil {
		identityStatus(w, r, 503)
		return
	}
	var input struct {
		Phone string `json:"phone"`
	}
	if !decodeIdentity(w, r, &input) {
		return
	}
	source, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		identityStatus(w, r, 400)
		return
	}
	receipt, err := h.otp.Request(r.Context(), input.Phone, source)
	if err != nil {
		h.logAction(r, "otp_request", "", "rejected")
		identityError(w, r, err)
		return
	}
	h.logAction(r, "otp_request", "", "accepted")
	writeIdentityJSON(w, 202, receipt)
}

func (h *IdentityHTTP) verifyOTP(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, UserEntry)
	if !ok || !h.sameOrigin(w, r, entry) {
		return
	}
	if h.otp == nil {
		identityStatus(w, r, 503)
		return
	}
	var input struct {
		ID   string `json:"challenge_id"`
		Code string `json:"code"`
	}
	if !decodeIdentity(w, r, &input) {
		return
	}
	if input.ID == "" || len(input.ID) > 128 || !codePattern.MatchString(input.Code) {
		identityStatus(w, r, 400)
		return
	}
	phone, err := h.otp.Verify(r.Context(), input.ID, input.Code)
	if err != nil {
		h.logAction(r, "otp_verify", "", "rejected")
		identityError(w, r, err)
		return
	}
	token, session, err := h.sessions.issueCustomer(r.Context(), phone)
	if err != nil {
		h.logAction(r, "otp_verify", "", "rejected")
		identityError(w, r, err)
		return
	}
	SetSessionCookie(w, token, session.ExpiresAt)
	h.logAction(r, "otp_verify", session.AccountID, "accepted")
	respondMe(w, session)
}

func staffHeaders(w http.ResponseWriter) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

func (h *IdentityHTTP) startStaff(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, AdminEntry, RiderEntry)
	if !ok {
		return
	}
	staffHeaders(w)
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		identityStatus(w, r, 400)
		return
	}
	if h.staff == nil {
		identityStatus(w, r, 503)
		return
	}
	flow, browser, err := h.staff.Begin(r.Context(), h.sessions, entry)
	if err != nil {
		identityError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: staffBrowserCookie, Value: browser, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(staffFlowTTL.Seconds())})
	w.Header().Set("Location", flow.AuthorizationURL)
	w.WriteHeader(302)
}

func (h *IdentityHTTP) completeStaff(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, AdminEntry, RiderEntry)
	if !ok {
		return
	}
	staffHeaders(w)
	if h.staff == nil {
		identityStatus(w, r, 503)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || r.Method != http.MethodGet || len(r.URL.RawQuery) > 8192 || len(q["state"]) != 1 || len(q["code"]) != 1 || q.Has("error") || (q.Has("iss") && (len(q["iss"]) != 1 || q.Get("iss") != h.staff.cfg.Issuer)) {
		identityStatus(w, r, 400)
		return
	}
	token, session, err := h.staff.Complete(r.Context(), h.sessions, entry, q.Get("state"), cookieValue(r, staffBrowserCookie), q.Get("code"))
	if err != nil {
		h.logAction(r, "staff_login", "", "rejected")
		identityError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: staffBrowserCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	SetSessionCookie(w, token, session.ExpiresAt)
	h.logAction(r, "staff_login", session.AccountID, "accepted")
	w.Header().Set("Location", h.policy.origins[entry].origin+"/")
	w.WriteHeader(302)
}

func (h *IdentityHTTP) me(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, UserEntry, RiderEntry, AdminEntry)
	if !ok {
		return
	}
	if session, ok := h.authorized(w, r, entry, false, allRoles...); ok {
		h.respondMeWithEmail(w, r, session)
	}
}

func (h *IdentityHTTP) logout(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.entry(w, r, UserEntry, RiderEntry, AdminEntry)
	if !ok {
		return
	}
	session, ok := h.authorized(w, r, entry, false, allRoles...)
	if !ok {
		return
	}
	if err := h.sessions.Revoke(r.Context(), cookieValue(r, SessionCookieName), entry); err != nil {
		identityError(w, r, err)
		return
	}
	ClearSessionCookie(w)
	h.logAction(r, "logout", session.AccountID, "accepted")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

const maxVersion = 9007199254740991

func pagination(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	limit, cursor := 20, q.Get("cursor")
	if q.Has("limit") && err == nil {
		limit, err = strconv.Atoi(q.Get("limit"))
	}
	if err != nil || len(q["limit"]) > 1 || len(q["cursor"]) > 1 || limit < 1 || limit > 100 || q.Has("cursor") && (cursor == "" || len(cursor) > 128) {
		identityStatus(w, r, 400)
		return "", 0, false
	}
	return cursor, limit, true
}

type identityPage[T any] struct {
	Items []T     `json:"items"`
	Next  *string `json:"next_cursor"`
}

func identityList[T any](items []T, limit int, id func(T) string) identityPage[T] {
	if items == nil {
		items = []T{}
	}
	var next *string
	if len(items) > limit {
		cursor := id(items[limit-1])
		next = &cursor
		items = items[:limit]
	}
	return identityPage[T]{items, next}
}

type addressResponse struct {
	ID           string `json:"id"`
	BuildingID   string `json:"building_id"`
	BuildingName string `json:"building_name"`
	Room         string `json:"room"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	Version      int64  `json:"version"`
}

func showAddress(a contract.AddressSnapshot) addressResponse {
	return addressResponse{a.AddressID, a.BuildingID, a.BuildingName, a.Room, a.ContactName, a.ContactPhone, a.Version}
}

func (h *IdentityHTTP) listServiceBuildings(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, UserEntry, false, contract.RoleCustomer)
	if !ok {
		return
	}
	cursor, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := h.directory.ListServiceBuildings(r.Context(), s.StationID, cursor, limit+1)
	if err != nil {
		identityError(w, r, err)
		return
	}
	writeIdentityJSON(w, http.StatusOK, identityList(rows, limit, func(b ServiceBuilding) string { return b.ID }))
}

func (h *IdentityHTTP) listAddresses(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, UserEntry, false, contract.RoleCustomer)
	if !ok {
		return
	}
	cursor, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := h.directory.ListAddresses(r.Context(), s.AccountID, s.StationID, cursor, limit+1)
	if err != nil {
		identityError(w, r, err)
		return
	}
	items := make([]addressResponse, 0, len(rows))
	for _, a := range rows {
		items = append(items, showAddress(a))
	}
	writeIdentityJSON(w, 200, identityList(items, limit, func(a addressResponse) string { return a.ID }))
}

func (h *IdentityHTTP) createAddress(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, UserEntry, false, contract.RoleCustomer)
	if !ok {
		return
	}
	var input AddressInput
	if !decodeIdentity(w, r, &input) {
		return
	}
	a, err := h.directory.CreateAddress(r.Context(), s.AccountID, s.StationID, input)
	if err != nil {
		identityError(w, r, err)
		return
	}
	h.logAction(r, "address_create", s.AccountID, "accepted")
	writeIdentityJSON(w, 201, showAddress(a))
}

func (h *IdentityHTTP) updateAddress(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, UserEntry, false, contract.RoleCustomer)
	if !ok {
		return
	}
	var input struct {
		AddressPatch
		Version int64 `json:"expected_version"`
	}
	if !decodeIdentity(w, r, &input) {
		return
	}
	if input.Version < 1 || input.Version > maxVersion {
		identityStatus(w, r, 400)
		return
	}
	a, err := h.directory.UpdateAddress(r.Context(), s.AccountID, s.StationID, r.PathValue("id"), input.Version, input.AddressPatch)
	if err != nil {
		identityError(w, r, err)
		return
	}
	h.logAction(r, "address_update", s.AccountID, "accepted")
	writeIdentityJSON(w, 200, showAddress(a))
}

func (h *IdentityHTTP) deleteAddress(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, UserEntry, false, contract.RoleCustomer)
	if !ok {
		return
	}
	values := r.Header.Values("If-Match")
	if len(values) != 1 || len(values[0]) < 3 || values[0][0] != '"' || values[0][len(values[0])-1] != '"' {
		identityStatus(w, r, 400)
		return
	}
	version, err := strconv.ParseInt(values[0][1:len(values[0])-1], 10, 64)
	if err != nil || version < 1 || version > maxVersion || strconv.FormatInt(version, 10) != values[0][1:len(values[0])-1] {
		identityStatus(w, r, 400)
		return
	}
	if err := h.directory.DeleteAddress(r.Context(), s.AccountID, s.StationID, r.PathValue("id"), version); err != nil {
		identityError(w, r, err)
		return
	}
	h.logAction(r, "address_delete", s.AccountID, "accepted")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}

func (h *IdentityHTTP) listRiders(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, AdminEntry, false, contract.RoleOperator)
	if !ok {
		return
	}
	cursor, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	items, err := h.directory.ListRiders(r.Context(), s.StationID, cursor, limit+1)
	if err != nil {
		identityError(w, r, err)
		return
	}
	writeIdentityJSON(w, 200, identityList(items, limit, func(a Rider) string { return a.ID }))
}

func (h *IdentityHTTP) getRider(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, AdminEntry, false, contract.RoleOperator)
	if !ok {
		return
	}
	rider, err := h.directory.GetRider(r.Context(), s.StationID, r.PathValue("id"))
	if err != nil {
		identityError(w, r, err)
		return
	}
	writeIdentityJSON(w, 200, rider)
}

func (h *IdentityHTTP) updateRider(w http.ResponseWriter, r *http.Request) {
	s, ok := h.Authenticated(w, r, AdminEntry, true, contract.RoleOperator)
	if !ok {
		return
	}
	var input struct {
		Enabled *bool  `json:"enabled"`
		Version int64  `json:"expected_version"`
		Reason  string `json:"reason_code"`
	}
	if !decodeIdentity(w, r, &input) {
		return
	}
	if input.Enabled == nil || input.Version < 1 || input.Version > maxVersion || !reasonCodePattern.MatchString(input.Reason) {
		identityStatus(w, r, 400)
		return
	}
	rider, err := h.directory.SetRiderEnabled(r.Context(), s.AccountID, s.StationID, r.PathValue("id"), *input.Enabled, input.Version, input.Reason, platform.RequestID(r.Context()))
	if err != nil {
		h.logAction(r, "rider_status", s.AccountID, "rejected")
		identityError(w, r, err)
		return
	}
	h.logAction(r, "rider_status", s.AccountID, "accepted")
	writeIdentityJSON(w, 200, rider)
}
