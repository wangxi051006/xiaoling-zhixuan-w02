package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"campus.local/xiaoling/internal/contract"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

const (
	staffCallbackPath = "/api/v1/auth/staff/callback"
	staffFlowTTL      = 5 * time.Minute
)

var ErrStaffLogin = errors.New("staff login rejected")

type StaffOIDCConfig struct {
	Issuer, AuthURL, TokenURL, JWKSURL string
	ClientID, ClientSecret, MFAContext string
}

type StaffOIDC struct {
	cfg      StaffOIDCConfig
	policy   *EntryPolicy
	clock    contract.Clock
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

func (cfg StaffOIDCConfig) Validate() error {
	for _, raw := range []string{cfg.Issuer, cfg.AuthURL, cfg.TokenURL, cfg.JWKSURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
			return errors.New("invalid staff IdP HTTPS endpoint")
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" || strings.TrimSpace(cfg.MFAContext) == "" {
		return errors.New("incomplete staff IdP configuration")
	}
	return nil
}

func NewStaffOIDC(cfg StaffOIDCConfig, policy *EntryPolicy, clock contract.Clock, clients ...*http.Client) (*StaffOIDC, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if policy == nil || clock == nil || len(clients) > 1 {
		return nil, errors.New("incomplete staff IdP configuration")
	}
	client := &http.Client{}
	if len(clients) == 1 && clients[0] != nil {
		client.Transport = clients[0].Transport
	}
	client.Timeout = 8 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), cfg.JWKSURL)
	verifier := oidc.NewVerifier(cfg.Issuer, keys, &oidc.Config{ClientID: cfg.ClientID, Now: clock.Now, SupportedSigningAlgs: []string{oidc.RS256}})
	return &StaffOIDC{cfg: cfg, policy: policy, clock: clock, verifier: verifier, client: client}, nil
}

type staffStart struct {
	AuthorizationURL, State, Nonce, Verifier string
}

func (s *StaffOIDC) oauth(entry Entry) (oauth2.Config, error) {
	if entry != RiderEntry && entry != AdminEntry && entry != MerchantEntry {
		return oauth2.Config{}, ErrStaffLogin
	}
	origin, ok := s.policy.origins[entry]
	if !ok {
		return oauth2.Config{}, ErrStaffLogin
	}
	return oauth2.Config{
		ClientID: s.cfg.ClientID, ClientSecret: s.cfg.ClientSecret,
		Endpoint:    oauth2.Endpoint{AuthURL: s.cfg.AuthURL, TokenURL: s.cfg.TokenURL, AuthStyle: oauth2.AuthStyleInHeader},
		RedirectURL: origin.origin + staffCallbackPath, Scopes: []string{oidc.ScopeOpenID},
	}, nil
}

func (s *StaffOIDC) Start(entry Entry) (staffStart, error) {
	cfg, err := s.oauth(entry)
	if err != nil {
		return staffStart{}, err
	}
	flow := staffStart{State: secretToken(), Nonce: secretToken(), Verifier: oauth2.GenerateVerifier()}
	flow.AuthorizationURL = cfg.AuthCodeURL(flow.State, oauth2.S256ChallengeOption(flow.Verifier), oidc.Nonce(flow.Nonce),
		oauth2.SetAuthURLParam("acr_values", s.cfg.MFAContext), oauth2.SetAuthURLParam("max_age", strconv.Itoa(int(recentMFAWindow/time.Second))))
	return flow, nil
}

func (s *StaffOIDC) Begin(ctx context.Context, sessions *Sessions, entry Entry) (staffStart, string, error) {
	flow, err := s.Start(entry)
	if err != nil {
		return staffStart{}, "", err
	}
	browser := secretToken()
	stateHash, browserHash := sha256.Sum256([]byte(flow.State)), sha256.Sum256([]byte(browser))
	now := s.clock.Now().UTC()
	_, err = sessions.pool.Exec(ctx, `WITH expired AS (
		DELETE FROM staff_login_flows WHERE state_digest IN (
			SELECT state_digest FROM staff_login_flows WHERE expires_at<=$7 LIMIT 64 FOR UPDATE SKIP LOCKED
		)
	) INSERT INTO staff_login_flows(state_digest,browser_digest,entry,nonce,verifier,expires_at) VALUES($1,$2,$3,$4,$5,$6)`,
		stateHash[:], browserHash[:], entry, flow.Nonce, flow.Verifier, now.Add(staffFlowTTL), now)
	if err != nil {
		return staffStart{}, "", err
	}
	return flow, browser, nil
}

func (s *StaffOIDC) Complete(ctx context.Context, sessions *Sessions, entry Entry, state, browser, code string) (string, Session, error) {
	cfg, err := s.oauth(entry)
	if err != nil || len(state) != 64 || len(browser) != 64 || code == "" || len(code) > 2048 {
		return "", Session{}, ErrStaffLogin
	}
	stateHash, browserHash := sha256.Sum256([]byte(state)), sha256.Sum256([]byte(browser))
	var nonce, verifier string
	err = sessions.pool.QueryRow(ctx, `DELETE FROM staff_login_flows
		WHERE state_digest=$1 AND browser_digest=$2 AND entry=$3 AND expires_at>$4 RETURNING nonce,verifier`,
		stateHash[:], browserHash[:], entry, s.clock.Now().UTC()).Scan(&nonce, &verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Session{}, ErrStaffLogin
	}
	if err != nil {
		return "", Session{}, err
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, s.client), 10*time.Second)
	defer cancel()
	tokens, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", Session{}, ErrStaffLogin
	}
	raw, _ := tokens.Extra("id_token").(string)
	if raw == "" || len(raw) > 16384 {
		return "", Session{}, ErrStaffLogin
	}
	idToken, err := s.verifier.Verify(ctx, raw)
	if err != nil || idToken.Issuer != s.cfg.Issuer {
		return "", Session{}, ErrStaffLogin
	}
	var claims staffClaims
	if err := idToken.Claims(&claims); err != nil {
		return "", Session{}, ErrStaffLogin
	}
	if idToken.AccessTokenHash != "" && idToken.VerifyAccessToken(tokens.AccessToken) != nil {
		return "", Session{}, ErrStaffLogin
	}
	mfa, err := validateStaffClaims(claims, nonce, s.cfg.MFAContext, s.cfg.ClientID, len(idToken.Audience), s.clock.Now().UTC())
	if err != nil {
		return "", Session{}, err
	}
	token, session, err := sessions.issue(ctx, "", s.cfg.Issuer, claims.Subject, "", entry, mfa)
	if errors.Is(err, ErrSession) {
		return "", Session{}, ErrStaffLogin
	}
	return token, session, err
}

type staffClaims struct {
	Subject         string `json:"sub"`
	Nonce           string `json:"nonce"`
	ACR             string `json:"acr"`
	AuthorizedParty string `json:"azp"`
	AuthTime        int64  `json:"auth_time"`
}

func validateStaffClaims(claims staffClaims, nonce, acr, clientID string, audiences int, now time.Time) (time.Time, error) {
	mfa := time.Unix(claims.AuthTime, 0).UTC()
	if claims.Subject == "" || len(claims.Subject) > 255 || nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 ||
		acr == "" || claims.ACR != acr || audiences < 1 || (audiences > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != clientID ||
		claims.AuthTime <= 0 || mfa.After(now) || now.Sub(mfa) >= recentMFAWindow {
		return time.Time{}, ErrStaffLogin
	}
	return mfa, nil
}
