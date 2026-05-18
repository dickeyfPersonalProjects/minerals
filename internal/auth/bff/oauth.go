package bff

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Tokens is the OAuth response shape — both Exchange and Refresh
// flatten the raw oauth2.Token + Keycloak's `refresh_expires_in`
// extra + the OIDC `id_token` extra into one struct so the caller
// never has to reach into Extra("...").
//
// Keycloak rotates the refresh token on every use (refresh-token
// rotation): the value in Refresh's return is a fresh string, not
// the one passed in. UpdateTokens replaces all four fields together
// to keep the row consistent.
type Tokens struct {
	AccessToken           string
	RefreshToken          string
	IDToken               string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
}

// OAuthClient is the boundary between BFF auth code and the
// Keycloak/OIDC libraries. Handlers and middleware depend ONLY on
// this interface — the underlying golang.org/x/oauth2 +
// coreos/go-oidc plumbing stays inside the package (docs/design/auth-bff.md
// §microservice-extraction).
type OAuthClient interface {
	// AuthCodeURL is the URL to redirect the browser to for the
	// authorization-code flow. state is the CSRF-defending nonce
	// the handler stored server-side. redirectURI overrides any
	// configured default — the same URI used here MUST be passed
	// to Exchange so Keycloak's redirect_uri validation succeeds.
	AuthCodeURL(state, redirectURI string) string

	// Exchange swaps an authorization code for tokens. redirectURI
	// must match the one used in AuthCodeURL for the same state.
	Exchange(ctx context.Context, code, redirectURI string) (Tokens, error)

	// Refresh uses a refresh token to mint a new token set. The
	// returned Tokens.RefreshToken is rotated (Keycloak invalidates
	// the input on success) — callers MUST persist the new value
	// or the next refresh will fail token-family detection.
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)

	// EndSessionURL is the Keycloak logout URL the BFF 302s to
	// after invalidating its own session row. idTokenHint lets
	// Keycloak terminate the SSO session without a user
	// confirmation prompt. postLogoutRedirectURI is where Keycloak
	// sends the browser after — must be on Keycloak's
	// post_logout_redirect_uris allowlist.
	EndSessionURL(idTokenHint, postLogoutRedirectURI string) string
}

// OAuthConfig is the static configuration for a Keycloak OAuth
// client. Issuer/ClientID/ClientSecret arrive from env vars
// (OIDC_ISSUER_URL, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET — see
// docs/design/auth-bff.md §configuration); the handlers populate
// DefaultRedirectURI / Scopes at construction.
type OAuthConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// Scopes are the OAuth scopes requested at AuthCodeURL time.
	// "openid" is required for OIDC; "profile" + "email" + "roles"
	// match Keycloak's standard claim-emitting scopes.
	Scopes []string
}

// keycloakClient implements OAuthClient against a Keycloak realm. It
// performs OIDC discovery once at construction so per-request work
// stays cheap and a misconfigured issuer fails fast at boot.
type keycloakClient struct {
	cfg           OAuthConfig
	endpoints     oauth2.Endpoint
	endSessionURL string
	// oauth2Config is the base config reused for AuthCodeURL,
	// Exchange and Refresh. Each call clones it before setting
	// RedirectURL so the shared instance never mutates.
	oauth2Config *oauth2.Config
}

// providerExtraClaims is the subset of the OIDC discovery document
// we need beyond what oauth2.Endpoint covers. Keycloak puts the
// logout URL under `end_session_endpoint` (RFC OIDC session-management
// draft).
type providerExtraClaims struct {
	EndSessionEndpoint string `json:"end_session_endpoint"`
}

// NewKeycloakOAuthClient discovers the OAuth/OIDC endpoints for cfg.Issuer
// and returns an OAuthClient. Discovery is a network call against the
// issuer's `.well-known/openid-configuration` endpoint — pass a ctx
// with a sensible timeout from the server bootstrap path. The result
// is safe for concurrent use.
//
// We pull the endpoints up front (rather than per-request) for two
// reasons: a misconfigured issuer surfaces at boot, not on the first
// user login; and AuthCodeURL/EndSessionURL stay pure-string builders
// with no hidden I/O, which the tests rely on.
func NewKeycloakOAuthClient(ctx context.Context, cfg OAuthConfig) (OAuthClient, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("bff: OAuthConfig.Issuer is required")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("bff: OAuthConfig.ClientID is required")
	}
	if cfg.ClientSecret == "" {
		return nil, errors.New("bff: OAuthConfig.ClientSecret is required")
	}

	provider, err := gooidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("bff: OIDC discovery: %w", err)
	}
	var extra providerExtraClaims
	if err := provider.Claims(&extra); err != nil {
		return nil, fmt.Errorf("bff: parse OIDC discovery extras: %w", err)
	}
	return newKeycloakClientFromEndpoints(cfg, provider.Endpoint(), extra.EndSessionEndpoint), nil
}

// newKeycloakClientFromEndpoints is the discovery-free constructor
// the tests use to avoid spinning up a full OIDC well-known document.
// Production callers go through NewKeycloakOAuthClient.
func newKeycloakClientFromEndpoints(cfg OAuthConfig, ep oauth2.Endpoint, endSessionURL string) *keycloakClient {
	return &keycloakClient{
		cfg:           cfg,
		endpoints:     ep,
		endSessionURL: endSessionURL,
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     ep,
			Scopes:       cfg.Scopes,
		},
	}
}

// AuthCodeURL builds the authorization redirect URL. We construct a
// per-call oauth2.Config copy with RedirectURL set so the same
// keycloakClient can serve multiple front-end origins (e.g. dev +
// staging) without mutating shared state.
func (c *keycloakClient) AuthCodeURL(state, redirectURI string) string {
	cfg := *c.oauth2Config
	cfg.RedirectURL = redirectURI
	return cfg.AuthCodeURL(state)
}

// Exchange performs the server-to-server code exchange against
// Keycloak's token endpoint. The returned Tokens carries the OIDC
// id_token (pulled from the response's `id_token` extra) and the
// refresh expiry (pulled from `refresh_expires_in`) — both are
// Keycloak-specific extensions on top of the bare oauth2.Token.
func (c *keycloakClient) Exchange(ctx context.Context, code, redirectURI string) (Tokens, error) {
	cfg := *c.oauth2Config
	cfg.RedirectURL = redirectURI
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return Tokens{}, fmt.Errorf("bff: code exchange: %w", err)
	}
	return tokensFromOAuth2(tok)
}

// Refresh exchanges a refresh_token for a fresh token set. We drive
// oauth2.TokenSource directly (rather than wrapping the original
// Token) because the BFF doesn't keep an in-memory Token between
// calls — every refresh is initiated from a fresh DB read, and the
// rotated refresh token is persisted before the next request runs.
func (c *keycloakClient) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	src := c.oauth2Config.TokenSource(ctx, &oauth2.Token{
		RefreshToken: refreshToken,
		// Setting Expiry in the past forces TokenSource to call
		// the refresh endpoint immediately rather than returning
		// the (empty) access token unchanged.
		Expiry: time.Unix(0, 0),
	})
	tok, err := src.Token()
	if err != nil {
		return Tokens{}, fmt.Errorf("bff: refresh: %w", err)
	}
	return tokensFromOAuth2(tok)
}

// EndSessionURL builds Keycloak's logout URL with id_token_hint and
// post_logout_redirect_uri query params. The fields use URL escaping
// via net/url so caller-supplied strings cannot inject params.
func (c *keycloakClient) EndSessionURL(idTokenHint, postLogoutRedirectURI string) string {
	if c.endSessionURL == "" {
		return ""
	}
	u, err := url.Parse(c.endSessionURL)
	if err != nil {
		// Discovery returned a non-URL; surface an empty string
		// so the handler can fall back to its post-logout
		// redirect target without a server-side panic.
		return ""
	}
	q := u.Query()
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	if postLogoutRedirectURI != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// tokensFromOAuth2 unpacks the oauth2.Token + Keycloak extras into
// the bff Tokens struct. Missing id_token surfaces as an error — the
// BFF needs it for the logout `id_token_hint`, and a token endpoint
// silently dropping it would otherwise stash an empty string in the
// row and break logout later.
func tokensFromOAuth2(tok *oauth2.Token) (Tokens, error) {
	idToken, _ := tok.Extra("id_token").(string)
	if idToken == "" {
		return Tokens{}, errors.New("bff: token response missing id_token")
	}

	refreshExpiresAt := tok.Expiry
	if v, ok := tok.Extra("refresh_expires_in").(float64); ok && v > 0 {
		// The JSON decoder uses float64 for unmarshalled numbers
		// regardless of whether the field is an integer in the
		// wire format. The cast is the same one
		// oauth2.tokenJSON.expiresIn uses internally.
		refreshExpiresAt = time.Now().Add(time.Duration(v) * time.Second)
	}

	return Tokens{
		AccessToken:           tok.AccessToken,
		RefreshToken:          tok.RefreshToken,
		IDToken:               idToken,
		AccessTokenExpiresAt:  tok.Expiry,
		RefreshTokenExpiresAt: refreshExpiresAt,
	}, nil
}
