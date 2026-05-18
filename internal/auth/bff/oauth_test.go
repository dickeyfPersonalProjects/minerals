package bff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeKeycloak is the minimal httptest fixture the OAuth tests need.
// We don't run real OIDC discovery — discovery is exercised in
// internal/oidc/verifier_test.go, and re-running it here would only
// duplicate the go-oidc library's tests.
type fakeKeycloak struct {
	server *httptest.Server
	// observed records the most recent token-endpoint request so
	// individual tests can assert on it.
	observed struct {
		grantType    string
		code         string
		refreshToken string
		redirectURI  string
		clientID     string
		clientSecret string
	}
	// nextResponse is what the token endpoint returns on the next
	// hit. Tests overwrite it before each call.
	nextResponse tokenResponse
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

func newFakeKeycloak(t *testing.T) *fakeKeycloak {
	t.Helper()
	fk := &fakeKeycloak{}
	mux := http.NewServeMux()
	mux.HandleFunc("/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		fk.observed.grantType = r.PostForm.Get("grant_type")
		fk.observed.code = r.PostForm.Get("code")
		fk.observed.refreshToken = r.PostForm.Get("refresh_token")
		fk.observed.redirectURI = r.PostForm.Get("redirect_uri")
		// Confidential client: client_id/client_secret arrive via
		// HTTP Basic per RFC 6749 §2.3.1 (oauth2.Config uses that
		// by default).
		fk.observed.clientID, fk.observed.clientSecret, _ = r.BasicAuth()

		w.Header().Set("Content-Type", "application/json")
		// gosec G117 flags the json-encoded "access_token" key as a
		// potential secret leak; it is — the field is the literal
		// OAuth payload an httptest stub is supposed to emit.
		_ = json.NewEncoder(w).Encode(fk.nextResponse) //nolint:gosec
	})
	fk.server = httptest.NewServer(mux)
	t.Cleanup(fk.server.Close)
	return fk
}

func (fk *fakeKeycloak) endpoint() oauth2.Endpoint {
	return oauth2.Endpoint{
		AuthURL:  fk.server.URL + "/protocol/openid-connect/auth",
		TokenURL: fk.server.URL + "/protocol/openid-connect/token",
	}
}

func newTestClient(t *testing.T, fk *fakeKeycloak) *keycloakClient {
	t.Helper()
	return newKeycloakClientFromEndpoints(
		OAuthConfig{
			Issuer:       fk.server.URL,
			ClientID:     "minerals-frontend",
			ClientSecret: "test-secret",
			Scopes:       []string{"openid", "profile", "email"},
		},
		fk.endpoint(),
		fk.server.URL+"/protocol/openid-connect/logout",
	)
}

// TestAuthCodeURL_ContainsRequiredParams asserts the redirect URL
// carries the OAuth 2.1 authorization-request parameters Keycloak
// validates server-side. Skipping any of these is a common cause of
// the "invalid_request" error at the Keycloak login screen.
func TestAuthCodeURL_ContainsRequiredParams(t *testing.T) {
	t.Parallel()
	fk := newFakeKeycloak(t)
	c := newTestClient(t, fk)

	got := c.AuthCodeURL("state-123", "https://app.example.com/auth/callback")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse auth code URL: %v", err)
	}
	q := u.Query()

	if q.Get("client_id") != "minerals-frontend" {
		t.Errorf("client_id = %q, want minerals-frontend", q.Get("client_id"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("redirect_uri") != "https://app.example.com/auth/callback" {
		t.Errorf("redirect_uri = %q, want https://app.example.com/auth/callback", q.Get("redirect_uri"))
	}
	if q.Get("state") != "state-123" {
		t.Errorf("state = %q, want state-123", q.Get("state"))
	}
	// Scopes are space-separated in the value.
	gotScopes := strings.Fields(q.Get("scope"))
	wantScopes := map[string]bool{"openid": true, "profile": true, "email": true}
	if len(gotScopes) != len(wantScopes) {
		t.Errorf("scope count = %d, want %d (got=%v)", len(gotScopes), len(wantScopes), gotScopes)
	}
	for _, s := range gotScopes {
		if !wantScopes[s] {
			t.Errorf("unexpected scope %q in %v", s, gotScopes)
		}
	}
}

// TestExchange_RoundTrip exercises code→tokens. The fake Keycloak
// returns a Keycloak-shaped payload (separate refresh_expires_in,
// id_token in the response body); the client must surface those
// in the Tokens struct.
func TestExchange_RoundTrip(t *testing.T) {
	t.Parallel()
	fk := newFakeKeycloak(t)
	c := newTestClient(t, fk)

	fk.nextResponse = tokenResponse{
		AccessToken:      "access-1",
		RefreshToken:     "refresh-1",
		IDToken:          "id-1",
		TokenType:        "Bearer",
		ExpiresIn:        300,
		RefreshExpiresIn: 1800,
	}

	before := time.Now()
	got, err := c.Exchange(context.Background(),
		"code-abc",
		"https://app.example.com/auth/callback",
	)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if fk.observed.grantType != "authorization_code" {
		t.Errorf("grant_type sent = %q, want authorization_code", fk.observed.grantType)
	}
	if fk.observed.code != "code-abc" {
		t.Errorf("code sent = %q, want code-abc", fk.observed.code)
	}
	if fk.observed.redirectURI != "https://app.example.com/auth/callback" {
		t.Errorf("redirect_uri sent = %q, want https://app.example.com/auth/callback",
			fk.observed.redirectURI)
	}
	if fk.observed.clientID != "minerals-frontend" || fk.observed.clientSecret != "test-secret" {
		t.Errorf("client credentials not sent via Basic: id=%q secret=%q",
			fk.observed.clientID, fk.observed.clientSecret)
	}
	if got.AccessToken != "access-1" || got.RefreshToken != "refresh-1" || got.IDToken != "id-1" {
		t.Errorf("tokens = %+v, want access-1/refresh-1/id-1", got)
	}
	// Within a generous window of the expected expiry — the
	// oauth2 library uses time.Now() internally and we can't pin
	// it without a clock seam, so accept anything in the right
	// neighborhood.
	if got.AccessTokenExpiresAt.Before(before.Add(290*time.Second)) ||
		got.AccessTokenExpiresAt.After(before.Add(310*time.Second).Add(5*time.Second)) {
		t.Errorf("AccessTokenExpiresAt = %v, want ~now+300s", got.AccessTokenExpiresAt)
	}
	if got.RefreshTokenExpiresAt.Before(before.Add(1790*time.Second)) ||
		got.RefreshTokenExpiresAt.After(before.Add(1810*time.Second).Add(5*time.Second)) {
		t.Errorf("RefreshTokenExpiresAt = %v, want ~now+1800s", got.RefreshTokenExpiresAt)
	}
}

// TestExchange_MissingIDTokenIsError guards the Tokens-from-OAuth2
// shaper: an OIDC token endpoint without an id_token is a
// misconfigured client, and the BFF can't logout cleanly later (no
// id_token_hint). Surface the error early.
func TestExchange_MissingIDTokenIsError(t *testing.T) {
	t.Parallel()
	fk := newFakeKeycloak(t)
	c := newTestClient(t, fk)

	fk.nextResponse = tokenResponse{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		// IDToken omitted
		TokenType: "Bearer",
		ExpiresIn: 300,
	}
	_, err := c.Exchange(context.Background(), "code", "https://app.example.com/cb")
	if err == nil {
		t.Fatal("Exchange succeeded with missing id_token; want error")
	}
	if !strings.Contains(err.Error(), "id_token") {
		t.Errorf("error = %v, want mention of id_token", err)
	}
}

// TestRefresh_RoundTrip exercises the refresh-token flow. Keycloak
// rotates the refresh token on every use; the test asserts the new
// value flows through.
func TestRefresh_RoundTrip(t *testing.T) {
	t.Parallel()
	fk := newFakeKeycloak(t)
	c := newTestClient(t, fk)

	fk.nextResponse = tokenResponse{
		AccessToken:      "access-2",
		RefreshToken:     "refresh-2-rotated",
		IDToken:          "id-2",
		TokenType:        "Bearer",
		ExpiresIn:        300,
		RefreshExpiresIn: 1800,
	}
	got, err := c.Refresh(context.Background(), "refresh-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if fk.observed.grantType != "refresh_token" {
		t.Errorf("grant_type sent = %q, want refresh_token", fk.observed.grantType)
	}
	if fk.observed.refreshToken != "refresh-1" {
		t.Errorf("refresh_token sent = %q, want refresh-1", fk.observed.refreshToken)
	}
	if got.RefreshToken != "refresh-2-rotated" {
		t.Errorf("RefreshToken = %q, want refresh-2-rotated (rotation)", got.RefreshToken)
	}
	if got.AccessToken != "access-2" || got.IDToken != "id-2" {
		t.Errorf("tokens = %+v, want access-2/id-2", got)
	}
}

// TestEndSessionURL_BuildsQueryParams asserts the logout URL carries
// id_token_hint and post_logout_redirect_uri — Keycloak skips the
// confirmation prompt only when id_token_hint is present.
func TestEndSessionURL_BuildsQueryParams(t *testing.T) {
	t.Parallel()
	fk := newFakeKeycloak(t)
	c := newTestClient(t, fk)

	got := c.EndSessionURL("id-token-1", "https://app.example.com/")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse end-session URL: %v", err)
	}
	if u.Path != "/protocol/openid-connect/logout" {
		t.Errorf("Path = %q, want /protocol/openid-connect/logout", u.Path)
	}
	q := u.Query()
	if q.Get("id_token_hint") != "id-token-1" {
		t.Errorf("id_token_hint = %q, want id-token-1", q.Get("id_token_hint"))
	}
	if q.Get("post_logout_redirect_uri") != "https://app.example.com/" {
		t.Errorf("post_logout_redirect_uri = %q, want https://app.example.com/",
			q.Get("post_logout_redirect_uri"))
	}
}

// TestEndSessionURL_EmptyDiscoveryReturnsEmpty: when the discovery
// document omits end_session_endpoint (some IdPs do), the helper
// returns "" rather than a malformed URL — the handler then falls
// back to its own post-logout target.
func TestEndSessionURL_EmptyDiscoveryReturnsEmpty(t *testing.T) {
	t.Parallel()
	fk := newFakeKeycloak(t)
	c := newKeycloakClientFromEndpoints(
		OAuthConfig{Issuer: fk.server.URL, ClientID: "id", ClientSecret: "s"},
		fk.endpoint(),
		"", // no end-session URL
	)
	if got := c.EndSessionURL("id-token", "https://app.example.com/"); got != "" {
		t.Errorf("EndSessionURL with empty discovery = %q, want \"\"", got)
	}
}

// TestNewKeycloakOAuthClient_DiscoveryURLOverride proves the
// OIDC_DISCOVERY_URL escape hatch (mi-8tnv) really does fetch the
// well-known doc from DiscoveryURL while keeping Issuer as the
// canonical `iss` value. The httptest server stands in for the
// in-network address (`http://keycloak:8080/...` in compose); the
// canonical Issuer URL is a literal that nothing actually serves —
// if discovery were going there, NewKeycloakOAuthClient would fail.
func TestNewKeycloakOAuthClient_DiscoveryURLOverride(t *testing.T) {
	t.Parallel()
	const canonicalIssuer = "https://auth.example.invalid/realms/minerals"

	disco := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// gosec G101: well-known OIDC discovery doc literals — public
		// metadata, no secrets. The hardcoded URLs are httptest /
		// fake-network strings, not credentials.
		_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:gosec
			// The discovery doc reports the canonical issuer — exactly
			// the split Keycloak produces with KC_HOSTNAME=<public> +
			// KC_HOSTNAME_BACKCHANNEL_DYNAMIC=true.
			"issuer":                                canonicalIssuer,
			"authorization_endpoint":                canonicalIssuer + "/protocol/openid-connect/auth",
			"token_endpoint":                        "http://keycloak:8080/realms/minerals/protocol/openid-connect/token",
			"jwks_uri":                              "http://keycloak:8080/realms/minerals/protocol/openid-connect/certs",
			"end_session_endpoint":                  canonicalIssuer + "/protocol/openid-connect/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	t.Cleanup(disco.Close)

	c, err := NewKeycloakOAuthClient(context.Background(), OAuthConfig{
		Issuer:       canonicalIssuer,
		DiscoveryURL: disco.URL,
		ClientID:     "minerals-frontend",
		ClientSecret: "test-secret",
		Scopes:       []string{"openid"},
	})
	if err != nil {
		t.Fatalf("NewKeycloakOAuthClient with DiscoveryURL: %v", err)
	}

	// Endpoints from the discovery doc must reach the in-network
	// addresses — proves the override flowed through the constructor.
	kc, ok := c.(*keycloakClient)
	if !ok {
		t.Fatalf("returned client type = %T, want *keycloakClient", c)
	}
	if kc.endpoints.TokenURL != "http://keycloak:8080/realms/minerals/protocol/openid-connect/token" {
		t.Errorf("token endpoint = %q, want in-network keycloak:8080 URL", kc.endpoints.TokenURL)
	}
	if kc.endpoints.AuthURL != canonicalIssuer+"/protocol/openid-connect/auth" {
		t.Errorf("auth endpoint = %q, want canonical public URL", kc.endpoints.AuthURL)
	}
	if kc.endSessionURL != canonicalIssuer+"/protocol/openid-connect/logout" {
		t.Errorf("end_session = %q, want canonical public URL", kc.endSessionURL)
	}
}

// TestNewKeycloakOAuthClient_RequiredFields locks the constructor
// invariants so a missing env var fails at boot rather than at the
// first user login.
func TestNewKeycloakOAuthClient_RequiredFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  OAuthConfig
		want string
	}{
		{"no issuer", OAuthConfig{ClientID: "c", ClientSecret: "s"}, "Issuer"},
		{"no client id", OAuthConfig{Issuer: "x", ClientSecret: "s"}, "ClientID"},
		{"no client secret", OAuthConfig{Issuer: "x", ClientID: "c"}, "ClientSecret"},
	}
	for _, tc := range cases {
		_, err := NewKeycloakOAuthClient(context.Background(), tc.cfg)
		if err == nil {
			t.Errorf("%s: NewKeycloakOAuthClient succeeded; want error mentioning %s", tc.name, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want mention of %s", tc.name, err, tc.want)
		}
	}
}
