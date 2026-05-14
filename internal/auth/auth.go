// Package auth provides the User type, request-scoped user identity,
// and the v1 stub auth middleware. Per CONTRACT.md §13 this is the
// single sanctioned entry point for reading the current user.
package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
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

// Auth is the v1 stub auth middleware. It always populates StubUser
// in the request context. Real-auth replacement (deferred) validates
// credentials and populates the resolved User instead.
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithUser(r.Context(), StubUser)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireUser returns 401 when no User is in the request context. v1
// passes through (Auth always populates); the middleware exists so
// real-auth replacement is mechanical.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := FromContext(r.Context())
		if u.ID == uuid.Nil {
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
