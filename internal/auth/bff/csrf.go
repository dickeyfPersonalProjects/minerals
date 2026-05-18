package bff

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// CSRFHeaderName is the request header the stored-synchronizer CSRF
// check reads on every non-safe method. SPA contract: see
// docs/design/auth-bff.md §csrf §spa-integration. Tokens travel ONLY
// in this header — never as a query parameter — so they do not land
// in logs, browser history, or Referer.
const CSRFHeaderName = "X-CSRF-Token"

// CSRFMiddleware enforces the stored-synchronizer CSRF check on every
// non-safe HTTP method when a session is attached. Composes UNDER
// SessionMiddleware; the mandatory chain order is:
//
//	SessionMiddleware → CSRFMiddleware → handler
//
// Three bypass branches exist by design (docs/design/auth-bff.md §csrf):
//   - Safe methods (GET/HEAD/OPTIONS) bypass — idempotent reads do not
//     mutate state, and the GET /api/v1/csrf endpoint must remain
//     reachable so the SPA can fetch its first token.
//   - Anonymous requests bypass — there is no session-bound token to
//     compare against. The handler decides whether to require auth.
//   - Authenticated unsafe requests are checked.
//
// Token shape: header value is base64url(no-padding) of the 32-byte
// session.CSRFToken. Comparison is constant-time via
// subtle.ConstantTimeCompare so a per-byte timing side channel
// cannot leak the token under repeated probing.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		sess, ok := SessionFromContext(r.Context())
		if !ok {
			// Anonymous — no session-bound token to defend with.
			// The handler will reject if it requires auth.
			next.ServeHTTP(w, r)
			return
		}

		header := r.Header.Get(CSRFHeaderName)
		if header == "" {
			writeCSRFError(w, http.StatusForbidden, "csrf_missing", "CSRF token required")
			return
		}
		expected := base64.RawURLEncoding.EncodeToString(sess.CSRFToken[:])
		// Length-prefix via ConstantTimeEq guards against a tampered
		// header of a different length triggering a fast-path mismatch
		// inside ConstantTimeCompare (it returns 0 immediately on
		// length mismatch, leaking length); the explicit equal-length
		// path keeps the comparison branchless on the wire-shape the
		// SPA actually sends. The codes the SPA distinguishes
		// (csrf_missing vs csrf_mismatch) collapse a wrong-length
		// header into csrf_mismatch — it is a mismatch, just an
		// obvious one.
		if subtle.ConstantTimeCompare([]byte(header), []byte(expected)) != 1 {
			writeCSRFError(w, http.StatusForbidden, "csrf_mismatch", "CSRF token does not match")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFHandler is the GET /api/v1/csrf endpoint. It returns the
// current session's CSRF token to the SPA. Composition requirement:
// MUST be mounted behind SessionMiddleware so an unauthenticated
// caller receives 401 — minting a token cross-site without an
// existing session would defeat the synchronizer (design §csrf
// §subtle-choices "CSRF endpoint requires auth").
//
// Cache-Control: no-store keeps proxies and the browser cache from
// holding the token long enough for a stale-tab to send a token
// belonging to a previous session.
//
// Body shape: {"token": "<base64url>"}. The encoding matches what
// CSRFMiddleware compares against; if either side changes encoding
// the chain breaks on every POST.
func CSRFHandler(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		writeCSRFError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(sess.CSRFToken[:])
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}

// writeCSRFError emits a CONTRACT §10 error envelope. Kept local to
// the bff package — the auth boundary must not depend on internal/api
// so the future auth-microservice extraction stays a clean cut
// (docs/design/auth-bff.md §microservice-extraction).
func writeCSRFError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	})
}
