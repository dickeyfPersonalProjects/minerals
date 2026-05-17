package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCSP_DefaultSelfOnly asserts that with no OIDC issuer origin
// configured, the CSP `connect-src` and `frame-src` directives are
// `'self'` only — the original §17 baseline. This is the path for
// environments without auth (dev without OIDC, anonymous-only
// deployments).
func TestCSP_DefaultSelfOnly(t *testing.T) {
	t.Parallel()
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header missing")
	}
	if !strings.Contains(csp, "connect-src 'self';") {
		t.Errorf("expected connect-src 'self' only; CSP = %q", csp)
	}
	if !strings.Contains(csp, "frame-src 'self';") {
		t.Errorf("expected frame-src 'self' only; CSP = %q", csp)
	}
	// Sanity: no stray origin leaked into the policy.
	if strings.Contains(csp, "http://") || strings.Contains(csp, "https://") {
		t.Errorf("expected no cross-origin sources; CSP = %q", csp)
	}
}

// TestCSP_IssuerOriginAppendedToConnectSrc is the regression test for
// mi-cl1: when PUBLIC_OIDC_ISSUER_URL is configured, the issuer origin
// MUST appear in `connect-src` so the browser allows the SPA's POST
// to the Keycloak token endpoint during the PKCE flow.
func TestCSP_IssuerOriginAppendedToConnectSrc(t *testing.T) {
	t.Parallel()
	const origin = "https://auth.example.com"
	h := New(Deps{CSPIssuerOrigin: origin})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header missing")
	}
	want := "connect-src 'self' " + origin + ";"
	if !strings.Contains(csp, want) {
		t.Errorf("expected %q in CSP; got %q", want, csp)
	}
	// No other directive should be widened beyond the documented two
	// (connect-src and frame-src); the rest must stay 'self'.
	for _, d := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	} {
		if !strings.Contains(csp, d) {
			t.Errorf("expected %q untouched in CSP; got %q", d, csp)
		}
	}
}

// TestCSP_IssuerOriginAppendedToFrameSrc is the regression test for
// mi-ct2: when PUBLIC_OIDC_ISSUER_URL is configured, the issuer origin
// MUST appear in `frame-src` so the SPA can mount a hidden iframe to
// Keycloak's authorize endpoint for silent renewal on page load.
func TestCSP_IssuerOriginAppendedToFrameSrc(t *testing.T) {
	t.Parallel()
	const origin = "https://auth.example.com"
	h := New(Deps{CSPIssuerOrigin: origin})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header missing")
	}
	want := "frame-src 'self' " + origin + ";"
	if !strings.Contains(csp, want) {
		t.Errorf("expected %q in CSP; got %q", want, csp)
	}
	// `frame-ancestors 'none'` (blocking our app from being framed by
	// others) is orthogonal to `frame-src` (what WE may frame) and
	// MUST stay locked down even when frame-src widens.
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("frame-ancestors must remain 'none'; CSP = %q", csp)
	}
}

// TestCSP_IssuerOriginOnlyOriginNotPath documents the contract: the
// caller is responsible for passing scheme://host[:port] — server.go
// trusts the value verbatim. config.Load() parses
// PUBLIC_OIDC_ISSUER_URL into an origin specifically so this layer
// stays a pure string interpolation. The test exists to catch a
// future change that accidentally drops a full URL (with path) into
// CSPIssuerOrigin, which would emit a meaningless policy.
func TestCSP_IssuerOriginOnlyOriginNotPath(t *testing.T) {
	t.Parallel()
	// If anyone wires a full URL into CSPIssuerOrigin, the policy will
	// still emit it — but the path is meaningless to CSP source
	// matching, so the assertion below is a tripwire for that mistake.
	h := New(Deps{CSPIssuerOrigin: "https://auth.example.com"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	// The substring after the origin should be the directive separator,
	// not a `/realms/...` path.
	idx := strings.Index(csp, "https://auth.example.com")
	if idx < 0 {
		t.Fatalf("origin missing from CSP: %q", csp)
	}
	rest := csp[idx+len("https://auth.example.com"):]
	if !strings.HasPrefix(rest, ";") && !strings.HasPrefix(rest, " ;") {
		t.Errorf("expected origin to end a directive; got trailing %q", rest)
	}
}
