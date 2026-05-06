# §5 — Auth slot design

Decided 2026-05-06 in design session.

## Summary

v1 has no real authentication, but it has all the *seams* that real
authentication will need: a middleware in the request pipeline, a `User`
type in `context`, a `FromContext` helper that handlers already use, and
routes pre-grouped into public vs protected buckets. Today's
implementation is a stub that always populates a single overseer user.
When OIDC via Keycloak lands, the stub middleware is replaced — handlers,
context keys, and route groupings stay identical.

## Decisions

- **Auth middleware in the chain from day one, no-op stub in v1.**
  ```go
  func Auth(next http.Handler) http.Handler {
      return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          ctx := context.WithValue(r.Context(), userCtxKey, stubUser)
          next.ServeHTTP(w, r.WithContext(ctx))
      })
  }
  ```
  Replaced wholesale (not extended) when real auth ships.

- **Single canonical `User` type with the minimum-viable field set.**
  ```go
  type User struct {
      ID    uuid.UUID
      Email string
  }
  ```
  No roles, no permissions, no display name in v1. The struct grows when
  a handler actually needs more — fields added speculatively become fields
  that things can come to depend on.

- **`auth.FromContext(ctx) User` is the only way handlers read the
  current user.** No globals, no per-handler ceremony. Every writable row
  populates `author_id` from `FromContext(r.Context()).ID`.

- **Two-middleware split: `Auth` + `RequireUser`.**
  - `Auth` populates the `User` in context if credentials present.
    v1 stub: always populates the stub user.
  - `RequireUser` returns 401 if no user in context.
    v1 stub: always passes (because Auth always populates).

  When real auth ships, `Auth` becomes "validate JWT, populate or pass
  through silently"; `RequireUser` stays as the 401 gate. Public-but-
  personalized routes (e.g. a public specimen page that knows if you're
  logged in) hit only `Auth`, not `RequireUser`.

- **Routes pre-grouped into public vs protected buckets, even though the
  gate is a no-op in v1.**

  Public (no auth required, ever):
  - `GET /healthz`
  - `GET /readyz`
  - `GET /docs`
  - `GET /api/v1/openapi.json`
  - Future: public-visibility specimen reads when public sharing ships

  Protected (Auth + RequireUser):
  - All other `/api/v1/...` routes (specimens, photos, journal entries,
    collectors, file uploads/downloads)

  Bucketing now, with the stub, forces the public-vs-protected judgment
  while the contract is still soft. Retrofitting "should this route be
  public?" after the SPA depends on it is the painful path.

- **Stub user identity (v1):**
  ```
  ID    = 00000000-0000-0000-0000-000000000001
  Email = overseer@minerals.local
  ```
  All `author_id` columns get this value in v1. When real auth ships, a
  one-time migration backfills these to the real overseer's user id.

## Deferred to v2 / later

- OIDC integration via the Keycloak operator already in the cluster
- Per-row authorization (visibility-based reads, ownership-based writes
  if multi-user actually arrives)
- CSRF mitigation — depends on the chosen auth model (cookies need it,
  bearer tokens in `Authorization` header don't); decided alongside auth
  implementation
- Audit logging of who edited what, when (the `author_id` + `updated_at`
  columns already capture enough to make this trivial later)
- Field-level access control (e.g. price hidden from non-owners) — none
  of this matters until multi-user is real
- Multi-stub users for testing (a `?as=email` debug header in dev) — only
  worth building once tests need it

## Open questions / flags

- **Migration of `author_id` at the real-auth cutover.** When real OIDC
  lands, the stub user id (`...0001`) needs to be replaced with the
  actual overseer's id in every row that references it. One-time migration
  script, not a recurring concern — but we should write it as part of the
  auth integration PR, not after.
- **Stub user is the same UUID across all environments.** Convenient
  (no env-specific seeding) but means dev DB and prod DB technically
  agree on a fictional user. Harmless in v1 because they're isolated.
- **Public-bucket route list is small now.** As the SPA grows, new
  routes default to protected — explicit opt-in to public should be
  reviewed. Worth a CONTRACT.md rule.
- **`RequireUser` as a separate middleware (vs. folded into `Auth`)** is
  a design choice for forward-looking flexibility (public-personalized
  routes). If we never end up needing them, `RequireUser` is harmless
  ceremony — but its presence today costs essentially nothing.
