# §6 — Dev/prod boundary & config

Decided 2026-05-06 in design session.

## Summary

The same Go binary runs in dev and prod; behavior diverges only via env
vars. Dev secrets are hardcoded as compose defaults (no `.env` file
required); prod requires every secret-bearing env var to be set
explicitly, with no silent fallback to dev defaults. Migrations are a
subcommand of the binary (`./minerals migrate up`), never auto-run on
HTTP server startup. The Vite dev server proxies `/api` and `/docs` to
the Go backend so dev and prod are both same-origin and no CORS layer
is ever needed.

## Decisions

### 6.1 — Env var inventory

| Variable | Default (dev) | Required in prod | Purpose |
|---|---|---|---|
| `PORT` | `8080` | no | HTTP listen port |
| `DATABASE_URL` | `postgres://minerals:minerals@localhost:5432/minerals?sslmode=disable` | **yes** | Postgres DSN |
| `S3_ENDPOINT` | `http://localhost:9000` | **yes** | MinIO endpoint URL |
| `S3_ACCESS_KEY_ID` | `minioadmin` | **yes** | MinIO access key |
| `S3_SECRET_ACCESS_KEY` | `minioadmin` | **yes** | MinIO secret key |
| `S3_BUCKET` | `minerals-dev` | **yes** | Bucket name (per §3.1) |
| `S3_REGION` | `us-east-1` | no | Required by AWS SDK; arbitrary for MinIO |
| `MAX_UPLOAD_BYTES` | `104857600` | no | 100 MiB cap (per §3.5) |
| `LOG_LEVEL` | `info` | no | `debug` / `info` / `warn` / `error` |
| `ENV` | `dev` | **yes** | `dev` / `prod`; informational + flips strictness |

- Single-URL form for `DATABASE_URL` (matches CNPG's Secret shape and
  `golang-migrate`'s expected input).
- Names are SCREAMING_SNAKE_CASE.
- Boolean envs are `true` / `false` strings.
- Durations use Go duration strings (`30s`, `10m`).
- Empty string is treated the same as unset (use the default).

### 6.2 — Secrets in dev: compose defaults, no `.env` required

`docker-compose.yml` hardcodes the dev creds; the Go binary's default env
values match. `docker compose up -d` then `make run` and it just works,
no per-developer setup.

```yaml
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: minerals
      POSTGRES_PASSWORD: minerals
      POSTGRES_DB: minerals
    ports: ["5432:5432"]
  minio:
    image: minio/minio:latest
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    ports: ["9000:9000", "9001:9001"]
    command: server /data --console-address ":9001"
```

These dev creds are not secret — they're known defaults on a local-only
port. `.gitignore` still excludes `.env*` so a developer can drop in
overrides if they want, but the project doesn't expect one.

### 6.3 — Strictness flip in prod

When `ENV=prod`:
- The binary refuses to start if any **required** env var is missing
  or empty
- Default values for required vars are NOT applied — there is no
  `minioadmin`-as-fallback in prod
- Error message names the missing variable explicitly (no
  `connection refused` debugging detours)

When `ENV=dev` (or unset): defaults apply normally.

### 6.4 — Migrations: subcommand, not auto-startup

`golang-migrate` runs through a subcommand of the same binary:

```
./minerals serve            # start HTTP server (default subcommand)
./minerals migrate up       # apply pending migrations
./minerals migrate down 1   # roll back one
./minerals migrate version  # report current schema version
./minerals migrate create NAME=...  # scaffold a new pair
```

Migrations live in `migrations/` in golang-migrate's format
(`0001_init.up.sql`, `0001_init.down.sql`).

**Prod deployment shape:** run `./minerals migrate up` as a k8s Job or
initContainer that completes before the app deployment rolls. This is
the operator-friendly pattern.

**On `serve` startup, the app checks the schema version and fails fast**
with a clear error if migrations are pending. It never auto-applies
them. Catches "forgot to run migrate" without silently mutating prod.

**Why not auto-migrate on startup?**
- Multiple replicas race for the migration lock
- A bad migration takes down a whole rollout instead of being caught by
  a separate job
- Loses the ability to roll a binary forward without committing to its
  schema changes
- Operator/initContainer pattern is the well-trodden path

**Documentation requirement:** this needs to be prominent in
CONTRACT.md (§8), with examples of dev workflow, prod Job manifest
shape, and the version-mismatch failure mode.

### 6.5 — Frontend dev: Vite proxy, no CORS

```js
// vite.config.ts
export default defineConfig({
  server: {
    port: 5173,
    proxy: {
      '/api':  { target: 'http://localhost:8080', changeOrigin: true },
      '/docs': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
})
```

Dev: browser hits `localhost:5173` (Vite SPA + HMR); `/api` and `/docs`
requests proxy to `localhost:8080` (Go). Same-origin from the browser's
view — no CORS preflight, no cross-origin cookies, no surprises.

Prod: SPA embedded in Go binary via `embed.FS`. Same server serves both
SPA (at `/`) and API (at `/api/v1/...`). Same-origin.

**Result: no CORS middleware in the Go server, ever.** Dev and prod
behave identically from the browser's perspective.

## Deferred to v2 / later

- **Prometheus metrics endpoint** (`/metrics`). Nice to have for k3s
  observability; not v1.
- **Sentry / external error reporting** (no DSN env var in v1).
- **Feature flags / runtime configuration** beyond env vars (no need
  yet; revisit if prod tuning ever requires it).
- **Per-developer `.env` overrides as the documented path.** The
  hardcoded compose defaults are the intended onboarding path; `.env`
  remains technically supported but not the recommended workflow.
- **Migration dry-run / SQL preview tooling.** Useful but not v1 —
  `golang-migrate`'s native tools are sufficient at our scale.

## Open questions / flags

- **Schema-version mismatch UX on startup.** The error message must
  name the expected vs current versions and tell the operator exactly
  what to run (`./minerals migrate up` for dev, the manifest reference
  for prod). Worth a polecat-level care item, not just a generic
  "migration required" log line.
- **`ENV=prod` strictness check semantics.** Define this once in app
  config code (a single `requiredInProd` set) — handlers shouldn't
  re-check. Goes in CONTRACT.md as a rule.
- **`docker-compose.yml` location.** Repo root is the natural place
  (`./docker-compose.yml`); make sure dev README documents the workflow.
- **MinIO bucket creation in dev.** `minerals-dev` doesn't exist when
  the compose container first comes up. Either: (a) the Go binary
  creates the bucket if missing on startup (idempotent S3 `CreateBucket`
  call); (b) a `make seed` target invokes `mc mb` against the running
  MinIO. Recommendation: (a) — fewer moving parts, single command flow.
  Only happens in dev (prod bucket is operator-provisioned).
- **Frontend embed at prod-build time.** The multi-stage Dockerfile
  must build the SPA first (Node stage), copy `dist/` into the Go build
  context, then `go build` consumes it via `embed.FS`. This is §7
  territory but flagged here so the dev/prod parity story is complete.
