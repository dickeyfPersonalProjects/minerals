package bff

import (
	"encoding/base64"
	"net/http"
	"time"
)

// SessionCookieName is the wire name of the BFF session cookie
// (docs/design/auth-bff.md §cookie-attributes). Exported so handlers
// and tests reference one constant rather than re-typing the string.
const SessionCookieName = "minerals_session"

// CookieConfig captures the per-environment cookie attributes the
// caller must supply. The other attributes (HttpOnly, the
// Name itself) are not configurable — they are security-critical
// invariants the helpers enforce.
//
// Path and Domain MUST match between the set and clear calls or the
// browser treats them as distinct cookies and the original is never
// cleared — this is the #1 logout bug in cookie auth per the design
// doc. The helpers below take a single CookieConfig so set/clear can
// never drift apart.
type CookieConfig struct {
	// Path is the cookie's Path attribute. Default and only sane
	// value is "/" — anything narrower triggers the prefix-matching
	// gotcha (`Path=/api` matches `/apiv2`). Required; empty
	// triggers a panic — the design forbids guessing.
	Path string

	// Secure flips the Secure attribute. True in prod/staging,
	// false in the dev compose stack (plain-HTTP localhost). The
	// decision is per-environment (config flag), not per-request:
	// don't infer from X-Forwarded-Proto.
	Secure bool

	// SameSite is the SameSite attribute. The design mandates Lax,
	// but the field is configurable so a future signing-redirect
	// flow can opt into Strict on its own cookie if needed; the
	// caller stays in control.
	SameSite http.SameSite

	// MaxAge is the cookie's Max-Age — how long the BROWSER keeps
	// the cookie. Server-side session lifetime
	// (absolute_expires_at) is shorter, so the cookie outlives the
	// session row and the expired-session response can cleanly
	// clear the cookie. Zero MaxAge means "session cookie"
	// (browser-lifetime); the design uses a configured value
	// (default 14 days).
	MaxAge time.Duration
}

// SetSessionCookie writes the session cookie for sessionID to w. The
// value is base64url(no-padding) of the 32-byte id (43 chars), which
// is the encoding the middleware decodes (design doc
// §session-middleware §1).
//
// HttpOnly is set unconditionally — `document.cookie` MUST NOT see
// the credential. The other attributes come from cfg; per the design
// doc, Domain is omitted (host-only) and Partitioned is never set.
func SetSessionCookie(w http.ResponseWriter, sessionID [32]byte, cfg CookieConfig) {
	// gosec G124 cannot tell statically that Secure is intentionally
	// per-environment (true in prod/staging, false in dev) — the
	// callsite, not this helper, owns the security-relevant choice.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     SessionCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(sessionID[:]),
		Path:     mustPath(cfg.Path),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
		MaxAge:   int(cfg.MaxAge.Seconds()),
	})
}

// ClearSessionCookie emits a clear-cookie response: empty value,
// MaxAge=-1 (expire immediately). Path / Domain / Secure / SameSite
// MUST match the original Set so the browser recognises the same
// cookie — taking the same CookieConfig as Set guarantees that by
// construction.
//
// Logout, session-not-found, invalid-cookie, and any other path that
// wants to evict the session cookie MUST go through this helper.
// Hand-rolled clears are the documented logout-bug source.
func ClearSessionCookie(w http.ResponseWriter, cfg CookieConfig) {
	// gosec G124: see SetSessionCookie — Secure is per-environment
	// by design.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     SessionCookieName,
		Value:    "",
		Path:     mustPath(cfg.Path),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
		MaxAge:   -1,
	})
}

// mustPath panics on an empty path. Defaulting silently here would
// hide the most common drift between Set and Clear callsites; a
// loud failure at construction time is preferable.
func mustPath(p string) string {
	if p == "" {
		panic("bff: CookieConfig.Path is required (must match between Set and Clear)")
	}
	return p
}
