package bff_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
)

// TestSetSessionCookie_AttributesMatchDesign locks every attribute
// the design doc (§cookie-attributes) prescribes. The cookie is the
// credential — drift in any of these flags is a real security
// regression, so they're asserted individually rather than as a
// string comparison.
func TestSetSessionCookie_AttributesMatchDesign(t *testing.T) {
	t.Parallel()
	var id [32]byte
	for i := range id {
		id[i] = byte(i)
	}
	cfg := bff.CookieConfig{
		Path:     "/",
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   14 * 24 * time.Hour,
	}

	rec := httptest.NewRecorder()
	bff.SetSessionCookie(rec, id, cfg)

	c := getCookie(t, rec.Result(), bff.SessionCookieName)

	if !c.HttpOnly {
		t.Errorf("HttpOnly = false, want true (the credential MUST be invisible to document.cookie)")
	}
	if !c.Secure {
		t.Errorf("Secure = false, want true (set in CookieConfig)")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want %v", c.SameSite, http.SameSiteLaxMode)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want %q", c.Path, "/")
	}
	if c.Domain != "" {
		t.Errorf("Domain = %q, want empty (host-only per design doc)", c.Domain)
	}
	if c.MaxAge != int((14 * 24 * time.Hour).Seconds()) {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, int((14 * 24 * time.Hour).Seconds()))
	}
	// Value is base64url(id) without padding (43 chars for 32-byte id).
	want := base64.RawURLEncoding.EncodeToString(id[:])
	if c.Value != want {
		t.Errorf("Value = %q, want %q", c.Value, want)
	}
	if strings.Contains(c.Value, "=") {
		t.Errorf("Value contains '=' padding; raw url encoding must be used")
	}
}

// TestSetSessionCookie_DevSecureFalse mirrors the dev compose stack
// where COOKIE_SECURE=false. Without this guard the helper could
// silently force Secure and break the dev flow.
func TestSetSessionCookie_DevSecureFalse(t *testing.T) {
	t.Parallel()
	var id [32]byte
	cfg := bff.CookieConfig{
		Path:     "/",
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   time.Hour,
	}
	rec := httptest.NewRecorder()
	bff.SetSessionCookie(rec, id, cfg)
	c := getCookie(t, rec.Result(), bff.SessionCookieName)
	if c.Secure {
		t.Errorf("Secure = true, want false in dev config")
	}
}

// TestClearSessionCookie_MatchesAttributesAndExpires confirms the
// clear cookie carries the same Path/SameSite/Secure as the original
// set, an empty value, and MaxAge=-1. Browser cookie identity is
// (Name, Domain, Path) — mismatched Path leaves the original cookie
// in place, which is the #1 logout bug per the design doc.
func TestClearSessionCookie_MatchesAttributesAndExpires(t *testing.T) {
	t.Parallel()
	cfg := bff.CookieConfig{
		Path:     "/",
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   14 * 24 * time.Hour,
	}
	rec := httptest.NewRecorder()
	bff.ClearSessionCookie(rec, cfg)
	c := getCookie(t, rec.Result(), bff.SessionCookieName)

	if c.Value != "" {
		t.Errorf("Value = %q, want empty", c.Value)
	}
	if c.MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1 (expire now)", c.MaxAge)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want %q (must match Set)", c.Path, "/")
	}
	if !c.HttpOnly {
		t.Errorf("HttpOnly = false on clear; must match Set")
	}
	if !c.Secure {
		t.Errorf("Secure = false on clear; must match Set")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v on clear; must match Set (was Lax)", c.SameSite)
	}
}

// TestCookieConfig_EmptyPathPanics guards the helper's invariant
// that Path is always supplied — silently defaulting "" to "/"
// would mask the Set/Clear drift that the helper exists to prevent.
func TestCookieConfig_EmptyPathPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty Path; got none")
		}
	}()
	rec := httptest.NewRecorder()
	bff.SetSessionCookie(rec, [32]byte{}, bff.CookieConfig{Path: ""})
}

// getCookie returns the named cookie from r.Cookies(). Fails the
// test if it is not present.
func getCookie(t *testing.T, r *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range r.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q not set; got cookies=%v", name, r.Cookies())
	return nil
}
