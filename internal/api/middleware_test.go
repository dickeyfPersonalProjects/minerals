package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCSP_SelfOnly asserts the §17 CSP `connect-src` is `'self'`
// only. Under V2 BFF the SPA never speaks OAuth directly, so no
// cross-origin source needs to be allow-listed.
func TestCSP_SelfOnly(t *testing.T) {
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
		t.Errorf("expected connect-src 'self'; CSP = %q", csp)
	}
	if strings.Contains(csp, "http://") || strings.Contains(csp, "https://") {
		t.Errorf("expected no cross-origin sources; CSP = %q", csp)
	}
}

// TestCSP_StyleSrcNoUnsafeInline asserts the global CSP locks
// <style>/<link> stylesheets to 'self' — `style-src 'self'` with no
// 'unsafe-inline' — so injected style elements cannot load (mi-97cl).
// 'unsafe-inline' is permitted only via `style-src-attr`, which governs
// the inline `style=""` attributes Svelte emits for dynamic values.
func TestCSP_StyleSrcNoUnsafeInline(t *testing.T) {
	t.Parallel()
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header missing")
	}
	if !strings.Contains(csp, "style-src 'self';") {
		t.Errorf("expected style-src 'self'; CSP = %q", csp)
	}
	if strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("style-src must not allow 'unsafe-inline'; CSP = %q", csp)
	}
	if !strings.Contains(csp, "style-src-attr 'unsafe-inline'") {
		t.Errorf("expected style-src-attr 'unsafe-inline' for dynamic inline style attrs; CSP = %q", csp)
	}
}
