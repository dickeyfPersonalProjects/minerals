package bff

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// DefaultRefreshLeeway is how early before access-token expiry the
// session middleware refreshes against Keycloak. The leeway absorbs
// clock drift between Keycloak and the backend so the next downstream
// call into Keycloak never trips a "token just expired" rejection.
// (docs/design/auth-bff.md §session-middleware §refresh-leeway)
const DefaultRefreshLeeway = 30 * time.Second

// DefaultTouchInterval is the debounce window for last_used_at writes.
// The middleware skips Touch when last_used_at moved within the
// window — avoids a UPDATE on every request while still keeping the
// idle-timeout signal current to the granularity we care about
// (docs/design/auth-bff.md §session-middleware §last-used-debounce).
const DefaultTouchInterval = 30 * time.Second

// UserLookup is the narrow user-row reader the session middleware
// uses to populate auth.User from a session row. Kept as its own
// interface rather than reaching for the full domain.UserRepo so the
// middleware's dependency surface stays minimal — the future
// microservice extraction (docs/design/auth-bff.md
// §microservice-extraction) can swap an RPC client in without
// dragging Create / MarkActive / UpdateFieldDefaults along.
type UserLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// MiddlewareDeps bundles the runtime dependencies of SessionMiddleware.
// Sessions / OAuth / Users / IdleTimeout / CookieConfig.Path are
// required; the constructor panics on missing values because a
// misconfigured middleware would silently anonymise every request,
// and a louder failure at boot is the right tradeoff for a
// security-critical chain element.
type MiddlewareDeps struct {
	// Sessions resolves session rows. The middleware never touches a
	// concrete Postgres repo — only this interface — which is what
	// makes the future cache decorator / auth-service RPC swap a
	// boundary change rather than an interior rewrite.
	Sessions SessionResolver

	// OAuth refreshes the access token when it nears expiry. The
	// middleware does not exchange or validate tokens — those are
	// session-creation concerns owned by the /auth/callback handler.
	OAuth OAuthClient

	// Users loads the user row referenced by a session so the
	// resulting auth.User carries Email / DisplayName / Pending. See
	// the package comment on UserLookup for why this is its own
	// interface rather than domain.UserRepo.
	//
	// Roles loading: per the design ('no cache day one') we take the
	// second-lookup hit on every request rather than caching roles
	// on the session row. The current users schema does not carry a
	// roles column — a follow-up bead adds it — so auth.User.Roles
	// is left empty here. The shape of the call stays correct so the
	// future change is field-population only, not a chain rework.
	// DO NOT 'optimise' this lookup away without revisiting that
	// decision in docs/design/auth-bff.md §caching-strategy.
	Users UserLookup

	// CookieConfig is the per-environment Path / Secure / SameSite /
	// MaxAge for the session cookie. Required so the middleware can
	// emit ClearSessionCookie with attributes that match the original
	// SetSessionCookie — mismatched attributes cause the browser to
	// treat the clear as a different cookie and the original
	// survives (the #1 cookie-auth logout bug, per the design doc).
	CookieConfig CookieConfig

	// IdleTimeout is the gap since last_used_at after which a
	// session is considered idle and revoked. Required (zero would
	// revoke every session on first request).
	IdleTimeout time.Duration

	// RefreshLeeway overrides DefaultRefreshLeeway. Zero falls back
	// to the default.
	RefreshLeeway time.Duration

	// TouchInterval overrides DefaultTouchInterval. Zero falls back
	// to the default.
	TouchInterval time.Duration

	// Now is the wall-clock source. Zero (nil) falls back to
	// time.Now. Tests inject a fixed clock to exercise the liveness,
	// refresh, and touch-debounce branches deterministically — no
	// time.Sleep in any test.
	Now func() time.Time
}

// sessionLookupDuration histograms the SessionResolver.GetByID cost
// per request. Today every observation carries result="miss" because
// the resolver always hits Postgres; when a cache decorator lands
// (docs/design/auth-bff.md §caching-strategy) "hit" will start firing
// and a before/after comparison is one PromQL query. The "error"
// label is reserved by the design for a future split — today errors
// are observed as "miss" so the histogram count matches the request
// count.
var sessionLookupDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "minerals_session_lookup_duration_seconds",
	Help:    "Duration of session lookup by the BFF session middleware.",
	Buckets: []float64{.0001, .0005, .001, .005, .01, .05, .1, .5, 1},
}, []string{"result"})

// sessionRefreshDuration histograms OAuth refresh attempts triggered
// by the session middleware. outcome="ok" on success,
// outcome="failed" on the Keycloak error path (session is revoked
// after the failed observation lands).
var sessionRefreshDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "minerals_session_refresh_duration_seconds",
	Help:    "Duration of OAuth token refresh during session middleware.",
	Buckets: []float64{.01, .05, .1, .25, .5, 1, 2, 5},
}, []string{"outcome"})

// SessionMiddleware returns the http middleware that resolves the
// session cookie, refreshes the access token when due, debounces
// last_used_at, and attaches the user to the request context. The
// returned middleware is safe for concurrent use; the per-session
// refresh mutex (sync.Map) ensures concurrent requests against the
// same expiring session trigger exactly one OAuth.Refresh call —
// Keycloak's refresh-token rotation treats a duplicate refresh as
// replay and revokes the entire token family, so the stampede
// defense is correctness, not just an optimisation.
//
// The middleware never returns 401 on a missing or bad cookie — it
// falls through anonymously. Handlers / downstream middleware
// (auth.RequireUser) decide whether the route requires a populated
// user. This is the design's "anonymous-permitted endpoints still
// work" invariant (docs/design/auth-bff.md §session-middleware §key-behaviors).
func SessionMiddleware(deps MiddlewareDeps) func(http.Handler) http.Handler {
	if deps.Sessions == nil {
		panic("bff: SessionMiddleware: Sessions is required")
	}
	if deps.OAuth == nil {
		panic("bff: SessionMiddleware: OAuth is required")
	}
	if deps.Users == nil {
		panic("bff: SessionMiddleware: Users is required")
	}
	if deps.IdleTimeout <= 0 {
		panic("bff: SessionMiddleware: IdleTimeout must be > 0")
	}
	// mustPath panics on empty Path; do it here so a misconfigured
	// chain fails at construction rather than on the first bad cookie.
	_ = mustPath(deps.CookieConfig.Path)

	refreshLeeway := deps.RefreshLeeway
	if refreshLeeway <= 0 {
		refreshLeeway = DefaultRefreshLeeway
	}
	touchInterval := deps.TouchInterval
	if touchInterval <= 0 {
		touchInterval = DefaultTouchInterval
	}
	nowFn := deps.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	cookieCfg := deps.CookieConfig

	// refreshMuxes serialises concurrent refresh attempts on the
	// same session. The sync.Map grows by one entry per distinct
	// session ID that ever hits the refresh path. Bounded by the
	// number of live sessions for the process lifetime, which is the
	// design's documented acceptable cost for "start single-replica;
	// switch to advisory lock when scaling out".
	var refreshMuxes sync.Map // map[[32]byte]*sync.Mutex

	getMu := func(id [32]byte) *sync.Mutex {
		if v, ok := refreshMuxes.Load(id); ok {
			return v.(*sync.Mutex)
		}
		fresh := &sync.Mutex{}
		actual, _ := refreshMuxes.LoadOrStore(id, fresh)
		return actual.(*sync.Mutex)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			cookie, err := r.Cookie(SessionCookieName)
			if err != nil {
				// No session cookie — anonymous. Handlers decide
				// whether that's acceptable for the route.
				next.ServeHTTP(w, r)
				return
			}

			raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
			if err != nil || len(raw) != 32 {
				// Malformed cookie. Clear it so the browser stops
				// re-sending and proceed anonymously.
				ClearSessionCookie(w, cookieCfg)
				next.ServeHTTP(w, r)
				return
			}
			var id [32]byte
			copy(id[:], raw)

			timer := prometheus.NewTimer(sessionLookupDuration.WithLabelValues("miss"))
			sess, err := deps.Sessions.GetByID(ctx, id)
			timer.ObserveDuration()
			if err != nil {
				if errors.Is(err, ErrSessionNotFound) {
					ClearSessionCookie(w, cookieCfg)
					next.ServeHTTP(w, r)
					return
				}
				slog.ErrorContext(ctx, "bff: session lookup failed", "err", err)
				writeInternalError(w, "session lookup failed")
				return
			}

			now := nowFn()
			if !sessionLive(sess, now, deps.IdleTimeout) {
				_ = deps.Sessions.Revoke(ctx, sess.ID)
				ClearSessionCookie(w, cookieCfg)
				next.ServeHTTP(w, r)
				return
			}

			if now.After(sess.AccessTokenExpiresAt.Add(-refreshLeeway)) {
				updated, ok := refreshSession(ctx, w, deps, cookieCfg, getMu(sess.ID), sess, nowFn, refreshLeeway)
				if !ok {
					// refreshSession already handled the response —
					// either revoked + cleared cookie + fell through
					// anonymously, or wrote a 500 envelope.
					return
				}
				sess = updated
			}

			if now.Sub(sess.LastUsedAt) > touchInterval {
				if terr := deps.Sessions.Touch(ctx, sess.ID, now); terr != nil {
					// Touch failure is non-fatal — the row is still
					// usable; we just lose this update's
					// freshness. Log and proceed.
					slog.WarnContext(ctx, "bff: session touch failed", "err", terr)
				}
			}

			user, err := deps.Users.GetByID(ctx, sess.UserID)
			if err != nil {
				slog.ErrorContext(ctx, "bff: load user for session", "err", err, "user_id", sess.UserID)
				writeInternalError(w, "user lookup failed")
				return
			}

			authUser := auth.User{
				ID:          user.ID,
				Sub:         sess.UserSub,
				Email:       user.Email,
				DisplayName: user.DisplayName,
				Pending:     user.Status == domain.UserStatusPending,
				// Roles: see MiddlewareDeps.Users docstring — left
				// empty until a roles column lands on the users
				// schema (or on the session row); the call shape is
				// correct so that change is field-population only.
			}

			ctx = auth.WithUser(ctx, authUser)
			ctx = WithSession(ctx, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// sessionLive evaluates the three middleware-owned liveness checks:
// soft-delete (revoked_at), absolute cap (absolute_expires_at), and
// idle (now - last_used_at > IdleTimeout). Pulled out so the policy
// is read at one glance — and so the tests can assert each branch
// without re-implementing the comparison.
func sessionLive(s Session, now time.Time, idleTimeout time.Duration) bool {
	if s.RevokedAt != nil {
		return false
	}
	if now.After(s.AbsoluteExpiresAt) {
		return false
	}
	if now.Sub(s.LastUsedAt) > idleTimeout {
		return false
	}
	return true
}

// refreshSession serialises the refresh of one session via the
// caller-supplied per-session mutex, re-reads the row after acquiring
// the lock (so the second-in-line request observes the first's
// refresh and skips its own), and persists the rotated tokens.
//
// Returns (session, true) on success — the caller continues with the
// updated row. Returns (_, false) when refreshSession has fully
// handled the response — refresh failure causes Revoke +
// ClearSessionCookie + fall-through-anonymous, persistence failure
// emits a 500 envelope.
func refreshSession(
	ctx context.Context,
	w http.ResponseWriter,
	deps MiddlewareDeps,
	cookieCfg CookieConfig,
	mu *sync.Mutex,
	sess Session,
	nowFn func() time.Time,
	refreshLeeway time.Duration,
) (Session, bool) {
	mu.Lock()
	defer mu.Unlock()

	// Re-read inside the critical section: a concurrent request may
	// have refreshed while we waited for the lock. Without this
	// check both waiters would call OAuth.Refresh and the second
	// one would replay an already-rotated refresh_token, which
	// Keycloak's token-family detection treats as compromise and
	// revokes everything.
	fresh, err := deps.Sessions.GetByID(ctx, sess.ID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			// The first refresher's failure path revoked the
			// session; soft-delete is still a row read, so this
			// branch only fires if Cleaner physically removed the
			// row between releases. Treat as session-gone.
			ClearSessionCookie(w, cookieCfg)
			return Session{}, false
		}
		slog.ErrorContext(ctx, "bff: re-read for refresh failed", "err", err)
		writeInternalError(w, "session lookup failed")
		return Session{}, false
	}
	if !nowFn().After(fresh.AccessTokenExpiresAt.Add(-refreshLeeway)) {
		// Another request already refreshed. Skip the OAuth round-trip.
		return fresh, true
	}

	start := nowFn()
	refreshed, rerr := deps.OAuth.Refresh(ctx, fresh.RefreshToken)
	elapsed := nowFn().Sub(start).Seconds()
	if rerr != nil {
		sessionRefreshDuration.WithLabelValues("failed").Observe(elapsed)
		// Refresh failed — the refresh_token is dead (rotated by
		// Keycloak, expired, or token-family revoked). Revoke our
		// row so a stale cookie doesn't keep poking Keycloak, and
		// fall through anonymously.
		_ = deps.Sessions.Revoke(ctx, fresh.ID)
		ClearSessionCookie(w, cookieCfg)
		return Session{}, false
	}
	sessionRefreshDuration.WithLabelValues("ok").Observe(elapsed)

	updated, uerr := deps.Sessions.UpdateTokens(ctx, fresh.ID, TokenSet(refreshed))
	if uerr != nil {
		// We've already burned the rotated refresh_token at Keycloak
		// but can't persist the new one. The session is now
		// out-of-sync; revoking is the only honest move — the next
		// request would fail anyway with the stale (now-invalid)
		// refresh_token.
		slog.ErrorContext(ctx, "bff: persist refreshed tokens failed", "err", uerr)
		_ = deps.Sessions.Revoke(ctx, fresh.ID)
		writeInternalError(w, "session refresh failed")
		return Session{}, false
	}
	return updated, true
}

// writeInternalError emits a CONTRACT §10 error envelope at 500. The
// bff package writes its own rather than depending on internal/api
// to keep the auth boundary clean (docs/design/auth-bff.md
// §microservice-extraction).
func writeInternalError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "internal_error",
			"message": msg,
		},
	})
}

// sessionCtxKey is the unexported key type for the session pointer
// stashed in the request context. Unexported type means no other
// package can collide with us — the only access path is
// WithSession / SessionFromContext.
type sessionCtxKey struct{}

// WithSession returns a copy of ctx that carries sess. The CSRF
// middleware (mi-gbzs / #5) reads it via SessionFromContext to
// constant-time-compare the X-CSRF-Token header against
// sess.CSRFToken; future audit / admin paths use it for the
// per-session row reference without a second DB lookup.
func WithSession(ctx context.Context, sess Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sess)
}

// SessionFromContext returns the session attached by SessionMiddleware,
// or (_, false) when the request did not carry a valid session cookie.
// Anonymous-permitted handlers MUST tolerate the false return.
func SessionFromContext(ctx context.Context) (Session, bool) {
	sess, ok := ctx.Value(sessionCtxKey{}).(Session)
	return sess, ok
}
