package bff

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// StateCookieName is the wire name of the short-lived signed
	// cookie that carries OAuth state + return_to between
	// /auth/login and /auth/callback. Independent from the session
	// cookie (different lifetime, different Path scope) and cleared
	// at the end of the auth dance.
	StateCookieName = "minerals_oauth_state"

	// StateTTL is how long the signed state cookie stays valid.
	// Five minutes covers a Keycloak login (typical: <30s, including
	// federation hops) and limits the window for replay of a stolen
	// state value. The CSRF-on-login risk is small but real.
	StateTTL = 5 * time.Minute

	// stateTokenLen is the byte length of the OAuth state value the
	// login handler mints. 32 bytes (256 bits) of entropy makes
	// guessing infeasible; base64url-encoded it is 43 chars on the
	// wire, well under any IdP's state-length limits.
	stateTokenLen = 32

	// minStateHMACKeyLen is the floor on the HMAC key length.
	// 32 bytes matches SHA-256's block-cipher security level —
	// anything shorter weakens the MAC and is rejected at sign /
	// verify time so a careless env var cannot downgrade the
	// guarantee.
	minStateHMACKeyLen = 32
)

// ErrStateInvalid is returned by VerifyState whenever the cookie
// value fails any check (malformed, bad HMAC, expired). Callers
// MUST translate it to a generic 400 invalid_state response — the
// exact failure reason is logged server-side but never disclosed,
// since the difference between "bad MAC" and "expired" would help
// an attacker probe for forgery.
var ErrStateInvalid = errors.New("bff: invalid state cookie")

// StateData is the payload stamped into the signed state cookie.
// State is the OAuth state value, ReturnTo the (validated) path
// to redirect the browser to after callback, Expires the absolute
// instant past which the cookie is considered stale.
type StateData struct {
	State    string
	ReturnTo string
	Expires  time.Time
}

// NewStateToken returns 32 cryptographically random bytes encoded
// as base64url (no padding) — the value the login handler embeds
// both in the state cookie and in the AuthCodeURL query string for
// the constant-time match on callback.
func NewStateToken() (string, error) {
	var b [stateTokenLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("bff: state token rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// SignState returns the cookie value for d. The wire format is:
//
//	cookie = base64url(payload) || "." || base64url(HMAC-SHA256(key, payload))
//
// with the payload binary-packed as
// stateLen(2 BE)||state||returnToLen(2 BE)||returnTo||expiresUnixNano(8 BE).
// Binary packing (rather than JSON) keeps the cookie small and the
// byte representation deterministic — JSON's map ordering would
// otherwise sneak in spurious MAC differences across Go versions.
func SignState(key []byte, d StateData) (string, error) {
	if len(key) < minStateHMACKeyLen {
		return "", fmt.Errorf("bff: state HMAC key must be >= %d bytes, got %d",
			minStateHMACKeyLen, len(key))
	}
	payload, err := packState(d)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyState decodes value, checks the HMAC in constant time, and
// returns the payload if the embedded Expires is still in the
// future at now. Every failure mode collapses to ErrStateInvalid
// — the caller writes a single 400 envelope regardless, so
// preserving distinct sentinels would only leak information.
func VerifyState(key []byte, value string, now time.Time) (StateData, error) {
	if len(key) < minStateHMACKeyLen {
		return StateData{}, fmt.Errorf("bff: state HMAC key must be >= %d bytes, got %d",
			minStateHMACKeyLen, len(key))
	}
	pb64, mb64, ok := strings.Cut(value, ".")
	if !ok {
		return StateData{}, ErrStateInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(pb64)
	if err != nil {
		return StateData{}, ErrStateInvalid
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(mb64)
	if err != nil {
		return StateData{}, ErrStateInvalid
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if subtle.ConstantTimeCompare(gotMAC, mac.Sum(nil)) != 1 {
		return StateData{}, ErrStateInvalid
	}
	d, err := unpackState(payload)
	if err != nil {
		return StateData{}, ErrStateInvalid
	}
	if now.After(d.Expires) {
		return StateData{}, ErrStateInvalid
	}
	return d, nil
}

// packState produces the deterministic binary payload signed by
// SignState. The 2-byte length prefixes cap state/returnTo at 65535
// bytes each — far past any sensible value, but explicit so an
// adversary cannot construct an overflowing length that would
// confuse unpackState.
func packState(d StateData) ([]byte, error) {
	if len(d.State) > 0xFFFF || len(d.ReturnTo) > 0xFFFF {
		return nil, errors.New("bff: state payload field too long")
	}
	out := make([]byte, 0, 2+len(d.State)+2+len(d.ReturnTo)+8)
	var buf [8]byte
	binary.BigEndian.PutUint16(buf[:2], uint16(len(d.State))) //nolint:gosec // bounded above
	out = append(out, buf[:2]...)
	out = append(out, d.State...)
	binary.BigEndian.PutUint16(buf[:2], uint16(len(d.ReturnTo))) //nolint:gosec // bounded above
	out = append(out, buf[:2]...)
	out = append(out, d.ReturnTo...)
	binary.BigEndian.PutUint64(buf[:8], uint64(d.Expires.UnixNano())) //nolint:gosec // round-trips via int64 below
	out = append(out, buf[:8]...)
	return out, nil
}

func unpackState(b []byte) (StateData, error) {
	if len(b) < 2 {
		return StateData{}, ErrStateInvalid
	}
	sl := int(binary.BigEndian.Uint16(b[:2]))
	if len(b) < 2+sl+2 {
		return StateData{}, ErrStateInvalid
	}
	state := string(b[2 : 2+sl])
	rest := b[2+sl:]
	rl := int(binary.BigEndian.Uint16(rest[:2]))
	if len(rest) < 2+rl+8 {
		return StateData{}, ErrStateInvalid
	}
	returnTo := string(rest[2 : 2+rl])
	expNS := int64(binary.BigEndian.Uint64(rest[2+rl : 2+rl+8])) //nolint:gosec // signed round-trip
	return StateData{
		State:    state,
		ReturnTo: returnTo,
		Expires:  time.Unix(0, expNS),
	}, nil
}

// SetStateCookie writes the signed state cookie. SameSite is fixed
// to Lax — Strict would block the cookie on the redirect back from
// Keycloak (a top-level cross-site GET nav), which is the exact
// path the cookie exists to support. Path is scoped to /auth so
// the cookie never travels on application requests.
func SetStateCookie(w http.ResponseWriter, value string, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is per-environment by design
		Name:     StateCookieName,
		Value:    value,
		Path:     "/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(StateTTL.Seconds()),
	})
}

// ClearStateCookie emits a clear-cookie response for the state
// cookie. Path / Secure / SameSite MUST match SetStateCookie or
// the browser treats the response as a different cookie and leaves
// the original in place (the documented logout-bug source for the
// session cookie applies here too).
func ClearStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is per-environment by design
		Name:     StateCookieName,
		Value:    "",
		Path:     "/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
