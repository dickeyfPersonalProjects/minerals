# §7 — Build, embed, observability

Decided 2026-05-06 in design session.

## Summary

Container images are published to GitHub Container Registry under the
`dickeyfPersonalProjects` org. Logs are stdlib `log/slog` JSON to
stdout, with per-request middleware that attaches a request id, method,
path, status, duration, and user id (from auth context — stable when
real auth lands). Health endpoints are split: `/healthz` is process-only
liveness; `/readyz` checks Postgres, MinIO, and schema version.
Container image is built in three stages — Node for SPA, Go for backend
(consuming the SPA via `embed.FS`), distroless static nonroot for
runtime. Prometheus metrics are deferred to v2.

## Decisions

### 7.1 — Container registry: ghcr.io

```
ghcr.io/dickeyfpersonalprojects/minerals:{tag}
```

- Tags: `latest` (rolling), `vX.Y.Z` (releases), `sha-{short}` (every
  commit on main)
- Image is **public** — nothing sensitive embedded; public images get
  free unlimited pulls and free CDN
- Auth shares the GitHub repo's identity — no separate registry account

### 7.2 — Logging: slog JSON, stdout, request-scoped attributes

- Format: structured JSON, one record per line
- Destination: stdout (k3s log collectors handle it)
- Level: `LOG_LEVEL` env var (per §6.1), default `info`
- Per-request middleware attaches: `request_id`, `method`, `path`,
  `status`, `duration_ms`, `user_id`

```json
{"time":"2026-05-06T10:23:11Z","level":"INFO","msg":"request",
 "request_id":"01H...","method":"GET","path":"/api/v1/specimens",
 "status":200,"duration_ms":12,
 "user_id":"00000000-0000-0000-0000-000000000001"}
```

- `request_id` is a ULID generated in middleware, attached to context,
  and echoed back as the `X-Request-Id` response header
- `user_id` comes from `auth.FromContext(r.Context()).ID` (per §5).
  Stable from day one — when real auth replaces the stub, the log
  schema is unchanged. This is a key payoff of the auth-slot pattern.

### 7.3 — Health endpoints: split liveness from readiness

- **`GET /healthz`** — liveness. Returns 200 with body `ok` if the
  process is running. **No dependency checks.** Restarting on a DB
  blip would just create a restart loop.
- **`GET /readyz`** — readiness. Returns 200 only when:
  - HTTP server has finished startup
  - Postgres `SELECT 1` succeeds (2s timeout)
  - MinIO `HeadBucket` against the configured bucket succeeds (2s
    timeout)
  - Schema version matches the binary (per §6.4)

  On failure, returns 503 with a per-check JSON body so an operator can
  see exactly what's broken:
  ```json
  {
    "ready": false,
    "checks": {
      "database": { "ok": true },
      "storage":  { "ok": false, "error": "head bucket: connection refused" },
      "schema":   { "ok": true, "version": 7 }
    }
  }
  ```
- No caching of the readyz result in v1. Personal-app traffic is too
  low for the per-request cost to matter; revisit if probe volume ever
  becomes meaningful.

### 7.4 — Multi-stage Dockerfile, distroless static nonroot

```dockerfile
# Stage 1: build the SPA
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build       # produces frontend/dist/

# Stage 2: build the Go binary, embedding dist/
FROM golang:1.23-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION:-dev}" \
    -o /out/minerals \
    ./cmd/minerals

# Stage 3: distroless runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /out/minerals /minerals
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/minerals"]
CMD ["serve"]
```

Key choices:
- **`distroless/static-debian12:nonroot`** — no shell, no package
  manager, ~15-25 MB final image
- **`CGO_ENABLED=0`** — pure-static binary, works on `static`
  distroless. Constrains us to pure-Go libraries (the §3.4 EXIF library
  `dsoprea/go-exif/v3` qualifies; if HEIC handling ever needs cgo,
  we'll revisit the base image)
- **`-trimpath -ldflags="-s -w"`** — reproducible builds, no debug
  symbols
- **`-X main.version=...`** — version baked at build time, surfaced via
  startup log line and the `/readyz` schema check
- **`USER nonroot`** — distroless nonroot variant pre-creates UID 65532
- **`ENTRYPOINT` + `CMD` split** — operators override `CMD` to
  `["migrate", "up"]` for the migration Job (per §6.4)

### 7.5 — SPA embed via `embed.FS`

```go
// internal/web/web.go
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

The Go server mounts `web.Dist` as the static handler at `/`, with
fallback to `index.html` for unknown paths (so SPA client-side routing
works).

### 7.6 — Dev iteration is Docker-free

`docker compose up -d` brings up Postgres + MinIO; `make run` runs the
Go binary natively. The Dockerfile is only exercised for image builds.
No need to rebuild containers on every code change.

## Deferred to v2 / later

- **Prometheus metrics endpoint** (`/metrics`). Useful for k3s
  observability; not v1.
- **Distributed tracing** (OpenTelemetry). Single-binary app — tracing
  buys little until there are multiple services to correlate across.
- **External error reporting** (Sentry / similar). Personal-scale; logs
  are sufficient for now.
- **Pretty dev logs** — a tty-detecting `slog` handler that prints
  human-readable text in dev only. The JSON-everywhere baseline is
  fine for v1.
- **`/readyz` result caching** with a short TTL. Add when probe volume
  justifies the complexity.
- **Build attestations / SBOM publishing**. Worth revisiting if/when
  the image is ever consumed beyond your own cluster.
- **Multi-arch image builds** (amd64 + arm64). Single-arch is fine
  until you actually deploy to arm64.

## Open questions / flags

- **`CGO_ENABLED=0` constrains library choices.** Most pure-Go
  libraries we need are fine (`dsoprea/go-exif`, `pgx`, AWS SDK Go v2,
  `golang-migrate`). HEIC processing might require cgo (`libheif`) —
  if so, we either switch the base to `distroless/base` (still small)
  or pre-convert HEIC to JPEG before processing. Decision deferred to
  the polecat implementing photo upload.
- **Version string source.** `${VERSION}` in the Dockerfile build arg
  should default to `dev` for local builds, and CI should set it to
  `vX.Y.Z` or the git short SHA on real builds. Wiring belongs in the
  CI configuration that lands later.
- **Image size budget.** Distroless static + a Go binary should land
  20-30 MB. If embed.FS gradually inflates the binary past ~100 MB
  (lots of large frontend assets), we revisit splitting the SPA out
  to a sidecar nginx — but unlikely at v1 scale.
- **`.dockerignore`** must exclude `bin/`, `node_modules/`, `.git/`,
  and `.dolt-data/` to keep the build context small. Polecat-level
  detail; flagging here so it's not forgotten.
- **Health endpoint paths in production.** k3s health probes need to
  be configured against `/healthz` and `/readyz`. The k3s manifest is
  outside this repo (you write it separately), so this is informational
  rather than a code concern.
