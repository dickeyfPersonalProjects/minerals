package api

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
)

// humaAuth is the huma-side analogue of auth.Auth + auth.RequireUser
// (per CONTRACT.md §13). v1 always populates StubUser; real-auth
// replacement validates credentials and either populates a User or
// aborts with 401. Apply via Operation.Middlewares for protected
// operations — system endpoints (healthz, readyz, openapi, docs) are
// public and MUST NOT carry this middleware.
func humaAuth(ctx huma.Context, next func(huma.Context)) {
	next(huma.WithContext(ctx, auth.WithUser(ctx.Context(), auth.StubUser)))
}
