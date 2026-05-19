//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// rejectingVerifier is a non-nil auth.TokenVerifier that rejects
// every bearer token. Wired into the BFF integration tests so
// humaAuth does NOT fall back to its StubUser path when the cookie
// is absent — post-logout assertions need that absence to surface as
// a real 401, not the test-only Overseer identity.
type rejectingVerifier struct{}

func (rejectingVerifier) Verify(_ context.Context, _ string) (*oidc.Claims, error) {
	return nil, errors.New("rejecting verifier: bearer auth not used in BFF round-trip")
}

// TestIntegration_BFF_AuthRoundTrip exercises the V2 BFF auth chain
// end-to-end at the HTTP layer (no real browser, no real Keycloak)
// against a real Postgres schema. Covers the bead's "Backend
// integration test" acceptance criteria (mi-sap2):
//
//  1. GET /auth/login → 302 to Keycloak, state cookie set
//  2. Simulated Keycloak callback exchanges code for tokens (stub
//     OAuthClient) → 302 to '/' with session cookie
//  3. GET /api/v1/profile with the cookie → 200, user populated
//  4. POST /api/v1/specimens without CSRF token → 403 csrf_missing
//  5. GET /api/v1/csrf with the cookie → 200, token returned
//  6. POST /api/v1/specimens with the CSRF header → 201
//  7. POST /auth/logout (with CSRF) → cookie cleared
//  8. Next /api/v1/profile request → 401 (session revoked)
//
// The stub OAuth client returns deterministic tokens whose id_token
// payload carries a known sub + email; the BFF callback handler
// parses those fields out (no JWKS verification) and the user
// resolver creates the local users row on first login.
func TestIntegration_BFF_AuthRoundTrip(t *testing.T) {
	pool := scopedDB(t)
	users := db.NewUserPostgres(pool)
	sessions := bff.NewPostgresResolver(pool)

	const (
		sub     = "kc-sub-bff-roundtrip"
		email   = "bff-roundtrip@example.invalid"
		hmacKey = "0123456789abcdef0123456789abcdef" // 32 bytes — minStateHMACKeyLen
	)

	oauth := &stubOAuthClient{
		sub:                   sub,
		email:                 email,
		accessTokenExpiresIn:  5 * time.Minute,
		refreshTokenExpiresIn: 30 * 24 * time.Hour,
	}

	enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enforcer); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	// userResolver bridges into the same resolve-or-create the
	// bearer-token path uses, so the BFF callback lands on the same
	// users row a future bearer-token request for the same sub would.
	// Marks the user active after first login so requireCompleteProfile
	// lets writes through; real callers go through the profile setup
	// endpoint, but the spec stays focused on the cookie chain.
	userResolver := func(ctx context.Context, sub, email string) (uuid.UUID, error) {
		row, err := api.ResolveOrCreateUser(ctx, users, auth.User{Sub: sub, Email: email})
		if err != nil {
			return uuid.Nil, err
		}
		if row.Status == domain.UserStatusPending {
			now := time.Now().UTC().Truncate(time.Microsecond)
			if err := users.MarkActive(ctx, nil, row.ID, "bff round-trip", now); err != nil {
				return uuid.Nil, err
			}
		}
		return row.ID, nil
	}

	// Bind an httptest server on an ephemeral port, but pre-compute its
	// URL via the listener so the BFF RedirectURI can be set on the
	// handlers before they register routes. The stub OAuth client
	// doesn't validate redirect_uri, so this is paranoia-grade only —
	// it keeps the handler config honest under inspection.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback listener: %v", err)
	}
	baseURL := "http://" + listener.Addr().String()
	redirectURI := baseURL + "/auth/callback"

	cfg := bff.HandlerConfig{
		RedirectURI:        redirectURI,
		StateHMACKey:       []byte(hmacKey),
		SessionAbsoluteMax: 7 * 24 * time.Hour,
		Cookie: bff.CookieConfig{
			Path:     "/",
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   14 * 24 * time.Hour,
		},
		StateCookieSecure:   false,
		EnforceCSRFOnLogout: true,
	}
	handlers, err := bff.NewHandlers(cfg, bff.HandlerDeps{
		OAuth:    oauth,
		Sessions: sessions,
		Users:    userResolver,
	})
	if err != nil {
		t.Fatalf("new handlers: %v", err)
	}
	sessionMW := bff.SessionMiddleware(bff.MiddlewareDeps{
		Sessions: sessions,
		OAuth:    oauth,
		Users:    users,
		CookieConfig: bff.CookieConfig{
			Path:     "/",
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   14 * 24 * time.Hour,
		},
		IdleTimeout: 24 * time.Hour,
	})
	h := api.New(api.Deps{
		Specimens:  db.NewSpecimenPostgres(pool),
		Collectors: db.NewCollectorPostgres(pool),
		Enforcer:   enforcer,
		Users:      users,
		Verifier:   rejectingVerifier{},
		BFFAuth:    handlers,
		SessionMW:  sessionMW,
		CSRFMW:     bff.CSRFMiddleware,
	})
	srv := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second},
	}
	srv.Start()
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		// Do not follow redirects automatically — the test inspects
		// each Location header to assert the BFF state machine.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// --- 1. /auth/login --------------------------------------------------
	resp, err := client.Get(srv.URL + "/auth/login")
	if err != nil {
		t.Fatalf("GET /auth/login: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/auth/login status = %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("/auth/login Location: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatal("/auth/login Location missing state param")
	}
	// State cookie must be set; the callback handler re-reads it. The
	// cookie's Path is /auth (see SetStateCookie), so query the jar
	// for cookies that would be sent on /auth/callback specifically.
	srvURL, _ := url.Parse(srv.URL)
	cbForJar, _ := url.Parse(srv.URL + "/auth/callback")
	var haveStateCookie bool
	for _, c := range jar.Cookies(cbForJar) {
		if c.Name == bff.StateCookieName {
			haveStateCookie = true
			break
		}
	}
	if !haveStateCookie {
		t.Fatal("/auth/login did not set state cookie for /auth/callback path")
	}

	// --- 2. /auth/callback ----------------------------------------------
	oauth.expectCode = "code-roundtrip"
	cbURL := srv.URL + "/auth/callback?code=" + oauth.expectCode + "&state=" + state
	resp, err = client.Get(cbURL)
	if err != nil {
		t.Fatalf("GET /auth/callback: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/auth/callback status = %d, want 302", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("/auth/callback Location = %q, want %q", got, "/")
	}
	var haveSessionCookie bool
	for _, c := range jar.Cookies(srvURL) {
		if c.Name == bff.SessionCookieName && c.Value != "" {
			haveSessionCookie = true
		}
	}
	if !haveSessionCookie {
		t.Fatal("/auth/callback did not set session cookie")
	}
	for _, c := range jar.Cookies(cbForJar) {
		if c.Name == bff.StateCookieName && c.Value != "" {
			t.Errorf("state cookie should have been cleared, still has value %q", c.Value)
		}
	}

	// --- 3. GET /api/v1/profile (cookie-authenticated) ------------------
	resp, err = client.Get(srv.URL + "/api/v1/profile")
	if err != nil {
		t.Fatalf("GET /api/v1/profile: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/profile status = %d body=%s, want 200", resp.StatusCode, body)
	}
	var prof struct {
		ID    uuid.UUID `json:"id"`
		Email string    `json:"email"`
	}
	if err := json.Unmarshal(body, &prof); err != nil {
		t.Fatalf("decode profile: %v body=%s", err, body)
	}
	if prof.Email != email {
		t.Errorf("profile email = %q, want %q", prof.Email, email)
	}
	if prof.ID == uuid.Nil {
		t.Error("profile id is zero — session middleware did not populate auth.User")
	}

	// --- 4. POST /api/v1/specimens without CSRF — 403 csrf_missing ------
	specimenJSON := []byte(`{"type":"mineral","name":"bff round-trip","visibility":"private"}`)
	resp, err = client.Post(srv.URL+"/api/v1/specimens",
		"application/json", bytes.NewReader(specimenJSON))
	if err != nil {
		t.Fatalf("POST /api/v1/specimens (no csrf): %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF: status = %d body=%s, want 403", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "csrf_missing") {
		t.Errorf("POST without CSRF: body = %s, want csrf_missing", body)
	}

	// --- 5. GET /api/v1/csrf -- fetch token ------------------------------
	resp, err = client.Get(srv.URL + "/api/v1/csrf")
	if err != nil {
		t.Fatalf("GET /api/v1/csrf: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/csrf status = %d body=%s, want 200", resp.StatusCode, body)
	}
	var csrfBody struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &csrfBody); err != nil {
		t.Fatalf("decode csrf: %v body=%s", err, body)
	}
	if csrfBody.Token == "" {
		t.Fatal("/api/v1/csrf returned empty token")
	}

	// --- 6. POST /api/v1/specimens with CSRF — 201 ----------------------
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/specimens", bytes.NewReader(specimenJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfBody.Token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/specimens (with csrf): %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST with CSRF: status = %d body=%s, want 201", resp.StatusCode, body)
	}

	// --- 7. POST /auth/logout (with CSRF) -------------------------------
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/auth/logout", nil)
	req.Header.Set("X-CSRF-Token", csrfBody.Token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /auth/logout: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	// EnforceCSRFOnLogout is true; without PostLogoutRedirectURI the
	// handler returns 204 after revoking. (CSRFMiddleware on /api/v1/*
	// doesn't fire for /auth/logout — that route has its own gate.)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusFound {
		t.Fatalf("POST /auth/logout status = %d, want 204 or 302", resp.StatusCode)
	}

	// --- 8. Next /api/v1/profile is anonymous (cookie cleared) ---------
	// CSRF middleware bypasses anonymous, and humaAuth + RequireUser
	// returns 401 because no user is in context.
	resp, err = client.Get(srv.URL + "/api/v1/profile")
	if err != nil {
		t.Fatalf("GET /api/v1/profile post-logout: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-logout GET /api/v1/profile status = %d body=%s, want 401",
			resp.StatusCode, body)
	}
}

// TestIntegration_BFF_LogoutRequiresCSRF asserts that /auth/logout
// fails closed when CSRFMiddleware fires on it without an
// X-CSRF-Token header. The mayor's review note on mi-sap2 called this
// out explicitly: wiring /auth/logout into the CSRF-protected chain
// is belt-and-suspenders with the logout handler's own
// EnforceCSRFOnLogout gate — a misconfigured chain that mounts one
// but not the other should still fail closed on a CSRF-less logout.
// The middleware sees the session cookie attached by SessionMiddleware
// and rejects the POST before the logout handler runs.
func TestIntegration_BFF_LogoutRequiresCSRF(t *testing.T) {
	pool := scopedDB(t)
	users := db.NewUserPostgres(pool)
	sessions := bff.NewPostgresResolver(pool)

	const (
		sub     = "kc-sub-logout-csrf"
		email   = "logout-csrf@example.invalid"
		hmacKey = "0123456789abcdef0123456789abcdef"
	)
	oauth := &stubOAuthClient{
		sub:                   sub,
		email:                 email,
		accessTokenExpiresIn:  5 * time.Minute,
		refreshTokenExpiresIn: 30 * 24 * time.Hour,
		expectCode:            "code-logout-csrf",
	}
	enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enforcer); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	userResolver := func(ctx context.Context, sub, email string) (uuid.UUID, error) {
		row, err := api.ResolveOrCreateUser(ctx, users, auth.User{Sub: sub, Email: email})
		if err != nil {
			return uuid.Nil, err
		}
		if row.Status == domain.UserStatusPending {
			now := time.Now().UTC().Truncate(time.Microsecond)
			if err := users.MarkActive(ctx, nil, row.ID, "logout-csrf", now); err != nil {
				return uuid.Nil, err
			}
		}
		return row.ID, nil
	}

	cfg := bff.HandlerConfig{
		// EnforceCSRFOnLogout intentionally left FALSE — the
		// middleware is the only line of defense in this case, and the
		// test proves the chain still fails closed.
		RedirectURI:        "http://localhost/auth/callback",
		StateHMACKey:       []byte(hmacKey),
		SessionAbsoluteMax: 7 * 24 * time.Hour,
		Cookie: bff.CookieConfig{
			Path:     "/",
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   14 * 24 * time.Hour,
		},
		StateCookieSecure:   false,
		EnforceCSRFOnLogout: false,
	}
	handlers, err := bff.NewHandlers(cfg, bff.HandlerDeps{
		OAuth: oauth, Sessions: sessions, Users: userResolver,
	})
	if err != nil {
		t.Fatalf("new handlers: %v", err)
	}
	sessionMW := bff.SessionMiddleware(bff.MiddlewareDeps{
		Sessions: sessions,
		OAuth:    oauth,
		Users:    users,
		CookieConfig: bff.CookieConfig{
			Path:     "/",
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   14 * 24 * time.Hour,
		},
		IdleTimeout: 24 * time.Hour,
	})
	h := api.New(api.Deps{
		Specimens:  db.NewSpecimenPostgres(pool),
		Collectors: db.NewCollectorPostgres(pool),
		Enforcer:   enforcer,
		Users:      users,
		Verifier:   rejectingVerifier{},
		BFFAuth:    handlers,
		SessionMW:  sessionMW,
		CSRFMW:     bff.CSRFMiddleware,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Establish a session via login + callback (state cookie path
	// is /auth, so the jar lookup uses /auth/callback below).
	resp, err := client.Get(srv.URL + "/auth/login")
	if err != nil {
		t.Fatalf("/auth/login: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	state := loc.Query().Get("state")

	cbURL := srv.URL + "/auth/callback?code=" + oauth.expectCode + "&state=" + state
	resp, err = client.Get(cbURL)
	if err != nil {
		t.Fatalf("/auth/callback: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/auth/callback status = %d", resp.StatusCode)
	}

	// POST /auth/logout WITHOUT X-CSRF-Token. CSRFMiddleware should
	// reject with 403 csrf_missing before the logout handler runs.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/auth/logout", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /auth/logout (no csrf): %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF: status = %d body=%s, want 403", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "csrf_missing") {
		t.Errorf("logout without CSRF: body = %s, want csrf_missing", body)
	}
}

// TestIntegration_BFF_AnonymousCSRFEndpoint asserts the
// authentication gate on /api/v1/csrf: with no session cookie the
// endpoint returns 401, not the token (design §csrf §subtle-choices:
// "CSRF endpoint requires auth — a pre-auth fetch would let an
// attacker mint a token cross-site").
func TestIntegration_BFF_AnonymousCSRFEndpoint(t *testing.T) {
	pool := scopedDB(t)
	users := db.NewUserPostgres(pool)
	sessions := bff.NewPostgresResolver(pool)

	enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enforcer); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	sessionMW := bff.SessionMiddleware(bff.MiddlewareDeps{
		Sessions: sessions,
		OAuth:    &stubOAuthClient{},
		Users:    users,
		CookieConfig: bff.CookieConfig{
			Path:     "/",
			Secure:   false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   14 * 24 * time.Hour,
		},
		IdleTimeout: 24 * time.Hour,
	})

	h := api.New(api.Deps{
		Specimens:  db.NewSpecimenPostgres(pool),
		Collectors: db.NewCollectorPostgres(pool),
		Enforcer:   enforcer,
		Users:      users,
		Verifier:   rejectingVerifier{},
		SessionMW:  sessionMW,
		CSRFMW:     bff.CSRFMiddleware,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v1/csrf")
	if err != nil {
		t.Fatalf("anonymous GET /api/v1/csrf: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/v1/csrf status = %d body=%s, want 401",
			resp.StatusCode, body)
	}
}

// stubOAuthClient implements bff.OAuthClient for the integration
// test. Exchange returns a deterministic token set whose id_token
// carries `sub` and `email` claims the BFF callback handler parses
// without signature verification. Refresh is unused by this test
// (the session was just created); EndSessionURL returns "" so the
// logout handler falls back to its 204 No Content path.
type stubOAuthClient struct {
	mu                    sync.Mutex
	sub                   string
	email                 string
	expectCode            string
	accessTokenExpiresIn  time.Duration
	refreshTokenExpiresIn time.Duration
}

func (s *stubOAuthClient) AuthCodeURL(state, redirectURI string) string {
	v := url.Values{}
	v.Set("state", state)
	v.Set("redirect_uri", redirectURI)
	v.Set("client_id", "stub")
	v.Set("response_type", "code")
	return "http://keycloak.test/realms/stub/protocol/openid-connect/auth?" + v.Encode()
}

func (s *stubOAuthClient) RegisterURL(state, redirectURI string) string {
	v := url.Values{}
	v.Set("state", state)
	v.Set("redirect_uri", redirectURI)
	v.Set("client_id", "stub")
	v.Set("response_type", "code")
	return "http://keycloak.test/realms/stub/protocol/openid-connect/registrations?" + v.Encode()
}

func (s *stubOAuthClient) Exchange(_ context.Context, code, _ string) (bff.Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expectCode != "" && code != s.expectCode {
		return bff.Tokens{}, fmt.Errorf("stub: code mismatch: got %q want %q", code, s.expectCode)
	}
	now := time.Now().UTC()
	return bff.Tokens{
		AccessToken:           "stub-access",
		RefreshToken:          "stub-refresh",
		IDToken:               makeStubIDToken(s.sub, s.email),
		AccessTokenExpiresAt:  now.Add(s.accessTokenExpiresIn),
		RefreshTokenExpiresAt: now.Add(s.refreshTokenExpiresIn),
	}, nil
}

func (s *stubOAuthClient) Refresh(_ context.Context, _ string) (bff.Tokens, error) {
	return bff.Tokens{}, errors.New("stub: Refresh not exercised")
}

func (s *stubOAuthClient) EndSessionURL(_, _ string) string { return "" }

// makeStubIDToken builds a JWT-shaped string whose payload carries
// the supplied sub + email claims. The BFF callback's
// claimsFromIDToken intentionally skips signature verification —
// the design trusts the TLS round-trip to Keycloak's token endpoint
// — so the header and signature can be arbitrary base64url strings.
func makeStubIDToken(sub, email string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		fmt.Sprintf(`{"sub":%q,"email":%q}`, sub, email),
	))
	sig := base64.RawURLEncoding.EncodeToString([]byte("stub-signature"))
	return header + "." + payload + "." + sig
}
