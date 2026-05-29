# SPA Decoupling — serve the frontend from a single shared source (mi-zomq)

Status: **in progress.** This document is the design of record for decoupling
the SPA frontend from the backend replicas. The in-repo *foundation* (a
backend API-only serving mode + the `minerals-web` bucket) has landed; the
*deploy/serve* half requires operator decisions and cross-repo (fleet-infra)
work — see "Open decisions" and "Remaining work" below.

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

Package the SPA as a standalone artifact and serve it from a **single shared
source** — a MinIO bucket (`minerals-web`), optionally fronted by a CDN. The
frontend is then decoupled from the N backend replicas: every client gets ONE
consistent build regardless of which backend pod handles API calls.

## Target architecture

```
            ┌──────────────────────────── Ingress ────────────────────────────┐
  client →  │  /            → SPA bundle (MinIO minerals-web, via static layer) │
            │  /assets/*    → SPA bundle (immutable long-cache)                 │
            │  /api/*       → backend Service :8080                             │
            │  /docs,/healthz (admin :9090 never on ingress)                    │
            └──────────────────────────────────────────────────────────────────┘
```

- **Backend** (`WEB_SERVE_MODE=disabled`): serves `/api`, `/docs`, `/healthz`
  only. No embedded frontend, no `/` catch-all. Identical response regardless
  of which replica answers, so replica count / rollout state no longer matter
  for the SPA.
- **SPA bundle**: built once in CI, published to `minerals-web` as a *versioned*
  artifact with correct content-types (`text/javascript` for `.js`, etc.) and
  cache headers (`immutable, max-age=31536000` for `/assets/*`;
  `no-cache`/short for `index.html`).
- **SPA history fallback** (serve `index.html` for unknown client routes) moves
  to the **static layer** (MinIO website hosting error-doc, or the ingress /
  nginx in front of the bucket) — NOT the backend.

## What landed in this repo (foundation)

1. **Backend API-only mode** — `WEB_SERVE_MODE` config (`embedded` default |
   `disabled`). `embedded` keeps v1 behavior (binary serves the embedded
   `dist/`); `disabled` skips the `/` catch-all so the backend is
   API/docs/health only. Wired in `cmd/minerals/serve.go` (`webHandler`) and
   `internal/config/config.go` (`WebServeMode` / `ServeFrontend()`). Default
   preserves current behavior, so this is safe to ship ahead of the serve path
   and reversible per-environment.
2. **`minerals-web` bucket** — added to the MinIO Tenant CRD
   (`kustomize/base/minio.yaml`) to hold the decoupled bundle.
3. **ConfigMap knob** — `WEB_SERVE_MODE: "embedded"` documented in
   `kustomize/base/configmap.yaml`; the prod overlay flips it to `disabled`
   once the serve path is live.

## Open decisions (need operator / architecture input — escalated on mi-zomq)

These are genuine forks, not preferences, and block the deploy/serve half:

1. **How does the bundle get into MinIO?** MinIO is `exposeServices.minio:
   false` (ClusterIP-only) and the `minio.yaml` comments state exposing
   services / ingress is "the GitOps overlay's call." GitHub Actions
   (external) therefore **cannot** push to MinIO directly. Options:
   - **In-cluster publish Job** (recommended): CI builds the bundle and pushes
     it as an OCI artifact / tiny image to GHCR (which CI *can* reach); a
     per-deploy k8s Job (sibling to the existing `migrate` initContainer)
     `mc mirror`s it into `minerals-web` under a versioned prefix. Keeps MinIO
     internal; no security-posture change.
   - **Expose MinIO / use an external bucket or CDN**: simpler CI, but changes
     the network posture — explicitly the GitOps overlay's call.
2. **Serving mechanism / SPA fallback**: MinIO static website hosting (error-doc
   = index.html) vs. a small nginx Deployment fronting the bucket vs. a CDN
   origin. This choice drives CSP (`connect-src`/`script-src`), CORS, and where
   the history fallback lives.
3. **Cross-repo ingress**: the real ingress + overlays live in the separate
   `fleet-infra` GitOps repo (this repo only carries `docs/deploy/example/*`).
   Routing `/`→bundle and `/api/*`→backend must be implemented there.

## Remaining work (tracked for follow-up once decisions land)

- **CI**: build the frontend as a versioned standalone artifact and publish it
  via the chosen path (Job/OCI image). `.github/workflows/main.yml`.
- **Serve/ingress**: stand up the static serving component + ingress routes in
  fleet-infra; preserve the strict CSP (same-origin if under the same host; if a
  CDN/MinIO subdomain, update `connect-src`/`script-src` + CORS).
- **promote-prod** (`mi-04b1`): today digest-pins the single image. After the
  split, promotion must version BOTH the backend image AND the frontend bundle
  in lockstep — otherwise a new skew class (backend vs frontend version
  disagreement) appears. `.github/workflows/promote-prod.yml`.
- **Flip the switch**: set `WEB_SERVE_MODE=disabled` in the prod overlay and
  drop the embedded frontend from the backend image (remove the Dockerfile Node
  stage + `//go:embed`) once the bundle is served externally.
- **Revert the interim mitigation**: once the SPA is decoupled, the prod
  Deployment can return to `RollingUpdate` with `replicas>1` safely.

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
