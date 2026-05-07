package api

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
)

// humaAuthMiddleware is the huma-shaped equivalent of
// auth.Auth + auth.RequireUser layered. v1 stub: it populates
// auth.StubUser on the request context unconditionally. When real
// auth ships, the population step grows to validate credentials
// and the rejection branch fires for unauthenticated requests.
//
// User identity is plumbed through auth.WithUser so handlers read
// it via auth.FromContext (CONTRACT §13: the single sanctioned
// reader).
func humaAuthMiddleware(ctx huma.Context, next func(huma.Context)) {
	newCtx := auth.WithUser(ctx.Context(), auth.StubUser)
	ctx = huma.WithContext(ctx, newCtx)

	if auth.FromContext(ctx.Context()).ID == uuid.Nil {
		// Unreachable with the stub above; kept so the shape
		// doesn't change when real auth replaces it. We can't reach
		// the API here; rely on the configured default error
		// writer to emit the §10 envelope.
		ctx.SetStatus(401)
		ctx.SetHeader("Content-Type", "application/json; charset=utf-8")
		_, _ = ctx.BodyWriter().Write([]byte(
			`{"error":{"code":"unauthorized","message":"authentication required"}}`))
		return
	}
	next(ctx)
}
