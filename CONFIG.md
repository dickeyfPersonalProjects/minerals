# Minerals — Settings inventory

This file is the **single source of truth** for every tunable setting of
the minerals app: env vars, ConfigMap keys, feature flags, runtime
knobs. CONTRACT.md and the design doc point here; they no longer hold
their own copies.

To add or change a setting, follow CONTRACT.md §15 "Adding a new
setting" — updating this inventory is the first and mandatory step.

## Inventory

| Name | Kind | Default (dev) | Required in prod | Purpose | Source |
|---|---|---|---|---|---|
| `PORT` | env | `8080` | no | HTTP listen port | `internal/config/config.go:62` |
| `ADMIN_PORT` | env | `9090` | no | Operator-facing admin listen port — serves Prometheus `/metrics` plus the kubelet's `/healthz` / `/readyz` probes. MUST NOT be wired into the public Ingress; the base `Service` exposes it as a named port (`admin`) only for in-cluster scrape and probe traffic (mi-2b1k). | `internal/config/config.go` |
| `DATABASE_URL` | env | `postgres://minerals:minerals@localhost:5432/minerals?sslmode=disable` | **yes** | Postgres DSN | `internal/config/config.go:71` |
| `S3_ENDPOINT` | env | `http://localhost:9000` | **yes** | MinIO endpoint URL | `internal/config/config.go:72` |
| `S3_ACCESS_KEY_ID` | env | `minioadmin` | **yes** | MinIO access key | `internal/config/config.go:73` |
| `S3_SECRET_ACCESS_KEY` | env | `minioadmin` | **yes** | MinIO secret key | `internal/config/config.go:74` |
| `S3_BUCKET` | env | `minerals-dev` | **yes** | Bucket name | `internal/config/config.go:75` |
| `S3_REGION` | env | `us-east-1` | no | Required by AWS SDK; arbitrary for MinIO | `internal/config/config.go:64` |
| `MAX_UPLOAD_BYTES` | env | `104857600` | no | 100 MiB upload cap | `internal/config/config.go:93` |
| `LOG_LEVEL` | env | `info` | no | `debug` / `info` / `warn` / `error` | `internal/config/config.go:63` |
| `ENV` | env | `dev` | **yes** | `dev` / `prod`; flips strictness | `internal/config/config.go:54` |
| `MINDAT_API_KEY` | env | _(unset)_ | no | Mindat REST API token for mineral-species lookup (mi-dtg / F-1). When unset, mineral lookup falls back to DB-only mode (no Mindat fallthrough). | `internal/config/config.go` |
| `OIDC_ISSUER_URL` | env | `http://localhost:8081/realms/minerals` | no | Keycloak realm URL used by the backend for JWT verification. Discovery (`{OIDC_ISSUER_URL}/.well-known/openid-configuration`) yields the JWKS endpoint. Consumed by `internal/oidc` via the auth middleware (mi-aw3a). | `internal/config/config.go` |
| `OIDC_CLIENT_ID` | env | `minerals-frontend` | no | Expected `aud` claim on bearer tokens reaching the backend. Audience check only — no client secret on the backend (pure resource server, JWKS validation). Consumed by `internal/oidc` via the auth middleware (mi-aw3a). | `internal/config/config.go` |
| `OIDC_JWKS_URL` | env | _(unset)_ | no | Overrides OIDC discovery for locating the realm's JWKS endpoint. When unset, the verifier discovers it from `OIDC_ISSUER_URL/.well-known/openid-configuration`. Set this when the canonical `OIDC_ISSUER_URL` (which must match browser-issued tokens' `iss` claim) is not reachable from inside the backend container — e.g. the docker-compose dev stack where the issuer is `http://localhost:8081/realms/minerals` (host-side) but the backend reaches Keycloak in-network at `http://keycloak:8080`. Consumed by `internal/oidc` (mi-dau). | `internal/config/config.go` |
| `OIDC_DISCOVERY_URL` | env | _(unset)_ | no | Overrides the URL the BFF OAuth client uses to fetch the OIDC discovery document. When unset, discovery happens at `OIDC_ISSUER_URL/.well-known/openid-configuration` (the production path). When set, the well-known doc is fetched from `{OIDC_DISCOVERY_URL}/.well-known/openid-configuration` and the canonical `OIDC_ISSUER_URL` is still used to validate the discovery doc's `iss` field and token `iss` claims. Sister setting to `OIDC_JWKS_URL` — same rationale (host-side `OIDC_ISSUER_URL` is unreachable from inside the backend container in dev compose), applied to the BFF OAuth client's discovery instead of the verifier's JWKS lookup. Consumed by `internal/auth/bff` (mi-8tnv). | `internal/config/config.go` |
| `PUBLIC_OIDC_ISSUER_URL` | env | `http://localhost:8081/realms/minerals` | no | Keycloak realm URL exposed to the SPA via `/api/v1/runtime-config` (the SPA uses it to discover the authorization endpoint for the PKCE login flow). The `PUBLIC_` prefix marks "safe to send to the browser". Consumed by `internal/api` (mi-5ew). | `internal/config/config.go` |
| `PUBLIC_OIDC_CLIENT_ID` | env | `minerals-frontend` | no | Public OIDC `client_id` the SPA uses for the auth-code-with-PKCE flow. Same value as `OIDC_CLIENT_ID` today (the backend's expected audience and the SPA's client id are the `minerals-frontend` Keycloak client). Served to the SPA via `/api/v1/runtime-config`. | `internal/config/config.go` |
| `PUBLIC_OIDC_REDIRECT_URI` | env | `http://localhost:5173/auth/callback` | no | Absolute URL the SPA hands Keycloak as the `redirect_uri` in the auth-code flow. Must match a `valid_redirect_uris` entry on the `minerals-frontend` Keycloak client (`terraform/keycloak/clients.tf`). Served to the SPA via `/api/v1/runtime-config`. Under V2 BFF (mi-1d5i) this is also the URL the BFF passes to Keycloak on `/auth/login` and reuses on `/auth/callback`'s code exchange — same URL, but backend-served. | `internal/config/config.go` |
| `OIDC_CLIENT_SECRET` | env | _(unset)_ | yes (when BFF enabled) | Confidential-client secret the BFF uses on the server-to-server code exchange (mi-bm5b). Required to enable `/auth/login` → `/auth/callback`; unset leaves the BFF handlers unregistered and the SPA on the legacy PKCE path. Provisioned in prod via the SealedSecret `minerals-oidc-secret` (mi-qnmy). Treat as a secret — never log. | `internal/config/config.go` |
| `OAUTH_STATE_HMAC_KEY` | env | _(unset)_ | yes (when BFF enabled) | HMAC-SHA256 key that signs the short-lived state cookie issued by `/auth/login` and verified on `/auth/callback` (mi-bm5b). 32-byte minimum, enforced at boot. Rotated by deploying a new value — in-flight logins fail with `400 invalid_state` and users retry. Treat as a secret. | `internal/config/config.go` |
| `COOKIE_SECURE` | env | `true` in prod, `false` in dev | no | Flips the `Secure` flag on the BFF session and state cookies. True in prod/staging (HTTPS-only); false in the dev compose stack (plain-HTTP localhost). Per-environment, never per-request — never inferred from `X-Forwarded-Proto`. | `internal/config/config.go` |
| `COOKIE_MAX_AGE_SECONDS` | env | `1209600` (14 days) | no | `Max-Age` carried on the BFF session cookie. MUST be longer than `SESSION_ABSOLUTE_EXPIRES_HOURS` so the server-side row expires first; a stale cookie arriving past expiry cleanly clears (design invariant). | `internal/config/config.go` |
| `SESSION_ABSOLUTE_EXPIRES_HOURS` | env | `168` (7 days) | no | Hard cap on a single BFF session row's lifetime. Stamped into `auth.sessions.absolute_expires_at` on Create; the session middleware (mi-ken4) revokes sessions past this even when Keycloak would still issue a refresh. | `internal/config/config.go` |
| `POST_LOGOUT_REDIRECT_URI` | env | _(unset)_ | no | Absolute URL the BFF asks Keycloak to bounce the browser back to after the SSO logout completes. MUST be on Keycloak's `post_logout_redirect_uris` allowlist. Empty disables the 302-to-Keycloak step (handler returns 204 after revoking the local session). | `internal/config/config.go` |
| `BFF_ENFORCE_CSRF_LOGOUT` | env | `false` | no | Gates the `/auth/logout` handler's CSRF-token check (mi-bm5b). Stays false until both the generic CSRF middleware (mi-gbzs) and the SPA wiring (mi-3vc4) ship; production flips it true once both are live. | `internal/config/config.go` |
| `TRUST_FORWARDED_FOR` | env | `false` | no | Enables `X-Forwarded-For`-based client-IP extraction in the BFF callback (used for the `auth.sessions.ip` forensics column). True only when the ingress strips/normalises the header so a hostile client cannot spoof the value. | `internal/config/config.go` |

`Kind` legend: `env` = process environment variable. New kinds
(`configmap` for non-env keys consumed directly, `flag` for runtime
feature flags, etc.) are added when the loading mechanism actually
diverges; today every setting is an env var.

The `PUBLIC_*` prefix is a convention, not a separate `Kind`. These
are backend env vars like all the others — the prefix marks values
the backend is allowed to ship to the SPA at runtime. The SPA itself
never reads them directly; the backend's `envFrom` pulls them from
`minerals-config` and serves the SPA-facing subset through the
`/api/v1/runtime-config` endpoint (mi-5ew). The split between
non-prefixed and `PUBLIC_*` is the trust boundary: anything without
the prefix MUST NOT be exposed to the browser.

**Prod routing.** In Kubernetes (`kustomize/base/`) the env vars split
into two sources:

- ConfigMap `minerals-config` (`kustomize/base/configmap.yaml`) supplies
  `PORT`, `ADMIN_PORT`, `ENV`, `S3_BUCKET`, `S3_REGION`, `MAX_UPLOAD_BYTES`,
  `LOG_LEVEL`, `S3_ENDPOINT`. The OIDC vars (`OIDC_ISSUER_URL`,
  `OIDC_CLIENT_ID`, `PUBLIC_OIDC_ISSUER_URL`, `PUBLIC_OIDC_CLIENT_ID`,
  `PUBLIC_OIDC_REDIRECT_URI`) are read by the app (backend JWT
  verification wired by mi-aw3a) but are not in the base ConfigMap —
  values are hostname-dependent and supplied by per-env overlays (see
  [`docs/deploy/example/`](./docs/deploy/example/)). `OIDC_JWKS_URL`
  and `OIDC_DISCOVERY_URL` are both left unset in prod (OIDC discovery
  against `OIDC_ISSUER_URL` is the canonical path) — they exist for
  dev stacks where the host-side issuer URL is unreachable from
  inside the backend container.
- Secret `minerals-s3-creds` supplies `S3_ACCESS_KEY_ID` and
  `S3_SECRET_ACCESS_KEY`.
- Secret `minerals-pg-app` (CNPG-managed) supplies `DATABASE_URL` via
  the `uri` key, mapped explicitly in `kustomize/base/deployment.yaml`.
- Optional Secret `minerals-mindat` (operator-provided in the GitOps
  overlay) supplies `MINDAT_API_KEY`. When the Secret is absent, the
  app starts in DB-only mineral-species mode without errors.
- No Secret is required for OIDC. The backend is a pure resource
  server — it validates JWTs against Keycloak's public JWKS endpoint
  and never holds a `client_secret`. The Terraform-provisioned
  `minerals-backend` confidential client exists for future
  service-to-service (Client Credentials) flows, not for the
  user-facing auth path.

The application reads everything as env vars regardless of upstream
source.

## Naming and value conventions

- **SCREAMING_SNAKE_CASE.** No `camelCase` or `kebab-case`.
- **Boolean values are the strings `true` / `false`.** No `1`/`0`, no
  `yes`/`no`, no `on`/`off`.
- **Durations are Go duration strings**: `30s`, `5m`, `1h30m`.
- **Empty string is treated as unset.** Both fall back to the default
  value (in dev) or trigger the strictness check (in prod).
- **Lists are comma-separated**, no whitespace handling beyond
  `strings.TrimSpace` on each entry.

## Loading and validation

- Env vars are loaded **once at startup** in `internal/config/`. There
  is one `Config` struct returned from one constructor, populated by
  exactly one read of `os.Getenv` per variable.
- **Polecats MUST NOT call `os.Getenv` outside `internal/config/`.** If
  a value is needed elsewhere, it's a field on the `Config` struct,
  passed via dependency injection (per CONTRACT.md §7 — no globals).
- The `Config` constructor returns an error on any validation failure
  (malformed URL, unknown enum value, etc.). `main()` exits non-zero
  with a clear message naming the failing variable.

## Production strictness

- When `ENV=prod`, the `Config` constructor:
  - Refuses to fall back to defaults for any variable marked "Required
    in prod" above
  - Returns an error explicitly naming the missing variable
  - Does NOT attempt to "guess" or use `localhost`-style defaults that
    are valid in dev
- When `ENV=dev` or `ENV` is unset, defaults apply normally.
- A polecat MUST NOT add a variable to the inventory marked "required
  in prod" without confirming the `Config` constructor enforces it and
  there's a test exercising the enforcement.

## Adding a new setting

Adding a new tunable — env var, ConfigMap key, feature flag, runtime
knob — IS a contract change. The full procedure (with the rules a PR
must satisfy) lives in CONTRACT.md §15 "Adding a new setting". The
first and mandatory step is to add the row to the inventory above.

**If the setting is a secret** (carries credentials, tokens, API keys,
or anything else that must not appear in plaintext in git), the same
PR must also update [`docs/deploy/secrets.md`](./docs/deploy/secrets.md)
— either adding a row for a new Kubernetes `Secret` or adding a key
to an existing one. `docs/deploy/secrets.md` is the operator-facing
inventory that says how each value gets into the cluster (operator-
sealed via kubeseal, CNPG-generated, cert-manager-generated, etc.).
Future Kind=secret rows in the inventory above should link to the
matching `secrets.md` row; today every row is `Kind=env` and the
secret/non-secret distinction lives in `secrets.md` rather than this
table.

**Secret data key MUST equal the env-var name.** When wiring a
secret-backed env var in `kustomize/base/deployment.yaml`, the
`secretKeyRef.key` MUST match the env-var name verbatim (per
CONTRACT.md §15). The Secret on the operator side stores the value
under that same key. This eliminates a class of silent-empty-value
bugs from key/var name drift (mi-ur0).

```yaml
# Correct: key matches env var name
- name: MINDAT_API_KEY
  valueFrom:
    secretKeyRef:
      name: minerals-mindat
      key: MINDAT_API_KEY
```

The lone sanctioned exception is `DATABASE_URL`, which reads the
CNPG-generated key `uri` because that key name is the CNPG operator's
contract. Do not introduce new exceptions without amending CONTRACT.md
§15.

## Secrets in dev: compose defaults, no `.env` required

- Dev creds (`minerals:minerals` for Postgres, `minioadmin:minioadmin`
  for MinIO) are hardcoded in `docker-compose.yml`. The Go binary's
  defaults match.
- `.env` files are gitignored (per CONTRACT.md §2). The project doesn't
  expect one, but a developer can drop one in for personal overrides.
- **Polecats MUST NOT introduce a project-required `.env.example`** as
  the documented onboarding path. The hardcoded compose defaults are
  the path; an example file would be a parallel source of truth that
  inevitably drifts.

## Secrets in prod (deferred decision)

- v1 deployments inject env vars via Kubernetes `Secret` resources,
  consumed via `envFrom` in the deployment manifest. The operator
  manages the Secret directly. The full inventory of which Secrets
  exist, who reads them, and how each value is provisioned lives in
  [`docs/deploy/secrets.md`](./docs/deploy/secrets.md).
- A future decision on a more durable secret-management strategy
  (Sealed Secrets, External Secrets Operator, Vault, SOPS) is deferred.
  The application doesn't care — secrets reach the binary as env vars
  regardless of the upstream mechanism.
- **A polecat MUST NOT change how secrets reach the binary** (e.g.
  reading a mounted file instead of an env var) without coordination.
  The env-var contract is part of the operator interface.
