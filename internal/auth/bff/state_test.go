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

const testHMACKey = "0123456789abcdef0123456789abcdef" // 32 bytes

// TestSignVerifyState_RoundTrip locks the canonical happy path —
// SignState followed by VerifyState recovers the original fields,
// and the resulting cookie value is base64url-clean (no padding,
// no `=`).
func TestSignVerifyState_RoundTrip(t *testing.T) {
	t.Parallel()
	key := []byte(testHMACKey)
	d := bff.StateData{
		State:    "state-abc",
		ReturnTo: "/specimens/123",
		Expires:  time.Now().Add(time.Minute).UTC(),
	}
	signed, err := bff.SignState(key, d)
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}
	if strings.Contains(signed, "=") {
		t.Errorf("cookie value contains '=' padding: %q", signed)
	}
	got, err := bff.VerifyState(key, signed, time.Now())
	if err != nil {
		t.Fatalf("VerifyState: %v", err)
	}
	if got.State != d.State || got.ReturnTo != d.ReturnTo {
		t.Errorf("round-trip lost data: got %+v want %+v", got, d)
	}
	// Expires round-trips with nanosecond precision via UnixNano.
	if !got.Expires.Equal(d.Expires) {
		t.Errorf("Expires lost precision: got %v want %v", got.Expires, d.Expires)
	}
}

// TestVerifyState_ExpiredRejected guards the TTL check — a
// just-expired cookie MUST surface ErrStateInvalid so the handler
// 400s rather than admitting a stale state across login attempts.
func TestVerifyState_ExpiredRejected(t *testing.T) {
	t.Parallel()
	key := []byte(testHMACKey)
	now := time.Now()
	signed, err := bff.SignState(key, bff.StateData{
		State:   "s",
		Expires: now.Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}
	if _, err := bff.VerifyState(key, signed, now); err == nil {
		t.Fatal("VerifyState accepted expired cookie; want ErrStateInvalid")
	}
}

// TestVerifyState_TamperRejected proves the HMAC defends against
// payload tampering. Flipping a byte of the payload (or the MAC)
// MUST collapse to ErrStateInvalid via the constant-time compare.
func TestVerifyState_TamperRejected(t *testing.T) {
	t.Parallel()
	key := []byte(testHMACKey)
	signed, err := bff.SignState(key, bff.StateData{
		State:   "good",
		Expires: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}
	// Flip a single bit in the payload portion (everything before the '.').
	dot := strings.IndexByte(signed, '.')
	if dot < 1 {
		t.Fatalf("malformed signed value: %q", signed)
	}
	flipIdx := dot / 2
	tampered := []byte(signed)
	// A character that decodes differently — change without
	// risking an illegal base64 character.
	if tampered[flipIdx] == 'A' {
		tampered[flipIdx] = 'B'
	} else {
		tampered[flipIdx] = 'A'
	}
	if _, err := bff.VerifyState(key, string(tampered), time.Now()); err == nil {
		t.Fatal("VerifyState accepted tampered cookie; want ErrStateInvalid")
	}
}

// TestVerifyState_WrongKeyRejected confirms the MAC is keyed —
// signing with one key and verifying with another MUST fail. This
// is the property that lets rotating OAUTH_STATE_HMAC_KEY
// immediately invalidate every in-flight login attempt.
func TestVerifyState_WrongKeyRejected(t *testing.T) {
	t.Parallel()
	signed, err := bff.SignState([]byte(testHMACKey), bff.StateData{
		State:   "s",
		Expires: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}
	other := []byte("ffffffffffffffffffffffffffffffff")
	if _, err := bff.VerifyState(other, signed, time.Now()); err == nil {
		t.Fatal("VerifyState accepted cookie under wrong key; want ErrStateInvalid")
	}
}

// TestVerifyState_MalformedShapesRejected covers all the wrong-shape
// inputs that a confused or hostile browser might present. Each
// MUST collapse to ErrStateInvalid (never panic, never decode
// successfully).
func TestVerifyState_MalformedShapesRejected(t *testing.T) {
	t.Parallel()
	key := []byte(testHMACKey)
	cases := []string{
		"",          // empty
		"no-dot",    // no separator
		"....",      // too many separators
		"!!!!.!!!!", // non-base64 chars
		"AAAA.AAAA", // valid base64 but wrong size
		base64.RawURLEncoding.EncodeToString([]byte("not-a-valid-payload")) + ".AAAA",
	}
	for _, raw := range cases {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := bff.VerifyState(key, raw, time.Now()); err == nil {
				t.Errorf("VerifyState(%q) accepted malformed value; want error", raw)
			}
		})
	}
}

// TestSignState_RejectsShortKey locks the 32-byte minimum on the
// HMAC key — a misconfigured env var MUST fail loudly at the first
// SignState call rather than silently weakening the MAC strength.
func TestSignState_RejectsShortKey(t *testing.T) {
	t.Parallel()
	if _, err := bff.SignState([]byte("too short"), bff.StateData{Expires: time.Now().Add(time.Minute)}); err == nil {
		t.Fatal("SignState accepted short key; want error")
	}
	if _, err := bff.VerifyState([]byte("too short"), "x.y", time.Now()); err == nil {
		t.Fatal("VerifyState accepted short key; want error")
	}
}

// TestNewStateToken_LengthAndAlphabet guards the entropy
// guarantee: 32 bytes encoded as base64url-no-pad is exactly 43
// chars and contains no '=' padding.
func TestNewStateToken_LengthAndAlphabet(t *testing.T) {
	t.Parallel()
	tok, err := bff.NewStateToken()
	if err != nil {
		t.Fatalf("NewStateToken: %v", err)
	}
	if len(tok) != 43 {
		t.Errorf("token len = %d, want 43 (base64url of 32 bytes)", len(tok))
	}
	if strings.Contains(tok, "=") {
		t.Errorf("token contains '=' padding: %q", tok)
	}
}

// TestSetStateCookie_Attributes asserts the state-cookie flags
// match the design (HttpOnly + Lax + Path=/auth + 5min MaxAge).
// Drift on any of these breaks either the OAuth round-trip
// (Strict blocks the redirect) or the security boundary (missing
// HttpOnly exposes the value to JS).
func TestSetStateCookie_Attributes(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	bff.SetStateCookie(rec, "sig.value", true)
	c := findCookie(t, rec.Result(), bff.StateCookieName)

	if c.Value != "sig.value" {
		t.Errorf("Value = %q, want sig.value", c.Value)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly = false, want true")
	}
	if !c.Secure {
		t.Error("Secure = false, want true (per the helper input)")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax (Strict would block the Keycloak redirect)", c.SameSite)
	}
	if c.Path != "/auth" {
		t.Errorf("Path = %q, want /auth", c.Path)
	}
	if c.MaxAge != int(bff.StateTTL.Seconds()) {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, int(bff.StateTTL.Seconds()))
	}
}

// TestClearStateCookie_Attributes confirms the clear-state helper
// matches Path/Secure/SameSite from the set helper. Mismatch would
// leave the original cookie alive — the same logout-bug pattern
// CookieConfig defends against for the session cookie.
func TestClearStateCookie_Attributes(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	bff.ClearStateCookie(rec, true)
	c := findCookie(t, rec.Result(), bff.StateCookieName)
	if c.Value != "" {
		t.Errorf("Value = %q, want empty", c.Value)
	}
	if c.MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", c.MaxAge)
	}
	if c.Path != "/auth" {
		t.Errorf("Path = %q, want /auth", c.Path)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
}

// findCookie is the local helper for tests in this file — the
// cookie_test.go helper is also named getCookie and we keep the
// two namespaces separate so each file is self-contained.
func findCookie(t *testing.T, r *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range r.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q not set; cookies=%v", name, r.Cookies())
	return nil
}
