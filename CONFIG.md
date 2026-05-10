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

`Kind` legend: `env` = process environment variable. New kinds
(`configmap` for non-env keys consumed directly, `flag` for runtime
feature flags, etc.) are added when the loading mechanism actually
diverges; today every setting is an env var.

**Prod routing.** In Kubernetes (`kustomize/base/`) the env vars split
into two sources:

- ConfigMap `minerals-config` (`kustomize/base/configmap.yaml`) supplies
  `PORT`, `ENV`, `S3_BUCKET`, `S3_REGION`, `MAX_UPLOAD_BYTES`,
  `LOG_LEVEL`, `S3_ENDPOINT`.
- Secret `minerals-s3-creds` supplies `S3_ACCESS_KEY_ID` and
  `S3_SECRET_ACCESS_KEY`.
- Secret `minerals-pg-app` (CNPG-managed) supplies `DATABASE_URL` via
  the `uri` key, mapped explicitly in `kustomize/base/deployment.yaml`.
- Optional Secret `minerals-mindat` (operator-provided in the GitOps
  overlay) supplies `MINDAT_API_KEY`. When the Secret is absent, the
  app starts in DB-only mineral-species mode without errors.

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
  manages the Secret directly.
- A future decision on a more durable secret-management strategy
  (Sealed Secrets, External Secrets Operator, Vault, SOPS) is deferred.
  The application doesn't care — secrets reach the binary as env vars
  regardless of the upstream mechanism.
- **A polecat MUST NOT change how secrets reach the binary** (e.g.
  reading a mounted file instead of an env var) without coordination.
  The env-var contract is part of the operator interface.
