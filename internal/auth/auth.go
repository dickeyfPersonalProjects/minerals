// Package auth provides the User type, request-scoped user identity,
// and the auth middleware. Per CONTRACT.md §13 this is the single
// sanctioned entry point for reading the current user.
//
// mi-aw3a: the middleware validates Keycloak-issued bearer tokens
// against the realm JWKS endpoint (via internal/oidc) and populates
// User from the verified claims. When constructed with a nil
// TokenVerifier it falls back to the v1 stub identity — that path
// exists only for tests that do not exercise authentication.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// User identifies the caller behind a request. v1 has no real auth, so
// every request is the StubUser. Real auth (mi-aw3) replaces the stub
// middleware with one that populates these fields from verified JWT
// claims; the first-login resolver (mi-2hf) then maps Sub → DB row,
// upserts a pending user when none exists, and overwrites ID/Email
// from that row before the request reaches the handler.
type User struct {
	// ID is the application row UUIDv7 from the users table. The
	// stub middleware sets it to StubUser.ID before resolution; the
	// resolver overwrites it with the matched users.id.
	ID uuid.UUID
	// Sub is the Keycloak JWT `sub` claim — the stable, opaque
	// subject identifier used to look up the user row. Empty means
	// "no auth context yet"; both the stub and JWT middleware fill
	// it before downstream middleware runs.
	Sub string
	// Email is the JWT `email` claim (or the stub email pre-auth).
	// Carried so the resolver can persist it on first-login insert
	// without re-parsing claims.
	Email string
	// DisplayName mirrors users.display_name — nil when the user
	// has not yet completed first-login setup.
	DisplayName *string
	// Pending is true when the user row exists but
	// status='pending'. The profile gate returns 403+redirect for
	// protected endpoints while this flag is set; the setup
	// endpoint itself is exempt.
	Pending bool
	// Roles is the caller's set of Keycloak realm roles, taken
	// verbatim from the JWT `realm_access.roles` claim. The authz
	// enforcer (mi-aw3b) evaluates each role independently. Empty
	// for the stub identity — the stub path predates RBAC.
	Roles []string
}

// TokenVerifier validates a raw bearer token and returns the subset
// of claims the app cares about. *oidc.Verifier satisfies it; tests
// inject a fake. A nil TokenVerifier passed to Auth / the huma auth
// middleware selects the v1 stub-identity fallback.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (*oidc.Claims, error)
}

// UserFromClaims builds the request-scoped User from verified JWT
// claims. ID is left as the Keycloak `sub` parsed into a UUID when
// it is UUID-shaped (Keycloak subjects are UUIDs); the resolver
// middleware (mi-2hf) overwrites ID with the application users.id
// before the request reaches a handler. A non-UUID sub leaves ID
// nil — RequireUser still admits the request via Sub, and the
// resolver fills ID from the DB row.
func UserFromClaims(c *oidc.Claims) User {
	u := User{Sub: c.Subject, Email: c.Email, Roles: c.Roles}
	if id, err := uuid.Parse(c.Subject); err == nil {
		u.ID = id
	}
	return u
}

// BearerToken extracts the token from an `Authorization: Bearer
// <token>` header. The bool is false when the header is absent or
// not a well-formed Bearer credential. The scheme match is
// case-insensitive per RFC 7235.
func BearerToken(header string) (string, bool) {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// StubUserSub is the keycloak_sub seeded by migration 0008 for the
// v1 placeholder identity. Resolving this sub returns the
// pre-seeded overseer row with status='active', so the stub auth
// path naturally bypasses the first-login gate.
const StubUserSub = "stub-overseer"

// StubUser is the fixed v1 identity used by the Auth middleware. The
// UUID does NOT follow the UUIDv7 rule (per §13) — it's a constant
// sentinel, recognizable in logs and dumps. The Sub matches the
// seeded users row so the resolver lifts the resolved User into
// context exactly the way real auth will.
var StubUser = User{
	ID:    uuid.MustParse("00000000-0000-0000-0000-000000000001"),
	Sub:   StubUserSub,
	Email: "overseer@minerals.local",
}

type ctxKey int

const (
	userKey ctxKey = iota
	requestIDKey
)

// WithUser attaches u to ctx for retrieval via FromContext.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// FromContext returns the current request's User. A zero-valued User
// is returned if no user is set (callers running through the
// Auth + RequireUser middleware chain will always get a populated
// value).
func FromContext(ctx context.Context) User {
	u, _ := ctx.Value(userKey).(User)
	return u
}

// WithRequestID attaches a per-request id (typically a ULID) to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the per-request id set by the request-id
// middleware, or an empty string if none has been set.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Auth returns the net/http auth middleware. With a real
// TokenVerifier it extracts the bearer token, validates it against
// the Keycloak JWKS, and populates User from the verified claims;
// a missing or invalid token short-circuits with a 401 envelope.
// With a nil verifier it falls back to populating the v1 StubUser
// (test-only path).
//
// This middleware guards the /api/v1 catch-all only — every real
// operation runs the huma-side chain in internal/api. The split
// mirrors CONTRACT.md §13's two-middleware design.
func Auth(v TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v == nil {
				ctx := WithUser(r.Context(), StubUser)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			tok, ok := BearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeUnauthorized(w)
				return
			}
			claims, err := v.Verify(r.Context(), tok)
			if err != nil {
				writeUnauthorized(w)
				return
			}
			ctx := WithUser(r.Context(), UserFromClaims(claims))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireUser returns 401 when no User is in the request context. A
// User is considered present when it carries either an application
// ID or a JWT subject — the latter covers the window after Auth has
// verified the token but before the resolver has mapped the subject
// to a users.id row.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := FromContext(r.Context())
		if u.ID == uuid.Nil && u.Sub == "" {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeUnauthorized emits the §10 error envelope.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	body := map[string]any{
		"error": map[string]any{
			"code":    "unauthorized",
			"message": "authentication required",
		},
	}
	_ = json.NewEncoder(w).Encode(body)
}
