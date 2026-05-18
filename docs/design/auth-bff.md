# V2 Auth: Backend-for-Frontend (BFF) Design

Status: **Design** (V2 public-launch blocker; tracked by `mi-1d5i`).
Last revised: 2026-05-18.
Authors: mayor + overseer, captured from design conversation.

> This document is the canonical reference for the V2 auth migration. Code, tests,
> and documentation MUST conform to the decisions and rationale below. Changes to
> the design require an explicit amendment commit and a note in `mi-1d5i`.

---

## Why we are migrating

V1 had no auth. V2 introduced auth via PKCE-in-the-SPA, which works for personal
use but has produced a steady stream of browser-tier bugs:

- `mi-cl1` — CSP `connect-src 'self'` blocked the cross-origin OIDC token POST.
- `mi-0ag` / `mi-iem` — SPA router didn't match `/auth/callback?...` due to
  module-load ordering with the path→hash rewrite.
- `mi-rb6k` / `mi-ct2` — silent renewal via hidden iframe; three rounds of
  attempts, all rejected; iframe-based PKCE is brittle in modern browsers.
- `mi-2eg6` — default-Keycloak-shape JWTs missing the `user` role; every
  authed write 403'd.
- `mi-lrqt` — `<img>` tags fetch without `Authorization` header; every
  non-public photo broken for every owner.

The PKCE-in-SPA pattern is the wrong default for an app going public. The bugs
above don't exist in a BFF design. V2's purpose is to go beyond personal use;
this migration is the prerequisite.

---

## The architecture

### Today (PKCE-in-SPA — being replaced)

```
Browser SPA ──PKCE──▶ Keycloak
    │                     │
    │ Authorization: Bearer <token-in-JS>
    ▼
Go backend (validates JWT against JWKS)
```

The SPA is the OAuth client. Access tokens live in browser memory. Every
authenticated API call carries a `Bearer` header attached by the SPA's wrapped
fetch client.

### Target (BFF — this design)

```
Browser SPA ──Login redirect──▶ Go backend ──server-side Code Exchange──▶ Keycloak
                                     │
                                     │ Set-Cookie: minerals_session=<id>;
                                     │   HttpOnly; Secure; SameSite=Lax; Path=/;
                                     │   Max-Age=1209600
                                     ▼
                                Browser cookie jar (JS cannot read)
                                     │
                                     │ Cookie sent automatically on every request
                                     ▼
                                Go backend (looks up session, refreshes token
                                            server-side, attaches user to context)
```

The Go backend is the OAuth client. Access + refresh + ID tokens are stored
server-side in a sessions table. The browser holds only an opaque session ID in
an HttpOnly cookie.

### User-visible flow

1. SPA renders Login as `<a href="/auth/login">`. No PKCE in the SPA. No JS-driven
   OAuth dance.
2. Browser hits `GET /auth/login`. Backend generates an OAuth `state` value,
   stores it short-term server-side, and 302s to Keycloak's `/auth?...` with
   `response_type=code`. (Not PKCE — we're a confidential client now.)
3. User authenticates at Keycloak (Keycloak's own UI).
4. Keycloak 302s back to `GET /auth/callback?code=...&state=...` on the BACKEND
   (no longer a SPA route).
5. Backend validates `state`, exchanges the code for tokens via a server-to-server
   POST to Keycloak's token endpoint using `client_id + client_secret`. Receives
   access + refresh + id tokens.
6. Backend creates a session row, sets the cookie, 302s the browser to `/` (or a
   stashed return-to).
7. Every subsequent SPA request automatically carries the cookie. The session
   middleware reads the cookie, looks up the session, refreshes the access token
   server-side if near expiry, and attaches the user to the request context.
8. Logout: SPA POSTs to `/auth/logout` with a CSRF token. Backend invalidates the
   session row, clears the cookie, and 302s to Keycloak's end-session endpoint
   with the id_token as `id_token_hint`.

---

## Sessions table

Lives in its own Postgres schema (`auth`), never co-mingled with domain tables.
This is the most important decision for future microservice extraction —
peeling auth out cleanly requires the table boundary to be clean.

```sql
CREATE SCHEMA IF NOT EXISTS auth;

CREATE TABLE auth.sessions (
  -- Identity
  id                       BYTEA       PRIMARY KEY,        -- 32 random bytes
  user_sub                 TEXT        NOT NULL,           -- Keycloak `sub` claim
  user_id                  UUID        NOT NULL,           -- FK to users.id

  -- Tokens (server-side only; never sent to browser)
  access_token             TEXT        NOT NULL,
  refresh_token            TEXT        NOT NULL,
  id_token                 TEXT        NOT NULL,

  -- Lifecycle
  access_token_expires_at  TIMESTAMPTZ NOT NULL,
  refresh_token_expires_at TIMESTAMPTZ NOT NULL,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  absolute_expires_at      TIMESTAMPTZ NOT NULL,

  -- CSRF
  csrf_token               BYTEA       NOT NULL,

  -- Forensics (write on creation only; read for audit)
  ip                       INET,
  user_agent               TEXT,

  -- Optional revocation flag (cheap soft-delete)
  revoked_at               TIMESTAMPTZ
);

CREATE INDEX sessions_user_id_idx ON auth.sessions (user_id);
CREATE INDEX sessions_absolute_expires_at_idx
  ON auth.sessions (absolute_expires_at)
  WHERE revoked_at IS NULL;
```

### Field-by-field rationale

**`id` — 32 random bytes (NOT a UUID).** Maximum entropy, no structure (an
attacker can't tell a session ID from random noise; no version bytes giving
hints), constant-time-comparison friendly. Generated via `crypto/rand`. Stored
as BYTEA; transmitted as base64url-encoded (43 chars) in the cookie value.

The cookie is the credential. Anyone holding a valid session ID is the user.
Secure + HttpOnly flags keep it out of JS and off non-HTTPS, but the value
itself must be unbruteforce-able.

**`user_sub` and `user_id` — denormalized for performance.** `user_sub` is the
Keycloak stable identifier; `user_id` is the FK to the local `users` table.
Having both avoids a JOIN on every request.

**`access_token`, `refresh_token`, `id_token` — the OAuth state.**
- `access_token` — bearer JWT. Used server-side when calling Keycloak
  (introspection, etc.). Mostly unused after creation.
- `refresh_token` — used to mint new access tokens. Keycloak rotates these:
  each use invalidates the old and issues a new one. Updated, not just read.
- `id_token` — OIDC identity token. Used as `id_token_hint` on Keycloak's
  logout endpoint to terminate the SSO session without a confirmation prompt.

Encryption at rest: Postgres-level TDE is sufficient. Per-row KMS encryption is
overkill — if the DB is compromised, the attacker also has all your domain
data; tokens aren't a separate concern.

**Four expiration concepts (don't conflate):**

| Concept | Driven by | Behavior when crossed |
|---|---|---|
| `access_token_expires_at` | Keycloak (~5 min default) | Refresh on the next request via Keycloak |
| `refresh_token_expires_at` | Keycloak (~30 days default) | Session dies; user re-authenticates |
| `absolute_expires_at` | App (default 7 days) | Hard cap; session dies regardless |
| Idle timeout | App (default 24h since `last_used_at`) | Session dies |

Token TTLs come from Keycloak's response at session creation. Application caps
are configurable env vars; defaults tuned for personal-ish usage.

**`csrf_token` — 32 random bytes.** Per-session CSRF token. Served via
`GET /api/v1/csrf`, attached by SPA as `X-CSRF-Token` header on mutating
requests. Constant-time-compared in middleware.

**`ip`, `user_agent` — forensics.** Capture on creation. Used for an "active
sessions" admin view someday and for breach investigation. NEVER used for auth
decisions (tying sessions to IPs is hostile to mobile users who change
networks).

**`revoked_at` — soft-delete.** Set on logout / admin force-logout. Middleware
filters by `revoked_at IS NULL`. Cleanup goroutine deletes rows where
`revoked_at < now() - 30 days` weekly.

### Cleanup

A periodic job (goroutine in the backend) runs:

```sql
DELETE FROM auth.sessions
WHERE (revoked_at IS NOT NULL AND revoked_at < now() - INTERVAL '30 days')
   OR absolute_expires_at < now() - INTERVAL '7 days';
```

Without it the table grows forever. The partial index on
`absolute_expires_at WHERE revoked_at IS NULL` makes the alive-session check
cheap.

---

## Session middleware: the hot path

Runs on every authenticated request. Pseudocode (Go-ish):

```go
func SessionMiddleware(deps Deps) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx := r.Context()

            // 1. Pull and decode cookie
            cookie, err := r.Cookie("minerals_session")
            if err != nil {
                next.ServeHTTP(w, r)  // anonymous; handlers decide if that's OK
                return
            }
            sessionID, err := base64.RawURLEncoding.DecodeString(cookie.Value)
            if err != nil || len(sessionID) != 32 {
                clearSessionCookie(w)
                next.ServeHTTP(w, r)
                return
            }

            // 2. Lookup via the SessionResolver interface
            timer := prometheus.NewTimer(sessionLookupDuration.WithLabelValues(""))
            sess, err := deps.Sessions.GetByID(ctx, [32]byte(sessionID))
            timer.ObserveDuration()
            if err != nil {
                if errors.Is(err, ErrSessionNotFound) {
                    clearSessionCookie(w)
                    next.ServeHTTP(w, r)
                    return
                }
                writeError(w, 500, "internal_error", "session lookup failed", nil)
                return
            }

            // 3. Liveness checks
            now := deps.Clock.Now()
            if sess.RevokedAt != nil ||
               now.After(sess.AbsoluteExpiresAt) ||
               now.Sub(sess.LastUsedAt) > deps.IdleTimeout {
                _ = deps.Sessions.Revoke(ctx, sess.ID)
                clearSessionCookie(w)
                next.ServeHTTP(w, r)
                return
            }

            // 4. Refresh access token if needed (serialized per-session)
            if now.After(sess.AccessTokenExpiresAt.Add(-refreshLeeway)) {
                refreshed, err := deps.OAuth.Refresh(ctx, sess.RefreshToken)
                if err != nil {
                    _ = deps.Sessions.Revoke(ctx, sess.ID)
                    clearSessionCookie(w)
                    next.ServeHTTP(w, r)
                    return
                }
                sess, _ = deps.Sessions.UpdateTokens(ctx, sess.ID, refreshed)
            }

            // 5. Touch last_used_at (debounced — only every 30s)
            if now.Sub(sess.LastUsedAt) > 30*time.Second {
                _ = deps.Sessions.Touch(ctx, sess.ID, now)
            }

            // 6. Attach to context, pass through
            ctx = auth.WithUser(ctx, userFromSession(sess))
            ctx = auth.WithSession(ctx, sess)  // for CSRF middleware
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### Key behaviors

- **Never 401 from this middleware on bad cookie.** Anonymous-permitted
  endpoints still need to work. The middleware attaches a user when possible;
  handlers decide whether auth is required.
- **Refresh leeway (30s).** Refresh slightly before the access token actually
  expires to absorb clock drift.
- **Stampede defense.** Concurrent requests on the same session whose access
  token is expiring must NOT all trigger refresh — Keycloak's refresh-token
  rotation would detect the second use as replay and revoke the entire token
  family. Mitigation:
  - Single replica: in-process per-session mutex (`sync.Map[id]*sync.Mutex`).
  - Multi-replica: `pg_try_advisory_xact_lock(hashtext(id))`.
  - Start single-replica; switch to advisory lock when scaling out.
- **Last-used debounce (30s).** Avoid a write on every request.
  Loses 30s of granularity on idle timeout, which doesn't matter when the
  idle window is tens of minutes.

### What's NOT in the hot path

- Re-introspecting roles via Keycloak. Roles come from session creation; don't
  re-fetch on every request.
- Re-validating access_token signatures. We minted them via a successful code
  exchange; trust the session row.
- IP pinning. Mobile users move.
- Full session logging. `session_id` is a credential. Log `user_id` and a
  hash for correlation only.

---

## Caching strategy

**No cache on day one.** Build behind the `SessionResolver` interface from the
start so caching can drop in later without changes to middleware or handlers.

Rationale:
- DB lookup is ~0.5ms warm; not a perf problem at current scale.
- Correctness risk from caching (stale logout, stale roles) is non-trivial.
- Without measurement, we don't know if caching would help.

### What we DO build day one

- `SessionResolver` interface (see below). The middleware depends only on this.
- Prometheus histogram metrics around the lookup with `result` labels
  (`hit` | `miss` | `error`). Today only `miss` fires. If/when a cache lands,
  the `hit` label automatically appears and before/after comparison is one
  PromQL query.

### What we'd add later (if measurement justifies)

- A `cachingSessionRepo` decorator wrapping the Postgres repo, with explicit
  cache invalidation on every mutation (revoke, refresh-update, role change).
- TTL of 30-60s for safety even when invalidation misses.
- Multi-replica: Postgres `LISTEN/NOTIFY` for cross-replica invalidation, OR
  short TTL with eventual-consistency UX.

---

## Cookie attributes

Every flag is deliberate. Final form:

```go
http.SetCookie(w, &http.Cookie{
    Name:     "minerals_session",
    Value:    base64.RawURLEncoding.EncodeToString(sessionID),
    Path:     "/",
    HttpOnly: true,
    Secure:   cfg.CookieSecure,                    // true in prod/staging, false in dev
    SameSite: http.SameSiteLaxMode,
    MaxAge:   int(cfg.CookieMaxAge.Seconds()),     // default 14 days
    // Domain: omitted (host-only)
    // Partitioned: not applicable
})
```

### `HttpOnly: true`

Locks the cookie away from JavaScript. `document.cookie` doesn't include it; no
JS API can read or modify it. The qualitative difference from in-memory or
sessionStorage tokens: an XSS payload can puppet the user's browser briefly
but cannot exfiltrate the credential for offline use from the attacker's
server.

Not sufficient — XSS can still issue requests as the user. CSRF tokens close
that gap.

### `Secure: true` (in non-dev environments)

Cookie sent only over HTTPS. Defends against public-Wi-Fi sniffing and SSL
stripping. Paired with HSTS (which the app already emits per CONTRACT §17),
the cookie is unreachable to network attackers in practice.

Dev compose serves on plain HTTP localhost, so the dev config sets
`COOKIE_SECURE=false`. The decision is per-environment (config flag), not
per-request — don't infer from `X-Forwarded-Proto`.

### `SameSite=Lax`

Top-level GET navigations (link clicks from other sites) carry the cookie —
shared-link UX works. Cross-site POSTs do NOT carry the cookie — layer-one
CSRF defense.

What Lax doesn't cover (and why we still need CSRF tokens):
- Same-site CSRF: a compromised subdomain (`auth.*`, future SaaS-on-subdomain)
  is same-site and CAN send the cookie.
- GET endpoints with side effects: never write any, but defense-in-depth.
- Inconsistent SameSite enforcement in mobile webviews and in-app browsers.
- SameSite is a privacy feature, not a security spec.

### `Path=/`

Cookie travels on every path. Set explicitly (not implied). Prefix-matching
gotcha: `Path=/api` matches `/apiv2`; always use `/` for the session cookie.

### `Domain`: omitted (host-only)

Cookie travels ONLY to the exact host that set it
(`www.mineral-staging.dickey.cloud`). Does NOT travel to `auth.*` subdomain
(Keycloak) or any future subdomain.

Setting `Domain=mineral-staging.dickey.cloud` would BROADEN scope to all
subdomains — counterintuitive but correct. Don't.

If V3+ ever splits SPA and backend across hostnames, that change must file an
explicit bead for setting Domain — making the security trade-off visible.

### `MaxAge`: configurable, default 14 days

Always longer than `absolute_expires_at` (default 7 days). The server is the
source of truth on session lifetime; the cookie outlives the session row so
that the expired-session response can cleanly clear the cookie.

DO NOT refresh the cookie's Max-Age on every request (sliding expiration on
the cookie). Sliding behavior happens server-side on `last_used_at`.

### `Partitioned`: not set

CHIPS (Cookies Having Independent Partitioned State) is for third-party iframe
embedding scenarios. Not applicable to a first-party web app.

### Cookie clearing (logout / session-invalid)

```go
http.SetCookie(w, &http.Cookie{
    Name:     "minerals_session",
    Value:    "",
    Path:     "/",                  // MUST match original
    HttpOnly: true,
    Secure:   cfg.CookieSecure,
    SameSite: http.SameSiteLaxMode,
    MaxAge:   -1,                   // -1 = expire now
})
```

**Crucial:** Path (and Domain if set) MUST match the original cookie. Mismatch
= browser treats as different cookie = original keeps going. This is the #1
logout bug in cookie auth. Always wrap cookie creation and clearing in helper
functions to avoid drift.

---

## CSRF: stored synchronizer

### What CSRF is

A logged-in user visits `evil.com`. Evil.com contains a form that POSTs to
your app. The browser attaches the session cookie automatically. Without
defense, the attacker's POST runs as the user.

### Why SameSite=Lax alone isn't enough

- Same-site CSRF (compromised subdomain).
- GET endpoints with side effects (defense-in-depth).
- Browsers with non-standard SameSite enforcement.
- SameSite isn't a security spec — privacy feature with security implications.

CSRF tokens are an explicit, app-layer defense that doesn't depend on browser
cooperation.

### The contract

1. Server stores a secret per session (`csrf_token` column).
2. Server serves it via `GET /api/v1/csrf` (authenticated).
3. SPA stores in a memory store, attaches as `X-CSRF-Token` header on every
   POST/PUT/PATCH/DELETE.
4. Server middleware compares the header to the session row; reject if missing
   or mismatch.

### Pattern choice: stored synchronizer (not double-submit)

Stored synchronizer chosen over double-submit cookie because:
- We already have a session row; adding a column is trivial.
- The session row gives an explicit rotation point (logout, role change).
- Double-submit requires a non-HttpOnly cookie that JS reads — adds
  surface area.
- Stored synchronizer is more robust against subdomain-takeover scenarios.

### Middleware skeleton

```go
func CSRFMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Safe methods bypass
        if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
            next.ServeHTTP(w, r); return
        }
        // Unauthenticated bypass — handler will reject if auth required
        sess := auth.SessionFromContext(r.Context())
        if sess == nil {
            next.ServeHTTP(w, r); return
        }

        header := r.Header.Get("X-CSRF-Token")
        if header == "" {
            writeError(w, 403, "csrf_missing", "CSRF token required", nil); return
        }

        expected := base64.RawURLEncoding.EncodeToString(sess.CSRFToken)
        if subtle.ConstantTimeCompare([]byte(header), []byte(expected)) != 1 {
            writeError(w, 403, "csrf_mismatch", "CSRF token does not match", nil); return
        }
        next.ServeHTTP(w, r)
    })
}
```

### Subtle choices

- **Constant-time compare.** Prevents byte-by-byte timing side-channel. Free.
- **Distinct error codes** (`csrf_missing` vs `csrf_mismatch`). SPA on
  mismatch refetches the token and retries ONCE. SPA on missing knows it has
  a bug.
- **Logout requires CSRF.** An attacker logging the user out is annoying;
  protect it.
- **Token in HEADER ONLY** — never query parameter (lands in logs, history,
  Referer).
- **Token in Svelte memory store** — never localStorage. XSS reading it is
  acceptable risk only when paired with HttpOnly session cookie (token alone
  is useless).
- **No per-request rotation.** Per-session is enough and avoids parallel-request
  failures.
- **CSRF endpoint requires auth.** A pre-auth fetch would let an attacker mint
  a token cross-site.

### SPA integration

```ts
// Once at app start, after auth-state confirms session:
const token = await client.GET('/api/v1/csrf').then(r => r.data.token);
csrfStore.set(token);

// Wrapped client middleware attaches:
function attachCsrf(req: Request): Request {
  if (req.method === 'GET' || req.method === 'HEAD') return req;
  const token = get(csrfStore);
  if (token) req.headers.set('X-CSRF-Token', token);
  return req;
}

// On 403 csrf_mismatch:
async function retryOnCsrfMismatch(req, response) {
  if (response.status === 403 && response.body.error.code === 'csrf_mismatch') {
    const fresh = await client.GET('/api/v1/csrf').then(r => r.data.token);
    csrfStore.set(fresh);
    return retry(req);  // ONCE
  }
  return response;
}
```

---

## Microservice extraction: future-proofing

V2 stays single-binary. V3+ may extract auth into its own service. The V2
design must NOT preclude that without rewrites.

### The boundary

All auth-related code lives in `internal/auth/bff/`:

- OAuth client (Keycloak interaction).
- `SessionResolver` interface and Postgres implementation.
- Session middleware.
- CSRF middleware.
- `/auth/login`, `/auth/callback`, `/auth/logout` handlers.
- Cookie-creation helpers.

Other packages depend ONLY on:
- The `SessionResolver` interface.
- The `auth.User` shape (already exists in `internal/auth/`).

No domain (specimens / collectors / photos) code reaches into `bff/`, into
the OAuth client, or into the Keycloak SDK.

### The `SessionResolver` interface

```go
type SessionResolver interface {
    GetByID(ctx context.Context, id [32]byte) (Session, error)
    Create(ctx context.Context, params CreateParams) (Session, error)
    UpdateTokens(ctx context.Context, id [32]byte, t TokenSet) (Session, error)
    Touch(ctx context.Context, id [32]byte, at time.Time) error
    Revoke(ctx context.Context, id [32]byte) error
    RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}
```

Today: a Postgres implementation.
Tomorrow (caching): a decorator wrapping the Postgres implementation.
Day after (microservice): an HTTP/gRPC client to auth-service.

The middleware contract doesn't change across these.

### Extraction pattern: Pattern B (each service calls auth-service)

When V3+ extracts auth:

1. Stand up `auth-service` as a separate Go binary. Owns `auth.sessions`.
2. Move OAuth handlers (`/auth/login`, `/auth/callback`, `/auth/logout`) to
   auth-service. Now served from `auth.yourapp.com`.
3. Minerals service's `SessionResolver` impl changes from
   "SELECT from auth.sessions" to "RPC to auth-service."
4. Cookie unchanged — auth-service sets it; minerals reads cookie from
   incoming requests, asks auth-service to resolve.
5. Everything else (handlers, domain, CSRF) is untouched.

**Pattern B (call auth-service directly), not Pattern A (gateway routes all
traffic through an edge proxy).** Pattern A is overkill until 5+ services
exist. Migrating from B to A later is real work but not architectural rewrite.

### What we explicitly DON'T do today

- Build a gateway proxy.
- Build auth-service.
- Pre-emptively split repos.

We make Pattern B easy by enforcing the interface boundary in V2.

---

## Configuration

### New backend env vars

| Variable | Type | Purpose |
|---|---|---|
| `OIDC_CLIENT_SECRET` | Secret | Confidential OAuth client secret |
| `COOKIE_SECURE` | Bool | True in prod/staging, false in dev |
| `COOKIE_MAX_AGE_SECONDS` | Int | Default 1209600 (14 days) |
| `SESSION_ABSOLUTE_EXPIRES_HOURS` | Int | Default 168 (7 days) |
| `SESSION_IDLE_TIMEOUT_MINUTES` | Int | Default 1440 (24 hours) |

### Removed (no longer needed in browser-served runtime-config)

- `PUBLIC_OIDC_ISSUER_URL`
- `PUBLIC_OIDC_CLIENT_ID`
- `PUBLIC_OIDC_REDIRECT_URI`

Backend retains its own:
- `OIDC_ISSUER_URL`
- `OIDC_CLIENT_ID`
- `OIDC_CLIENT_SECRET` (new — was not needed for public PKCE client)

### Keycloak (Terraform) changes

- Change `minerals-frontend` client `access_type` from `PUBLIC` to
  `CONFIDENTIAL`. Client now has a secret; secret reaches the backend as a
  SealedSecret in the gitops overlay.
- `valid_redirect_uris` updated to the backend's `/auth/callback` (e.g.
  `https://www.mineral-staging.dickey.cloud/auth/callback` — same URL, but
  now backend-served).
- PKCE config (`pkce_code_challenge_method = "S256"`) can be removed —
  irrelevant for confidential clients (though harmless to keep).
- Audience mapper unchanged.
- `web_origins` can become more restrictive (no SPA cross-origin POSTs to
  Keycloak anymore).

---

## What gets simpler (deletion-only PRs after migration)

The SPA loses an entire layer:

- `frontend/src/lib/oidc/` — PKCE, callback handling, token store, silent
  renewal, code verifier persistence. **All deleted.**
- `frontend/src/routes/AuthCallback.svelte` — deleted.
- The path-to-hash rewrite in `frontend/src/main.ts` — deleted.
- LoginButton's `beginLogin` JS — becomes plain `<a href="/auth/login">`.
- ProfileMenu's `beginLogout` — becomes form POST with CSRF token.
- mi-lrqt's Blob URL workaround for `<img>` tags — can be reverted; cookies
  travel on `<img>` requests automatically.

The backend gains the BFF code in `internal/auth/bff/` but loses the
bearer-token validation middleware (or repurposes it for any service-to-service
auth if introduced later).

---

## Out of scope (deliberately)

- **Multi-factor auth.** Keycloak handles MFA at its login form; nothing for
  this design to do.
- **SSO with non-Keycloak IdPs.** Keycloak can broker; we don't.
- **Cross-tab session sync.** Cookies + Page Visibility API provide enough.
- **Refresh-token-grant in the SPA.** Refresh happens server-side only.
- **Auth microservice itself.** Future V3+ work; design enables but doesn't
  build.
- **Migration shim for live PKCE users.** Production is V1 (no auth code).
  Staging is the only V2 environment with auth; can be rebuilt on the BFF
  side directly. No coexistence work.
- **Session encryption at rest beyond Postgres TDE.** Per-row KMS encryption
  is overkill for the threat model.
- **Per-request CSRF token rotation.** Per-session is sufficient.
- **"Remember me" toggle.** Always-persistent cookie; close the tab to log
  out.
- **IP pinning.** Hostile to mobile users.

---

## Acceptance criteria (epic-level)

- Full login → app → refresh → still logged in → click around → logout works
  on staging from a real browser. End-to-end Playwright spec asserts.
- All existing Playwright smokes pass against the BFF backend.
- `mi-lrqt`'s Blob URL workaround is reverted; `<img>` tags work naturally
  with cookies.
- CSRF defense is enforced and tested: cross-origin POST with cookie but no
  CSRF token → 403.
- Logout invalidates session server-side; old cookie cannot be replayed.
- Token refresh happens transparently; no user-visible interruption mid-session.
- All PKCE code path deleted from frontend.
- `internal/auth/bff/` boundary is clean; no other package reaches inside.
- All relevant docs updated: CONFIG.md, deploy/README.md, keycloak.md,
  CONTRACT §13.
- Prometheus session-lookup histogram metrics in place.

---

## References

- Tracking bead: `mi-1d5i` (V2 BFF migration EPIC).
- Bugs this design eliminates: `mi-cl1`, `mi-0ag`, `mi-iem`, `mi-rb6k`,
  `mi-ct2`, `mi-2eg6`, `mi-lrqt`.
- Companion V2-launch blocker: `mi-c1y` (bootstrap-claim-orphans).
- CONTRACT.md §13 — visibility / auth model (extends; doesn't replace).
- IETF RFC 6265bis — current cookie spec.
- OWASP Cheat Sheet — Cross-Site Request Forgery Prevention.
