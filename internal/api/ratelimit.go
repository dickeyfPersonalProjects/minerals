package api

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/ratelimit"
)

// RateLimitTier is one tier's budget: Requests calls per Window per
// key. A zero value disables that tier (the limiter fails closed).
type RateLimitTier struct {
	Requests int
	Window   time.Duration
}

// RateLimitOptions configures NewRateLimitMiddleware. The four tiers
// map to the request classes in mi-tnru:
//
//   - Auth: /auth/login, /auth/callback, /auth/logout, /api/v1/csrf.
//     Strict, ALWAYS keyed by client IP (brute-force is a per-source
//     concern; these endpoints are pre- or cross-session).
//   - File: GET on /api/v1/photos/*, /api/v1/files/*,
//     /api/v1/journal-files/* — the bandwidth/cost-amplification
//     surface. Keyed by user when authenticated, else IP.
//   - Write: POST/PUT/PATCH/DELETE on /api/v1/*. Keyed by user, else IP.
//   - Read: any other GET/HEAD on /api/v1/*. Generous. Keyed by user,
//     else IP.
//
// Requests outside /api/v1 and /auth (the SPA, /healthz, /readyz,
// /docs) are not limited here — coarse flood protection for those is
// the Cloudflare edge's job (see docs ops note).
type RateLimitOptions struct {
	Auth  RateLimitTier
	Read  RateLimitTier
	Write RateLimitTier
	File  RateLimitTier

	// IdleTTL is how long an idle per-key bucket survives before the
	// limiter evicts it (bounds memory under a scan of distinct keys).
	// Zero selects a sensible default.
	IdleTTL time.Duration

	// Now is the injected clock. Nil selects time.Now. Tests pass a
	// controllable clock so the limiter is exercised without sleeping.
	Now ratelimit.Clock
}

const defaultRateLimitIdleTTL = 10 * time.Minute

// tier identifies which limiter a request maps to, and how its key is
// derived.
type rlTier struct {
	limiter *ratelimit.Limiter
	// ipOnly forces IP keying even for authenticated requests (the
	// auth tier: brute-force defense is per-source).
	ipOnly bool
}

// NewRateLimitMiddleware returns a middleware enforcing the configured
// tiers. It MUST be mounted UNDER the session middleware so the
// authenticated user (auth.FromContext) is already attached — account
// keying depends on it. See server.go's chain wiring.
//
// Exceeding a tier's budget returns 429 with a Retry-After header and
// the §10 `rate_limited` envelope. Requests that match no tier pass
// through untouched.
func NewRateLimitMiddleware(opts RateLimitOptions) func(http.Handler) http.Handler {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	idle := opts.IdleTTL
	if idle <= 0 {
		idle = defaultRateLimitIdleTTL
	}
	mk := func(t RateLimitTier) *ratelimit.Limiter {
		return ratelimit.NewLimiter(t.Requests, t.Window, idle, now)
	}
	authL := mk(opts.Auth)
	readL := mk(opts.Read)
	writeL := mk(opts.Write)
	fileL := mk(opts.File)

	classify := func(r *http.Request) *rlTier {
		p := r.URL.Path
		switch {
		case isAuthPath(p):
			return &rlTier{limiter: authL, ipOnly: true}
		case strings.HasPrefix(p, "/api/v1/"):
			if isReadMethod(r.Method) {
				if isFilePath(p) {
					return &rlTier{limiter: fileL}
				}
				return &rlTier{limiter: readL}
			}
			return &rlTier{limiter: writeL}
		default:
			return nil
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := classify(r)
			if t == nil {
				next.ServeHTTP(w, r)
				return
			}
			key := rateLimitKey(r, t.ipOnly)
			allowed, retryAfter := t.limiter.Allow(key)
			if !allowed {
				secs := int(math.Ceil(retryAfter.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				writeError(w, http.StatusTooManyRequests,
					"rate_limited", "rate limit exceeded", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isReadMethod reports whether m is a safe/read method for tiering.
func isReadMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead
}

// isAuthPath matches the strict-tier auth + CSRF endpoints. Exact
// matches: these are fixed routes, not prefixes (so /api/v1/csrf does
// not sweep in unrelated /api/v1 paths).
func isAuthPath(p string) bool {
	switch p {
	case "/auth/login", "/auth/callback", "/auth/logout", "/api/v1/csrf":
		return true
	default:
		return false
	}
}

// isFilePath matches the binary-serving routes (the expensive,
// cost-amplification surface).
func isFilePath(p string) bool {
	return strings.HasPrefix(p, "/api/v1/photos/") ||
		strings.HasPrefix(p, "/api/v1/files/") ||
		strings.HasPrefix(p, "/api/v1/journal-files/")
}

// rateLimitKey derives the bucket key. When the request is
// authenticated and the tier is not IP-only, the key is the account
// id — so all of a user's sessions/tokens/devices share ONE bucket
// (the explicit operator requirement: using 3 tokens still draws from
// the single account allowance). Otherwise the key is the client IP.
func rateLimitKey(r *http.Request, ipOnly bool) string {
	if !ipOnly {
		if u := auth.FromContext(r.Context()); u.ID != uuid.Nil {
			return "user:" + u.ID.String()
		}
	}
	return "ip:" + clientIP(r)
}

// clientIP resolves the anonymous-keying source. Behind Cloudflare
// (with the origin locked to Cloudflare's IP ranges — mi-1d7q) the
// CF-Connecting-IP header is the real, non-spoofable client IP. Absent
// that header (local dev without Cloudflare) it falls back to the
// socket RemoteAddr. X-Forwarded-For is deliberately NOT trusted for
// limiting — it is attacker-controllable at the origin and is used
// only for log attribution elsewhere (§17).
func clientIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
