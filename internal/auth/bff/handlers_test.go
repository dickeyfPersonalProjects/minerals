package bff_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
)

// fakeOAuth is a deterministic OAuthClient for handler tests. It
// records each call and replays whatever the test pre-loaded as
// the next response — never touches the network.
type fakeOAuth struct {
	mu sync.Mutex

	// AuthCodeURL: returns AuthURL with the state appended.
	AuthURL string
	// RegisterURL: returns RegisterURLBase with the state appended.
	// Defaults to AuthURL with a `/register` suffix at New time so a
	// test that doesn't care can rely on a non-empty distinct URL.
	RegisterURLBase string
	// Exchange: returns ExchangeResp / ExchangeErr.
	ExchangeResp bff.Tokens
	ExchangeErr  error
	// EndSessionURL: returns EndURL (parameters appended).
	EndURL string

	calls struct {
		authCode struct {
			state, redirect string
		}
		register struct {
			state, redirect string
		}
		exchange struct {
			code, redirect string
		}
		endSession struct {
			idToken, postLogout string
		}
	}
}

func (f *fakeOAuth) AuthCodeURL(state, redirectURI string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.authCode.state = state
	f.calls.authCode.redirect = redirectURI
	return f.AuthURL + "?state=" + url.QueryEscape(state) + "&redirect_uri=" + url.QueryEscape(redirectURI)
}

func (f *fakeOAuth) RegisterURL(state, redirectURI string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.register.state = state
	f.calls.register.redirect = redirectURI
	return f.RegisterURLBase + "?state=" + url.QueryEscape(state) + "&redirect_uri=" + url.QueryEscape(redirectURI)
}

func (f *fakeOAuth) Exchange(_ context.Context, code, redirectURI string) (bff.Tokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.exchange.code = code
	f.calls.exchange.redirect = redirectURI
	if f.ExchangeErr != nil {
		return bff.Tokens{}, f.ExchangeErr
	}
	return f.ExchangeResp, nil
}

func (f *fakeOAuth) Refresh(context.Context, string) (bff.Tokens, error) {
	return bff.Tokens{}, errors.New("fakeOAuth.Refresh not used in handler tests")
}

func (f *fakeOAuth) EndSessionURL(idTokenHint, postLogoutRedirectURI string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.endSession.idToken = idTokenHint
	f.calls.endSession.postLogout = postLogoutRedirectURI
	if f.EndURL == "" {
		return ""
	}
	return f.EndURL + "?id_token_hint=" + url.QueryEscape(idTokenHint) +
		"&post_logout_redirect_uri=" + url.QueryEscape(postLogoutRedirectURI)
}

// memSessions is an in-memory SessionResolver covering the calls
// the handlers exercise (Create / GetByID / Revoke). The
// integration test in postgres_integration_test.go covers the real
// Postgres impl; here we only need to assert handler wiring.
type memSessions struct {
	mu        sync.Mutex
	byID      map[[32]byte]bff.Session
	createErr error
	getErr    error
	revokeErr error
}

func newMemSessions() *memSessions {
	return &memSessions{byID: map[[32]byte]bff.Session{}}
}

func (m *memSessions) Create(_ context.Context, p bff.CreateParams) (bff.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return bff.Session{}, m.createErr
	}
	var id, csrf [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return bff.Session{}, err
	}
	if _, err := rand.Read(csrf[:]); err != nil {
		return bff.Session{}, err
	}
	now := time.Now()
	s := bff.Session{
		ID:                    id,
		UserSub:               p.UserSub,
		UserID:                p.UserID,
		AccessToken:           p.AccessToken,
		RefreshToken:          p.RefreshToken,
		IDToken:               p.IDToken,
		AccessTokenExpiresAt:  p.AccessTokenExpiresAt,
		RefreshTokenExpiresAt: p.RefreshTokenExpiresAt,
		AbsoluteExpiresAt:     p.AbsoluteExpiresAt,
		CreatedAt:             now,
		LastUsedAt:            now,
		CSRFToken:             csrf,
		IP:                    p.IP,
		UserAgent:             p.UserAgent,
	}
	m.byID[id] = s
	return s, nil
}

func (m *memSessions) GetByID(_ context.Context, id [32]byte) (bff.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return bff.Session{}, m.getErr
	}
	s, ok := m.byID[id]
	if !ok {
		return bff.Session{}, bff.ErrSessionNotFound
	}
	return s, nil
}

func (m *memSessions) UpdateTokens(context.Context, [32]byte, bff.TokenSet) (bff.Session, error) {
	return bff.Session{}, errors.New("memSessions.UpdateTokens not used in handler tests")
}

func (m *memSessions) Touch(context.Context, [32]byte, time.Time) error {
	return errors.New("memSessions.Touch not used in handler tests")
}

func (m *memSessions) Revoke(_ context.Context, id [32]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revokeErr != nil {
		return m.revokeErr
	}
	s, ok := m.byID[id]
	if ok {
		now := time.Now()
		s.RevokedAt = &now
		m.byID[id] = s
	}
	return nil
}

func (m *memSessions) RevokeAllForUser(context.Context, uuid.UUID) error {
	return errors.New("memSessions.RevokeAllForUser not used in handler tests")
}

// makeIDToken builds a minimal unsigned JWT for handler tests.
// claimsFromIDToken deliberately does not verify the signature
// (the token came from a TLS round-trip authenticated with the
// client secret), so a placeholder signature segment is fine.
func makeIDToken(t *testing.T, sub, email string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadJSON, err := json.Marshal(map[string]string{"sub": sub, "email": email})
	if err != nil {
		t.Fatalf("marshal id_token payload: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".signature"
}

func defaultCookieCfg() bff.CookieConfig {
	return bff.CookieConfig{
		Path:     "/",
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   14 * 24 * time.Hour,
	}
}

func newHandlers(t *testing.T, opts ...func(*bff.HandlerConfig)) (*bff.Handlers, *fakeOAuth, *memSessions) {
	t.Helper()
	oauth := &fakeOAuth{
		AuthURL:         "https://keycloak.example/realms/minerals/protocol/openid-connect/auth",
		RegisterURLBase: "https://keycloak.example/realms/minerals/protocol/openid-connect/registrations",
		EndURL:          "https://keycloak.example/realms/minerals/protocol/openid-connect/logout",
	}
	sessions := newMemSessions()
	users := bff.UserResolver(func(_ context.Context, sub, _ string) (uuid.UUID, error) {
		_ = sub
		return uuid.MustParse("11111111-1111-1111-1111-111111111111"), nil
	})
	cfg := bff.HandlerConfig{
		RedirectURI:           "https://app.example/auth/callback",
		PostLogoutRedirectURI: "https://app.example/",
		StateHMACKey:          []byte(testHMACKey),
		SessionAbsoluteMax:    7 * 24 * time.Hour,
		Cookie:                defaultCookieCfg(),
		StateCookieSecure:     false,
		RegistrationEnabled:   true,
		EnforceCSRFOnLogout:   true,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	h, err := bff.NewHandlers(cfg, bff.HandlerDeps{
		OAuth:    oauth,
		Sessions: sessions,
		Users:    users,
	})
	if err != nil {
		t.Fatalf("NewHandlers: %v", err)
	}
	return h, oauth, sessions
}

// TestLogin_RedirectsToKeycloakAndSetsStateCookie locks the happy
// path: the response is a 302 to the Keycloak auth URL with the
// state and redirect_uri carried through, and the state cookie is
// set with the security flags the design mandates.
func TestLogin_RedirectsToKeycloakAndSetsStateCookie(t *testing.T) {
	t.Parallel()
	h, oauth, _ := newHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if !strings.HasPrefix(loc.String(), oauth.AuthURL) {
		t.Errorf("Location = %q, want prefix %q", loc.String(), oauth.AuthURL)
	}
	if loc.Query().Get("state") == "" {
		t.Error("Location missing state query param")
	}
	if loc.Query().Get("redirect_uri") != "https://app.example/auth/callback" {
		t.Errorf("redirect_uri = %q, want https://app.example/auth/callback",
			loc.Query().Get("redirect_uri"))
	}

	c := findCookie(t, rec.Result(), bff.StateCookieName)
	if c.Value == "" {
		t.Error("state cookie has empty value")
	}
	// Verify the cookie roundtrips and the state inside matches the
	// query param (proves the same value was put in both places).
	sd, verr := bff.VerifyState([]byte(testHMACKey), c.Value, time.Now())
	if verr != nil {
		t.Fatalf("VerifyState on emitted cookie: %v", verr)
	}
	if sd.State != loc.Query().Get("state") {
		t.Errorf("cookie state = %q, query state = %q; must match",
			sd.State, loc.Query().Get("state"))
	}
}

// TestLogin_StashesValidReturnTo confirms that a same-origin path
// in `?return_to=` makes it into the state cookie and is honored
// on the callback's final 302.
func TestLogin_StashesValidReturnTo(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/login?return_to=/specimens/abc", nil)
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	c := findCookie(t, rec.Result(), bff.StateCookieName)
	sd, err := bff.VerifyState([]byte(testHMACKey), c.Value, time.Now())
	if err != nil {
		t.Fatalf("VerifyState: %v", err)
	}
	if sd.ReturnTo != "/specimens/abc" {
		t.Errorf("ReturnTo = %q, want /specimens/abc", sd.ReturnTo)
	}
}

// TestLogin_RejectsAbsoluteReturnTo guards the open-redirect
// defense. `?return_to=https://evil.com` must collapse to ""; the
// callback then defaults to "/" instead of bouncing the user to
// the attacker after a successful Keycloak round-trip.
func TestLogin_RejectsAbsoluteReturnTo(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t)
	cases := []string{
		"https://evil.com/",
		"//evil.com/path",
		"javascript:alert(1)",
		"/safe\r\nX-Injected: header",
	}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/auth/login?return_to="+url.QueryEscape(raw), nil)
			rec := httptest.NewRecorder()
			h.Login(rec, req)
			c := findCookie(t, rec.Result(), bff.StateCookieName)
			sd, err := bff.VerifyState([]byte(testHMACKey), c.Value, time.Now())
			if err != nil {
				t.Fatalf("VerifyState: %v", err)
			}
			if sd.ReturnTo != "" {
				t.Errorf("ReturnTo = %q for hostile input %q; want \"\"", sd.ReturnTo, raw)
			}
		})
	}
}

// TestRegister_RedirectsToKeycloakRegistrationsEndpoint locks the
// happy path for self-signup: a 302 to Keycloak's /registrations
// endpoint with state + redirect_uri carried through, plus the
// signed state cookie the callback handler will verify.
func TestRegister_RedirectsToKeycloakRegistrationsEndpoint(t *testing.T) {
	t.Parallel()
	h, oauth, _ := newHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/register", nil)
	rec := httptest.NewRecorder()
	h.Register(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if !strings.HasPrefix(loc.String(), oauth.RegisterURLBase) {
		t.Errorf("Location = %q, want prefix %q", loc.String(), oauth.RegisterURLBase)
	}
	if loc.Query().Get("state") == "" {
		t.Error("Location missing state query param")
	}
	if loc.Query().Get("redirect_uri") != "https://app.example/auth/callback" {
		t.Errorf("redirect_uri = %q, want https://app.example/auth/callback",
			loc.Query().Get("redirect_uri"))
	}

	c := findCookie(t, rec.Result(), bff.StateCookieName)
	if c.Value == "" {
		t.Error("state cookie has empty value")
	}
	sd, verr := bff.VerifyState([]byte(testHMACKey), c.Value, time.Now())
	if verr != nil {
		t.Fatalf("VerifyState on emitted cookie: %v", verr)
	}
	if sd.State != loc.Query().Get("state") {
		t.Errorf("cookie state = %q, query state = %q; must match",
			sd.State, loc.Query().Get("state"))
	}
}

// TestRegister_StashesValidReturnTo confirms the same return_to
// allow-list that Login enforces also applies to Register — a user
// who hit "Register" from /specimens/abc lands back there after
// signup, with hostile inputs collapsed to "".
func TestRegister_StashesValidReturnTo(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/auth/register?return_to=/specimens/abc", nil)
	rec := httptest.NewRecorder()
	h.Register(rec, req)

	c := findCookie(t, rec.Result(), bff.StateCookieName)
	sd, err := bff.VerifyState([]byte(testHMACKey), c.Value, time.Now())
	if err != nil {
		t.Fatalf("VerifyState: %v", err)
	}
	if sd.ReturnTo != "/specimens/abc" {
		t.Errorf("ReturnTo = %q, want /specimens/abc", sd.ReturnTo)
	}
}

// TestRegister_DisabledReturns404 covers the operator-disabled
// case: when RegistrationEnabled is false the route MUST 404 so an
// inadvertent SPA click can't bypass the no-signup policy. No
// state cookie is set — there is no flow to start.
func TestRegister_DisabledReturns404(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t, func(cfg *bff.HandlerConfig) {
		cfg.RegistrationEnabled = false
	})
	req := httptest.NewRequest(http.MethodGet, "/auth/register", nil)
	rec := httptest.NewRecorder()
	h.Register(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if cookies := rec.Result().Cookies(); len(cookies) > 0 {
		t.Errorf("disabled register set cookies: %v", cookies)
	}
}

// TestCallback_HappyPath drives a full /auth/callback: valid state
// cookie + matching query state + good token exchange ends in a
// session row + session cookie + 302 to the return_to.
func TestCallback_HappyPath(t *testing.T) {
	t.Parallel()
	h, oauth, sessions := newHandlers(t)

	idTok := makeIDToken(t, "kc-sub-123", "user@example.com")
	oauth.ExchangeResp = bff.Tokens{
		AccessToken:           "access-1",
		RefreshToken:          "refresh-1",
		IDToken:               idTok,
		AccessTokenExpiresAt:  time.Now().Add(5 * time.Minute),
		RefreshTokenExpiresAt: time.Now().Add(30 * time.Minute),
	}

	state := "state-abc"
	stateCookie, err := bff.SignState([]byte(testHMACKey), bff.StateData{
		State:    state,
		ReturnTo: "/specimens/xyz",
		Expires:  time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=auth-code&state="+state, nil)
	req.AddCookie(stateRequestCookie(stateCookie))
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/specimens/xyz" {
		t.Errorf("Location = %q, want /specimens/xyz", loc)
	}

	if oauth.calls.exchange.code != "auth-code" {
		t.Errorf("Exchange called with code = %q, want auth-code", oauth.calls.exchange.code)
	}
	if oauth.calls.exchange.redirect != "https://app.example/auth/callback" {
		t.Errorf("Exchange called with redirectURI = %q, want https://app.example/auth/callback",
			oauth.calls.exchange.redirect)
	}

	sc := findCookie(t, rec.Result(), bff.SessionCookieName)
	if sc.Value == "" {
		t.Error("session cookie not set")
	}
	if !sc.HttpOnly || sc.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie flags wrong: HttpOnly=%v SameSite=%v", sc.HttpOnly, sc.SameSite)
	}

	// State cookie cleared (MaxAge -1, empty value).
	state2 := findCookie(t, rec.Result(), bff.StateCookieName)
	if state2.Value != "" || state2.MaxAge != -1 {
		t.Errorf("state cookie not cleared: value=%q maxAge=%d", state2.Value, state2.MaxAge)
	}

	// Exactly one session row landed, with the resolved user ID
	// and tokens from the fake Exchange.
	if got := len(sessions.byID); got != 1 {
		t.Fatalf("session count = %d, want 1", got)
	}
	for _, s := range sessions.byID {
		if s.UserSub != "kc-sub-123" {
			t.Errorf("UserSub = %q, want kc-sub-123", s.UserSub)
		}
		if s.AccessToken != "access-1" || s.RefreshToken != "refresh-1" || s.IDToken != idTok {
			t.Errorf("tokens not persisted: %+v", s)
		}
	}
}

// TestCallback_StateMismatch400 — the query `state` not matching
// the cookie payload MUST surface as 400 invalid_state. This is
// the canonical CSRF-on-login defense.
func TestCallback_StateMismatch400(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t)
	cookieVal, err := bff.SignState([]byte(testHMACKey), bff.StateData{
		State:   "cookie-state",
		Expires: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=c&state=query-state", nil)
	req.AddCookie(stateRequestCookie(cookieVal))
	rec := httptest.NewRecorder()
	h.Callback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_state") {
		t.Errorf("body = %q, want invalid_state envelope", rec.Body.String())
	}
}

// TestCallback_ExpiredStateCookie400 confirms a TTL-expired cookie
// also surfaces invalid_state. Together with the tamper test in
// state_test.go this covers the three failure modes the design
// requires VerifyState to collapse.
func TestCallback_ExpiredStateCookie400(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t)
	expired, err := bff.SignState([]byte(testHMACKey), bff.StateData{
		State:   "s",
		Expires: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=c&state=s", nil)
	req.AddCookie(stateRequestCookie(expired))
	rec := httptest.NewRecorder()
	h.Callback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_state") {
		t.Errorf("body = %q, want invalid_state envelope", rec.Body.String())
	}
}

// TestCallback_KeycloakErrorParam400 — Keycloak returns
// ?error=... when the user hits "Cancel" or the auth request is
// invalid. The handler MUST render a clean 400, not crash with a
// 500 that would page someone for an external cause.
func TestCallback_KeycloakErrorParam400(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t)
	req := httptest.NewRequest(http.MethodGet,
		"/auth/callback?error=access_denied&error_description=User+cancelled", nil)
	rec := httptest.NewRecorder()
	h.Callback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "oauth_error") {
		t.Errorf("body = %q, want oauth_error envelope", rec.Body.String())
	}
}

// TestLogout_RequiresCSRFHeader proves the gated CSRF check fires
// for a real, authenticated logout when EnforceCSRFOnLogout is on
// — even with a valid session cookie, an absent X-CSRF-Token MUST
// 403 with csrf_missing.
func TestLogout_RequiresCSRFHeader(t *testing.T) {
	t.Parallel()
	h, _, sessions := newHandlers(t)
	id := seedSession(t, sessions)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(sessionRequestCookie(id))
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "csrf_missing") {
		t.Errorf("body = %q, want csrf_missing envelope", rec.Body.String())
	}
}

// TestLogout_HappyPath: valid session + matching CSRF header →
// session revoked, cookie cleared, 302 to Keycloak end-session.
func TestLogout_HappyPath(t *testing.T) {
	t.Parallel()
	h, oauth, sessions := newHandlers(t)
	id := seedSession(t, sessions)
	csrf := sessions.byID[id].CSRFToken
	sess := sessions.byID[id]

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(sessionRequestCookie(id))
	req.Header.Set("X-CSRF-Token", base64.RawURLEncoding.EncodeToString(csrf[:]))
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, oauth.EndURL) {
		t.Errorf("Location = %q, want prefix %q", loc, oauth.EndURL)
	}
	if !strings.Contains(loc, "id_token_hint=") {
		t.Errorf("Location missing id_token_hint: %q", loc)
	}

	// Session cookie cleared on the response.
	c := findCookie(t, rec.Result(), bff.SessionCookieName)
	if c.Value != "" || c.MaxAge != -1 {
		t.Errorf("session cookie not cleared: value=%q maxAge=%d", c.Value, c.MaxAge)
	}

	// Row marked revoked.
	revoked := sessions.byID[id]
	if revoked.RevokedAt == nil {
		t.Error("session row not revoked")
	}

	// The EndSession call carried the id_token from the row.
	if oauth.calls.endSession.idToken != sess.IDToken {
		t.Errorf("end_session id_token_hint = %q, want %q",
			oauth.calls.endSession.idToken, sess.IDToken)
	}
}

// TestLogout_WithoutSessionIsIdempotent200 covers the
// stale-tab/double-click case: a POST with no session cookie MUST
// return 200, not error. CSRF check is skipped — there is nothing
// to mutate.
func TestLogout_WithoutSessionIsIdempotent200(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlers(t)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	rec := httptest.NewRecorder()
	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestLogout_CSRFMismatch403 — a header that doesn't match
// sess.CSRFToken under constant-time compare MUST surface
// csrf_mismatch (distinct from csrf_missing so the SPA can refetch
// the token on mismatch and retry once).
func TestLogout_CSRFMismatch403(t *testing.T) {
	t.Parallel()
	h, _, sessions := newHandlers(t)
	id := seedSession(t, sessions)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(sessionRequestCookie(id))
	req.Header.Set("X-CSRF-Token", "wrong-token-here")
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "csrf_mismatch") {
		t.Errorf("body = %q, want csrf_mismatch envelope", rec.Body.String())
	}
}

// TestLogout_CSRFGateOffSkipsCheck — with EnforceCSRFOnLogout
// false (the default until the CSRF middleware lands in mi-gbzs),
// a logout without an X-CSRF-Token still succeeds. This is the
// gate the bead requires so the SPA does not break during the
// transition.
func TestLogout_CSRFGateOffSkipsCheck(t *testing.T) {
	t.Parallel()
	h, _, sessions := newHandlers(t, func(cfg *bff.HandlerConfig) {
		cfg.EnforceCSRFOnLogout = false
	})
	id := seedSession(t, sessions)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(sessionRequestCookie(id))
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	// 302 to Keycloak end-session; CSRF was bypassed.
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (CSRF gate off)", rec.Code)
	}
}

// TestNewHandlers_RequiredFields locks the constructor invariants
// so a misconfigured deployment fails at boot rather than on the
// first user login.
func TestNewHandlers_RequiredFields(t *testing.T) {
	t.Parallel()
	good := bff.HandlerConfig{
		RedirectURI:        "https://app/cb",
		StateHMACKey:       []byte(testHMACKey),
		SessionAbsoluteMax: time.Hour,
		Cookie:             defaultCookieCfg(),
	}
	goodDeps := bff.HandlerDeps{
		OAuth:    &fakeOAuth{},
		Sessions: newMemSessions(),
		Users:    func(context.Context, string, string) (uuid.UUID, error) { return uuid.Nil, nil },
	}
	cases := []struct {
		name string
		cfg  bff.HandlerConfig
		deps bff.HandlerDeps
		want string
	}{
		{
			name: "missing RedirectURI",
			cfg:  func() bff.HandlerConfig { c := good; c.RedirectURI = ""; return c }(),
			deps: goodDeps,
			want: "RedirectURI",
		},
		{
			name: "short StateHMACKey",
			cfg:  func() bff.HandlerConfig { c := good; c.StateHMACKey = []byte("short"); return c }(),
			deps: goodDeps,
			want: "StateHMACKey",
		},
		{
			name: "zero SessionAbsoluteMax",
			cfg:  func() bff.HandlerConfig { c := good; c.SessionAbsoluteMax = 0; return c }(),
			deps: goodDeps,
			want: "SessionAbsoluteMax",
		},
		{
			name: "missing Cookie.Path",
			cfg: func() bff.HandlerConfig {
				c := good
				c.Cookie = bff.CookieConfig{}
				return c
			}(),
			deps: goodDeps,
			want: "Cookie.Path",
		},
		{
			name: "missing OAuth",
			cfg:  good,
			deps: bff.HandlerDeps{OAuth: nil, Sessions: goodDeps.Sessions, Users: goodDeps.Users},
			want: "OAuth",
		},
		{
			name: "missing Sessions",
			cfg:  good,
			deps: bff.HandlerDeps{OAuth: goodDeps.OAuth, Sessions: nil, Users: goodDeps.Users},
			want: "Sessions",
		},
		{
			name: "missing Users",
			cfg:  good,
			deps: bff.HandlerDeps{OAuth: goodDeps.OAuth, Sessions: goodDeps.Sessions, Users: nil},
			want: "Users",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := bff.NewHandlers(tc.cfg, tc.deps)
			if err == nil {
				t.Fatalf("NewHandlers succeeded; want error mentioning %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want mention of %s", err, tc.want)
			}
		})
	}
}

// stateRequestCookie is the state-cookie equivalent of
// sessionRequestCookie — same gosec-G124 rationale. Browsers
// don't echo security attributes on inbound requests.
func stateRequestCookie(value string) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // inbound request cookie; security attrs only meaningful on Set-Cookie
		Name:  bff.StateCookieName,
		Value: value,
	}
}

// sessionRequestCookie builds an *http.Cookie suitable for
// AddCookie on an inbound request. Browsers only echo Name + Value
// — the Secure/HttpOnly/SameSite attributes are server-set on the
// response, never carried on the request — so gosec G124's
// "missing security attrs" check doesn't apply here. Centralising
// the construction keeps the lint waiver in one place.
func sessionRequestCookie(id [32]byte) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // inbound request cookie; security attrs only meaningful on Set-Cookie
		Name:  bff.SessionCookieName,
		Value: base64.RawURLEncoding.EncodeToString(id[:]),
	}
}

// seedSession plants a fully-formed session row in mem and
// returns its id so logout tests can reference it. We rebuild the
// row's CSRFToken in the caller via sessions.byID[id].CSRFToken
// — exposing it through a helper keeps the tests narrow.
func seedSession(t *testing.T, m *memSessions) [32]byte {
	t.Helper()
	sess, err := m.Create(context.Background(), bff.CreateParams{
		UserSub:           "kc-sub",
		UserID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AccessToken:       "access",
		RefreshToken:      "refresh",
		IDToken:           "id-tok",
		AbsoluteExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess.ID
}
