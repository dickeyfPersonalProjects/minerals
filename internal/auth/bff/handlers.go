package bff

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UserResolver maps a verified Keycloak identity (sub + email) to
// the application's users.id. The Postgres implementation calls
// the same first-login resolve-or-create logic the JWT bearer path
// uses (mi-2hf) — the two auth flows MUST land on the same users
// row, or a Keycloak user logging in via cookie would shadow their
// own bearer-path row.
//
// A function rather than an interface so the bff package does not
// pull in internal/api (per docs/design/auth-bff.md
// §microservice-extraction); the wiring in cmd/minerals adapts the
// api package's resolver into this shape.
type UserResolver func(ctx context.Context, sub, email string) (uuid.UUID, error)

// HandlerConfig is the per-environment configuration for the three
// /auth handlers. Everything that varies between dev / staging /
// prod (URLs, secrets, cookie flags) flows through this struct so
// the handlers themselves are pure functions of (request, deps).
type HandlerConfig struct {
	// RedirectURI is the absolute URL the BFF sends to Keycloak as
	// `redirect_uri` on /auth/login and reuses verbatim in the
	// Exchange call on /auth/callback. MUST be an exact match for a
	// `valid_redirect_uris` entry on the Keycloak client; mismatch
	// surfaces as an opaque "Invalid redirect URI" at the Keycloak
	// login screen and is the top BFF configuration footgun.
	RedirectURI string

	// PostLogoutRedirectURI is where Keycloak sends the browser
	// after the SSO logout completes. MUST be on Keycloak's
	// `post_logout_redirect_uris` allowlist. Empty disables the
	// 302-to-Keycloak step — the handler still revokes its own
	// session and clears the cookie, then returns 204 No Content.
	PostLogoutRedirectURI string

	// StateHMACKey signs the short-lived state cookie. 32-byte
	// minimum enforced by SignState; NewHandlers rejects shorter
	// keys at construction so a misconfigured env var fails at
	// boot, not on the first user login.
	StateHMACKey []byte

	// SessionAbsoluteMax is the hard cap on a single session's
	// lifetime. Stamped into auth.sessions.absolute_expires_at on
	// Create. The session middleware (mi-ken4) revokes sessions
	// past this even if Keycloak would still issue a refresh.
	// Default per design: 7 days.
	SessionAbsoluteMax time.Duration

	// Cookie carries the session-cookie attributes (Path, Secure,
	// SameSite, MaxAge). The set/clear pair is invariant — see
	// CookieConfig — and is shared with the future session
	// middleware so the cookie a callback emits matches the cookie
	// the middleware reads.
	Cookie CookieConfig

	// StateCookieSecure mirrors Cookie.Secure for the short-lived
	// state cookie. A separate field so a deployment that wants
	// asymmetric behavior (testing) stays explicit; normally
	// callers pass the same value as Cookie.Secure.
	StateCookieSecure bool

	// EnforceCSRFOnLogout gates the logout-handler CSRF check. The
	// generic CSRF middleware lands in mi-gbzs (#5); until that
	// ships and the SPA (mi-3vc4) wires the X-CSRF-Token header,
	// deployments run with this false so the existing SPA logout
	// keeps working. Production flips it true once the SPA wiring
	// lands.
	EnforceCSRFOnLogout bool

	// TrustForwardedFor controls whether the callback handler
	// extracts the client IP from the leftmost X-Forwarded-For
	// entry. True only when the Ingress strips/normalises the
	// header so a hostile client cannot spoof its way into the
	// session row's IP forensics column.
	TrustForwardedFor bool

	// Clock is the time source used for state-cookie expiry and
	// session absolute-expires computation. Defaults to time.Now
	// when nil; tests inject a fixed clock so assertions stay
	// deterministic.
	Clock func() time.Time
}

// HandlerDeps gathers the runtime collaborators the handlers need:
// the OAuth client, the session resolver, and the bridge into the
// application's user table.
type HandlerDeps struct {
	OAuth    OAuthClient
	Sessions SessionResolver
	Users    UserResolver
}

// Handlers serves GET /auth/login, GET /auth/callback, and POST
// /auth/logout per docs/design/auth-bff.md. Construct once at
// server bootstrap (NewHandlers) and call RegisterRoutes on the
// main mux. The three routes MUST NOT be wrapped by any
// session-middleware chain — login + callback are pre-session by
// definition, and logout reads its own cookie.
type Handlers struct {
	cfg  HandlerConfig
	deps HandlerDeps
	now  func() time.Time
}

// NewHandlers validates cfg + deps and returns a ready *Handlers.
// Performs no I/O; the OAuth discovery happened in
// NewKeycloakOAuthClient (or did not, in unit tests).
func NewHandlers(cfg HandlerConfig, deps HandlerDeps) (*Handlers, error) {
	if cfg.RedirectURI == "" {
		return nil, errors.New("bff: HandlerConfig.RedirectURI is required")
	}
	if len(cfg.StateHMACKey) < minStateHMACKeyLen {
		return nil, fmt.Errorf("bff: HandlerConfig.StateHMACKey must be >= %d bytes, got %d",
			minStateHMACKeyLen, len(cfg.StateHMACKey))
	}
	if cfg.SessionAbsoluteMax <= 0 {
		return nil, errors.New("bff: HandlerConfig.SessionAbsoluteMax must be > 0")
	}
	if cfg.Cookie.Path == "" {
		return nil, errors.New("bff: HandlerConfig.Cookie.Path is required")
	}
	if deps.OAuth == nil {
		return nil, errors.New("bff: HandlerDeps.OAuth is required")
	}
	if deps.Sessions == nil {
		return nil, errors.New("bff: HandlerDeps.Sessions is required")
	}
	if deps.Users == nil {
		return nil, errors.New("bff: HandlerDeps.Users is required")
	}
	clk := cfg.Clock
	if clk == nil {
		clk = time.Now
	}
	return &Handlers{cfg: cfg, deps: deps, now: clk}, nil
}

// RegisterRoutes wires the three handlers onto mux. Uses Go 1.22's
// method-prefixed patterns so a wrong-method request gets a 405
// straight from the mux rather than a 200 from a no-method
// matcher.
//
// The handlers register at the top-level mux because the public
// middleware chain (Recovery / RequestID / SecurityHeaders / CSP /
// Logging) wraps everything, and these routes MUST be in that
// chain but MUST NOT be in the future session middleware chain
// (which is scoped to /api/v1).
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", h.Login)
	mux.HandleFunc("GET /auth/callback", h.Callback)
	mux.HandleFunc("POST /auth/logout", h.Logout)
}

// Login starts the OAuth code flow. Generates a 32-byte state,
// stashes {state, return_to, expires} in an HMAC-signed cookie,
// and 302s the browser to Keycloak's authorization endpoint.
//
// The return_to is taken from `?return_to=`, validated to be a
// same-origin path (starts with "/", not "//"), and rejected
// otherwise — an attacker-controlled absolute URL would otherwise
// turn the callback into an open redirector.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	returnTo := validateReturnTo(r.URL.Query().Get("return_to"))

	state, err := NewStateToken()
	if err != nil {
		slog.ErrorContext(r.Context(), "auth.login: state token", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal_error", "login init failed")
		return
	}
	signed, err := SignState(h.cfg.StateHMACKey, StateData{
		State:    state,
		ReturnTo: returnTo,
		Expires:  h.now().Add(StateTTL),
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "auth.login: sign state", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal_error", "login init failed")
		return
	}
	SetStateCookie(w, signed, h.cfg.StateCookieSecure)

	http.Redirect(w, r, h.deps.OAuth.AuthCodeURL(state, h.cfg.RedirectURI), http.StatusFound)
}

// Callback completes the OAuth code flow. Validates the state
// cookie (HMAC + expiry + constant-time match against the query
// `state`), exchanges `code` for tokens, resolves the user
// identity, persists a session row, and 302s the browser to the
// validated return_to (default "/").
//
// Every failure path clears the state cookie — it is spent
// regardless of outcome, and leaving stale state on the browser
// causes a confusing "invalid_state" the next time the user retries.
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// IdP-side error response: Keycloak surfaces consent-screen
	// rejection, expired authorization request, misconfigured
	// client, etc. here. Render a clean page rather than a 500 —
	// the user did nothing wrong, and a 500 would trip uptime
	// alarms on a fundamentally external cause.
	if errParam := q.Get("error"); errParam != "" {
		slog.WarnContext(r.Context(), "auth.callback: idp error",
			"err", errParam, "description", q.Get("error_description"))
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusBadRequest, "oauth_error",
			"Authentication failed at the identity provider. Please try again.")
		return
	}

	stateCookie, err := r.Cookie(StateCookieName)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_state", "missing state cookie")
		return
	}
	sd, err := VerifyState(h.cfg.StateHMACKey, stateCookie.Value, h.now())
	if err != nil {
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusBadRequest, "invalid_state", "invalid state cookie")
		return
	}

	queryState := q.Get("state")
	if subtle.ConstantTimeCompare([]byte(queryState), []byte(sd.State)) != 1 {
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusBadRequest, "invalid_state", "state mismatch")
		return
	}

	code := q.Get("code")
	if code == "" {
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusBadRequest, "invalid_request", "missing code")
		return
	}

	tokens, err := h.deps.OAuth.Exchange(r.Context(), code, h.cfg.RedirectURI)
	if err != nil {
		slog.WarnContext(r.Context(), "auth.callback: exchange failed", "err", err)
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusBadGateway, "exchange_failed",
			"Authentication failed. Please try again.")
		return
	}

	sub, email, err := claimsFromIDToken(tokens.IDToken)
	if err != nil {
		slog.WarnContext(r.Context(), "auth.callback: parse id_token", "err", err)
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusBadGateway, "invalid_id_token",
			"Authentication failed. Please try again.")
		return
	}

	userID, err := h.deps.Users(r.Context(), sub, email)
	if err != nil {
		slog.ErrorContext(r.Context(), "auth.callback: resolve user", "sub", sub, "err", err)
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusInternalServerError, "internal_error", "user resolve failed")
		return
	}

	sess, err := h.deps.Sessions.Create(r.Context(), CreateParams{
		UserSub:               sub,
		UserID:                userID,
		AccessToken:           tokens.AccessToken,
		RefreshToken:          tokens.RefreshToken,
		IDToken:               tokens.IDToken,
		AccessTokenExpiresAt:  tokens.AccessTokenExpiresAt,
		RefreshTokenExpiresAt: tokens.RefreshTokenExpiresAt,
		AbsoluteExpiresAt:     h.now().Add(h.cfg.SessionAbsoluteMax),
		IP:                    clientIP(r, h.cfg.TrustForwardedFor),
		UserAgent:             r.UserAgent(),
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "auth.callback: session create", "err", err)
		ClearStateCookie(w, h.cfg.StateCookieSecure)
		writeAuthError(w, http.StatusInternalServerError, "internal_error", "session create failed")
		return
	}

	SetSessionCookie(w, sess.ID, h.cfg.Cookie)
	ClearStateCookie(w, h.cfg.StateCookieSecure)

	target := sd.ReturnTo
	if target == "" {
		target = "/"
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// Logout revokes the session row and clears the session cookie.
// Idempotent: a missing or invalid cookie returns 200 without
// failing, so a stale tab or double-click does not surface as an
// error. When EnforceCSRFOnLogout is true and an authenticated
// session is present, the X-CSRF-Token header MUST match
// sess.CSRFToken (constant-time compare) — an attacker logging the
// user out is a real, if mild, hostile action.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		writeLogoutOK(w)
		return
	}

	rawID, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(rawID) != 32 {
		ClearSessionCookie(w, h.cfg.Cookie)
		writeLogoutOK(w)
		return
	}
	var id [32]byte
	copy(id[:], rawID)

	sess, err := h.deps.Sessions.GetByID(r.Context(), id)
	switch {
	case errors.Is(err, ErrSessionNotFound):
		ClearSessionCookie(w, h.cfg.Cookie)
		writeLogoutOK(w)
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "auth.logout: session lookup", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal_error", "logout failed")
		return
	}

	if h.cfg.EnforceCSRFOnLogout {
		header := r.Header.Get("X-CSRF-Token")
		if header == "" {
			writeAuthError(w, http.StatusForbidden, "csrf_missing", "CSRF token required")
			return
		}
		expected := base64.RawURLEncoding.EncodeToString(sess.CSRFToken[:])
		if subtle.ConstantTimeCompare([]byte(header), []byte(expected)) != 1 {
			writeAuthError(w, http.StatusForbidden, "csrf_mismatch", "CSRF token does not match")
			return
		}
	}

	if err := h.deps.Sessions.Revoke(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), "auth.logout: revoke", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal_error", "logout failed")
		return
	}
	ClearSessionCookie(w, h.cfg.Cookie)

	// Server-driven SSO logout: 302 to Keycloak's end-session
	// endpoint with id_token_hint (skips the confirmation prompt)
	// and post_logout_redirect_uri. When discovery returned no
	// end-session URL or the deployment did not configure a
	// post-logout target, fall back to a 204 — the local session
	// is already gone.
	if h.cfg.PostLogoutRedirectURI != "" {
		if endURL := h.deps.OAuth.EndSessionURL(sess.IDToken, h.cfg.PostLogoutRedirectURI); endURL != "" {
			http.Redirect(w, r, endURL, http.StatusFound)
			return
		}
		http.Redirect(w, r, h.cfg.PostLogoutRedirectURI, http.StatusFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeLogoutOK is the idempotent-success response: 200 with an
// empty JSON body. The body shape matches what the SPA's wrapped
// fetch client expects (a JSON envelope) so a stale-cookie logout
// does not look like a parse error in the browser.
func writeLogoutOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

// validateReturnTo enforces same-origin semantics on the
// return_to query param. The only accepted shape is an
// absolute-path URI ("/foo/bar") — no "//", no scheme, no host,
// no CR/LF (response-splitting). Anything else collapses to ""
// so the callback uses "/" as the default target.
//
// This is the open-redirect defense: without it,
// /auth/login?return_to=https://evil.com would 302 the user
// straight to the attacker after a successful Keycloak round-trip.
func validateReturnTo(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	if strings.ContainsAny(raw, "\r\n") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "" || u.Host != "" {
		return ""
	}
	return raw
}

// claimsFromIDToken extracts sub + email from the id_token's
// payload. We deliberately skip signature verification: the
// id_token arrived over a TLS round-trip to Keycloak's token
// endpoint authenticated with the client secret, which already
// established the chain of trust. Re-validating against the same
// JWKS would only add a JWKS fetch on every callback.
//
// The padded base64 fallback covers IdPs (rare, but observed) that
// emit standard-padded payloads instead of the JWT spec's
// base64url-no-pad.
func claimsFromIDToken(idToken string) (sub, email string, err error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", "", errors.New("bff: id_token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", fmt.Errorf("bff: id_token payload decode: %w", err)
		}
	}
	var raw struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return "", "", fmt.Errorf("bff: id_token payload parse: %w", err)
	}
	if raw.Sub == "" {
		return "", "", errors.New("bff: id_token missing sub")
	}
	return raw.Sub, raw.Email, nil
}

// clientIP returns the request's originating client IP. When the
// deployment trusts the X-Forwarded-For header (Ingress strips it,
// or there is no Ingress at all), the leftmost entry is preferred
// over RemoteAddr — that is the originating client per the de
// facto convention. An unparseable source yields a zero
// netip.Addr; the session row stores NULL for that case.
func clientIP(r *http.Request, trustForwarded bool) netip.Addr {
	if trustForwarded {
		if h := r.Header.Get("X-Forwarded-For"); h != "" {
			first := h
			if i := strings.IndexByte(h, ','); i >= 0 {
				first = strings.TrimSpace(h[:i])
			}
			if addr, perr := netip.ParseAddr(first); perr == nil {
				return addr
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, _ := netip.ParseAddr(host)
	return addr
}

// authErrorEnvelope mirrors the §10 error envelope used by
// internal/api. The bff package re-declares it rather than
// importing api so the microservice-extraction boundary
// (docs/design/auth-bff.md §microservice-extraction) holds — bff
// has no inbound dependency on the application's HTTP package.
type authErrorEnvelope struct {
	Error authErrorBody `json:"error"`
}

type authErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(authErrorEnvelope{
		Error: authErrorBody{Code: code, Message: message},
	})
}
