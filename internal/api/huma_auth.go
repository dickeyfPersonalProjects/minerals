package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// makeHumaAuth builds the huma-side analogue of auth.Auth +
// auth.RequireUser (per CONTRACT.md §13). With a real TokenVerifier
// it extracts the bearer token, validates it against the Keycloak
// JWKS, and either populates User from the verified claims or aborts
// with a 401 envelope. With a nil verifier it falls back to
// populating StubUser — the test-only path for suites that do not
// exercise authentication.
//
// Apply via Operation.Middlewares for protected operations — system
// endpoints (healthz, readyz, openapi, docs, runtime-config) are
// public and MUST NOT carry this middleware. Read-side operations
// on visibility-scoped resources use makeHumaOptionalAuth instead
// (CONTRACT.md §13 v2).
func makeHumaAuth(v auth.TokenVerifier) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if v == nil {
			next(huma.WithContext(ctx, auth.WithUser(ctx.Context(), auth.StubUser)))
			return
		}
		tok, ok := auth.BearerToken(ctx.Header("Authorization"))
		if !ok {
			writeHumaError(ctx, http.StatusUnauthorized,
				"unauthorized", "authentication required")
			return
		}
		claims, err := v.Verify(ctx.Context(), tok)
		if err != nil {
			slog.WarnContext(ctx.Context(), "auth: token verification failed", "err", err)
			writeHumaError(ctx, http.StatusUnauthorized,
				"unauthorized", "invalid or expired token")
			return
		}
		next(huma.WithContext(ctx, auth.WithUser(ctx.Context(), auth.UserFromClaims(claims))))
	}
}

// makeHumaOptionalAuth is the anonymous-friendly analogue of
// makeHumaAuth for read-side endpoints on visibility-scoped
// resources (CONTRACT.md §13 v2). A missing Authorization header
// does NOT 401 — the request continues with no User in context, and
// downstream handlers (or the DB-level visibility scoping) decide
// what an anonymous caller may see. An invalid token still 401s
// (the caller deliberately presented a credential and verification
// failed).
func makeHumaOptionalAuth(v auth.TokenVerifier) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if v == nil {
			next(huma.WithContext(ctx, auth.WithUser(ctx.Context(), auth.StubUser)))
			return
		}
		tok, ok := auth.BearerToken(ctx.Header("Authorization"))
		if !ok {
			next(ctx)
			return
		}
		claims, err := v.Verify(ctx.Context(), tok)
		if err != nil {
			slog.WarnContext(ctx.Context(), "auth: token verification failed", "err", err)
			writeHumaError(ctx, http.StatusUnauthorized,
				"unauthorized", "invalid or expired token")
			return
		}
		next(huma.WithContext(ctx, auth.WithUser(ctx.Context(), auth.UserFromClaims(claims))))
	}
}

// ProfileSetupPath is the SPA route the profile gate hands the
// frontend when an authenticated-but-pending user hits a protected
// API. Both the backend gate and the frontend redirect handler
// agree on this string.
const ProfileSetupPath = "/profile/setup"

// authMiddlewares is the per-operation middleware chain applied to
// every protected operation. The chain is built once in api.New()
// from the configured UserRepo, then handed to every
// register*Operations helper.
//
// Chain order:
//  1. humaAuth         — populate User (stub or JWT-derived)
//  2. resolveUser      — look up users row by Sub; insert pending
//     row on first-login; overwrite ID/Pending in
//     ctx with the persisted values
//  3. requireComplete  — return 403 + redirect when Pending
//
// The profile setup endpoint omits step 3 so a pending user can
// reach it; see profile.go.
type authMiddlewares struct {
	humaAuth         func(huma.Context, func(huma.Context))
	humaOptionalAuth func(huma.Context, func(huma.Context))
	resolveUser      func(huma.Context, func(huma.Context))
	// optionalResolveUser no-ops on anonymous callers (no Sub in
	// context) and otherwise behaves like resolveUser. Used by the
	// Optional() chain on read-side endpoints (CONTRACT.md §13 v2).
	optionalResolveUser func(huma.Context, func(huma.Context))
	requireComplete     func(huma.Context, func(huma.Context))
	// verifier is the same TokenVerifier humaAuth closes over,
	// retained so the net/http download routes (photos, journal
	// files) can build the auth.Auth chain with the identical
	// verification behavior. nil selects the stub fallback.
	verifier auth.TokenVerifier
}

// Protected returns the full chain — auth, resolve, gate — used by
// every protected operation. Order is significant: auth must run
// before resolve, resolve before requireComplete.
func (m authMiddlewares) Protected() huma.Middlewares {
	if m.resolveUser == nil {
		// No UserRepo wired (tests that don't exercise auth).
		return huma.Middlewares{m.humaAuth}
	}
	return huma.Middlewares{m.humaAuth, m.resolveUser, m.requireComplete}
}

// Optional returns the anonymous-friendly chain used by GET list
// and detail endpoints on visibility-scoped resources (CONTRACT.md
// §13 v2). The chain populates auth.User when a valid token is
// present and leaves the context anonymous otherwise — handlers
// MUST NOT 401 on a missing user; the DB scoping layer (or, for
// detail endpoints, a 404-on-deny check in the handler) does the
// filtering. An invalid token still 401s.
func (m authMiddlewares) Optional() huma.Middlewares {
	if m.optionalResolveUser == nil {
		return huma.Middlewares{m.humaOptionalAuth}
	}
	return huma.Middlewares{m.humaOptionalAuth, m.optionalResolveUser, optionalRequireCompleteProfile}
}

// SetupAllowed returns the abbreviated chain used by the
// /api/v1/profile setup endpoint — auth and resolve run, but the
// gate is skipped so a pending user can complete their profile.
func (m authMiddlewares) SetupAllowed() huma.Middlewares {
	if m.resolveUser == nil {
		return huma.Middlewares{m.humaAuth}
	}
	return huma.Middlewares{m.humaAuth, m.resolveUser}
}

// newAuthMiddlewares builds the per-request chain bound to repo and
// verifier. When repo is nil the chain collapses to just the auth
// step (no resolver, no profile gate) — the path for tests that
// don't wire a UserRepo. verifier is threaded into the auth step:
// nil selects the stub-identity fallback, a real verifier enables
// bearer-token validation.
func newAuthMiddlewares(repo domain.UserRepo, verifier auth.TokenVerifier) authMiddlewares {
	humaAuth := makeHumaAuth(verifier)
	humaOptionalAuth := makeHumaOptionalAuth(verifier)
	if repo == nil {
		return authMiddlewares{
			humaAuth:         humaAuth,
			humaOptionalAuth: humaOptionalAuth,
			verifier:         verifier,
		}
	}
	resolve := makeResolveUserMiddleware(repo)
	return authMiddlewares{
		humaAuth:            humaAuth,
		humaOptionalAuth:    humaOptionalAuth,
		resolveUser:         resolve,
		optionalResolveUser: makeOptionalResolveUserMiddleware(resolve),
		requireComplete:     requireCompleteProfile,
		verifier:            verifier,
	}
}

// makeOptionalResolveUserMiddleware wraps the standard resolver so
// it no-ops on anonymous callers (no Sub in context) while behaving
// identically for authenticated ones. The Optional() chain uses
// this so a missing token reaches the handler with no User —
// resolveUser would otherwise 401 the request.
func makeOptionalResolveUserMiddleware(
	resolve func(huma.Context, func(huma.Context)),
) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		u := auth.FromContext(ctx.Context())
		if u.Sub == "" {
			next(ctx)
			return
		}
		resolve(ctx, next)
	}
}

// optionalRequireCompleteProfile is the anonymous-friendly variant
// of requireCompleteProfile: anonymous callers (no Sub) skip the
// gate entirely; authenticated-but-pending users still 403 with the
// SPA redirect path. Used by the Optional() chain.
func optionalRequireCompleteProfile(ctx huma.Context, next func(huma.Context)) {
	u := auth.FromContext(ctx.Context())
	if u.Sub == "" {
		next(ctx)
		return
	}
	requireCompleteProfile(ctx, next)
}

// makeResolveUserMiddleware closes over repo and returns the
// middleware that maps the JWT-derived sub onto a users row. On
// first-login it inserts a pending row; on subsequent requests it
// just reads the existing one. The resolved row's ID and pending
// flag are written back into the request's auth.User.
func makeResolveUserMiddleware(repo domain.UserRepo) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		u := auth.FromContext(ctx.Context())
		if u.Sub == "" {
			// No sub means humaAuth didn't populate the user
			// (misconfiguration or test path). Surface as 401 so
			// downstream handlers don't see a half-built context.
			writeHumaError(ctx, http.StatusUnauthorized,
				"unauthorized", "authentication required")
			return
		}

		resolved, err := resolveOrCreateUser(ctx.Context(), repo, u)
		if err != nil {
			slog.ErrorContext(ctx.Context(), "auth: resolve user failed",
				"sub", u.Sub, "err", err)
			writeHumaError(ctx, http.StatusInternalServerError,
				"internal_error", "failed to resolve user")
			return
		}

		merged := u
		merged.ID = resolved.ID
		merged.Email = resolved.Email
		merged.DisplayName = resolved.DisplayName
		merged.Pending = resolved.Status == domain.UserStatusPending

		next(huma.WithContext(ctx, auth.WithUser(ctx.Context(), merged)))
	}
}

// requireCompleteProfile is the gate: it returns 403 + the SPA
// redirect path when the resolved user is still pending. The
// /api/v1/profile endpoint registers without this middleware so a
// pending user can complete setup.
func requireCompleteProfile(ctx huma.Context, next func(huma.Context)) {
	u := auth.FromContext(ctx.Context())
	if u.Pending {
		writeHumaErrorDetails(ctx, http.StatusForbidden,
			"profile_setup_required",
			"profile setup required",
			map[string]any{"redirect": ProfileSetupPath})
		return
	}
	next(ctx)
}

// resolveOrCreateUser is the resolver's idempotent core: look up the
// row by sub, insert a pending row when missing, and re-read on the
// rare race where two concurrent first-login requests both miss.
// The re-read keeps the resolver linearizable without holding a
// transaction across the entire middleware chain.
func resolveOrCreateUser(
	ctx context.Context, repo domain.UserRepo, u auth.User,
) (domain.User, error) {
	row, err := repo.GetBySub(ctx, u.Sub)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, domain.ErrUserNotFound) {
		return domain.User{}, err
	}

	now := time.Now().UTC()
	fresh := domain.User{
		ID:          domain.NewID(),
		KeycloakSub: u.Sub,
		Email:       u.Email,
		Status:      domain.UserStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := repo.Create(ctx, nil, fresh); err != nil {
		if errors.Is(err, domain.ErrUserConflict) {
			// Race: another request just created the row. Re-read.
			return repo.GetBySub(ctx, u.Sub)
		}
		return domain.User{}, err
	}
	return fresh, nil
}

// writeHumaError is a small wrapper that returns a §10 envelope
// through huma's context API. Middlewares can't return errors the
// way handlers do, so we set the status and body inline.
func writeHumaError(ctx huma.Context, status int, code, msg string) {
	writeHumaErrorDetails(ctx, status, code, msg, nil)
}

func writeHumaErrorDetails(
	ctx huma.Context, status int, code, msg string, details map[string]any,
) {
	ctx.SetStatus(status)
	ctx.SetHeader("Content-Type", "application/json; charset=utf-8")
	body := errorEnvelope{Error: errorBody{Code: code, Message: msg, Details: details}}
	if err := json.NewEncoder(ctx.BodyWriter()).Encode(body); err != nil {
		slog.ErrorContext(ctx.Context(), "auth: write error envelope failed", "err", err)
	}
}
