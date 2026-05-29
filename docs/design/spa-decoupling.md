# SPA Decoupling — serve the frontend from a single shared source (mi-zomq)

Status: **serve half landed (mi-gpyc).** This document is the design of record
for decoupling the SPA frontend from the backend replicas. The in-repo
*foundation* (a backend API-only serving mode) landed in mi-zomq; the in-repo
*serve* half — a minimal **nginx-only container** that serves the SPA, its
kustomize Deployment/Service, the CI to build/publish it, and lockstep
promotion — landed in mi-gpyc. The only remaining piece is **cross-repo**: the
operator wires the ingress split in fleet-infra (a proposed shape ships as
`docs/deploy/example/prod/ingress-split.yaml`). Cloudflare fronting is a later,
drop-in step. See "Chosen direction" and "Remaining work" below.

## Problem (diagnosed 2026-05-26)

cabinet.rocks served a blank page in prod. Root cause: the SPA build is
**embedded per-pod** in the backend image, and prod runs `replicas=2`. The two
pods were on different image versions (stuck/partial rollout), each with a
different content-hashed bundle:

- Pod A: `index.html` → `/assets/index-zUB9exxa.js` (only has that asset)
- Pod B: `index.html` → `/assets/index-DnPEbxGS.js` (only has that asset)

The load balancer round-robins. A browser fetches `index.html` from one pod but
its `/assets/index-<hash>.js` request lands on the other pod, which doesn't have
that hash → 404 → the SPA catch-all returns `index.html` with
`Content-Type: text/html` → "Failed to load module script: MIME type text/html"
→ blank page.

This is **fundamental** to multi-replica + per-pod embedded SPA: even a clean
RollingUpdate has a window where an old pod serves `index.html` and a new pod
serves assets (or vice-versa). Asset hashes are version-specific, so any
cross-version mix is fatal.

### Interim mitigation (already applied, NOT the real fix)

The operator switched the prod Deployment to the `Recreate` strategy so the two
replicas never run mixed versions simultaneously (brief downtime on deploy, no
cross-version skew). This unblocks prod until this work lands.

## Chosen direction (operator decision)

Serve the SPA from a **single shared source**: a separate, minimal
**nginx-only container** (`minerals-web`) that ships the built `dist/` and
nothing else. The frontend is then decoupled from the N backend replicas:
every client gets ONE consistent build regardless of which backend pod handles
API calls.

**Why an nginx container, not the MinIO bucket** (mi-gpyc resolved the mi-zomq
open decisions): mi-zomq's foundation added a `minerals-web` MinIO bucket as a
*possible* serving path, but serving from MinIO needed either an in-cluster
publish Job (`mc mirror` on every deploy) or exposing MinIO / a CDN — extra
moving parts and a network-posture change. A standalone nginx image is simpler:
CI already builds container images, the bundle is just `COPY dist/` into
`nginx:alpine`, the image is versioned and promoted exactly like the backend,
and history-fallback + MIME + cache headers are a few lines of `nginx.conf`.
MinIO stays ClusterIP-only and untouched. **The `minerals-web` bucket is
therefore dropped** from `kustomize/base/minio.yaml`; it was the artifact of
the MinIO-serving path this approach replaces.

## Target architecture

```
            ┌──────────────────────────── Ingress ────────────────────────────┐
  client →  │  /            → minerals-web Service :8080 (nginx; SPA fallback)  │
            │  /assets/*    → minerals-web Service :8080 (immutable long-cache) │
            │  /api/*,/auth → backend Service :8080                             │
            │  /docs,/healthz,/readyz → backend Service :8080                   │
            │  (admin :9090 never on ingress)                                   │
            └──────────────────────────────────────────────────────────────────┘
```

- **Backend** (`WEB_SERVE_MODE=disabled`): serves `/api`, `/auth`, `/docs`,
  `/healthz`, `/readyz` only. No embedded frontend, no `/` catch-all. Identical
  response regardless of which replica answers, so replica count / rollout
  state no longer matter for the SPA.
- **minerals-web** (`nginx:alpine` + `dist/`): built once per commit in CI,
  published as a *versioned* multi-arch image. `nginx.conf` sets correct
  content-types (`text/javascript` for `.js` via stock mime.types — the fix
  for the original blank-page MIME bug), cache headers (`immutable,
  max-age=31536000` for `/assets/*`; `no-cache` for `index.html`), gzip, and
  the same strict CSP the backend emits. SINGLE replica + `Recreate` strategy
  (`kustomize/base/web.yaml`) so two SPA versions never serve at once.
- **SPA history fallback** (serve `index.html` for unknown client routes) lives
  in nginx (`try_files $uri $uri/ /index.html`) — NOT the backend. Hashed
  `/assets/*` that miss return 404 (never fall back to HTML), which is what
  prevents the MIME/blank-page failure mode.
- **Same origin**: the ingress puts the SPA and `/api` on one host, so the
  backend's strict `connect-src 'self'` CSP holds with no relaxation.

## What landed in this repo

**Foundation (mi-zomq):**

1. **Backend API-only mode** — `WEB_SERVE_MODE` config (`embedded` default |
   `disabled`). `embedded` keeps v1 behavior (binary serves the embedded
   `dist/`); `disabled` skips the `/` catch-all so the backend is
   API/docs/health only. Wired in `cmd/minerals/serve.go` (`webHandler`) and
   `internal/config/config.go` (`WebServeMode` / `ServeFrontend()`). Default
   preserves current behavior, so this is safe to ship ahead of the serve path
   and reversible per-environment.

**Serve half (mi-gpyc):**

2. **minerals-web image** — `Dockerfile.web` (Node build stage → `nginx:alpine`
   final stage with `dist/` only) + `web/nginx.conf` (history fallback, MIME,
   cache headers, gzip, strict CSP; runs unprivileged on :8080 with temp/pid
   under `/tmp`).
3. **Deployment + Service** — `kustomize/base/web.yaml`: `minerals-web`,
   single replica, `Recreate`, read-only-root / non-root securityContext, tiny
   resources. A distinct `app.kubernetes.io/component` label keeps the backend
   and web Services' selectors disjoint despite the base's shared `app:
   minerals` label.
4. **CI** — `main.yml` builds + pushes `minerals-web` multi-arch
   (`latest`/`staging`/`sha-<short>`) alongside the backend image.
   `promote-prod.yml` versions BOTH images in lockstep (retag + digest-pin),
   closing the backend-vs-frontend skew class the split would otherwise add.
5. **Bucket dropped** — the `minerals-web` MinIO bucket (added by the
   foundation as a possible serving path) is **removed** from
   `kustomize/base/minio.yaml`; the nginx container replaces it.
6. **Prod overlay example** — `docs/deploy/example/prod/mineral.yaml` sets
   `WEB_SERVE_MODE: disabled`, adds the web image to the digest-pinned
   `images[]`, and `ingress-split.yaml` proposes the split routing.

## Resolved decisions (mi-gpyc)

The mi-zomq open decisions are resolved by the nginx-container choice:

1. **Bundle delivery** — no MinIO publish path needed; the bundle is a
   container image built and pushed by existing CI. MinIO is untouched.
2. **Serving mechanism / SPA fallback** — a minimal nginx Deployment; fallback
   lives in `nginx.conf`. Same-origin keeps CSP strict (`connect-src 'self'`),
   no CORS.

Still cross-repo (operator):

3. **Ingress split** — the real ingress lives in the separate `fleet-infra`
   GitOps repo. Routing `/` + `/assets/*` → `minerals-web` and
   `/api|/auth|/docs|/healthz|/readyz` → backend must be applied there; a
   proposed shape ships as `docs/deploy/example/prod/ingress-split.yaml`.

## Remaining work

- **Operator: apply the split in fleet-infra** — copy `ingress-split.yaml`,
  set `WEB_SERVE_MODE=disabled` on the prod overlay, bootstrap the
  `minerals-web` digest pin. Until then prod stays `embedded` (safe).
- **Flip the switch + slim the backend image**: after the split is live, the
  backend image can drop the embedded frontend (remove the `Dockerfile` Node
  stage + `//go:embed`). Deferred so local/default mode keeps working until the
  external serve path is proven in prod.
- **Revert the interim mitigation**: once decoupled, the backend Deployment can
  return to `RollingUpdate` with `replicas>1` safely.
- **Cloudflare (later, drop-in)**: front the ingress as origin to offload the
  homelab. The origin routing above is unchanged; only DNS + an origin cert
  move. Not implemented now.

## Acceptance (from mi-zomq)

- SPA served from a single shared source (MinIO/CDN); `index.html` + assets
  always version-consistent regardless of backend replica count or rollout
  state.
- Backend image no longer embeds the frontend; serves API/docs/health only.
- GHA builds + publishes both artifacts with correct content-types + cache
  headers.
- A backend rolling update (or `replicas>1`) no longer breaks the SPA.
- promote-prod handles both artifacts coherently.
- Strict CSP intact; no MIME/blank-page regressions.
