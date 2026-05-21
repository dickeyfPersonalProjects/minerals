# API rate limiting (ops)

App-level rate limiting (mi-tnru) protects the API before it goes
public (V3). It is the **primary** defense for per-user and
per-endpoint nuance; the Cloudflare edge is a coarse supplementary
layer (see "Division of labor" below).

## Tiers

The limiter classifies every request into one tier and applies a
token-bucket budget (requests-per-window) per key:

| Tier | Matches | Keyed by | Default budget |
|---|---|---|---|
| **auth** | `/auth/login`, `/auth/callback`, `/auth/logout`, `/api/v1/csrf` | **client IP, always** (brute-force defense; pre-/cross-session) | 10 / 60s |
| **file** | `GET /api/v1/photos/*`, `/api/v1/files/*`, `/api/v1/journal-files/*` | account when authenticated, else IP | 120 / 60s |
| **write** | `POST/PUT/PATCH/DELETE /api/v1/*` | account when authenticated, else IP | 60 / 60s |
| **read** | other `GET/HEAD /api/v1/*` | account when authenticated, else IP | 300 / 60s |
| (none) | SPA, `/healthz`, `/readyz`, `/docs`, everything else | — | unlimited (edge handles flood) |

Buckets refill continuously: a tier of `N` requests per window starts
full (a burst of `N` is allowed immediately) and then replenishes at
`N/window` tokens per second.

## Keying

- **Authenticated → account bucket.** The key is the resolved
  application user id (`auth.FromContext(ctx).ID`). **All of a user's
  sessions/tokens/devices share ONE bucket** — using three tokens still
  draws from the single account allowance. (The auth tier is the
  exception: it keys per IP even when a session is attached, because
  brute-force is a per-source concern.)
- **Anonymous → per-IP** via the `CF-Connecting-IP` header. Behind
  Cloudflare with the origin locked to Cloudflare's IP ranges (mi-1d7q,
  done), this header is the real, non-spoofable client IP.
  `X-Forwarded-For` is deliberately **not** trusted for limiting.
- **Local dev (no Cloudflare):** `CF-Connecting-IP` is absent, so the
  limiter falls back to the socket `RemoteAddr`.

## Response

Exceeding a budget returns **`429 Too Many Requests`** with:
- a `Retry-After` header (integer seconds until the next token), and
- the §10 error envelope `{"error":{"code":"rate_limited", ...}}`.

Hard block — no soft/observe-only phase.

## Configuration

All knobs are env vars (defaults above); see `CONFIG.md` for the full
inventory:

- `RATE_LIMIT_ENABLED` (default `true`) — master switch.
- `RATE_LIMIT_{AUTH,READ,WRITE,FILE}_REQUESTS`
- `RATE_LIMIT_{AUTH,READ,WRITE,FILE}_WINDOW_SECONDS`

Tuning requires no code change — adjust the ConfigMap and restart.
Each value must be a positive integer; a non-positive value is a boot
error (a zero budget would fail closed and reject everything).

## Storage & the multi-replica caveat

Buckets are **in-memory, per replica**. Prod runs 2 replicas, so a
caller balanced across both can draw up to **2× a tier's budget**. This
is an **accepted ops tradeoff** (operator decision, mi-tnru): the
limits are deliberately treated as approximate. Do not add Redis for
this. A shared store (Redis) would be the upgrade if exact global
limits ever matter — until then, set budgets with the ~2× slack in
mind. The code carries a comment marking the approximation as
intentional (`internal/ratelimit/limiter.go`).

## Division of labor with the Cloudflare edge

- **App limiter (this):** per-account and per-endpoint nuance, the
  trustworthy `CF-Connecting-IP` per-IP key, the `rate_limited`
  envelope. Primary defense.
- **Cloudflare edge:** crude per-IP flood protection at the edge
  (cheaper than app-level) AND the origin lockdown that makes
  `CF-Connecting-IP` trustworthy. Cloudflare's free tier offers a
  limited Rate Limiting Rules allowance — enable a basic rule as a
  coarse outer layer; verify the current free allowance in the
  dashboard. Not the primary defense.

DDoS protection and CAPTCHA-on-auth are out of scope here (edge/IdP
concerns).

## Middleware order

The limiter MUST run **after** the session middleware (so the
authenticated user is attached for account keying) and before CSRF.
The wired chain is:

```
… → SessionMW → RateLimitMW → CSRFMW → handler
```

If the limiter ran before the session middleware, every authenticated
request would key by IP instead of account. See
`internal/api/server.go` (`New`).
