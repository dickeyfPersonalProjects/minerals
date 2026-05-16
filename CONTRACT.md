# CONTRACT.md

The minerals project's operational rulebook. Read end-to-end on first
onboarding; consult the relevant section before touching unfamiliar
territory.

The reasoning behind these rules lives in
[`docs/design/01-07.md`](docs/design/) — the records of the design
session that produced this contract. CONTRACT.md tells you **what to
do**; the design docs tell you **why we chose to do it that way**.

## Table of contents

1. [Purpose & scope of this document](#1--purpose--scope-of-this-document)
2. [Repository layout](#2--repository-layout)
3. [Local development workflow](#3--local-development-workflow)
4. [Build & release](#4--build--release)
5. [Continuous integration (GitHub Actions)](#5--continuous-integration-github-actions)
6. [Database migrations](#6--database-migrations)
7. [Code conventions](#7--code-conventions)
8. [Time, locale & encoding](#8--time-locale--encoding)
9. [Testing requirements](#9--testing-requirements)
10. [API contract rules](#10--api-contract-rules)
11. [Data layer rules](#11--data-layer-rules)
12. [File & storage rules](#12--file--storage-rules)
13. [Auth rules](#13--auth-rules)
14. [Logging & observability](#14--logging--observability)
15. [Configuration & env vars](#15--configuration--env-vars)
16. [Dependencies & libraries](#16--dependencies--libraries)
17. [Security never-do list](#17--security-never-do-list)
18. [Git, commits, PRs](#18--git-commits-prs)
19. [Polecat workflow & definition of done](#19--polecat-workflow--definition-of-done)
20. [References & glossary](#20--references--glossary)

---

# §1 — Purpose & scope of this document

## What this is

CONTRACT.md is the operational rulebook for working in the minerals
codebase. It tells contributors — polecats, mayors, and human operators —
what they MUST do, MAY do, and MUST NOT do when adding code, running the
app, or shipping changes.

It is the canonical reference for "what does this project expect."

## What this is NOT

This document does not explain *why* decisions were made. The reasoning
behind the architecture lives in `docs/design/01-07.md` — the records of
the design session that produced this contract. If you want to understand
why a rule exists, follow the cross-references into the design docs.
If you want to know what the rule *is*, read CONTRACT.md.

## Who reads it

- **Polecats** picking up a bead — read end-to-end on first onboarding;
  consult relevant section before touching unfamiliar territory.
- **Mayors** coordinating work — reference when slinging a bead, to
  confirm the requested change is contract-compliant.
- **Human operators** deploying or maintaining the app — sections 3
  (Local development), 4 (Build & release), 6 (Database migrations),
  and 15 (Configuration & env vars) are the load-bearing ones for
  operators.

## Authority

When CONTRACT.md and a design doc disagree:

- Design docs describe **intent at the time of the design session**.
- CONTRACT.md describes **current rules in force**.
- Where a rule has evolved past the design doc, CONTRACT.md is
  authoritative. Update the design doc with a "Superseded by
  CONTRACT.md §X on YYYY-MM-DD" note rather than rewriting it — the
  design history matters.

When CONTRACT.md and the code disagree, the code is wrong. Fix the code
(or, if the rule no longer makes sense, propose a CONTRACT.md change
through a PR — don't drift silently).

## Lifecycle

CONTRACT.md is a living document. Changes go through normal PR review.
A change to a contract rule is a meaningful event — the PR description
should explain what changed and why, and (if applicable) what migrations
or fixups are needed in existing code.

## Cross-references

The design records this contract was synthesized from:

- [§1 — Scope & v1 cut line](docs/design/01-scope.md)
- [§2 — Domain model](docs/design/02-domain-model.md)
- [§3 — Photo & file handling](docs/design/03-files-and-photos.md)
- [§4 — API shape](docs/design/04-api-shape.md)
- [§5 — Auth slot design](docs/design/05-auth-slot.md)
- [§6 — Dev/prod boundary & config](docs/design/06-dev-prod-config.md)
- [§7 — Build, embed, observability](docs/design/07-build-embed-observability.md)

The agenda bead that drove the design session: `hq-8h4`.

# §2 — Repository layout

## Top-level structure

```
.
├── CONTRACT.md                      # this document
├── README.md                        # short orientation; points to CONTRACT.md
├── Makefile                         # build/run/test/migrate targets
├── Dockerfile                       # multi-stage; produces ghcr image
├── docker-compose.yml               # dev Postgres + MinIO
├── .dockerignore
├── .gitignore
├── go.mod
├── go.sum
├── cmd/
│   └── minerals/
│       └── main.go                  # entry point + subcommand dispatch
├── internal/
│   ├── api/                         # HTTP layer: handlers, routing, middleware
│   ├── auth/                        # User type, FromContext, middleware stub
│   ├── config/                      # env var loading, prod-strictness check
│   ├── db/                          # Postgres connection + repository impls
│   ├── domain/                      # core types and business logic
│   ├── storage/                     # MinIO/S3 client and operations
│   └── web/
│       └── dist/                    # SPA build output (populated at Docker build time)
├── migrations/                      # golang-migrate SQL files
│   ├── 0001_init.up.sql
│   └── 0001_init.down.sql
├── frontend/
│   ├── package.json
│   ├── vite.config.ts
│   ├── tsconfig.json
│   ├── index.html
│   └── src/
│       ├── main.ts                  # entry
│       ├── App.svelte               # root component
│       ├── lib/                     # shared utilities, API client
│       ├── components/              # reusable components
│       └── routes/                  # client-side routes (if any)
├── docs/
│   └── design/
│       ├── 01-scope.md
│       ├── 02-domain-model.md
│       ├── ...
│       └── 07-build-embed-observability.md
└── bin/                             # local build output (gitignored)
```

## Where new code goes

### Backend (Go)

- **`cmd/minerals/`** — entry point and subcommand dispatch ONLY.
  Subcommand implementations (`serve`, `migrate`) live here as small
  files that wire `internal/` packages together. No business logic.
- **`internal/api/`** — HTTP handlers, router setup, middleware
  (request_id, logging, auth, panic recovery). Handlers depend on
  `internal/domain` and `internal/db`; never import each other.
- **`internal/auth/`** — `User` type, `FromContext`, the auth middleware.
  See §13 (Auth rules). Single package, do not split until real auth
  lands.
- **`internal/config/`** — env var loading, `ENV=prod` strictness check
  (per design §6.3). Single struct returned from one constructor.
- **`internal/db/`** — Postgres connection pool setup and repository
  implementations (one per domain entity: `SpecimenRepo`, `PhotoRepo`,
  etc.). Repository interfaces are defined in `internal/domain`, not
  here.
- **`internal/domain/`** — core types (`Specimen`, `Photo`,
  `JournalEntry`, `Collector`, `File`), `TypeData` structs
  (`MineralData`, `RockData`, `MeteoriteData`), and repository
  **interfaces**. No SQL here, no HTTP here. Pure types + business
  logic.
- **`internal/storage/`** — MinIO/S3 client wrapper. Upload, download,
  variant generation, EXIF allowlist filtering. Used by `internal/api`
  upload handlers.
- **`internal/web/`** — `embed.FS` host. The `dist/` subdirectory is
  populated by the Dockerfile's frontend stage; in dev it may be empty
  (the SPA is served by Vite, not the Go binary).

Polecats MAY introduce new sub-packages within `internal/` if a clear
seam emerges (e.g. `internal/exif/` extracted from `internal/storage/`
once it grows). Polecats MUST NOT introduce a new top-level directory
without coordination — the layout above is the contract surface.

### Frontend (Svelte/TS)

- **`frontend/src/lib/`** — shared TS modules, including the OpenAPI-
  generated API client. The client is generated, not hand-written;
  see §10 (API contract rules) for regeneration workflow.
- **`frontend/src/components/`** — reusable Svelte components.
- **`frontend/src/routes/`** — client-side route components if you adopt
  a router (e.g. `svelte-spa-router`). Optional in v1; flat
  `App.svelte` acceptable until routing exists.

### Migrations

- **`migrations/`** at repo root — golang-migrate format only:
  `NNNN_description.up.sql` and `NNNN_description.down.sql` pairs.
  See §6 (Database migrations) for naming and ordering rules.

### Documentation

- **`docs/design/`** — frozen records of design decisions. Do NOT edit
  past records to reflect later changes; if a design changes,
  supersede it via a CONTRACT.md update (§1 Authority).
- **Other `docs/`** subdirectories MAY be added (e.g.
  `docs/runbooks/`) as the project matures.

## Files that MUST NOT exist in tracked source

- Any `.env` file (gitignored; per §15, dev defaults are in compose)
- Any binary, archive, or build output (`bin/`, `dist/`, `*.test`,
  `*.out`, `node_modules/`)
- Any image/photo/file from your collection — production data does
  not belong in the repo

## Generated files

- **`frontend/src/lib/api/`** (or wherever the API client lands) is
  generated from the OpenAPI spec. Do not hand-edit; regenerate.
- **`go.sum`** is generated by `go mod tidy`; commit it but never
  edit by hand.

When a generated file is committed to the repo, the workflow that
regenerates it MUST be in the Makefile (e.g. `make gen-api-client`).

## .gitignore discipline

The `.gitignore` is the enforcement mechanism for the "must not exist
in tracked source" list above. The two MUST stay in sync.

### Baseline patterns (the v1 scaffold ships these)

- **Build artifacts**: `/minerals`, `/bin/`, `/dist/`
- **Go test/coverage output**: `*.test`, `*.out`, `coverage.txt`,
  `coverage.html`
- **Environment files**: `.env`, `.env.local`, `.env.*.local`
- **Frontend dependencies**: `node_modules/`
- **Editor / OS noise**: `.DS_Store`, `.idea/`, `.vscode/`, `*.swp`

### When to update `.gitignore`

A polecat MUST update `.gitignore` when:

- Introducing a new build-output directory or file
  (e.g. `/coverage/`, `/.gen/`)
- Introducing a new generated artifact that is committed to the repo
  intentionally — make sure the `.gitignore` does NOT shadow it (a
  common bug)
- Adding a new tool whose state directory should be excluded
  (e.g. `.cache/`, `__pycache__/`)

A polecat MUST NOT add patterns that:

- Hide files the project actually needs to track (e.g. don't
  blanket-ignore `*.json` because one config happens to be JSON)
- Are personal / per-developer concerns — those belong in
  `.git/info/exclude` or a global `~/.gitignore_global`, not in the
  repo's tracked `.gitignore`. Examples: editor-specific scratch files,
  your own `notes.md`, OS-specific paths your laptop happens to
  produce.

### Rules of thumb

- If a file would never be useful to anyone else who clones the repo,
  it belongs ignored.
- If a file is generated and re-derivable from tracked source, it
  belongs ignored.
- If a file contains secrets or per-developer state, it belongs
  ignored AND is fail-loudly rejected by code review if it sneaks
  into a commit.
- The `.gitignore` is a public document — anyone reading the repo
  can see what's excluded. Don't put anything in there that itself
  reveals sensitive structure or naming.

## .dockerignore discipline

`.dockerignore` controls what files get sent to the Docker daemon as
build context. It is NOT a duplicate of `.gitignore` — it serves a
different purpose and SHOULD be MORE restrictive in most cases.

Three things `.dockerignore` buys us:

1. **Build speed** — every file in the build context is streamed to
   the daemon. A bloated context slows every build.
2. **Cache invalidation hygiene** — touching a file outside the
   context (e.g. editing a design doc) shouldn't invalidate Docker
   layer caches for the application build.
3. **Image security** — files in the build context can end up in
   intermediate stage layers (e.g. our `backend` stage has all
   source). Secrets in the build context = secrets in cached layers
   = potential leak through `docker history` if those layers ever
   get pushed.

### Baseline patterns (the v1 scaffold ships these)

```
# VCS and CI metadata
.git/
.github/
.gitignore

# Build outputs
/bin/
/dist/
frontend/dist/
internal/web/dist/

# Dependencies (rebuilt inside container stages)
node_modules/
frontend/node_modules/

# Test and coverage artifacts
*.test
*.out
coverage.txt
coverage.html

# Secrets / env files
.env
.env.local
.env.*.local

# Editor / OS noise
.DS_Store
.idea/
.vscode/
*.swp

# Documentation (not needed at build time)
docs/
*.md

# Gas Town local state
.dolt-data/
.beads/
```

### What MUST stay in the build context (NOT ignored)

- `cmd/`, `internal/` — Go source
- `migrations/` — SQL files embedded into the binary at build time
- `frontend/` (the directory itself — only `frontend/node_modules/`
  and `frontend/dist/` are excluded; the source must be present for
  the Node stage to build it)
- `go.mod`, `go.sum`, `Dockerfile`, `Makefile` — build inputs
- `frontend/package.json`, `frontend/package-lock.json`,
  `frontend/vite.config.ts`, `frontend/tsconfig.json`,
  `frontend/index.html`, `frontend/src/`

### When to update `.dockerignore`

A polecat MUST update `.dockerignore` when:

- Introducing a new generated artifact, dependency directory, or
  build output that's not already covered
- Adding tooling that creates state directories during local
  development (e.g. `.cache/`, `tmp/`)
- Adding any file that contains secrets or per-developer state —
  even if it's also gitignored, defense-in-depth says exclude it
  from the build context

A polecat MUST NOT add patterns that:

- Exclude source code, migrations, or build inputs the Dockerfile
  actually needs — the build will fail mysteriously, and the
  failure mode (`COPY` finds nothing where files were expected) is
  surprisingly hard to diagnose
- Use overly broad patterns like `*.json` (would exclude
  `package.json`) or `*` with narrow re-includes

### Rule of thumb

If you would not include a file in the binary's build inputs OR
want to invalidate the build cache when it changes, ignore it.
Default to ignoring; the build will tell you loudly if you ignored
too much.

### Why excluding `docs/` and `*.md` is safe

The final distroless image contains only the binary — no source,
no migrations, no docs. Docs are excluded purely to avoid cache
invalidation on doc-only commits. The README and CONTRACT.md are
useful for contributors browsing the repo on GitHub, but they're
not build inputs.

## README.md discipline

The repo's `README.md` exists as a fast onboarding pointer, NOT a
duplicate of CONTRACT.md or the design docs. Four rules govern it:

- **Every command in `README.md` MUST be verified to work on a fresh
  clone** before merge. A command that doesn't work is worse than no
  command — it lies to a fresh contributor about how to start. Manual
  verification by the polecat (or operator) is the gate; automated
  smoke testing in CI is a deferred improvement (see below).
- **The README's role is orientation + quickstart, then deferral to
  CONTRACT.md.** It is NOT the place for design rationale, contract
  rules, or operator runbooks. If a section starts to feel like a
  CONTRACT.md chapter, move it to CONTRACT.md and leave a one-line
  pointer in the README.
- **Onboarding flow changes mandate README updates in the same PR.**
  When env vars (per §15.4), Make targets, required tools, port
  numbers, default credentials, or any "what does a fresh contributor
  type to get the app running" fact changes, the README MUST be
  updated in the same PR or the PR description MUST explicitly state
  that the change does not affect onboarding flow. A reviewer can
  refuse to merge a PR that changes onboarding without addressing
  README.
- **Brevity is a feature.** A README that grows past ~150 lines is
  signaling that material belongs in CONTRACT.md or a dedicated doc.
  Aim for: project name, one-paragraph "what is this," quickstart,
  pointer block.

A polecat MUST NOT close a bead that touches onboarding without
either updating README in the same PR or noting in the PR description
that README is unaffected.

### Deferred to v2

- Automated smoke-testing of README quickstart commands in CI (a job
  that bootstraps from scratch and runs the documented commands).
  Until this lands, manual verification on a fresh clone is the
  required gate. **Partially closed** by mi-7r2: the `compose-smoke`
  job in `pr.yml` and `main.yml` exercises `docker compose up -d`
  against the committed `docker-compose.yml` and validates Postgres +
  MinIO come up healthy, so README drift on the compose layer no
  longer slips past CI. The remaining piece — running every
  README-documented command (`make migrate-up`, `make run`, etc.) —
  is still deferred.

## Infrastructure-as-code layout

This repo declares **what** to run; environment-specific deployment
configuration lives in a separate GitOps repo. Two locations only:

- **`docker-compose.yml`** at the repo root — single file declaring
  the dev services (Postgres, MinIO, and the `app` build; see §3 for
  the two operating modes). At the root because that's where
  `docker compose up -d` and `podman compose up -d` look by default;
  moving it forces every contributor to type `-f path/...` in every
  command. The file is dev-only; not used in production.
- **`kustomize/base/`** at the repo root — Kustomize base manifests
  for the app's Kubernetes resources: CNPG `Cluster`, MinIO
  `Tenant`, app `Deployment` (with a migrate initContainer per §6.4),
  `Service`, and `ConfigMap`. **Base ONLY**: no overlays, no patches,
  no environment-specific values, no Namespace, no Ingress, no
  Secrets — those live in the operator's GitOps repo. The base
  references two external Secrets by name (`minerals-minio-config`
  and `minerals-s3-creds`); the GitOps overlay provides them via
  kubeseal.

### Why no Kustomize overlays in this repo

Environment-specific deployment (dev cluster, prod cluster, any
future variants) lives in the operator's separate **GitOps repo**.
That repo references this repo's `kustomize/base/` as a remote base,
applies overlays, and injects values (image tags, replica counts,
ingress hostnames, secrets references) per environment.

This separation:
- Keeps secrets and environment-specific URLs out of the app repo
- Lets the app repo stay public-friendly (no infra exposure)
- Allows the same base to power any number of environments without
  forks
- Aligns with standard GitOps practice (Argo CD, Flux): app source +
  base manifests in one repo, deployment overlays in another

A polecat MUST NOT add `kustomize/overlays/`, `deploy/`, `helm/`,
`terraform/`, or any other deployment-shape directory to this repo
without coordination. If a directory like that becomes necessary,
it's a contract change requiring its own PR.

### Forbidden alternatives

- **`dev/docker-compose.yml`** or `infra/compose.yaml` — adds a
  required `-f` flag to every dev invocation. Do not.
- **`kustomize/overlays/dev/`** or `kustomize/overlays/prod/` —
  these are the GitOps repo's concern, not this repo's.
- **`docker-compose.prod.yml`** or any production Compose variant —
  production runs on k3s via Kustomize; there is no production
  Compose deployment in this project.

# §3 — Local development workflow

This section is for anyone bringing the app up locally for the first
time, and for the daily inner loop after that.

## Prerequisites

- **Go 1.23+** (matches `go.mod`)
- **Node 22+** (matches the Node stage in the Dockerfile)
- **Docker** with Compose v2 (i.e. `docker compose`, not the legacy
  `docker-compose`)
- **make** (for Makefile targets)
- **git**

No other native dependencies. The build is `CGO_ENABLED=0` (per §16),
so you don't need a C toolchain unless you're hacking on something
that specifically requires it (and per §16, that's not a polecat-level
decision anyway).

## Two operating modes

Local dev runs in one of two modes. Pick by service selection at
`docker compose up` time — there are no profiles, no separate compose
files, no env-var toggles (per the IaC layout rule below).

### Mode A — Standard onboarding (full stack in containers)

The default. `docker compose up -d` (no service args) brings up
Postgres, MinIO, and the `app` service built from the local
`Dockerfile`. The app listens on `:8080` and serves the embedded SPA
— open <http://localhost:8080> from a fresh clone.

```bash
git clone <repo-url> minerals && cd minerals
docker compose up -d                          # OR `make compose-up`
curl -fsS http://localhost:8080/healthz       # → "ok"
```

This is the path a contributor or operator should use for: a
read-only browse of the running app, smoke-testing a release, or any
scenario where the working tree is the source of truth and rebuilding
on every code change is acceptable. The `app` image is rebuilt by
`docker compose up -d` whenever the Dockerfile context changes; for
faster iteration, switch to Mode B.

The `serve` subcommand auto-applies pending migrations on startup
when `ENV=dev`, so a fresh `docker compose up -d` lands a usable app
on `:8080` without a separate `make migrate-up` step (see
`cmd/minerals/serve.go autoMigrateDev`). In prod (`ENV=prod`) the
schema is owned by the separate migrate Job per design §6.4 — `serve`
does NOT auto-migrate there.

### Mode B — Hot-reload dev (deps in containers, app on the host)

For Vite HMR and fast Go rebuilds, run only Postgres + MinIO in
containers and run the backend + frontend natively on the host:

```bash
make compose-deps                             # = docker compose up -d postgres minio
make migrate-up                               # apply migrations to the dev DB
cd frontend && npm ci && cd ..                # one-time

# Two terminals:
make run                                      # backend on :8080
cd frontend && npm run dev                    # Vite on :5173 (proxies to :8080)
```

Browse to **http://localhost:5173**. Vite serves the SPA with HMR;
`/api/...` and `/docs` requests are proxied to `localhost:8080` (the
host-side Go server) — see design §6.5 for why this is same-origin
in both dev and prod.

`make run` and the `:5173` Vite proxy collide with Mode A's `app`
container on `:8080`; pick one mode at a time. To switch from Mode A
to Mode B, `make compose-down` first (or `docker compose stop app
migrate`).

The MinIO bucket (`minerals-dev` by default) is auto-created by the
Go binary on first startup if it doesn't exist — no separate `mc mb`
needed. The Postgres database (`minerals`) is created by the
postgres container's `POSTGRES_DB` env var.

If any of these steps fails, see "Common issues" below before
proceeding.

## Running tests

```bash
make test              # Go unit tests, all packages
make test-integration  # Go integration tests (requires docker compose up)
cd frontend && npm test   # frontend tests (when they exist)
```

Integration tests hit a real Postgres and a real MinIO (per §9 testing
rules — no mocks at the storage boundary). They use the same
`docker compose` services as the dev workflow, against a separate
schema or namespaced bucket prefix. See §9 for what counts as a unit
vs integration test and what each MUST cover.

## Common Makefile targets

```
make build               build the Go binary into bin/
make run                 run the backend natively (default subcommand: serve)
make test                run Go unit tests
make test-integration    run Go integration tests (needs docker compose)
make fmt                 go fmt ./...
make vet                 go vet ./...
make tidy                go mod tidy
make clean               rm -rf bin/

make migrate-up          apply pending migrations
make migrate-down N=1    roll back N migrations (default 1)
make migrate-create NAME=add_x   scaffold a new migration pair

make gen-api-client      regenerate the frontend API client from the OpenAPI spec

make compose-up          full stack:  docker compose up -d
make compose-deps        deps only:   docker compose up -d postgres minio
make compose-down        tear down:   docker compose down
make compose-down-v      tear + wipe: docker compose down -v
```

A polecat MUST keep the Makefile targets working as the canonical
entry points. If you change a workflow, update both the Makefile and
this section.

## Resetting state

To wipe the dev DB and MinIO entirely (start fresh):

```bash
make compose-down-v          # -v removes the volumes too
make compose-up              # in Mode A, the app auto-applies migrations on startup
# OR for Mode B:
make compose-deps && make migrate-up
```

This is destructive; it loses every specimen, photo, and journal
entry in your dev environment. Use deliberately.

To reset only the database (keep MinIO files): connect to Postgres
and `DROP DATABASE minerals; CREATE DATABASE minerals;`, then
`make migrate-up`.

## Common issues

### "Port already in use" on docker compose up

Default ports: Postgres 5432, MinIO 9000 (API) / 9001 (console).
If you already run something on these ports, edit the port mappings
in `docker-compose.yml` and adjust the corresponding env vars
(`DATABASE_URL`, `S3_ENDPOINT`) when running the backend.

Do NOT commit personal port-mapping changes. If a port is
chronically conflicted, propose a contract change.

### "Schema version mismatch" on backend startup

The binary expects a specific migration version and refuses to
start if the DB is behind. The error message names the expected
version. Fix:

```bash
make migrate-up
```

In prod, this is a Job/initContainer concern (see §6).

### HEIC uploads fail to process

HEIC handling may require additional decode support depending on
the chosen library (per design §3.4 / §16 flag). If you're testing
with HEIC files and uploads are rejected with a decode error, this
is a known gap; convert to JPEG locally for now and file a bead to
revisit the HEIC story.

### `make run` exits immediately with "missing required env var"

You're running with `ENV=prod` set in your shell. The prod-strictness
check (per §15) refuses to start without explicit values for the
required vars. Either `unset ENV` or `export ENV=dev`.

### "MinIO endpoint refused" with bucket auto-create errors

The MinIO container takes a few seconds to come up after
`docker compose up -d`. If you race it, the backend may try to
create the bucket before MinIO is ready. Retry — or wait for
MinIO's readiness:

```bash
until curl -fsS http://localhost:9000/minio/health/live; do sleep 1; done
```

## What this section is NOT

This is the workflow reference, not the rationale. Why dev defaults
are hardcoded rather than `.env`-based, why migrations are a
subcommand instead of auto-startup, why the SPA is proxied in dev —
those questions are answered in `docs/design/06-dev-prod-config.md`.

# §4 — Build & release

## What this section covers

How the project's container images are built and tagged, where
they're published, and what process produces a release. Local
development builds (the `make build` target that produces
`bin/minerals` natively) are covered in §3.

## Image registry

All images go to:

```
ghcr.io/dickeyfpersonalprojects/minerals:{tag}
```

The image is **public**. Nothing sensitive is embedded — secrets
enter only through env vars at runtime.

## Tag conventions

Five kinds of tags coexist on the same image stream:

- **`vX.Y.Z`** — immutable release tags. Once pushed, they MUST NOT
  be overwritten. If a release is broken, ship `vX.Y.Z+1`, never
  re-push `vX.Y.Z`.
- **`sha-{short}`** — every commit on `main` produces an image
  tagged with its short git SHA. Immutable. Useful for "deploy
  exactly this commit."
- **`latest`** — moves with `main`. Always points at the most
  recent build of the `main` branch's HEAD. Mutable; operator
  pinning discouraged.
- **`staging`** — moves with `main`. Auto-tracked: every push to
  `main` retags `staging` alongside `latest` and `sha-{short}`,
  all sharing the same buildx manifest. The gitops staging
  environment pulls this tag.
- **`prod`** — manual promotion only. Moved by the
  `Promote to :prod` workflow (`.github/workflows/promote-prod.yml`,
  `workflow_dispatch`-only), which retags from a source tag
  (default `staging`) to `:prod` via
  `docker buildx imagetools create` — a manifest-only operation
  that preserves the original image (no rebuild, no digest churn,
  attestations intact). The gitops production environment pulls
  this tag.

A single image build typically receives multiple tags simultaneously
(e.g. a `main` build gets `latest`, `staging`, and `sha-abc1234`;
a release commit gets `v0.3.0`, `sha-abc1234`, and `latest`).
Promotion to `:prod` does NOT rebuild — it republishes the same
manifest under a new name.

## Versioning scheme

The project follows **semantic versioning** (`MAJOR.MINOR.PATCH`):

- **MAJOR** bumps for breaking changes to user-visible behavior or
  the operator contract (env vars, migration ordering, image entry-
  point). Polecats SHOULD flag any change that might warrant a major
  bump in the PR description.
- **MINOR** bumps for new features that are backwards-compatible.
- **PATCH** bumps for bug fixes, doc changes, dependency updates
  with no behavior change.

Pre-1.0: API and operator contract are still solidifying. Treat
MINOR bumps as potentially breaking until v1.0.0 ships. Once v1.0.0
ships, the rules above apply strictly.

The version string is baked into the binary at build time via
`-X main.version=...` ldflag (per design §7.4). The binary surfaces
its version:

- In a startup log line
  (`{"msg":"server starting","version":"v0.3.0",...}`)
- In the `/readyz` response body (alongside the schema version)

For non-release local builds, `version` defaults to `dev` or
`{git-short-sha}-dirty` if there are uncommitted changes; the build
tooling decides.

## Local image builds

```bash
# Build with version derived from git (short SHA, +dirty if uncommitted)
make image-build

# Build with an explicit version
make image-build VERSION=v0.3.0

# Push to ghcr (requires `gh auth login` or equivalent registry credentials)
make image-push VERSION=v0.3.0

# Build and push in one step
make image-release VERSION=v0.3.0
```

`make image-build` invokes the multi-stage Dockerfile (per design
§7.4). It's a normal `docker build` — no buildx tricks required for
single-arch v1 (amd64 only; multi-arch is deferred per design §7).

## Release process (cutting a versioned release)

In normal operation, releases are cut by **CI on tag push** (see
§5). The manual flow below remains as a fallback when CI is broken
or unavailable; it is not the default path.

1. **Confirm the change set is releasable.** All beads being shipped
   are closed; CONTRACT.md and design docs are up to date; tests
   pass on `main`.

2. **Bump the version** in any place it appears as a literal
   (e.g. a `CHANGELOG.md` if the project gets one). For v1 there is
   no `CHANGELOG.md` yet — the git history is the changelog.

3. **Tag the commit:**
   ```bash
   git tag -a v0.3.0 -m "Release v0.3.0"
   git push origin v0.3.0
   ```
   Tags MUST be annotated (`-a`), never lightweight. They MUST be
   signed if the operator's `git` config has signing keys set up.

4. **CI builds and pushes the image** in response to the tag (see
   §5). Verify the workflow ran and the image landed on ghcr. If CI
   is unavailable, fall back to the manual `make image-release
   VERSION=v0.3.0`.

5. **Verify on ghcr.** Image visible at
   `ghcr.io/dickeyfpersonalprojects/minerals:v0.3.0`, manifest sane,
   size in the expected ballpark (~20-30 MB per design §7.4).

6. **Deploy.** Cluster manifest references `v0.3.0`. The migration
   Job runs first, then the app rolls.

## What MUST stay true across builds

- **Same source = same binary** (modulo timestamps in the build).
  The build is `-trimpath` and `-ldflags="-s -w"` (per design §7.4)
  for reproducibility. Don't introduce non-deterministic build
  steps.
- **Image size stays in the 20-30 MB ballpark.** Embedded SPA
  inflates this gradually; if a single build adds more than a few
  MB, ask why.
- **The image runs as `nonroot` (UID 65532).** Never override to
  root in deploy manifests. If you find yourself wanting to, the
  application is doing something it shouldn't.
- **The binary starts with `serve` by default.** Operators override
  via `CMD` to run `migrate up`. Don't add new default subcommands
  without coordinating — the contract surface is small for a reason.

## What this section is NOT

The how-and-why of the multi-stage Dockerfile shape, the choice of
distroless static, and the embed.FS approach all live in
`docs/design/07-build-embed-observability.md`. This section tells
you what to do; that section tells you why.

# §5 — Continuous integration (GitHub Actions)

The repo's CI runs on **GitHub Actions**, gating PRs and automating
container image publication. Four workflows make up v1's contract:
three automated build/test workflows plus one manual promotion
workflow. A polecat MAY assume CI is in place; treating "but it
works on my machine" as a defense is not acceptable for a
merge-ready PR.

## The workflows

### 5.1 — PR validation (`.github/workflows/pr.yml`)

Triggered: on PR open, reopen, and every push to a PR branch
targeting `main`.

Steps:
- Check out the repo at the PR's HEAD merge commit
- Bring up `docker compose` services (Postgres + MinIO) for
  integration tests
- **Backend**:
  - `make fmt-check` (fails if `gofmt` would change anything)
  - `make vet`
  - `golangci-lint run` (config from repo root `.golangci.yml`)
  - `make test`
  - `make test-integration`
- **Frontend**:
  - `npm ci` in `frontend/`
  - `npx prettier --check .`
  - `npx eslint .`
  - `npm test`
- Report status to the PR (green/red check)

This workflow MUST pass before the PR can be merged. Branch
protection on `main` (see "Branch protection" below) enforces this.

### 5.2 — Main-branch build (`.github/workflows/main.yml`)

Triggered: on push to `main` (i.e. when a PR is merged).

Steps:
- Run the full test suite (same as 5.1)
- If tests pass: build the Docker image via the multi-stage
  `Dockerfile`
- Tag as `latest` AND `staging` AND `sha-{short}` (where `{short}`
  is the 7-char prefix of the merge commit SHA), all three tags
  sharing the same buildx manifest (single build, multiple tag
  entries — never three separate pushes)
- Push all tags to `ghcr.io/dickeyfpersonalprojects/minerals`

If tests fail, no image is pushed. The branch protection on `main`
makes test failure on `main` rare (PRs can't merge with red checks),
but `main` builds still re-run the suite as a belt-and-suspenders
check against flaky-on-merge issues.

### 5.3 — Release tag build (`.github/workflows/release.yml`)

Triggered: on push of any git tag matching `v*` (e.g. `v0.3.0`,
`v1.0.0`, `v1.2.3-rc.1`).

Steps:
- Run the full test suite
- Build the Docker image
- Tag as **`vX.Y.Z`** AND `sha-{short}` AND `latest`, all three
- Push to ghcr

Image tagging policy follows §4 — `vX.Y.Z` tags are immutable; if a
release is broken, ship `vX.Y.Z+1` rather than retagging.

This workflow is the default path for releases. The
`make image-release` Makefile target stays, but only as a fallback
when CI is broken or unavailable.

### 5.4 — Production promotion (`.github/workflows/promote-prod.yml`)

Triggered: **`workflow_dispatch` only.** Never on push, PR, or tag.

Inputs:
- `from_tag` — string, default `staging`. Allows promoting from any
  source tag (a future test might promote a specific `sha-...` tag
  if the staging tag is suspect).

Steps:
- Log in to ghcr via the runner's built-in `GITHUB_TOKEN`
- Inspect the source manifest digest
- `docker buildx imagetools create -t ${IMAGE}:prod
  ${IMAGE}:${from_tag}` — manifest-only retag (no pull, no rebuild,
  no re-push of layers; attestations and any multi-manifest
  structure are preserved)
- Inspect the `:prod` manifest digest after retag and write a job
  summary with source tag, source digest, prod digest, and a
  match check (fails the job if digests diverge)

The workflow declares `permissions: { packages: write }` explicitly
because default token permissions vary by repo settings.

This workflow is intentionally manual in v1 — there is no automated
gating from staging into prod. A future bead may add a
staging-test gate before promotion, but that's out of scope here.

## Branch protection

The `main` branch on GitHub has the following protections enabled:

- **Require PR before merging** — direct pushes to `main` are
  forbidden.
- **Require status checks to pass before merging**, with the PR
  validation workflow (5.1) listed as a required check.
- **Require branches to be up to date before merging** — PRs must
  rebase or merge `main` before they can land. Avoids "passes on
  its branch, breaks on `main`" drift.
- **Allow merge commits AND squash merging**, polecat's choice per
  the workflow rules in §18.
- **No force-pushes to `main`**, ever.
- **No branch deletion of `main`**, ever.

These are repo-settings, not workflow-file content, but they're
part of the contract — an operator MUST keep them on. If you find
them disabled, restore them immediately and investigate why.

## Secrets and credentials

- **No secrets are stored in workflow files.** Workflows reference
  GitHub-provided values only.
- **`GITHUB_TOKEN`** is the built-in token GitHub Actions provides
  per-run. It has push permissions to `ghcr.io/<org>/<repo>` by
  default — no separate registry credential needed for the org's own
  images.
- If a future workflow ever needs an external secret (e.g. signing
  key, deploy credential), it goes in **GitHub Actions secrets**,
  not in environment variables hard-coded in the workflow YAML. The
  polecat MUST escalate before adding such a secret — this changes
  the operator's threat model.

## What CI does NOT do in v1

CI is the boundary that ends at "image pushed to ghcr." It does
not:

- **Deploy** to any cluster. Deployment to k3s remains a manual
  operator step. When a deploy step lands, it goes through a
  separate workflow with a separate secret model and its own
  contract section.
- **Run e2e / browser tests.** Those are deferred (per §9).
- **Run security scans** (CodeQL, Trivy, Snyk). Deferred to v2;
  cheap to add when motivated.
- **Update dependencies automatically.** Dependabot or Renovate is
  fine to add later; it's a separate config, not a workflow.
- **Sign images** (cosign / Sigstore). Worth doing eventually for
  supply-chain hygiene; deferred.
- **Publish SBOMs.** Same — deferred but easy to add.

## Local equivalence

Every CI step has a corresponding `make` target. A polecat MUST be
able to reproduce CI's verdict locally:

| CI step                 | Local equivalent             |
|-------------------------|------------------------------|
| `make fmt-check`        | `make fmt-check`             |
| `make vet`              | `make vet`                   |
| `golangci-lint run`     | `make lint`                  |
| `make test`             | `make test`                  |
| `make test-integration` | `make test-integration`      |
| Frontend prettier check | `make fmt-check-frontend`    |
| Frontend eslint         | `make lint-frontend`         |
| Frontend tests          | `cd frontend && npm test`    |
| Image build             | `make image-build`           |

If CI passes a step that fails locally (or vice versa), it's a bug
in either the Makefile or the workflow — fix the divergence, don't
paper over it.

## When CI fails

- **Red on a PR**: don't merge. Click the failing job, read the
  log, fix the actual issue. Do not retry-until-green; that's the
  flakiness anti-pattern from §9 at the CI scale.
- **Red on `main` after a merge**: revert the merging PR. Fix on a
  branch. Re-merge. Don't push a "fix" directly to `main` to skip
  the PR review (branch protection should make that impossible
  anyway, but the rule stands).
- **Red on a release tag build**: the tag is already pushed but
  the image is not. Ship `vX.Y.Z+1` after the fix; do NOT delete
  and re-push the broken tag.
- **Flaky CI**: same rule as flaky tests (§9) — fix it or delete
  it. No retry policy.

## Deferred to v2

- Deployment workflow (image → k3s)
- E2E browser tests in CI (Playwright / Cypress)
- Security scanning (CodeQL, Trivy, dependency CVE checks)
- Image signing (cosign) and SBOM publishing
- Multi-arch image builds (`linux/amd64` + `linux/arm64`)
- Automated dependency updates (Dependabot or Renovate config)
- Caching for Docker layers across PR builds

## What this section is NOT

Not the content of the workflow YAML files — those live in
`.github/workflows/` and evolve with CI itself. This section is the
**contract** for what CI must do, what gates it enforces, and what
boundary it ends at.

# §6 — Database migrations

The minerals app uses **golang-migrate** to manage schema changes.
The operational rules below are non-negotiable: violating them
risks data loss, broken deployments, or (worst) silent schema
drift between dev and prod.

## Where migrations live

```
migrations/
├── 0001_init.up.sql
├── 0001_init.down.sql
├── 0002_add_collectors.up.sql
├── 0002_add_collectors.down.sql
└── ...
```

Plain SQL files at the repo root in golang-migrate's standard
format. The Go binary embeds them via `embed.FS` at build time —
the runtime container has no separate `migrations/` directory;
everything is inside the binary.

## Naming convention

Each migration is a **pair** of files:

```
NNNN_<description>.up.sql       # forward migration
NNNN_<description>.down.sql     # reverse migration
```

- `NNNN` is a **zero-padded 4-digit** sequence number, no gaps.
  Migrations apply in numeric order.
- `<description>` is **snake_case**, brief but specific:
  `add_collectors_table` (good), `update_schema` (bad), `fix_thing`
  (bad).
- Both files MUST exist for every migration. There are no
  one-way-only migrations in this project (see "Reversibility"
  below).

## Creating a new migration

```bash
make migrate-create NAME=add_visibility_index
```

This scaffolds an empty `NNNN_add_visibility_index.up.sql` and
`NNNN_add_visibility_index.down.sql` pair with the next sequence
number. Edit both files before committing.

If two polecats independently create migration `0008` on different
branches, **the second to merge MUST renumber** to `0009` and
rebase on top of the first. Never push migrations with duplicate
or out-of-order numbers.

## Writing the migration

### Up file rules

- Wrap the migration in an explicit transaction when possible:
  ```sql
  BEGIN;

  ALTER TABLE specimens ADD COLUMN ... ;
  -- etc.

  COMMIT;
  ```
  golang-migrate runs each migration in its own transaction by
  default for Postgres; the explicit `BEGIN`/`COMMIT` makes intent
  clear and is harmless.

- For statements that **cannot run in a transaction** (e.g.
  `CREATE INDEX CONCURRENTLY`), put them in a migration by
  themselves and mark it explicitly:
  ```sql
  -- migrate:up transaction:false
  CREATE INDEX CONCURRENTLY ...
  ```
  These migrations CANNOT be auto-rolled-back if they fail mid-way;
  the polecat MUST document the manual recovery in the migration
  comment.

- Use **idempotent operations** where the SQL allows it
  (`CREATE TABLE IF NOT EXISTS`, `DROP INDEX IF EXISTS`). Belt and
  suspenders against partial-apply state.

- For **data migrations** (UPDATE / INSERT to populate new columns
  or tables), keep them in the same migration as the related
  schema change, inside the same transaction. Do NOT split schema
  and data across separate migration numbers — they need to be
  atomic.

### Down file rules

- Every up file has a down file. The down file's job is to restore
  the schema (not the data) to its pre-up state.
- It is acceptable for a down to lose data: dropping a column
  drops its values; reverting a `NOT NULL` constraint loses
  information about prior NULL-ness. Polecats SHOULD note this in
  a comment at the top of the down file.
- The down file MUST actually work. Don't write `-- can't reverse,
  sorry` and ship it. If a migration is genuinely irreversible,
  see "Truly one-way changes" below.

### Truly one-way changes

Some operations destroy information that can't be reconstructed:
dropping a table with rows, removing an enum value that's in use,
backward-incompatible type changes. For these:

1. The polecat MUST escalate before writing the migration. This
   warrants discussion, not unilateral action.
2. If approved, the down file is written as a clear **warning**
   that explains what cannot be reversed and what the operator
   should do instead (typically: restore from backup).
3. The migration is flagged in its filename:
   `NNNN_DESTRUCTIVE_<description>.up.sql`. The uppercase prefix
   makes the destructive nature visible at a glance in any
   directory listing.

## Applying migrations

### In development

```bash
make migrate-up           # apply all pending migrations
make migrate-down N=1     # roll back the last N (default 1)
make migrate-version      # report current schema version
```

The `migrate-up` target invokes `./minerals migrate up`, which
uses the same `DATABASE_URL` as the running app. After pulling a
branch with new migrations, run `make migrate-up` before
`make run`.

### In production

Migrations run as a **Kubernetes Job or initContainer**, NOT
auto-applied by the running app. The migration runs to completion
before the app deployment rolls.

The Job reuses the same image as the app, overriding `CMD`:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: minerals-migrate-v0-3-0
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: ghcr.io/dickeyfpersonalprojects/minerals:v0.3.0
          command: ["/minerals"]
          args: ["migrate", "up"]
          envFrom:
            - secretRef:
                name: minerals-config
```

The deployment manifest then uses the same image tag and rolls
only after the Job completes successfully. If the Job fails, the
deployment does NOT roll — the operator investigates, fixes, and
either retries or rolls back.

### Schema version mismatch on serve startup

If the app starts (`./minerals serve`) and finds the DB at a
different schema version than the binary expects:

- **DB behind binary** (migrations not yet applied): refuse to
  start with an error naming the expected version, the current
  version, and a hint:
  ```
  schema version mismatch: binary expects v8, database is at v6.
  In development, run: make migrate-up
  In production, run the migrate Job before rolling the deployment.
  ```
- **DB ahead of binary** (rolled back to an older binary without
  rolling back migrations): refuse to start with a similar error,
  pointing at the operator's options (apply a forward-compatible
  binary, or roll back the migration).

The app NEVER auto-applies migrations during `serve`. This is by
design (per design §6.4); do not add an "auto-migrate-on-startup"
feature.

## Things polecats MUST NOT do

- **Edit a migration that has been pushed to `main`**, even if
  you realize it has a bug. Migrations applied to any environment
  (your laptop counts) are immutable. To fix a bad migration,
  write a new migration that corrects the state.
- **Renumber migrations after pushing.** If you discover a
  conflict on rebase, renumber **before** pushing. Renumbering
  pushed migrations rewrites schema history and breaks every
  deployed environment.
- **Skip a migration number.** Numbers must be contiguous;
  golang-migrate enforces this on apply.
- **Combine multiple unrelated changes in one migration.** One
  migration = one logical change. Adding a column AND populating
  it is one logical change (atomic by intent). Adding a column
  AND creating an unrelated index are two separate migrations.
- **Use `DROP TABLE` casually.** Even if a table is "obviously
  unused," confirm via grep, design doc cross-reference, and
  ideally a Mayor or operator before shipping. See "Truly one-way
  changes."
- **Run migrations against prod from a developer laptop.**
  Migrations run as Jobs in the cluster. There is no
  `make migrate-up-prod` target and there will not be one.

## Things that get reviewed extra carefully

- Migrations on tables with significant row counts (locking,
  downtime risk).
- Migrations that change `NOT NULL` or `UNIQUE` constraints
  (silent failure on existing data is real).
- Migrations that change column types (rewrite cost, silent
  truncation).
- Anything renaming a column or table (breaks running queries
  during the rollout window — usually requires a multi-step
  migration).

When in doubt, ask before writing.

## Common gotchas

### "Dirty" migration state

If a migration fails partway through, golang-migrate marks the
schema as **dirty**. Subsequent `migrate-up` calls refuse to
proceed until the dirty state is resolved. Recovery in dev:

```bash
./minerals migrate force VERSION
```

…where `VERSION` is the version you've manually verified the
schema matches. **Never use `force` in production without a
thorough investigation** — it can mask real schema corruption.

### "I changed an `*.up.sql` file and now my dev DB is wrong"

Migrations are immutable once applied. To recover in dev:

```bash
docker compose down -v
docker compose up -d
make migrate-up
```

This wipes the dev database and reapplies all migrations cleanly.
If you can't afford to lose the dev data, you've got a different
problem (see §3 "Resetting state").

## What this section is NOT

The decision *why* migrations are a subcommand instead of
auto-applied at startup, *why* they live at the repo root, *why*
they're embedded in the binary — those questions are answered in
`docs/design/06-dev-prod-config.md` §6.4. This section tells you
what to do.

# §7 — Code conventions

This section covers language-level style and structural rules.
Topics that touch testing, data access, auth, logging, files, or
security are deferred to their own sections (§9, §11–14, §17).

## 7a — Go

### Formatting and linting

- **`gofmt`** is non-negotiable. CI rejects unformatted code; the
  Makefile target `make fmt` is your friend.
- **`go vet`** must pass clean. `make vet` runs it.
- **`golangci-lint`** is the recommended additional linter. A
  baseline `.golangci.yml` ships at the repo root with the
  v2 `standard` preset (`govet`, `errcheck`, `staticcheck`,
  `ineffassign`, `unused`) plus a curated expansion: `bodyclose`
  (unclosed HTTP response bodies), `errorlint` (`errors.Is/As`
  misuse, fmt verbs on wrapped errors), `gosec` (security
  smells), `noctx` (HTTP requests without context), `sloglint`
  (`slog` key/value discipline). `noctx` is excluded for
  `_test.go` files — `httptest` requests don't traverse a real
  client. Polecats MUST NOT silence linter warnings with
  `//nolint:` comments without a real reason; if you do, the
  comment MUST name the specific lint (e.g. `//nolint:gosec //
  G115: bounded above`) and explain why.

### Package layout

- Package names are **short, lowercase, no underscores or
  camelCase** (`auth`, `config`, `db`, `storage`). One word is
  best; two-word packages are a smell.
- One package per directory. Sub-packages live in subdirectories.
- **Interfaces are defined where they're CONSUMED, not where
  they're implemented.** Repository interfaces live in
  `internal/domain/` (the consumer); concrete `*Postgres`
  implementations live in `internal/db/`. Don't put interfaces
  next to their implementations.
- **No `internal/util/` package.** "Utility" packages always grow
  into junk drawers. If you have a helper, it goes in the package
  that uses it; if multiple packages need it, find the right home
  or flag for review.
- **No circular imports.** If you find yourself fighting the
  compiler about this, rethink the package boundaries.

### Error handling

- **Every `err` is checked.** Either propagate (`return err`),
  wrap (`return fmt.Errorf("...: %w", err)`), or handle
  deliberately. Never `_ = err` to silence it.
- **Wrap errors with context** when they cross a layer boundary:
  ```go
  if err := r.db.QueryRow(ctx, sql, id).Scan(&s); err != nil {
      return nil, fmt.Errorf("specimen repo: get by id: %w", err)
  }
  ```
- **Sentinel errors** (`errors.New`) for conditions callers might
  branch on. Define them at package top-level with `Err...` prefix:
  ```go
  var ErrSpecimenNotFound = errors.New("specimen not found")
  ```
  Match with `errors.Is(err, ErrSpecimenNotFound)`.
- **Never `panic` outside `main()`** unless the program genuinely
  cannot continue (e.g. `must`-style helpers that fail on
  impossible input). The HTTP layer has panic recovery middleware
  that turns panics into 500s and logs them; don't rely on it as
  control flow.

### Context propagation

- Every function that does I/O (DB, S3, HTTP, anything that can
  block) takes `context.Context` as its **first** parameter.
- The context flows from the HTTP request all the way down — no
  `context.Background()` invented in the middle of a call chain.
- **Cancellation is honored.** If you have a long-running loop,
  check `ctx.Done()` periodically.
- **Don't store `context.Context` in struct fields**
  (well-established Go anti-pattern). Pass it explicitly through
  function calls.

### Dependency injection (no globals)

- Configuration, DB pools, S3 clients, loggers — all passed via
  struct fields, never stored as package-level globals.
- `main()` constructs the dependencies and wires them into the
  HTTP server / subcommand handlers. Everything below `main()`
  receives what it needs.
- The single sanctioned exception is `slog.Default()` for log
  emission — but the default handler MUST be configured exactly
  once, in `main()`, before any goroutine runs.

### Logging from Go

- Use `log/slog` only. The legacy `log` package is forbidden.
- Log records use **structured attributes**, not `Sprintf`:
  ```go
  // good
  slog.Info("specimen created", "id", s.ID, "type", s.Type)

  // bad
  slog.Info(fmt.Sprintf("specimen %s created (type=%s)", s.ID, s.Type))
  ```
- The full logging contract (what fields MUST appear, what NEVER
  may) is in §14.

### Imports

Imports are grouped, in this order, with blank lines between
groups:

```go
import (
    "context"
    "fmt"

    "github.com/jackc/pgx/v5"
    "github.com/oklog/ulid/v2"

    "github.com/dickeyfPersonalProjects/minerals/internal/auth"
    "github.com/dickeyfPersonalProjects/minerals/internal/domain"
)
```

`goimports` enforces this. Run it via your editor or `make fmt`.

### Comments

- Default to **no comments**. Code should explain itself.
- A comment is justified when it explains **why**, not what:
  a non-obvious constraint, a workaround for a specific bug, an
  invariant that isn't obvious from the types.
- Public types and exported functions get a **godoc comment** if
  they're part of a stable surface (e.g. an exported package
  consumed by `cmd/`). Internal-only helpers don't need them.

### Concurrency

- Default to **synchronous code**. Reach for goroutines only when
  there's a real concurrency requirement.
- When you do use goroutines: every goroutine has a defined
  shutdown path. No fire-and-forget.
- Channels for coordination, mutexes for shared state. Don't mix
  them in the same scope unless the design genuinely calls for it.
- The HTTP server uses one goroutine per request (Go's stdlib
  default). Handlers are responsible for not leaking goroutines
  they spawn.

## 7b — Frontend (Svelte / TypeScript)

### TypeScript strictness

- `tsconfig.json` ships with `"strict": true` and stays that way.
- No `any` without a comment explaining why. Use `unknown` and
  narrow it instead.
- No `// @ts-ignore` to silence the type checker. If you must,
  use `// @ts-expect-error` (which fails the build if the
  underlying problem goes away — keeps the suppression honest).

### Component conventions

- Component files are **PascalCase**: `SpecimenCard.svelte`,
  `PhotoGallery.svelte`.
- One top-level component per file. Keep components focused; if a
  component grows past ~200 lines, look for a split.
- **Scoped styles by default.** Svelte's `<style>` blocks are
  scoped per-component; rely on that. Global styles go in
  `frontend/src/app.css` only.
- Component **props are typed**, not implicitly `any`:
  ```svelte
  <script lang="ts">
    export let specimen: Specimen;
    export let onEdit: () => void;
  </script>
  ```

### State and stores

- Local component state: `let` declarations and reactive `$:`
  blocks.
- Cross-component state: Svelte stores (`writable`, `readable`,
  `derived`).
- **No Redux, no MobX, no third-party state library** in v1.
  Stores are sufficient for this app's complexity. Reach for more
  only with coordination.

### Styling

- **`tailwindcss` is the sanctioned utility-first CSS framework.**
  Allowed in `frontend/`. Other frameworks (daisyUI, Bulma,
  Bootstrap, Material UI) require coordination.
- Per-component scoped Svelte styles (`<style>` blocks) are still
  allowed and preferred for component-local overrides that don't
  fit cleanly in utility classes.
- Global styles go in `frontend/src/app.css` — typically just the
  `@tailwind` directives plus minimal CSS variables for the theme
  palette.
- A polecat MUST NOT add a CSS-in-JS library (styled-components and
  equivalents) — Svelte's scoped CSS + tailwind is the contract.

### Theming

The SPA ships with a **dark theme as default** plus a light theme
toggle. Both themes are first-class:

- **Dark theme is the default applied state on first visit.**
- A theme toggle MUST be present in the SPA chrome (header or nav).
- Theme choice MUST persist across page loads (typically via
  `localStorage` under a stable key like `minerals.theme`).
- Theme respects the user's `prefers-color-scheme` media query when
  no explicit choice has been persisted (system preference wins
  initially; the toggle records an explicit override).
- Implementation MAY use either `class="dark"` toggling on `<html>`
  (tailwind's `darkMode: 'class'`) or `data-theme="..."` attributes
  — polecat picks; document the choice in the relevant PR.

Both themes MUST clear basic legibility / contrast checks (WCAG AA
contrast ratio for body text). Polecat is not required to run an
audit tool, but body text against background SHOULD be checked
visually with a contrast picker.

### Routing

- **`svelte-spa-router`** is the sanctioned client-side router for
  v1. Hash-based routing (`/#/specimens`, `/#/specimens/{id}`)
  rather than history-API routing — keeps backend serving simple
  (the embed.FS handler always serves `index.html` for unknown
  paths regardless).
- Route definitions live in `frontend/src/routes/` (one file per
  top-level route) plus a small `routes.ts` map.

### API access

- The frontend talks to the backend **only through the generated
  API client** in `frontend/src/lib/api/`. Direct
  `fetch('/api/v1/...')` calls are forbidden.
- Why: the client encodes the OpenAPI types, error envelope shape,
  and authentication headers correctly and centrally. Hand-rolled
  fetches drift.
- Regenerate the client when the OpenAPI spec changes:
  ```bash
  make gen-api-client
  ```
  This regenerates `frontend/src/lib/api/` from the spec served by
  a running backend (`/api/v1/openapi.json`). The generated files
  are committed to the repo (per §2 — generated but tracked).

### Error handling

- Every `await` on an API call is wrapped in `try/catch` or
  consumes `.then`/`.catch`. **No unhandled promise rejections.**
- Error UI: surface errors via toast / inline message — never
  `alert()` or silent swallow. The error envelope (per §10) gives
  you a `code` to branch on for user-facing copy.

### Formatting and linting

- **Prettier** with the project's default config (no
  per-developer customization).
- **ESLint** with the Svelte plugin. Polecats MUST NOT add
  `// eslint-disable-...` comments without a stated reason.
- `make fmt-frontend` runs Prettier; `make lint-frontend` runs
  ESLint.

### Accessibility (a11y)

The bar for v1 is "doesn't actively fight a screen reader":

- Use semantic HTML (`<button>`, `<a>`, `<nav>`, etc.) — not
  styled `<div>` elements posing as interactive.
- Every interactive element has either a visible label or an
  `aria-label`.
- Forms have labels associated with inputs (`<label for="...">`
  or wrapping).
- Images that convey information have `alt` text. Decorative
  images use `alt=""`.
- Color is never the *only* signal for state (errors, required
  fields, etc.).

Beyond that — keyboard navigation testing, focus management on
modals, full WCAG audit — is deferred. Polecats are encouraged to
do better than the bar but only required to clear it.

## What this section is NOT

This section is style and structure. It does NOT define:

- What gets tested or how (§9)
- How the data layer is structured beyond package layout (§11)
- How to write upload handlers (§12)
- What logs MUST and MUST NOT contain (§14)
- The security never-do list (§17)

# §8 — Time, locale & encoding

This section covers three rules that cut across every layer: how
timestamps are stored and rendered, how text encoding is handled,
and what stance v1 takes on user-language localization.

## Timestamps: UTC in the database, ISO 8601 over the wire, local at render

### Storage (Postgres)

- **Every timestamp column uses `timestamptz`** (`TIMESTAMP WITH
  TIME ZONE`). Internally Postgres stores these as UTC-relative
  values; the type also enforces correct conversions at the driver
  boundary.
- **Never use `timestamp` without timezone.** The bare type
  silently treats values as wall-clock-in-some-undefined-locale and
  is a reliable source of bugs. CONTRACT.md is unambiguous: every
  timestamp column is `timestamptz`.
- The Postgres server's session timezone setting is not relied on.
  Application code does not read or write `SET TIMEZONE = ...`.

### Application code (Go)

- All time values in Go are `time.Time`.
- At the **database write boundary**, normalize to UTC: `t.UTC()`.
  The `pgx` driver handles `timestamptz` correctly when given UTC
  values; this rule is for explicitness and for the rare paths
  that don't go through `pgx`.
- At the **database read boundary**, values come back as
  `time.Time` in UTC (driver guarantee for `timestamptz`). Don't
  second-guess it.
- Avoid stashing time as strings or epoch ints in Go structs. If
  you need an epoch int (e.g. for a JWT claim), convert at the
  boundary, not internally.

### JSON serialization

- All timestamps in API request and response bodies are
  **ISO 8601 with offset**, always `Z` (UTC):
  ```
  2026-05-06T14:23:11Z
  2026-05-06T14:23:11.123Z      (sub-second precision when applicable)
  ```
- Go's `time.Time` marshals to RFC 3339 (a strict subset of ISO
  8601) by default — that's what we want; do not customize the
  format.
- The frontend types (in the generated API client) MUST treat
  timestamps as `string` at the JSON layer and convert to `Date`
  at use.

### Frontend rendering

- Receive timestamps as ISO 8601 strings; parse with
  `new Date(...)`.
- Render in the **user's local timezone** using
  `Intl.DateTimeFormat`. Don't compute timezone offsets manually.
- Format choices (date-only, date-and-time, relative) are a UI
  concern, but the underlying source is always the UTC ISO string
  from the API.
- A small helper module wraps these patterns
  (`frontend/src/lib/time.ts`); polecats SHOULD use it rather
  than re-deriving formatting per component.

## Character encoding: UTF-8 everywhere, no gymnastics

### Storage

- Postgres database is created with `ENCODING 'UTF8'` and a UTF-8
  collation (`en_US.UTF-8` or `C.UTF-8`). The compose container's
  defaults satisfy this.
- All `text` columns are implicitly UTF-8 (Postgres default for
  that encoding). No length restrictions unless the domain
  genuinely needs them.
- **Never use `bytea` for string data.** `bytea` is for opaque
  binary blobs (file SHA256 if we stored them as bytes,
  encryption ciphertext, etc.). Strings go in `text`.

### Application code

- Go strings are UTF-8 by convention. The standard library and
  the `pgx` driver assume this. Don't introduce a different
  encoding at any layer.
- **No transliteration, no normalization at the storage boundary.**
  If a user enters `Ångström`, we store `Ångström` — not
  `Angstrom`, not the NFC-normalized form (Postgres doesn't care;
  if someone later searches for the un-normalized form we'll add
  normalization at the search layer, not the storage layer).
- Locality and collector names regularly contain non-ASCII
  characters (French accents, Cyrillic, German umlauts, geographic
  terms in other Latin alphabets). The system MUST round-trip
  these byte-for-byte through upload, storage, retrieval, and
  display.

### File names

- File contents are stored under UUID keys (per §12), so filename
  encoding is never material at the storage layer.
- The original filename, if displayed, is a `text` field on the
  `files` row — same UTF-8 rules.

## Internationalization stance for v1

- **The UI is English-only in v1.** No translation framework
  (`svelte-i18n`, `t()` functions, locale files) ships in v1.
- Polecats SHOULD write UI strings as plain text in components,
  not via a translation function. Avoiding the `t()` indirection
  now keeps the code straightforward; adding it later is a
  structural refactor that's easier to do all at once than
  progressively.
- **However**, the *data* path (storage, API, search) is fully
  Unicode-clean from day one. There is no v1 cut where we strip
  non-ASCII characters or collapse to ASCII transliterations.
  When real i18n arrives, it adds UI translation; it does not
  need to also fix data handling.

## What this section is NOT

This is the cross-cutting rule, not the rationale. The reasoning
behind UTC-everywhere and the "data Unicode-clean from day one
but UI English-only" split lives in the design discussion record.
The rules above are non-negotiable; the rationale is informative.

## Deferred to v2

- A translation framework + locale files for the UI. Decision on
  which framework happens when actual translation work is on the
  table.
- Search-side normalization (e.g. accent-insensitive matching for
  `tsvector` queries). Easy to add via the `unaccent` Postgres
  extension when search results show the gap is real.
- Per-user timezone preferences (currently render is in the
  browser's local timezone — fine for a personal app where the
  browser already knows where you are).

# §9 — Testing requirements

## Philosophy

This project distinguishes **two test categories** with different
purposes, different infrastructure needs, and different rules:

- **Unit tests** exercise pure business logic in isolation. They
  run in milliseconds, have no external dependencies, and never
  touch Postgres, MinIO, or the network.
- **Integration tests** exercise code paths that cross the
  application/infrastructure boundary. They use real Postgres and
  real MinIO (via `docker compose`) — **no mocks at the storage
  boundary**. The cost of "test passes against mock, fails against
  real DB" is too high; we just use the real thing.

There is no third category in v1. End-to-end browser tests
(Playwright, etc.) are deferred until the SPA has enough surface
to warrant them.

## What MUST have tests

A polecat MUST NOT close a bead without tests for any of the
following work:

- **Repository methods** (`internal/db/`) — integration tests
  against real Postgres. Cover happy path, not-found, constraint
  violations, and transaction boundaries where the method
  advertises them.
- **HTTP handlers** (`internal/api/`) — integration tests that
  hit the running server (`httptest.NewServer` against a fully
  wired handler tree). Cover happy path, the error envelope
  shape (per §10), and at least one auth-rejection case for
  protected routes.
- **Domain logic with branching** — unit tests for any function
  whose output depends on input shape. Validation rules, type-data
  marshaling, business invariants.
- **Migration pairs** — integration tests that apply the
  migration, roll it back, reapply, and verify schema state at
  each step. Catches "down file is broken" before it bites in
  prod.
- **File upload pipeline** — integration tests covering the full
  path: upload accepted, variants generated, EXIF allowlist
  applied, `files` row written, and the transactional rollback
  case (DB insert fails → S3 object cleaned up).
- **Auth middleware** — unit tests for `Auth` (stub populates
  context correctly) and `RequireUser` (401 when no user, pass-
  through when user present). Integration test verifying the
  public bucket is reachable without auth and the protected
  bucket is not.

If you can name a code path that branches and has no test, you
either need a test for it or a written exception in the bead's
acceptance criteria.

## What is exempt from testing

The following do not require their own tests:

- **Trivial getters/setters** — anything where the test would
  just restate the implementation.
- **Pure data type definitions** — structs without methods, enums
  without behavior.
- **One-line passthroughs** — wrappers whose entire body is
  `return underlying.X()`.
- **Generated code** — the OpenAPI client
  (`frontend/src/lib/api/`), any future codegen output. Test the
  generator's output by using it, not by re-asserting its shape.
- **Standard library and framework behavior** — don't test that
  `pgx` connects, that `slog` writes JSON, that `embed.FS`
  embeds.

When in doubt, write the test. Exemptions are for genuinely
trivial code, not for code you'd rather not test.

## Test categorization (Go)

- **Unit test files**: `<file>_test.go`, default build tags. Run
  via `go test ./...` and `make test`.
- **Integration test files**: same naming pattern, but with a
  build tag at the top:
  ```go
  //go:build integration

  package db_test
  ```
  Run via `go test -tags integration ./...` and
  `make test-integration`.

`make test` runs unit tests only — fast, no infrastructure
required, suitable for the inner loop. `make test-integration`
requires `docker compose up -d` and runs the full suite.

## Test data and cleanup

### Postgres

- Each integration test runs against the same `minerals` database
  but uses a **per-test schema** or **per-test transaction-rollback
  pattern** to isolate state:
  - **Transaction rollback** is preferred for tests that don't
    need to commit: open a tx, run the test, defer rollback. Fast
    and clean.
  - **Per-test schema** for tests that need committed state (e.g.
    migration tests): create a schema named after the test, apply
    migrations into it, drop on teardown.
- **No shared fixture data.** Each test creates the rows it needs.
  Fixtures that "everyone reuses" turn into "everyone implicitly
  depends on" and break in surprising ways.

### MinIO

- Each integration test uses object keys prefixed with the test
  name or a random uuid. Cleanup deletes the prefix on teardown.
- A test that uploads must clean up its uploads. Helper functions
  in `internal/storage/storagetest/` handle this; use them.

### Determinism

- No tests rely on system time. If a test needs to verify a
  timestamp, either freeze time via a clock interface or assert
  bounds (`After(t0) && Before(t0+5s)`), not exact equality.
- No tests rely on iteration order of maps, of S3 listings, or of
  database rows without an explicit `ORDER BY`.
- Tests that use random data MUST seed deterministically (pass a
  fixed seed to `rand.New`) or assert only on properties that
  random values must satisfy.

## Running tests

```bash
make test                 # unit tests only
make test-integration     # unit + integration (requires docker compose up)
make test-cover           # unit tests with coverage report → coverage.html
go test ./internal/db/... # specific package
go test -run TestX -v ./internal/foo  # single test, verbose
```

CI runs `make test` on every PR and `make test-integration` on
`main` merges and tagged releases (per §5). Polecats SHOULD run
integration tests locally before signaling `gt done` on a bead
that touches storage or HTTP.

## Flakiness and skipped tests

- **Flaky tests are bugs.** A test that fails 1% of the time
  still fails — fix the determinism issue or delete the test.
  There is no "rerun until green" policy.
- **`t.Skip()` is allowed only with a reason.** Skipped tests in
  committed code MUST have a comment explaining why and what
  condition would un-skip them:
  ```go
  if testing.Short() {
      t.Skip("integration test; set -tags integration to run")
  }
  ```
  A skip without a reason fails review.

## Frontend tests

For v1 the bar is intentionally modest:

- **Unit tests** for utility modules in `frontend/src/lib/` —
  pure functions, formatting helpers, the time helpers from §8.
  Run via Vitest.
- **Component tests** for critical UI flows (specimen create
  form, photo upload widget). Use Svelte Testing Library +
  Vitest.
- **E2E / browser tests deferred** to when the SPA's surface
  justifies the investment.

Frontend tests run via:

```bash
cd frontend && npm test
```

The exemption rules from "What is exempt from testing" above
apply analogously: don't test trivial Svelte template renderings,
don't test the framework, do test branching logic.

## What this section is NOT

This is the rule for what gets tested and how. The rule for what
counts as "done" — including tests passing — lives in §19.

# §10 — API contract rules

This section defines the rules that every HTTP endpoint in the
backend MUST follow. The contract here is what frontend and
backend agree on; violating it silently breaks the SPA and any
future external client.

The reasoning behind these choices lives in
`docs/design/04-api-shape.md`. This section is the rulebook.

## URL contract

- **All API endpoints live under `/api/v1/`.** No bare `/api/`,
  no version-less paths. Adding a new endpoint anywhere outside
  `/api/v1/` is a contract violation.
- **Resource names are plural lowercase nouns**: `specimens`,
  `photos`, `journal-entries`, `collectors`, `files`. Hyphens for
  multi-word resources, never underscores or camelCase.
- **Identifiers in paths are UUIDs** (per §11). Never numeric IDs,
  never the human `catalog_number` (which is mutable).
- **Nested URLs for "part-of" relationships, flat URLs for direct
  ops by ID** (per design §4.2):
  ```
  POST   /api/v1/specimens/{id}/photos       # create photo for specimen
  GET    /api/v1/specimens/{id}/photos       # list specimen's photos
  GET    /api/v1/photos/{id}                 # direct ops by photo id
  PATCH  /api/v1/photos/{id}
  DELETE /api/v1/photos/{id}
  ```

## HTTP method conventions

| Verb     | Semantics                                | Idempotent |
|----------|------------------------------------------|------------|
| `GET`    | Retrieve, never modify state             | yes        |
| `POST`   | Create, or non-idempotent action         | no         |
| `PATCH`  | Partial update                           | yes (if same body) |
| `DELETE` | Remove                                   | yes        |
| `PUT`    | **Not used** — we patch, we don't replace |           |

`PUT` is deliberately absent. Replace-the-whole-resource
semantics add ceremony for no benefit at our scale; `PATCH`
covers the use cases. If a polecat finds themselves wanting
`PUT`, escalate.

## Status codes

Use the right code, not the convenient one. Common cases:

| Code | When                                                            |
|------|-----------------------------------------------------------------|
| 200  | Successful `GET` / `PATCH` / `DELETE` with response body        |
| 201  | Successful `POST` that created a resource (return Location)     |
| 204  | Successful `DELETE` / `PATCH` with no response body             |
| 400  | Malformed request (bad JSON, missing required field)            |
| 401  | No / invalid auth credentials (per §13)                         |
| 403  | Authenticated but not authorized for this resource              |
| 404  | Resource not found, OR resource exists but caller can't see it  |
| 409  | Conflict (uniqueness violation, version mismatch)               |
| 415  | Unsupported media type (per §12 content-type allowlist)         |
| 422  | Semantically invalid (e.g. `acquired_after > acquired_before`)  |
| 500  | Internal server error (always logged)                           |

The 404-vs-403 choice for "exists but you can't see it":
**return 404**. Don't reveal existence of resources the caller
isn't entitled to know about. (When public sharing ships and
visibility=public exists, those are 200 to anyone.)

## Error envelope (mandatory shape)

Every error response — regardless of status code — MUST be JSON
in this exact shape:

```json
{
  "error": {
    "code": "specimen_not_found",
    "message": "No specimen with id abc-123",
    "details": {
      "field": "catalog_number",
      "constraint": "unique"
    }
  }
}
```

Rules:
- **`code`** is stable, machine-readable, snake_case. Clients
  branch on this. Never on `message`.
- **`message`** is human-readable for logs and developer
  tooling. The SPA decides end-user copy based on `code`, not
  `message`.
- **`details`** is optional, structured per error type. When the
  caller can usefully act on the specifics (which field failed
  validation, when to retry), put that information here.
- **No stack traces, no internal paths, no SQL fragments** in any
  field. Internal diagnostic information goes to logs only.
  (See §17 — security never-do list.)
- **Codes are namespaced informally**: `specimen_not_found`,
  `photo_too_large`, `auth_token_expired`. Keep them stable;
  renaming a code is a breaking change to the API.

## Pagination contract

All list endpoints use **cursor-based pagination** (per design
§4.3):

Request:
```
GET /api/v1/specimens?limit=50&cursor=eyJjcmVhdGVkX2F0IjoiMjAyNi0wNS0wNiI...
```

Response:
```json
{
  "items": [ ... ],
  "next_cursor": "eyJjcmVhdGVkX2F0IjoiMjAyNi0wNC0xNSI..."
}
```

- **`limit`** defaults to 50, capped at 200. Larger values are
  silently clamped to 200; do not reject with 400.
- **`cursor`** is opaque base64 (encoded `{created_at, id}`).
  Clients MUST treat it as an opaque string. Servers MAY change
  the encoded shape between versions without breaking clients.
- **`next_cursor: null`** means end of results.
- **Default ordering**: `created_at DESC, id DESC`. When the
  request includes a search term (`q=`), order shifts to
  `ts_rank DESC`, which **invalidates any cursor previously
  issued under the default ordering**. The SPA discards cursors
  when filters or `q` change.

A polecat MUST NOT add `?page=` / `?offset=` parameters. They are
incompatible with cursor pagination and would create two parallel
contract surfaces.

## Filtering and search

All filter parameters are **query string**, never request body
(`GET` with body is non-standard and proxy-unfriendly). Filters
compose with **AND**:

```
GET /api/v1/specimens?type=mineral&visibility=private&collector_id=abc
```

The full set of v1 filter params is locked in design §4.4.
Adding a new filter parameter is a contract change — the OpenAPI
spec MUST be updated, the frontend client MUST be regenerated,
and the addition MUST be reflected in CONTRACT.md or a downstream
design doc if the rule has evolved.

Search is `?q=<text>` and runs against the Postgres `tsvector`
(per design §4.4).

## Multipart endpoints

Photo uploads (`POST /api/v1/specimens/{id}/photos`) and journal-
entry attachment uploads
(`POST /api/v1/journal-entries/{id}/files`) use
`multipart/form-data`. Rules:

- The file field is named **`file`** (singular). One file per
  request; multiple-file uploads are deferred.
- Metadata fields are sibling form parts (e.g. `taken_at`).
- The `Content-Type` of the file part is what's enforced against
  the allowlist (per §12). The request's outer
  `Content-Type: multipart/form-data` is structural, not
  authoritative.
- Max body size enforcement happens BEFORE handler dispatch —
  the HTTP server's `MaxBytesReader` wraps the request body
  using the `MAX_UPLOAD_BYTES` env var (per §15).

## OpenAPI: stays in sync, period

- The OpenAPI 3 spec is the **machine-readable contract**. It is
  generated from Go types and handler signatures via the chosen
  framework (per design §4.6.A).
- The spec is served at `GET /api/v1/openapi.json`. Redoc is
  served at `GET /docs`. Both endpoints are public (per §13).
- **The frontend API client is regenerated from this spec.** The
  client lives at `frontend/src/lib/api/` and is committed to the
  repo (per §2). Hand-editing it is forbidden.
- **A polecat MUST NOT merge a PR that changes the API surface
  without:**
  1. Verifying the OpenAPI spec reflects the change (spot-check
     the `/api/v1/openapi.json` in a running build).
  2. Regenerating the frontend client (`make gen-api-client`)
     and committing the result.
  3. Confirming the SPA still type-checks against the new client.
- If the type-derived framework produces a spec that doesn't
  accurately reflect the handler (rare but possible — multipart
  edge cases, polymorphic responses), fix the framework's view
  of the handler, not the generated spec by hand.

## Public vs protected route placement

Every new route MUST be placed in one of two router groups:

- **Public** — no auth required, ever. Currently:
  `GET /healthz`, `GET /readyz`, `GET /docs`,
  `GET /api/v1/openapi.json`. Plus future
  `GET /api/v1/specimens/{id}` for `visibility=public` specimens
  when public sharing ships.
- **Protected** — wrapped with `Auth` and `RequireUser`
  middleware. Everything else.

A polecat adding a new endpoint MUST consciously choose the
bucket. "It works in either group" is not an answer — pick
deliberately. The defaults: data-modifying endpoints are always
protected; read-only endpoints default to protected unless
there's a stated public-exposure reason. See §13 for the auth
rules.

## API versioning policy

- `v1` stays stable for the lifetime of v1. Breaking changes
  ship as `v2`, not as silent breaks to `v1`.
- Adding new endpoints, new optional fields, new filter params,
  new enum values: backwards-compatible — stays in `v1`.
- Removing endpoints, renaming fields, changing field types,
  removing enum values, changing required-vs-optional, changing
  pagination contract: breaking — needs `v2`.
- When `v2` ships, `v1` stays running side-by-side until the SPA
  has migrated. There's no flag-day cutover.

## What this section is NOT

This is the wire contract. The internals of *how* the backend
implements these rules — handler structure, repository pattern,
validation libraries — live in §11 and §7.

# §11 — Data layer rules

This section defines how application code interacts with Postgres.
The rules here protect against three failure modes: silent SQL
injection, drift between application invariants and DB state, and
implicit coupling between modules through shared global state.

## Repository pattern

- **Interfaces are defined in `internal/domain/`**, the consumer
  side.
  ```go
  // internal/domain/specimen.go
  type SpecimenRepo interface {
      Create(ctx context.Context, tx Tx, s Specimen) error
      GetByID(ctx context.Context, id uuid.UUID) (Specimen, error)
      List(ctx context.Context, filter SpecimenFilter, page Page) ([]Specimen, Cursor, error)
      // etc.
  }
  ```
- **Implementations live in `internal/db/`**, named `*Postgres`:
  ```go
  // internal/db/specimen_postgres.go
  type SpecimenPostgres struct { pool *pgxpool.Pool }
  func (r *SpecimenPostgres) Create(...) error { ... }
  ```
- One repo per domain entity. Repos do NOT call each other; the
  service layer in `internal/api/` orchestrates cross-entity
  logic.
- Repos accept a `Tx` interface (defined in `internal/domain/`)
  that abstracts over `*pgxpool.Pool` and `pgx.Tx`. This lets the
  same method run inside or outside a transaction without
  overloads.

## UUID generation: UUIDv7 only

All UUIDs generated for new database rows MUST be **UUIDv7** (RFC
9562 — timestamp-prefixed, time-ordered).

- Use `uuid.NewV7()` from `github.com/google/uuid` v1.6+.
- Polecats MUST NOT use `uuid.New()` (which generates UUIDv4) or
  `uuid.NewRandom()` for new rows. UUIDv4's random distribution
  causes B-tree index fragmentation and page splits at scale (per
  design §2's "Catalog numbering" rationale). UUIDv7's
  timestamp-prefixed bytes give the index the locality of
  `bigserial` while preserving every benefit we wanted from
  UUIDs.
- This rule applies to every PK in the domain: `specimens.id`,
  `photos.id`, `journal_entries.id`, `journal_entry_files`
  (composite, but the `entry_id` and `file_id` are both UUIDv7
  values originating from their parent rows), `files.id`,
  `collectors.id`.
- The Postgres `uuid` column type is agnostic to which version
  produced the value — it stores 16 bytes either way. The
  discipline is enforced in Go code only.
- A simple wrapper helper SHOULD live in `internal/domain/` (or a
  small shared module) so that every call site uses the same
  function:
  ```go
  func NewID() uuid.UUID {
      id, err := uuid.NewV7()
      if err != nil {
          // uuid.NewV7 only errors on a broken OS RNG; treat as fatal
          panic(fmt.Errorf("uuid v7 generation: %w", err))
      }
      return id
  }
  ```

## Transactions

- **Transaction boundaries are defined at the SERVICE layer**,
  not in repos. A repo method takes a `Tx` and uses it; the
  service decides what to wrap in a transaction.
- Use the `RunInTx` helper from `internal/db/`:
  ```go
  err := db.RunInTx(ctx, pool, func(tx pgx.Tx) error {
      if err := specimenRepo.Create(ctx, tx, s); err != nil {
          return err
      }
      if err := collectorRepo.Link(ctx, tx, s.ID, collectorIDs); err != nil {
          return err
      }
      return nil
  })
  ```
- Default isolation is **READ COMMITTED** (Postgres default).
  Use `SERIALIZABLE` only when there's a documented reason; the
  documented reason goes in a code comment at the call site.
- **No nested transactions.** If you need partial rollback, use
  savepoints inside the same transaction.
- Long-running transactions are forbidden. A transaction that
  holds open across an HTTP boundary, an external API call, or a
  sleep WILL eventually deadlock something. Keep them short.

## `author_id` is populated on every writable row

- Every row that represents user-created data carries an
  `author_id` column (see §13 — Auth rules and design §5).
- Repo `Create` and `Update` methods that touch a writable table
  MUST extract the user from context and populate `author_id`:
  ```go
  func (r *SpecimenPostgres) Create(ctx context.Context, tx Tx, s Specimen) error {
      user := auth.FromContext(ctx)
      _, err := tx.Exec(ctx, `INSERT INTO specimens (..., author_id) VALUES (..., $N)`, ..., user.ID)
      return err
  }
  ```
- A repo method that inserts a writable row WITHOUT populating
  `author_id` is a contract violation, even if the column has a
  default. The application is the authoritative source.
- The stub user (per §13) has a fixed UUID; in v1 every row ends
  up with that UUID. When real auth ships, a one-time migration
  rewrites those rows to the actual overseer's id.

## JSONB `type_data` discipline

The `specimens.type_data` column holds type-specific fields per
design §2. Rules:

- Marshalling and unmarshalling go through **typed Go structs**,
  one per specimen type:
  - `domain.MineralData`
  - `domain.RockData`
  - `domain.MeteoriteData`
- Reading: dispatch on `specimen.type` to choose the struct,
  then `json.Unmarshal(specimen.RawTypeData, &target)`.
- Writing: marshal the typed struct, validate it before passing
  to the repo, write the bytes to `type_data`.
- **No `map[string]any` or `json.RawMessage` flowing past the
  service layer.** Handlers and downstream code see typed
  structs.
- A new field on a type-specific struct is a code change — not a
  schema migration. JSON is forgiving about new optional fields.
- A field that **changes type or semantics** on a type-specific
  struct IS a contract change and warrants careful thought (and
  often a data migration). Don't rename a `type_data` field
  casually.
- Repos NEVER write `type_data` for the wrong `specimen.type`.
  The service layer enforces this; repos may assume it.

## Query patterns: always parameterized, never concatenated

- **Every value interpolated into SQL goes through a parameter
  placeholder** (`$1`, `$2`, ...). String concatenation,
  `fmt.Sprintf`, `strings.Join`, and similar are NEVER used to
  build a SQL query with user-controlled data.
- This rule is absolute — no "but it's just an integer"
  exceptions. Static SQL with parameters is the only path. (See
  §17 — Security.)
- Dynamic query construction (e.g. for variable filter sets)
  uses a query builder (`squirrel`, `goqu`, or hand-rolled) that
  generates placeholders correctly. The result is still entirely
  parameterized; the builder just decides which placeholders
  apply.
- **No ORM in v1.** Direct `pgx` queries with parameter
  placeholders give us the control we want at this scale. If an
  ORM lands later, it's a coordinated decision, not a polecat-
  level swap.

## Error mapping at the repo boundary

Repo methods return **domain sentinel errors**, not raw `pgx`
errors:

```go
func (r *SpecimenPostgres) GetByID(ctx context.Context, id uuid.UUID) (Specimen, error) {
    var s Specimen
    err := r.pool.QueryRow(ctx, `SELECT ... WHERE id = $1`, id).Scan(...)
    if errors.Is(err, pgx.ErrNoRows) {
        return Specimen{}, domain.ErrSpecimenNotFound
    }
    if pgErr := (&pgconn.PgError{}); errors.As(err, &pgErr) && pgErr.Code == "23505" {
        return Specimen{}, domain.ErrSpecimenConflict
    }
    if err != nil {
        return Specimen{}, fmt.Errorf("specimen repo: get by id: %w", err)
    }
    return s, nil
}
```

Handlers branch on domain errors with `errors.Is`, never on
`pgx` internals. This keeps the choice of database driver an
implementation detail.

## Connection pool

- One `*pgxpool.Pool` per running binary, constructed in `main()`
  from `DATABASE_URL` (per §15 — Configuration & env vars).
- Default pool sizing (sensible for v1 traffic):
  - `max_conns`: 10
  - `min_conns`: 2
  - `max_conn_lifetime`: 1 hour
  - `max_conn_idle_time`: 30 minutes
- These can be overridden via the URL's query string
  (`?pool_max_conns=...`) without code changes.
- The pool is wired into every repo via constructor injection.
  Repos do not reach for a global pool.

## Schema is owned by migrations, period

- **All schema changes go through migrations** (per §6).
  Application code does NOT issue `CREATE TABLE`, `ALTER TABLE`,
  `CREATE INDEX`, etc., except inside migration files.
- The startup schema-version check (per §6) ensures the running
  binary's expected version matches the DB. Failed check =
  refuse to start; never auto-apply.
- **Generated columns** (e.g. `search_tsv` from design §4) are
  declarative — fine. They live in migrations.
- **Indexes** are explicit. Foreign keys do NOT auto-index in
  Postgres; migration writers MUST add an index for any FK
  column used in joins or filters.
- **Triggers and stored procedures: NOT used in v1.** The
  application is the single source of truth for behavior. If a
  case ever surfaces where a trigger genuinely is the right tool
  (rare), it warrants escalation, not unilateral introduction.
  `updated_at` and similar timestamp columns are populated by
  application code on write, not by triggers.

## Indexing discipline

A polecat adding a new query pattern (filter, sort, search)
MUST ensure the relevant indexes exist:

- Foreign-key columns used in joins: index in the same migration
  that introduces the FK.
- Columns used in `WHERE` clauses on list endpoints: index
  alongside the feature that adds the query.
- Columns used in `ORDER BY` on list endpoints (typically
  `created_at`): composite index that covers the `ORDER BY` and
  the relevant filter.
- `GIN` index on the `tsvector` search column (per design §4.4).
- JSONB indexes (`GIN` with `jsonb_path_ops`) only when a query
  pattern actually filters on JSONB content. Don't index
  `type_data` speculatively.

When in doubt: write the query, run `EXPLAIN ANALYZE` on a
realistically-populated dev DB, and act on what you see.

## Future-proofing for consistent-snapshot export

The data layer MUST remain compatible with a future point-in-time
export feature (per the deferred backup tooling discussion).
Concretely:

- **No encrypted-at-rest data without a key-recovery path.** If
  encryption ever becomes necessary, the keys MUST be exportable
  alongside the ciphertext, or the data MUST be re-derivable.
- **No append-only or stream-reconstructed state where a
  snapshot cannot get a coherent point-in-time read.** Every
  domain entity's current state MUST be readable from a single
  Postgres `SELECT` (possibly joining a few tables), not by
  replaying events from a log.
- **No data stored exclusively in MinIO without a corresponding
  DB row.** Every file in MinIO has a `files` row that ties it
  to its ownership context. An export tool can iterate `files`
  and pull matching objects.

These rules cost nothing in v1 (they describe what we're already
doing) and keep the export feature a tractable v2 add.

## What this section is NOT

This is the data-access contract. The schema itself — what
tables exist, what columns they have — lives in migrations and
is summarized in `docs/design/02-domain-model.md`. The rules for
files in MinIO (distinct from `files` rows in Postgres) live in
§12.

# §12 — File & storage rules

This section governs how files reach MinIO, how they leave, and
what guarantees the application makes about their integrity. The
reasoning lives in `docs/design/03-files-and-photos.md`; this
section is the rulebook.

## Storage layout

```
{bucket}/files/{file_id}                — original upload (EXIF allowlisted)
{bucket}/files/{file_id}.display.jpg    — 1600px JPEG q85   (images only)
{bucket}/files/{file_id}.thumb.jpg      — 400px JPEG q80    (images only)
```

- Bucket name comes from the `S3_BUCKET` env var (per §15). The
  bucket is per-environment (`minerals-dev`, `minerals-prod`,
  …); a misconfigured client hitting the wrong bucket produces a
  403, not a silent cross-environment write.
- Object keys are flat under `files/`. Polecats MUST NOT
  introduce alternative key schemes (`specimens/{id}/...`,
  content-addressed paths, etc.) without a contract change. The
  DB owns the relationship between specimens and their files.
- Object keys are derived from UUIDs (`file_id`) only. **No
  user-provided string is ever included in a key**, even
  sanitized — there is no precedent for that ever being safe
  long-term.

## Upload flow (transactional)

Uploads are Go-proxied (per design §3 / §1) and follow this
exact order. Polecats MUST NOT reorder these steps:

1. **Reject early**:
   - `Content-Type` against the allowlist (per "Content type and
     size enforcement" below) → 415 if rejected
   - Body size against `MAX_UPLOAD_BYTES` via
     `http.MaxBytesReader` → request fails before fully buffered
2. **Read into memory or a tempfile**, depending on size; pure-
   Go processing for v1 (no shelling out).
3. **For image content types**: extract EXIF `DateTimeOriginal`
   → default `taken_at`; apply the EXIF allowlist; generate
   `display` and `thumbnail` variants from the filtered bytes.
4. **Compute SHA256** of the bytes that are about to be stored
   (post-EXIF-filter for images).
5. **Generate `file_id`** (UUIDv7 — per §11).
6. **Write to MinIO**:
   - Original at `files/{file_id}` with `If-None-Match: *`
     (conditional put — fails if a key with that UUID somehow
     exists, which guards against the cosmologically unlikely
     collision case).
   - For images: write `files/{file_id}.display.jpg` and
     `files/{file_id}.thumb.jpg` (no conditional put needed —
     they're derived).
7. **Insert the `files` row** in Postgres, populating `id`,
   `s3_key`, `content_type`, `byte_size`, `sha256`, `uploaded_by`
   (from `auth.FromContext(ctx).ID`), `uploaded_at`.
8. **Insert the relating row** (`photos` or
   `journal_entry_files`) in the same transaction as step 7.
9. **On any failure in steps 7-8**: delete the just-written
   MinIO objects (original + variants) before returning the
   error. The cleanup MUST be best-effort; if the cleanup itself
   fails, log loudly and let the orphan cleanup process
   (deferred — see "Orphan handling" below) reclaim it
   eventually.

The reverse order (insert DB row, then write to MinIO) is
forbidden. A failed S3 write after a committed DB row leaves the
application referencing a nonexistent object — far worse than
the orphan that "S3 first, DB second" can produce.

## Content type and size enforcement

### Photos (`POST /api/v1/specimens/{id}/photos`)

- Allowlist (per design §3.5):
  - `image/jpeg`
  - `image/png`
  - `image/webp`
  - `image/heic`
- Anything else → 415 with the error envelope; `details.allowed`
  lists the allowed types so the client can render a useful
  message.
- **The `Content-Type` checked is the inner multipart part's
  Content-Type**, not the outer request's `multipart/form-data`.
- The polecat MAY add **content sniffing**
  (`http.DetectContentType`) as an extra check against
  mismatched Content-Type headers, but the rejection authority
  remains the declared inner Content-Type.

### Journal-entry attachments (`POST /api/v1/journal/{id}/files`)

- Allowlist for v1 (locked by mi-720 / C-2):
  - `application/pdf` — primary non-image attachment (lab
    certs, XRD scans, analytical reports).
  - `image/jpeg`, `image/png`, `image/webp` — accepted as raw
    files. **No variants are generated** for journal-attached
    images; the photo-pipeline variants are reserved for the
    specimen gallery (per "Variant generation" below). HEIC is
    intentionally absent for the same reason as photos (§16
    pure-Go constraint).
  - `text/plain`, `text/csv`, `text/markdown` — field notes
    and tabular lab data.
  - `application/json`, `application/xml` — machine-readable
    analysis output.
- Anything else → 415 with `details.allowed` listing the set.
- Same 100 MiB ceiling.
- Single file per request: the form field is `file` (singular,
  required); a polecat MUST NOT accept `files[]`-style multi-
  file uploads in v1 — multi-attachment is a sequence of POSTs.

### Size cap

- `MAX_UPLOAD_BYTES` env var (default 100 MiB; 104857600 bytes).
- Enforced via `http.MaxBytesReader` wrapping the request body
  BEFORE the handler reads it. A request larger than the cap
  fails with the error envelope, status 413 (Payload Too Large).

## Download flow

Downloads in v1 are also Go-proxied:

```
GET /api/v1/files/{file_id}                # original
GET /api/v1/files/{file_id}/display        # display variant (image only)
GET /api/v1/files/{file_id}/thumb          # thumbnail (image only)
```

- The Go handler authorizes the request (per §13), then streams
  the object from MinIO to the response body.
- **No presigned-URL generation in v1.** The decision to revisit
  this for public specimens is deferred (per design §1, §3).
- `Content-Type` and `Content-Length` are set from the stored
  metadata, not from MinIO's response.
- `ETag: "{sha256}"` is set on responses. Clients that send
  `If-None-Match` get a 304 when content hasn't changed.

## Variant generation

- Triggered automatically on upload if the content type is in
  the image allowlist; skipped for everything else.
- Pure Go: image decoding via `image/jpeg`, `image/png`,
  `golang.org/x/image/webp`, plus an HEIC decoder. (HEIC support
  may need extra work — per design §3.4, this is flagged for
  the implementing polecat to resolve; see "Open issues" below.)
- Resize via `golang.org/x/image/draw` with high-quality kernel.
- Variants are JPEG output regardless of input format (smaller,
  universally supported by browsers).
- Quality / dimensions:
  - `display`: long edge 1600 px, JPEG quality 85
  - `thumbnail`: long edge 400 px, JPEG quality 80
- **Variants are NOT tracked in the DB.** They live at
  predictable keys derived from the original's `file_id`. If a
  variant is ever missing on read, the handler MAY regenerate
  it on demand from the original; the v1 implementation MAY
  return 404 instead, with the regen-on-miss fast path
  deferred. Polecat's call.

## EXIF allowlist (the implemented allow-set)

This is the authoritative list of EXIF tags the system **keeps**.
Everything not on this list — including the entire GPS IFD, all
of XMP, all of IPTC, all MakerNotes, embedded thumbnails — is
**dropped**.

Kept tags (canonical names per the EXIF spec):

```
ImageWidth, ImageLength, BitsPerSample, Compression, PhotometricInterpretation
SamplesPerPixel, PlanarConfiguration, Orientation
XResolution, YResolution, ResolutionUnit
Make, Model
DateTime, DateTimeOriginal, DateTimeDigitized, SubSecTime, SubSecTimeOriginal, SubSecTimeDigitized
ExposureTime, FNumber, ISOSpeedRatings, ExposureProgram, ExposureBiasValue
ShutterSpeedValue, ApertureValue, BrightnessValue
MeteringMode, LightSource, Flash
FocalLength, FocalLengthIn35mmFilm
LensMake, LensModel, LensSerialNumber, LensSpecification
WhiteBalance, ExposureMode, SceneCaptureType, SceneType
ColorSpace, CFAPattern, CustomRendered, DigitalZoomRatio
ContrastValue, SaturationValue, SharpnessValue
```

Adding a tag to this list is a contract change and goes through
PR review. Removing one (further-restricting the allowlist) is
also a contract change but warrants less review than adding.

## Integrity

- The `files.sha256` column is authoritative for content
  integrity.
- The download handler MAY verify SHA256 on read for paranoid
  endpoints, but doing it on every read is unnecessary cost.
- A future ingest job that detects a SHA256 mismatch (file in
  MinIO doesn't match `files.sha256`) MUST fail loudly — that's
  data corruption, not a recoverable error.

## Bucket lifecycle

### Development

- The Go binary auto-creates `S3_BUCKET` on `serve` startup if
  it doesn't exist (idempotent `CreateBucket` call). This lets
  a fresh `docker compose up -d` followed by `make run` "just
  work" without a separate `mc mb` step.
- Auto-create only fires when `ENV=dev` or unset. In `ENV=prod`,
  the bucket MUST already exist; refusing to start is the right
  behavior (the operator is responsible for tenant
  provisioning).

### Production

- Bucket is provisioned via the MinIO operator's `Tenant` /
  `Bucket` CRD by the human operator. The application doesn't
  create or destroy buckets in prod.
- The application's S3 user has only the IAM permissions needed
  for read/write of objects under the configured bucket. No
  `s3:CreateBucket`, no `s3:DeleteBucket`, no listing of
  unrelated buckets.

## Orphan handling (deferred, but flagged)

An "orphan" is an S3 object with no corresponding `files` row,
OR a `files` row pointing at a missing S3 object. In v1:

- The **transactional upload flow above** keeps orphans rare in
  practice. A successful `serve` request leaves both sides
  consistent; a failure cleans up the side that did succeed.
- **No periodic cleanup job exists in v1.** When orphan-cleanup
  tooling lands, it'll be a separate subcommand
  (`./minerals reconcile`) that walks `files` vs MinIO and
  reports drift. Deletion of orphans on either side will require
  explicit flags (no autopilot).
- **A polecat investigating an orphan in v1** SHOULD use `mc rm`
  (MinIO admin client) for the S3 side and `DELETE FROM files
  WHERE id = ...` for the DB side, in the order that matches
  the drift direction. Coordinate with an operator before
  running these in prod.

## Open issues for implementing polecats

- **HEIC decoding** may need cgo (`libheif`) or a pure-Go path
  (current pure-Go HEIC support is limited). This collides with
  §16 (Dependencies) — pure-Go is required by the build. Three
  options:
  1. Use a pure-Go HEIC library if one is sufficient at v1
     quality.
  2. Drop HEIC from the allowlist, document as a known v1 gap.
  3. Escalate for a base-image change (per §16's escape hatch).
  The polecat implementing photo upload MUST land on one and
  document the choice.
- **EXIF library coverage** of the allowlist may differ from
  the spec. The library used SHOULD be tested against
  representative files (JPEG from a phone, JPEG from a DSLR,
  PNG with EXIF, WebP with EXIF). Tests are integration tests
  against real fixtures — see §9 (Testing).

## What this section is NOT

The decision *why* uploads are server-proxied, why variants
aren't DB-tracked, why EXIF is allowlist-not-blocklist, why
downloads don't use presigned URLs in v1 — all in
`docs/design/03-files-and-photos.md`. This section is the rules.

# §13 — Auth rules

This section governs how the application identifies the current
user, who can reach which routes, and how user identity flows
through backend code. v1 has no real authentication — the rules
below mostly describe the **shape** that v1 honors so real auth
ships as a mechanical replacement, not a refactor.

The reasoning lives in `docs/design/05-auth-slot.md`.

## Reading the current user

- **`auth.FromContext(ctx) auth.User`** is the single sanctioned
  way to read the current user inside any handler, service, or
  repo.
  ```go
  user := auth.FromContext(r.Context())
  // user.ID, user.Email
  ```
- **Polecats MUST NOT** read user information any other way:
  - No globals
  - No re-parsing request headers in handlers
  - No re-validating tokens after the middleware has run
  - No "fall back to a default user" pattern — `FromContext`
    MUST return a populated `User` for any request that reaches
    a handler, by construction (see "Population" below).
- The `User` struct shape (per design §5):
  ```go
  type User struct {
      ID    uuid.UUID
      Email string
  }
  ```
  No roles, no permissions, no display name in v1. When a real
  auth identity provider lands, the struct grows; handlers that
  don't care about new fields stay unchanged.

## Population: two middlewares, layered

- **`auth.Auth`** populates `User` in the request context.
  - v1 stub: always populates a fixed stub user (`ID =
    00000000-0000-0000-0000-000000000001`, `Email =
    overseer@minerals.local`).
  - Real-auth (deferred): validate the bearer token / cookie /
    OIDC session; populate from the validated claims; populate
    nothing if no valid credentials present.
- **`auth.RequireUser`** rejects requests that don't have a
  `User` in context.
  - v1: passes through (because `Auth` always populates).
  - Real-auth: returns 401 with the error envelope when no user
    is set, allowing through anonymously-allowable routes that
    ran only `Auth`.

The two-middleware split is deliberate. It supports a future
"public-but-personalized" route (e.g. a public specimen page
that shows different chrome to a logged-in user) without
re-shaping the middleware layer.

## Route bucket placement (mandatory)

Every endpoint MUST be placed in **exactly one** of two router
groups:

### Public group — no auth required, ever

Currently:
- `GET /healthz`
- `GET /readyz`
- `GET /docs`
- `GET /api/v1/openapi.json`

Future additions (when public sharing ships):
- `GET /api/v1/specimens/{id}` — only when the specimen's
  `visibility = 'public'` (the handler enforces this; the route
  is in the public group)

Public-group routes do NOT receive the `Auth` middleware in v1.
When real auth lands and the "public-but-personalized" pattern
appears, public routes will run `Auth` (so a logged-in user is
populated if present) but NOT `RequireUser`.

### Protected group — `Auth` + `RequireUser`

Everything else in `/api/v1/...`. The default for any new
endpoint is **protected**. A polecat adding a public-bucket
route MUST justify the choice; a polecat adding a protected-
bucket route just follows the default.

A new endpoint that fits neither bucket cleanly — e.g. "public
if the resource has visibility=public, protected otherwise" —
goes in the public bucket and the handler enforces the
visibility check itself. This pattern is rare; if it shows up,
document the choice inline.

## `author_id` is populated for every writable row

Cross-reference to §11 (Data layer rules):

- Every repo `Create` and `Update` method that writes to a
  table with an `author_id` column MUST populate it from
  `auth.FromContext(ctx).ID`.
- Tables with `author_id` in v1: `specimens`, `journal_entries`,
  `files`, `collectors`. Any new writable user-created table
  MUST carry `author_id` from its first migration.
- Defaulting `author_id` at the database level is forbidden —
  the application is the source of truth. Migrations that
  create an `author_id` column MUST declare it `NOT NULL` (no
  nullability fallback, no schema-level default).

## Things polecats MUST NOT do

- **Bypass the middleware chain** by calling handler functions
  directly from one route to another. Each route reaches its
  handler through the router; the middleware stack always runs.
- **Re-validate auth in a handler.** If you got past
  `RequireUser`, the user is authenticated. Trust the
  middleware.
- **Use `auth.User{}`** as a sentinel "no user." A zero-valued
  `User` reaching a handler is a bug — `RequireUser` should
  have rejected the request first. Don't write code that
  defends against this case; assert it.
- **Manufacture a `User` in handler code** for "convenience" —
  e.g. for a background job triggered by a request. The job
  runs with the requesting user's identity (passed through
  context) or, if it must run as the system, with an explicit
  "system user" constant that the polecat MUST flag for review
  when introducing. No silent identity changes mid-request.
- **Log full credentials, raw tokens, or session cookies.**
  When real auth lands, the logging middleware (per §14) MUST
  scrub these from any captured request data. Even in v1,
  don't dump request bodies to logs — `slog` records named
  fields only.

## v1 stub user identity

```
ID    = 00000000-0000-0000-0000-000000000001
Email = overseer@minerals.local
```

- The stub user UUID does NOT follow UUIDv7 (per §11) —
  it's a hardcoded constant, not a generated value. The
  UUIDv7-generation rule applies to UUIDs the application
  generates per row at insert time; the stub is a fixed
  sentinel whose bit pattern doesn't matter for index locality
  (it's a constant lookup, not a stream of inserts). Leaving
  the bit pattern as the all-zeros-with-a-trailing-1 form
  keeps it immediately recognizable in logs and dumps.
- Same UUID across all environments — convenient (no env-
  specific seeding), harmless (DBs are isolated per env, and
  no cross-env identity comparison happens).
- Defined as a single constant in `internal/auth/`. Polecats
  MUST NOT scatter the stub UUID literal around the codebase;
  reference the constant.
- When real auth lands, a one-time migration backfills the
  stub's `author_id` rows with the actual overseer's user id.
  The migration is part of the same PR that flips on real
  auth.

## Deferred to v2 (do not implement in v1)

- OIDC integration via the Keycloak operator (the design's
  end-state for real auth)
- Per-row authorization (visibility-based reads, ownership-
  based writes if multi-user lands)
- CSRF mitigation — depends on the chosen auth model; cookies
  need it, bearer tokens in `Authorization` headers don't
- Audit logging of who edited what and when (the `author_id`
  and `updated_at` columns already capture enough; the read
  side is the deferred work)
- Field-level access control (e.g. price hidden from
  non-owners)
- A debug `?as=<email>` header in dev for impersonation
  testing
- Refresh-token / session-renewal handling

A polecat MUST NOT add any of the above as a "cheap
improvement." Each is a coordinated change that affects the
threat model.

## v2 — RBAC design (Keycloak + Casbin)

This subsection documents the authorization model that ships with v2
real auth. Polecats MUST NOT implement any of this in v1.

### Identity source

The Keycloak OIDC provider issues a JWT on successful login. The Go
`auth.Auth` middleware validates the token signature against the Keycloak
JWKS endpoint and populates an extended `User` struct:

```go
type User struct {
    ID    uuid.UUID  // mapped from JWT `sub`
    Email string     // mapped from JWT `email`
    Roles []string   // mapped from JWT realm roles claim
}
```

Requests with no valid JWT populate `User` with `Roles: []string{"anonymous"}`.
`RequireUser` still guards protected routes. Public routes run `Auth`
only — anonymous users are allowed through and the handler uses the
`anonymous` role for permission checks.

### Permission string format

```
<resource>:<operations>:<instance>
```

- **resource** — one of: `specimens`, `photos`, `journal`, `collectors`,
  `qr-sheets`, `devops`, `users`
- **operations** — comma-separated: `view`, `create`, `edit`, `delete`.
  `*` means all operations. Sharing a resource is implied by ownership
  (`edit:own`) — there is no separate `share` operation.
- **instance** — `*` (all), `own` (rows where `author_id = user.ID`),
  `shared` (rows explicitly shared with the user via the `shares` table),
  or a specific UUID. Omitting instance implies `*`.

### Roles and permissions

| Role | Permissions |
|------|-------------|
| `anonymous` | `specimens:view:public`, `specimens:view:unlisted`, `photos:view:public`, `photos:view:unlisted` |
| `user` | `specimens:*:own`, `photos:*:own`, `journal:*:own`, `collectors:*:own`, `qr-sheets:*:own`, `specimens:view:shared`, `photos:view:shared` |
| `devops-viewer` | `devops:view:*` |
| `devops-admin` | `devops:view:*`, `devops:edit:*` |
| `admin` | `*:*:*`, `users:*:*` |

Every authenticated user is assigned the `user` role by Keycloak in
addition to any other roles. `devops-admin` inherits `devops-viewer`
via Casbin role inheritance — not Keycloak composite roles.

The Keycloak realm defines exactly four roles (see `terraform/keycloak/roles.tf`):
`user`, `devops-viewer`, `devops-admin`, `admin`.

### Visibility tiers

Every user-created resource carries a `visibility` column:

| Value | Appears in lists | Direct URL | Who can access |
|-------|-----------------|------------|----------------|
| `public` | ✓ | ✓ | Anyone, including anonymous |
| `unlisted` | ✗ | ✓ | Anyone with the link — the URL is the access control |
| `private` | ✗ | ✗ | Owner + explicitly shared users only |

`unlisted` is a **discoverability control, not a security boundary.**
The resource is not returned in list or search results, but no credential
is required to fetch it by ID. Do not rely on `unlisted` for privacy.

### Hybrid enforcement strategy

Two layers work together. Polecats MUST NOT collapse them into one.

**Layer 1 — DB-level scoping (list queries)**

List endpoints filter at the SQL level. `unlisted` and `private`
resources are excluded from lists unless the user owns them or has an
explicit share:

```sql
WHERE (
    author_id = $userID                        -- own (any visibility)
    OR visibility = 'public'                   -- public: anyone
    OR id IN (                                 -- explicitly shared
        SELECT resource_id FROM shares
        WHERE resource_type = $type AND shared_with = $userID
    )
)
```

For anonymous requests, omit the `author_id` and `shares` clauses —
only `public` resources appear in anonymous lists.

**Layer 2 — Casbin per-resource check (point lookups and writes)**

After fetching the resource, the handler evaluates access in order:

1. `visibility = 'public'` or `'unlisted'` → permit `view`; writes
   fall through to Casbin (only owner/admin may edit or delete).
2. `visibility = 'private'` → Casbin enforcer checks `own` or `shared`.

```go
switch resource.Visibility {
case "public", "unlisted":
    if action == "view" { return permit }
    // writes fall through to Casbin
}
allowed, err := enforcer.Enforce(user.ID.String(), resource, action)
```

Do not use Casbin for list queries. Do not use raw SQL visibility
filters for point lookups. The split is intentional and MUST be
maintained.

### Enforcement library

Use `github.com/casbin/casbin/v2` with the Postgres adapter. Policies
are stored in the DB alongside application data.

Register a custom matcher function `isSharedWith(resourceType, resourceID,
userID)` that resolves the `:shared` instance by querying the `shares`
table:

```go
enforcer.AddFunction("isSharedWith", func(args ...interface{}) (interface{}, error) {
    // SELECT 1 FROM shares
    // WHERE resource_type=$1 AND resource_id=$2 AND shared_with=$3
})
```

### `shares` table

```sql
CREATE TABLE shares (
    id            UUID PRIMARY KEY,
    resource_type TEXT        NOT NULL,  -- e.g. 'specimens'
    resource_id   UUID        NOT NULL,
    shared_by     UUID        NOT NULL REFERENCES users(id),
    shared_with   UUID        NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Shares are not cascade-deleted when the resource is deleted — a
background job cleans orphaned shares after resource deletion.

### HTTP semantics for visibility-scoped resources

The per-row authorization rules above translate to the HTTP layer
as follows. New endpoints touching a visibility-scoped resource
MUST follow these conventions.

**List endpoints** — `GET /<resource>` (e.g. `/specimens`,
`/collectors`):

- Always return **200** with a JSON list. Never return 401 or 403
  for missing/insufficient auth.
- The list is filtered to what the caller may see. Anonymous
  callers see public items only; authenticated users additionally
  see items they own and items shared with them; `devops_admin`
  sees all. Filtering is enforced in SQL (the repo `List`
  method's WHERE clause), not in the handler.
- An empty result is a valid response — it means "nothing matches
  your visibility, here, or there is nothing here."

**Detail endpoints** — `GET /<resource>/<id>`:

- Return **404** when the caller cannot see the resource. Do
  **not** return 403 or 401, and do not differentiate between
  "does not exist" and "exists but you can't see it." This avoids
  leaking existence to anonymous or unauthorized callers.

**Write endpoints** — `POST | PATCH | DELETE /<resource>[/<id>]`:

- **401** when the request has no valid authentication (anonymous
  or invalid token).
- **403** when the caller is authenticated but lacks the required
  role/ownership/share to perform the write.
- **404** is also acceptable for `PATCH/DELETE` on a resource the
  caller can't see, on the same don't-leak-existence principle as
  detail endpoints.

**Sub-resource list endpoints** — `GET /<resource>/<parent_id>/<sub>`
(e.g. `/specimens/<id>/images`):

- Resolve and visibility-check the **parent** first. If the caller
  cannot see the parent, return **404** before evaluating the
  sub-list at all. The sub-list is not "filter to nothing" in
  this case — the URL is meaningless to the caller.
- Once the parent is visible, the sub-list is returned in full.
  Sub-resources inherit their parent's visibility; they do not
  have their own per-row visibility column. If we ever add
  per-image (or other per-sub-resource) visibility, this rule
  needs revisiting.

## What this section is NOT

This is the rule for **how user identity flows through the
code**. What auth provider we eventually use, what claims a JWT
will carry, what the OIDC client config looks like — those are
choices for the real-auth implementation phase and live in
`docs/design/05-auth-slot.md` plus any subsequent design records.

# §14 — Logging & observability

This section defines the runtime visibility contract: what gets
logged, how, what NEVER gets logged, and how operators verify the
service is alive and ready. The reasoning lives in
`docs/design/07-build-embed-observability.md`.

## Logging substrate

- **Stdlib `log/slog` only**, JSON handler, writes to stdout.
- The legacy `log` package is forbidden (cross-ref §7 — Code
  conventions).
- Log level is controlled by `LOG_LEVEL` (per §15). Default
  `info`.
- The default `slog.Logger` is configured exactly once, in
  `main()`, before any goroutine runs. After that, every package
  uses `slog.Info(...)`, `slog.With(...)`, etc.

## Per-request middleware (mandatory fields)

Every HTTP request is logged exactly once by the request-logging
middleware. The log line MUST include these fields:

| Field          | Source                                                  |
|----------------|---------------------------------------------------------|
| `request_id`   | ULID, generated at request entry, attached to context   |
| `method`       | HTTP method                                             |
| `path`         | URL path (NOT the full URL — query string excluded)     |
| `status`       | HTTP response status code                               |
| `duration_ms`  | Wall-clock duration from request entry to response complete |
| `user_id`      | `auth.FromContext(ctx).ID` (per §13)                    |
| `bytes_out`    | Response body byte count (when measurable)              |
| `remote_ip`    | Client IP (`X-Forwarded-For` if present, else direct)   |

The middleware logs at level `info` for 2xx and 3xx responses,
`warn` for 4xx, `error` for 5xx.

A polecat MUST NOT add a per-request log line in handler code
that duplicates this middleware's output. Add specific events as
needed (see "Event logging" below); don't re-log "request
completed."

## Request ID propagation

- The middleware generates a ULID at request entry and stores
  it in the request context under a known key.
- The ULID is echoed back to the client as the `X-Request-Id`
  response header.
- Inbound `X-Request-Id` headers are honored if present and
  well-formed (a ULID), allowing upstream callers to thread
  their own trace ID through. Otherwise generated fresh.
- All log lines for a request — middleware, application events,
  errors — MUST include the same `request_id`. The pattern:
  ```go
  logger := slog.With("request_id", auth.RequestID(ctx))
  ```
  Or use the request-scoped logger that the middleware places in
  context (`slog.LoggerFromContext` / equivalent helper).

## Event logging

Beyond the per-request line, polecats SHOULD log events that
matter for operational visibility:

- **Startup**: `server starting` with `version`, `port`, `env`.
- **Shutdown**: `shutdown initiated`, `shutdown complete`.
- **Migration apply**: which migration ran, success/failure
  (logged by the `migrate` subcommand, not `serve`).
- **Bucket auto-create** (dev only): one line confirming the
  bucket was created.
- **Recoverable infrastructure blip**: e.g. transient DB
  connection failure that retried and succeeded — log at
  `warn`.
- **Server-side errors** (5xx): always logged at `error` with
  enough context to diagnose. The error is the value, not just
  a string:
  ```go
  slog.Error("photo upload failed", "err", err, "specimen_id", id, "byte_size", n)
  ```

What NOT to add:
- Per-DB-query log lines. Use `pgx`'s tracer if you need this
  during debugging — don't ship it.
- "Entered function X" / "exited function X" tracing. That's
  what a debugger is for.
- Repeated logs on a hot path (per-record loops). Aggregate
  first.

## What MUST NEVER appear in logs

These categories are in scope for blocking PR review. If a
polecat finds a log line containing any of the below, the line
is the bug.

- **Authentication credentials**: bearer tokens, session
  cookies, OAuth codes, refresh tokens, passwords, API keys.
  When real auth lands, the middleware MUST scrub
  `Authorization` headers and `Cookie` headers from any request
  data captured to logs.
- **Raw request bodies**, especially for endpoints that accept
  user content: specimen `description`, journal `body_md`, file
  contents, multipart parts. Markdown bodies in particular are
  a freeform user-input attack surface — never log them.
- **SQL query parameters with user data**. The query template
  (with `$1`, `$2`) is fine to log at `debug`; the bound values
  are not.
- **PII beyond `user_id` and (when applicable) `email`**. The
  application doesn't store names or addresses; if it ever
  does, those fields go into the same scrubbing list.
- **Precise `locality` data** (lat/lon at full precision). For
  debugging logs that include locality at all, round to the
  nearest country / region.
- **Stack traces or panic messages** sent to the client (per
  §10 — error envelope rules). Stack traces in *server logs*
  are fine and encouraged for debugging server-side errors.
- **Internal file system paths or SQL fragments** in any field
  that a client could see (e.g. error messages echoed via the
  envelope). Server-side logs may carry these; the wire never
  does.

When in doubt, log the field name and a placeholder
(`"body_md": "<%d bytes>"`) instead of the value.

## Log level discipline

- **DEBUG**: developer tracing. Never enabled in production by
  default. Polecats SHOULD use `slog.DebugContext` for verbose
  paths so they're free to switch on with `LOG_LEVEL=debug` in
  dev.
- **INFO**: normal operations — startup, shutdown, request log
  for 2xx/3xx, significant state transitions. The steady-state
  prod log volume.
- **WARN**: recoverable issues — retried infrastructure blips,
  4xx responses, deprecated configuration values, near-cap
  utilization.
- **ERROR**: things requiring attention — 5xx responses, panic
  recovery, dependencies unavailable, integrity check failures.
  Every `error`-level log SHOULD be something an operator could
  reasonably page on; if it isn't, it should be `warn`.

A polecat MUST NOT use `slog.Error` to draw attention to a
normal request rejection (e.g. a 404 from a handler is not an
error from the server's perspective).

## Health endpoints (operational contract)

### `GET /healthz` — liveness

- Returns `200 OK` with body `ok` if the process is running.
- **Performs NO dependency checks.** No DB ping, no S3 ping, no
  cache lookups. Liveness answers "should the orchestrator
  restart this pod?" — a transient DB outage shouldn't trigger
  a restart loop.
- Polecats MUST NOT add dependency checks to `/healthz` even
  if it feels useful. That logic belongs in `/readyz`.

### `GET /readyz` — readiness

- Returns `200 OK` only when ALL of the following are true:
  - HTTP server has finished startup
  - Postgres `SELECT 1` succeeds (2-second timeout)
  - MinIO `HeadBucket` against `S3_BUCKET` succeeds (2-second
    timeout)
  - The DB schema version matches the binary's expectation
- On failure, returns `503` with a per-check JSON body:
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
- The body shape is part of the operator contract — operators
  may scrape and display these fields. Don't change the field
  names without coordination.
- The check runs on every request in v1 (no caching). When
  probe volume justifies it, a 5-second result cache MAY be
  added; the cache MUST NOT exceed 5 seconds without
  coordination.

## What's deferred to v2

- **Prometheus `/metrics`** endpoint. Useful eventually; a
  polecat MUST NOT implement metrics in v1 without coordination,
  because cardinality choices made early are painful to revisit.
- **Distributed tracing** (OpenTelemetry, Jaeger). Single-binary
  app — tracing buys little until cross-service correlation
  matters.
- **External error reporting** (Sentry, Bugsnag). Logs are
  sufficient at v1 scale.
- **Log shipping configuration** (Vector, Fluent Bit, Loki).
  The cluster's log collector handles stdout; nothing app-side
  to configure.
- **Pretty dev logs** (a tty-detecting `slog` handler that
  prints human-readable text). The JSON-everywhere baseline is
  fine.

## What this section is NOT

This is the runtime-visibility contract. The choice of `slog`
over alternatives, the rationale for healthz/readyz separation,
the deferral of metrics — those are in
`docs/design/07-build-embed-observability.md`. This section
tells you what to log, how to log it, and what visibility
operators rely on.

# §15 — Configuration & env vars

## The canonical settings inventory

The canonical inventory of all settings lives in
[`CONFIG.md`](./CONFIG.md). CONTRACT.md no longer duplicates it.

## Naming and value conventions

- **SCREAMING_SNAKE_CASE.** No `camelCase` or `kebab-case`.
- **Boolean values are the strings `true` / `false`.** No
  `1`/`0`, no `yes`/`no`, no `on`/`off`.
- **Durations are Go duration strings**: `30s`, `5m`, `1h30m`.
- **Empty string is treated as unset.** Both fall back to the
  default value (in dev) or trigger the strictness check (in
  prod).
- **Lists are comma-separated**, no whitespace handling beyond
  `strings.TrimSpace` on each entry.
- **Secrets that back env vars use the env-var name as the
  key.** A Secret carrying `S3_ACCESS_KEY_ID` stores it under
  key `S3_ACCESS_KEY_ID`, not `access_key`. The deployment
  manifest's `secretKeyRef.key` matches the env-var name
  verbatim. This eliminates a class of silent-empty-value bugs
  from key/var name drift (mi-ur0: a Mindat secret that stored
  the value under `api_key` while the manifest read `key:
  MINDAT_API_KEY` — and vice versa — produced a permanently
  empty env var with no error at startup).

  **Polecats MUST NOT introduce a `secretKeyRef.key` that
  differs from the consumer env-var name. Reviewers MUST
  reject such PRs.**

  **Exception — CNPG `DATABASE_URL`.** The CloudNativePG
  operator generates an app Secret whose DSN lives under the
  key `uri`. That key name is the operator's contract and
  cannot be changed on our side, so the deployment maps
  `DATABASE_URL` → `secretKeyRef.key: uri`. This is the only
  sanctioned exception; new exceptions require contract
  amendment.

## Loading and validation

Validation happens in two phases, with strictness deliberately
deferred to the active subcommand:

1. **Format validation, at load time.** `internal/config/.Load()`
   reads env vars **once at startup**. One `Config` struct, one
   constructor, exactly one read of `os.Getenv` per variable.
   `Load()` rejects malformed values (bad URL, unknown enum,
   non-integer where an integer is required) and returns an error
   naming the offending variable.
2. **Strictness validation, per subcommand.** "Required in prod"
   enforcement lives in per-subcommand methods on `Config`, not in
   `Load()`. `main()` calls the right one before dispatching:
   - `(*Config).ValidateForServe()` — full set
   - `(*Config).ValidateForMigrate()` — `DATABASE_URL` only
     (the migrate path doesn't talk to S3)

   This split lets the prod migrate Job / initContainer run
   without S3 credentials being present in its env (mi-dmv).

- **Polecats MUST NOT call `os.Getenv` outside
  `internal/config/`.** If a value is needed elsewhere, it's a
  field on the `Config` struct, passed via dependency injection
  (per §7 — no globals).
- `main()` exits non-zero with a clear message naming the failing
  variable on either phase's failure.

## Production strictness

- When `ENV=prod`, the active subcommand's `ValidateFor*` method:
  - Refuses to fall back to defaults for any variable in its
    required set (a subset of "Required in prod" above, scoped
    to what the subcommand actually uses)
  - Returns an error explicitly naming the missing variable
  - Does NOT attempt to "guess" or use `localhost`-style
    defaults that are valid in dev
- When `ENV=dev` or `ENV` is unset, defaults apply normally and
  `ValidateFor*` is a no-op (`Load()` has already filled
  defaults).
- A polecat MUST NOT add a variable to the inventory marked
  "required in prod" without:
  - Adding it to the appropriate `ValidateFor*` method(s) — and
    only those methods whose subcommands actually consume it.
  - Adding a test exercising the enforcement.

## Adding a new setting

Adding a new tunable — env var, ConfigMap key, feature flag,
runtime knob — IS a contract change. The PR MUST:

1. **Update [`CONFIG.md`](./CONFIG.md)** — add the setting to
   the inventory with name, kind, default, prod-required flag,
   purpose, and source location. This is the first and
   mandatory step.
2. Update the `Config` struct and constructor in
   `internal/config/` (or the equivalent loader for non-env
   kinds)
3. Update the prod-strictness check if the new setting is
   required in prod
4. Update the dev `docker-compose.yml` if the setting
   references a compose service
5. Update the dev README if the setting changes the
   onboarding flow

The design doc `docs/design/06-dev-prod-config.md` no longer
holds the inventory; `CONFIG.md` is canonical. The design doc
captures frozen rationale only.

**Polecats MUST NOT introduce a new setting without updating
`CONFIG.md` in the same PR.** CI / review will reject otherwise.

A polecat introducing a new setting that doesn't fit the
existing naming patterns (e.g. snake_case, or a non-standard
duration unit) MUST surface the choice for review.

## Secrets in dev: compose defaults, no `.env` required

- Dev creds (`minerals:minerals` for Postgres,
  `minioadmin:minioadmin` for MinIO) are hardcoded in
  `docker-compose.yml`. The Go binary's defaults match.
- `.env` files are gitignored (per §2). The project doesn't
  expect one, but a developer can drop one in for personal
  overrides.
- **Polecats MUST NOT introduce a project-required
  `.env.example`** as the documented onboarding path. The
  hardcoded compose defaults are the path; an example file
  would be a parallel source of truth that inevitably drifts.

## Secrets in prod (deferred decision)

- v1 deployments inject env vars via Kubernetes `Secret`
  resources, consumed via `envFrom` in the deployment manifest.
  The operator manages the Secret directly.
- A future decision on a more durable secret-management
  strategy (Sealed Secrets, External Secrets Operator, Vault,
  SOPS) is deferred. The application doesn't care — secrets
  reach the binary as env vars regardless of the upstream
  mechanism.
- **A polecat MUST NOT change how secrets reach the binary**
  (e.g. reading a mounted file instead of an env var) without
  coordination. The env-var contract is part of the operator
  interface.

## What this section is NOT

This is the rule for what configuration shapes look like and
how they're loaded. The rationale for env-vars-only (over
config files, flags, or a config server), the dev-defaults
pattern, and the prod-strictness check all live in
`docs/design/06-dev-prod-config.md`.

# §16 — Dependencies & libraries

## Pure-Go is required (not preferred)

The build is `CGO_ENABLED=0` on
`distroless/static-debian12:nonroot` (per design §7.4). A
cgo-only library will fail at link time. This is not a stylistic
preference — it's a hard build constraint.

A polecat MUST NOT add a Go dependency that requires cgo without
escalating. The escape hatch — switching to `distroless/base`
and `CGO_ENABLED=1` — is an architectural change to the runtime
image, not a library swap. It needs review and a contract
update.

The same constraint does NOT apply to **build-time tooling**
(e.g. `oapi-codegen`, `golangci-lint`) that runs on a developer
machine or in CI. Those can use whatever they need.

## When to add a library

Adding a library is a contract change. A polecat MUST justify
the addition in the PR that introduces it, covering:

- **Why a library at all** — could this be ~50 lines of Go in
  `internal/` instead?
- **Why this specific library** — among the candidates, why is
  this one the best fit?
- **Maintenance signal** — last release date, GitHub issue
  responsiveness, whether the project is archived
- **License** — see "License policy" below
- **Pure-Go status** — explicit confirmation that adding this
  won't break the build

A library that does very little (a one-function dependency) is
usually better written in-line. A library that touches a
load-bearing concern (DB driver, HTTP framework, image
processing) deserves the full evaluation — substituting later
is expensive.

## Pre-approved libraries (use these by default)

These are the libraries this project commits to. Polecats SHOULD
use these without asking and SHOULD NOT introduce competitors
without coordination:

| Concern | Library |
|---|---|
| Postgres driver | `github.com/jackc/pgx/v5` |
| Postgres connection pool | `github.com/jackc/pgx/v5/pgxpool` |
| S3/MinIO client | `github.com/aws/aws-sdk-go-v2` (with `UsePathStyle: true`) |
| Migrations | `github.com/golang-migrate/migrate/v4` |
| UUIDs | `github.com/google/uuid` (≥ v1.6 — required for `uuid.NewV7()`, per §11) |
| ULIDs (request IDs) | `github.com/oklog/ulid/v2` |
| EXIF parsing & filtering | `github.com/dsoprea/go-exif/v3` (subject to allowlist coverage — see §12) |
| Markdown parsing | `github.com/yuin/goldmark` (recommended; see §17 sanitizer pipeline) |
| HTML sanitization | `github.com/microcosm-cc/bluemonday` (per §17) |
| OpenAPI / type-derived API | `github.com/danielgtaylor/huma/v2` (MIT, OpenAPI 3.1, stdlib `net/http` via `humago` adapter; locked in by mi-cy4 — see PR for justification) |
| Image resize | `golang.org/x/image/draw` (high-quality kernel) |
| WebP decode | `golang.org/x/image/webp` |
| Linting | `github.com/golangci/golangci-lint` |

Frontend pre-approvals:

| Concern | Library |
|---|---|
| Build tool | `vite` |
| Framework | `svelte` (Svelte 5+) |
| Testing | `vitest`, `@testing-library/svelte` |
| Linting | `eslint`, `eslint-plugin-svelte` |
| Formatting | `prettier`, `prettier-plugin-svelte` |
| OpenAPI client codegen | `openapi-typescript` (devDep) + `openapi-fetch` (runtime) — type-only, no runtime tax (locked in by mi-cy4) |
| CSS framework | `tailwindcss` (with `@tailwindcss/postcss` or the standard PostCSS pipeline) |
| Client-side router | `svelte-spa-router` (hash-based) |
| Form state | `felte` + `@felte/validator-zod` (form management, errors/touched/dirty, async submit) |
| Validation schemas | `zod` (shared client/server schema definitions where useful) |

A polecat introducing a competitor to anything in these tables
(e.g. swapping `pgx` for `database/sql` + `lib/pq`, or Svelte
for React) MUST escalate.

## Version pinning

- **Go**: `go.mod` and `go.sum` together pin the exact module
  versions. Polecats MUST commit both. `go mod tidy` is the
  only sanctioned way to update them.
- **Frontend**: `package-lock.json` pins exact npm dependency
  versions. Polecats MUST commit it. `npm ci` (not
  `npm install`) is used in CI to ensure reproducibility.
- **No `replace` directives** in `go.mod` for normal builds.
  They're acceptable temporarily for fork debugging, but not in
  commits to `main`.

## Dependency updates

- Updates are **manual in v1**: a polecat or operator runs
  `go get -u <module>@latest` and `go mod tidy`, or
  `npm update <pkg>`, reviews the diff, and commits.
- A version bump MUST be its own commit (or its own PR for
  major bumps), separate from feature work. Don't fold a `pgx`
  upgrade into a feature PR — if the upgrade breaks something,
  you want a clean revert path.
- **Major version bumps** (e.g. `pgx/v5` → `pgx/v6`, Svelte 4 →
  5) are contract changes. They warrant a dedicated PR with a
  release-notes read, a breaking-change review, and full test
  runs.
- **Automated version updates** are enabled via
  `.github/dependabot.yml` (gomod, npm in `/frontend`,
  github-actions, and docker — all weekly). Dependabot opens
  PRs; a polecat or operator still reviews and merges per the
  rules above (one bump per commit/PR, majors get their own
  PR). GitHub's automated security alerts continue to work in
  parallel.

## License policy

The allowlist for direct dependencies, transitive dependencies,
and any code copied into the repo:

**Allowed (permissive)**:
- MIT
- BSD-2-Clause, BSD-3-Clause
- Apache-2.0
- ISC
- MPL-2.0
- Public domain (`Unlicense`, `CC0-1.0`)

**Forbidden**:
- GPL family (GPL-2.0, GPL-3.0, LGPL — even with linking
  exceptions, the analysis costs aren't worth it for this
  project)
- AGPL family (AGPL-3.0 in particular forces source disclosure
  for any SaaS-style hosted use; not compatible with eventual
  public Cloudflare exposure)
- Commercial / source-available licenses (BSL, SSPL, Elastic
  License, Confluent, etc.) — these aren't OSI-approved and
  have use restrictions
- Anything custom or unknown

A polecat encountering a transitive dependency on a forbidden
license MUST surface the issue for review, not silently accept
it. The project's stance is "we want to keep redistribution and
operation options open."

**Enforcement**: the allowlist above is mechanically checked in
CI by `go-licenses` (per mi-q7n). The `license-check` Makefile
target is the local equivalent; run it before committing
dependency changes. The CI gate fails on any direct or
transitive dependency whose detected SPDX license is outside the
allowlist, or whose LICENSE file is missing/unrecognized. First-
party packages (this module itself) are skipped via `--ignore`
because the repo has no top-level LICENSE file; their
*dependencies* are still checked. If a future upstream module
ships without a recognizable LICENSE, prefer a documented
override in the Makefile over widening `--ignore`.

## Vulnerability scanning (SCA)

Known CVEs in direct + transitive Go dependencies (and in the Go
standard library) are scanned by `govulncheck`
(`golang.org/x/vuln/cmd/govulncheck`, BSD-3-Clause). It walks the
reachable callgraph, so its findings are scoped to code actually
invoked — lower noise than naive SBOM scanners. CI runs
`govulncheck ./...` after the unit-test step and fails on any
finding (per mi-xql / Q-1 R3). The `vulncheck` Makefile target is
the local equivalent; run it before bumping deps or the Go
toolchain.

## Vendoring

- v1 does NOT vendor dependencies. `go.mod` + `go.sum` + Go
  module proxy give us reproducibility without the diff noise
  of a `vendor/` directory.
- A polecat MUST NOT add `vendor/` without coordination.
  Vendoring is a build-isolation tool that costs in PR review
  weight; we'd only adopt it if the project genuinely needed
  network-isolated builds.

## Removing a library

Removing an unused library is encouraged and doesn't need
ceremony. `go mod tidy` cleans `go.mod`/`go.sum`; review the
diff to make sure you didn't remove something a less-obvious
code path needed.

## What this section is NOT

This is the rule for how dependencies enter and leave the
project. What specific feature each library supports is
documented in the code that uses it (and, for load-bearing ones,
in the relevant CONTRACT section — `pgx` is referenced from
§11, `aws-sdk-go-v2` from §12, etc.).

# §17 — Security never-do list

This section is the consolidated security contract. It collects:

- Hard rules from earlier sections (recapped briefly)
- Decisions specific to security — markdown rendering, response
  headers, CSP, file serving — that didn't fit cleanly elsewhere
- The threat-model assumptions v1 makes about its operating
  environment

When CONTRACT.md says "MUST NOT" anywhere in this document, the
authority for that prohibition is anchored here. PR review on
security-relevant changes is non-negotiable; the rules below are
not advisory.

## Threat-model assumptions for v1

These are the assumptions on which v1's security posture rests.
If any of these stops being true, the deferred mitigations need
to land:

- **The application runs on a local network or behind a
  Cloudflare-proxied public ingress.** Network-level controls
  (private network, Cloudflare's anti-abuse, k3s ingress
  controls) provide the first line of defense.
- **Single overseer with stub auth in v1.** No untrusted users
  authenticate. Inputs come from one human who controls the
  client.
- **The SPA and API are same-origin in both dev and prod** (per
  §10). No cross-origin client to defend against.
- **No public exposure of `private` specimens, ever.** The
  visibility column ships from day one (per design §1); the
  API enforces it before public reads land.

The application operates with these assumptions baked in. A
polecat relaxing any of them — adding a public endpoint,
exposing the API to a different origin, supporting third-party
clients — MUST treat it as a threat-model change, not a
feature, and escalate.

## Required positive obligations

### Markdown rendering pipeline

User-supplied markdown (specimen `description`, journal entry
`body_md`) is rendered to HTML on the **server side** before it
reaches the SPA. The SPA renders the trusted HTML directly.

- **Library**: `github.com/microcosm-cc/bluemonday` for HTML
  sanitization, paired with whichever CommonMark library
  produces the initial HTML
  (`github.com/yuin/goldmark` is the recommended default;
  polecat MAY choose another active CommonMark library if
  bluemonday's input expectations are met).
- **Sanitizer policy** (the strict allowlist):
  - **Allowed elements**: `p`, `br`, `h1`-`h6`, `strong`,
    `em`, `del`, `code`, `pre`, `ul`, `ol`, `li`,
    `blockquote`, `hr`, `table`, `thead`, `tbody`, `tr`, `th`,
    `td`, `a`
  - **Allowed link schemes**: `http`, `https`, `mailto`
  - **NO**: `img`, `iframe`, `object`, `embed`, `script`,
    `style`, `form`, `input`, `button`, `svg`, `math`, inline
    `style` attributes, `class` attributes (except sanitizer-
    injected code-block classes if any), event handlers
    (`onclick`, etc.), `data:` URLs, `javascript:` URLs,
    `file:` URLs
  - **`<a>` attribute hardening**: `rel="noopener noreferrer"`
    and `target="_blank"` added by the sanitizer (or rendering
    layer), so external links don't pierce the SPA's window
    context
- The pipeline is **input → CommonMark parse → HTML →
  bluemonday sanitize → output**. The bluemonday step is
  mandatory; do NOT trust the CommonMark library's output as
  already-safe.
- **Cache the sanitized output** on read paths if profiling
  shows the cost is meaningful; do NOT cache the raw markdown's
  parse tree across user-content boundaries.

### Response headers

Every HTTP response from the Go server sets the following
headers (applied via middleware, not per-handler):

- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Referrer-Policy: no-referrer`
- `Permissions-Policy: accelerometer=(), camera=(), geolocation=(), microphone=(), payment=(), usb=()`
- `Content-Security-Policy:` see below
- `Strict-Transport-Security: max-age=63072000; includeSubDomains`
  — emitted **only** when the request's effective protocol is
  HTTPS (i.e. when behind the production ingress). Detect via
  the `X-Forwarded-Proto` header (set by the ingress) or a
  known-prod config flag. Never emit HSTS over plain HTTP — it's
  discarded by spec but signals confusion.

A polecat MUST NOT remove or relax any of these headers without
coordination. Adding additional security headers (e.g.
`Cross-Origin-Opener-Policy`) is fine and encouraged when
justified.

### Content Security Policy

The CSP shipped on all SPA responses:

```
default-src 'self';
script-src 'self';
style-src 'self' 'unsafe-inline';
img-src 'self' data:;
font-src 'self';
connect-src 'self' <PUBLIC_OIDC_ISSUER_URL origin, if configured>;
frame-ancestors 'none';
base-uri 'self';
form-action 'self';
```

- `'unsafe-inline'` for `style-src` is concession to Svelte's
  scoped styles; tightening to nonces is a v2 improvement.
- Polecats MUST NOT add `'unsafe-inline'` or `'unsafe-eval'` to
  `script-src`. If a third-party script "needs" them, that's a
  reason to not adopt the third-party script.
- Polecats MUST NOT add wildcard sources (`*`, `https://*`) to
  any directive. If a specific origin needs allow-listing
  (rare), name it explicitly.

#### `connect-src` and the OIDC issuer (mi-cl1)

The browser PKCE flow performs a cross-origin POST from the SPA
to Keycloak's token endpoint. `'self'` alone forbids that, so
when `PUBLIC_OIDC_ISSUER_URL` is set, the **origin** portion
(`scheme://host[:port]`) is appended to `connect-src`. When the
variable is unset, the policy stays exactly as the baseline
above — no auth, no cross-origin.

Hard rules:

- The configured value is the **origin only** — never the full
  issuer URL with realm path. CSP source matching is
  origin-based; including a path is meaningless and misleading.
- The origin is derived from `PUBLIC_OIDC_ISSUER_URL` by URL
  parsing in `internal/config`. A malformed value fails startup
  rather than silently emitting a broken policy.
- Adding any other cross-origin source to `connect-src` (or any
  other directive) is a contract amendment, not a one-off
  change. Today's allow-list is `'self'` + the one OIDC origin
  the SPA already uses. Nothing else.
- `frame-src`, `script-src`, `form-action` stay `'self'`. The
  SPA does not use Keycloak's session-check iframe, does not
  load Keycloak JS, and does not POST HTML forms to Keycloak —
  if any of those changes, that's a new bead.

### File-serving response hygiene

When the Go server proxies a file from MinIO to a client (per
§12):

- `Content-Type` is set from the stored `files.content_type`,
  never sniffed from the bytes.
- `Content-Disposition` is `inline; filename="..."` for image
  variants (so the SPA can render them in `<img>` tags), and
  `attachment; filename="..."` for non-image originals.
- The filename in `Content-Disposition` MUST be RFC 6266
  encoded — never raw-interpolated. Filenames may contain
  characters that break the header otherwise.
- `X-Content-Type-Options: nosniff` is on every response (per
  the headers list above) — this is what makes the
  Content-Type-from-storage rule actually safe against browser
  re-interpretation.

## The never-do list

### Input handling

- **NEVER** concatenate user data into SQL. Parameter
  placeholders only. (Cross-ref §11.)
- **NEVER** parse untrusted XML, YAML, or `gob`. JSON is the
  only format the API accepts on the wire. Configuration files
  in TOML/YAML are fine because they're trusted operator input.
- **NEVER** invoke a shell — `os/exec` with a shell interpreter
  is banned. If `os/exec` is needed at all (rare), invoke
  binaries directly with their argument list, never through
  `sh -c`.
- **NEVER** use a regex to validate URLs, email addresses, or
  any other structured input that has a parser. Use the parser.
- **NEVER** trust client-provided `Content-Length` in lieu of
  `MaxBytesReader`. The reader is the boundary.
- **NEVER** assume an integer fits in an `int32`. Use `int64`
  for anything that could grow (sizes, counts, durations in
  nanoseconds).

### Output handling

- **NEVER** inject user input into HTML except through the
  sanitized markdown pipeline above. No `template.HTML`, no
  `dangerouslySetInnerHTML`-equivalent in Svelte
  (`{@html ...}`) except for output of the sanitizer.
- **NEVER** interpolate user input into HTTP response headers.
  Header injection (newlines in values) is a real attack on
  naive string concat. Use the standard `Set` API, which
  rejects CR/LF.
- **NEVER** echo a user-supplied URL in a redirect without
  validating the target is on a known allowlist. Open redirect
  endpoints are phishing infrastructure.
- **NEVER** include stack traces, SQL fragments, internal file
  paths, or environment variable values in error responses to
  clients (cross-ref §10 — error envelope).

### Identity & access

- **NEVER** bypass the auth middleware chain (cross-ref §13).
- **NEVER** read user identity from any source other than
  `auth.FromContext(ctx)`.
- **NEVER** trust the `X-Forwarded-For` header for security
  decisions. It's user-controlled. It's fine for logging
  (`remote_ip` in §14) where the worst case is wrong
  attribution in a log line.
- **NEVER** include the stub user's UUID as a hardcoded literal
  in app code outside the single constant in `internal/auth/`
  (per §13).

### Cryptography & randomness

- **NEVER** roll your own cryptography. Stdlib `crypto/...` and
  `golang.org/x/crypto/...` are the sanctioned sources. If a
  cryptographic primitive isn't there (rare), escalate.
- **NEVER** use `math/rand` (or `math/rand/v2`) for anything
  security-relevant: tokens, nonces, request IDs that affect
  trust decisions, salts. Use `crypto/rand`.
  - `math/rand/v2` IS fine for non-security randomness (test
    data shuffling, jitter on retries, picking a random color
    in the UI).
- **NEVER** weaken TLS on outbound calls
  (`InsecureSkipVerify: true`, custom `RootCAs` excluding
  system CAs, etc.) without coordination. The application has
  zero outbound-call needs in v1; if one shows up, TLS
  verification is on by default.

### Code execution & dynamic behavior

- **NEVER** evaluate user input as code. No `eval`, no template
  rendering with user-controlled templates, no Lua/JS embedding
  with user-supplied scripts.
- **NEVER** download and execute remote code at runtime. The
  application's behavior is determined entirely by the binary
  shipped — no plug-ins, no remote feature flags that change
  control flow without a redeploy.
- **NEVER** swallow a `panic` to keep going. The recovery
  middleware turns a panic into a 500; that's the only
  sanctioned pattern. Any other `recover()` call needs
  justification in a comment at the call site.

### Logging & error responses

- **NEVER** log credentials, tokens, cookies, raw request
  bodies, or PII beyond `user_id`/`email`. (Cross-ref §14.)
- **NEVER** include the same internal detail in both an error
  response and a server log. Logs are for operators; error
  responses are for clients. Different audiences, different
  appropriate detail.

### Outbound calls (preemptive)

The application makes no outbound calls in v1. If that ever
changes:

- **NEVER** make an outbound call to a host derived from user
  input without a strict allowlist (SSRF prevention).
- **NEVER** follow HTTP redirects to internal addresses
  (RFC 1918, link-local, loopback) when the redirect chain
  started from user input.
- **NEVER** disable timeouts on outbound calls. Default
  `http.Client` (no timeout) is unsafe; always supply a
  `Timeout`.

### Repository hygiene

- **NEVER** commit `.env` files, secrets, certificates, or
  database dumps to the repo. The pre-commit / CI checks
  should catch this; assume they will, but also assume they
  won't.
- **NEVER** push a tag pointing at a branch that contains
  uncommitted secrets-in-history. If a secret is ever
  committed by accident, rotate it AND scrub history (this is
  operator-level work, not polecat-level).
- **NEVER** disable `.gitignore` or `.dockerignore` patterns
  to "just see if it works." The patterns are part of the
  security surface (cross-ref §2).

## Filesystem usage

The production container runs with `readOnlyRootFilesystem: true`
(per the kustomize/base/deployment.yaml securityContext). The
**only writable path** is `/tmp`, mounted as an `emptyDir` volume.
This is a hard constraint, not a preference.

- The application MUST NOT write to any path other than `/tmp`.
  Not `/var/...`, not `/data`, not `/cache`, not `/app/...`. If
  some future need surfaces a real "I need a persistent volume"
  case, that's a coordinated change (new volume + securityContext
  + manifest), not a unilateral polecat decision.
- `/tmp` is **scratch space, ephemeral**. Anything written there
  is gone on pod restart. Do NOT use `/tmp` for anything that
  needs to survive a restart — that's S3 (per §12) or Postgres
  (per §11).
- The application MUST clean up its own `/tmp` usage:
  - Multipart upload tempfiles: `defer form.RemoveAll()` after
    parsing
  - Tempfiles created via `os.CreateTemp`: `defer os.Remove(f.Name())`
    plus `defer f.Close()`
  - Image-processing intermediates: cleared before the handler
    returns
  - Best-effort, but expected. Polecat MUST NOT rely on the
    `emptyDir` sizeLimit + pod restart as cleanup.
- Tempfile names SHOULD use `os.CreateTemp("", "minerals-*")` (or
  similar prefix) so they're recognizable in logs / `df` output.

A polecat introducing code that writes outside `/tmp` MUST
escalate. There is no v1 use case for it that I can foresee, but
saying "no" preemptively is cheaper than discovering it on a
prod deploy (we just learned this lesson the hard way with
multipart spilling to /tmp + readOnlyRootFilesystem).

## Rate limiting & abuse mitigation: explicit deferral

> v1 operates on a local network or behind a Cloudflare-proxied
> public ingress. Network-level controls (private network,
> Cloudflare's anti-abuse) are the application's abuse
> mitigation. The Go server does NOT implement application-
> level rate limiting, request throttling, or per-user quotas
> in v1. Adding these is a coordinated change that affects the
> operator threat model.

A polecat MUST NOT introduce ad-hoc rate limiting, IP-blocking
middleware, request throttling, or any other application-side
abuse mitigation in v1. If the threat model changes — public
exposure without Cloudflare, multi-user with anonymous reads —
the mitigation strategy is decided once, holistically, not
glued on per-endpoint.

## When in doubt

The default answer to a security question that isn't covered
above is **escalate**. There is no "I'll fix it later" pattern
for security-relevant code; the cost of getting it wrong
silently shipped is much higher than the cost of asking.

## What this section is NOT

This is the security rulebook. It does NOT enumerate every
defensive coding practice in Go or TypeScript (that's what
linters and code review are for). It DOES enumerate the rules
a polecat could plausibly violate without realizing it was a
security decision — and the threat-model assumptions that
govern what those decisions look like.

# §18 — Git, commits, PRs

This section covers the mechanics of source-control hygiene:
how branches are named, how commits are written, how PRs are
framed, and which git operations are sanctioned.

The branch protection on `main` (per §5 — CI) enforces some of
the rules below at the platform level. The rules apply equally
on any branch — protection on `main` is belt-and-suspenders, not
the contract itself.

## Branch naming

Branches are named with a **type prefix** and a **slug**:

```
<type>/<short-slug>
```

Where `<type>` is one of:

- `feat` — new functionality
- `fix` — bug fix
- `refactor` — internal cleanup, no behavior change
- `docs` — documentation only (including CONTRACT.md and
  `docs/design/`)
- `chore` — dependency bumps, build tooling, infra
- `test` — adding or fixing tests, no production change
- `revert` — reverting a previous merge

When a branch is associated with a specific bead, append the
bead id as a tail segment:

```
feat/photo-upload-pipeline-bd-8h4
fix/healthz-timeout-bd-cwj
```

A polecat MUST NOT push branches named `main`, `master`, `dev`,
or anything that mimics protected branch names. A polecat MUST
NOT push branches with secrets or large binaries in their
history (force-push to delete them does NOT recover the
situation — rotate the secret).

Slugs are lowercase, hyphen-separated, and brief. No camelCase,
no underscores. If the slug would be longer than ~50 characters,
the branch is doing too much — split it.

## Commit messages

Every commit message follows this shape:

```
<type>: <subject>

<optional body, wrapped at 72 chars>

<optional trailers>
```

Where `<type>` matches the branch types above. Subject:

- **No trailing period.**
- **Imperative mood**: "add photo upload handler", not "added"
  or "adds"
- **~50 character cap** (hard cap at 72)
- **Lowercase first letter** (except proper nouns and
  identifiers)

Body (when present):

- Separated from the subject by a blank line
- Wrapped at 72 characters
- Explains **why** the change is being made, not what (the diff
  shows what)

Trailers (when relevant):

- `Co-Authored-By: ...` for collaborative commits
- `Refs: bd-1234` to reference a bead
- `Closes: bd-1234` when the commit (or merged PR) closes a
  bead

### Examples

Good:

```
feat: add photo upload handler with EXIF allowlist

Implements POST /api/v1/specimens/{id}/photos per CONTRACT.md §12.
EXIF is filtered through the strict allowlist before storage; GPS
and XMP are dropped. Display and thumbnail variants are generated
synchronously in the upload request.

Refs: bd-cwj
```

```
docs: design decisions from session §3
```

```
fix: reject zero-byte uploads in photo handler

Empty multipart parts were silently producing zero-byte files
rows. Reject with 400 + photo_empty error code.

Refs: bd-cxh
```

Bad:

```
WIP                              # vague; what's WIP?
fix bug                          # which bug?
Updated stuff                    # which stuff? past tense?
feat: Added new feature.         # past tense; trailing period
chore: lots of changes           # split into separate commits
```

## PR title and body

PR title:

- **Same shape as a commit message subject**
  (`<type>: <subject>`)
- For single-commit PRs, the PR title MUST match the commit
  subject
- For multi-commit PRs, the PR title is the umbrella description

PR body MUST contain:

```markdown
## Summary

<1–3 bullets describing what this PR changes and why.>

## Test plan

- [ ] <how this was tested>
- [ ] <edge cases covered>
- [ ] <integration test added (if applicable)>

## Notes

<Any caveats, follow-up items, or rationale for non-obvious choices.
Optional but encouraged.>
```

Plus, when relevant:

- A line referencing the bead: `Refs: bd-1234` or
  `Closes: bd-1234`
- Screenshots or short clips for UI changes
- Migration callouts (for PRs touching `migrations/`)
- Breaking-change callouts if the API contract or operator
  interface shifts

## When to squash, merge, or rebase

The repo allows three merge strategies; the polecat picks the
right one for the PR's shape:

- **Squash and merge** is the default for feature branches with
  multiple WIP-style commits. Produces one clean commit on
  `main`.
- **Merge commit** is for branches where the individual commits
  each have value (e.g. a refactor done in clear stages). Use
  when the history is genuinely useful.
- **Rebase and merge** is for tiny PRs where the branch is one
  or two clean commits and a merge commit would be noise.

A polecat MUST NOT use "merge commit" to land a branch with
WIP commits. Squash those first.

## When to amend, when NOT to

- **Amend is OK** on commits that are **local-only and
  unpushed**. Rewriting your own draft history before pushing
  is fine.
- **Amend is FORBIDDEN** on commits that have been pushed to a
  shared branch — including your own remote feature branch if
  another polecat (or a CI run) has pulled it. Force-pushing
  rewrites history that other people may have built on.
- **Amend is FORBIDDEN on `main`**, full stop. The branch
  protection enforces this; the rule stands as a reminder.

If you realize an already-pushed commit needs fixing, write a
follow-up commit. Don't rewrite history.

## Rebasing your feature branch on `main`

Branch protection requires PRs to be up to date with `main`
before merge (per §5). To bring your branch up to date:

```bash
git fetch origin
git rebase origin/main
git push --force-with-lease   # NOT --force; --force-with-lease
                              # refuses to overwrite if the remote
                              # has commits you didn't see
```

`--force-with-lease` is the sanctioned form. A polecat MUST NOT
use plain `--force` on a feature branch — even one they own —
because it can destroy commits a collaborator pushed in the
interval.

## Tags

- Tags follow the rules in §4 — Build & release. Recap:
  - Annotated only (`git tag -a`), never lightweight
  - Format `vX.Y.Z` (semver)
  - Once pushed, immutable
- A polecat MUST NOT create tags from feature branches. Tags
  always come from `main`.

## Who can push directly to `main`

**No one.** Branch protection forbids it. Every change reaches
`main` through a reviewed PR.

The exception that isn't an exception: emergency hotfixes still
go through a PR. The PR review may be expedited and the polecat
may self-merge if no one else is around, but the PR + CI gate
doesn't get bypassed. If the situation is dire enough that
those gates genuinely need bypassing, the operator (not a
polecat) handles it with full awareness of what's being skipped.

## Things polecats MUST NOT do

- **Force-push to `main`.** Branch protection forbids it; the
  rule stands as documentation.
- **Force-push (without `--force-with-lease`)** to any shared
  branch.
- **Commit secrets, credentials, or large binaries.** Even for
  "just testing." (Cross-ref §17.)
- **Commit generated artifacts that should be gitignored**
  (per §2).
- **Commit auto-generated files that the project intentionally
  doesn't track** (e.g. local IDE state).
- **Bypass `gt commit` discipline.** Agent commits go through
  `gt commit` so the agent identity is recorded; using bare
  `git commit` from an agent context defeats the audit trail.
  (Operator commits don't go through `gt commit`; that's
  expected.)
- **Skip pre-commit hooks** with `--no-verify`. If a hook
  fails, fix the underlying issue. Hooks fail loudly for a
  reason.
- **Skip GPG signing** with `--no-gpg-sign` if the project's
  git config has signing keys set up. Same rule, different
  mechanism.

## What this section is NOT

This is the source-control rulebook. It doesn't tell you how
to write code (see §7), how to design a change (see the design
docs), or what counts as "done" (see §19). It tells you how to
land a change in the repo without making a mess.

# §19 — Polecat workflow & definition of done

This section governs the rules for working a bead from claim to
close. The mechanics of Gas Town's coordination machinery (`gt`,
`bd`, the refinery) are covered in the Gas Town tooling docs;
this section covers the **rules** a polecat follows, not the
commands they type.

## The bead lifecycle

A bead moves through these states:

```
open → claimed → in_progress → ready_for_review → merged → closed
```

A polecat's responsibilities at each transition:

### Claiming a bead

- A polecat MUST read the full bead description, acceptance
  criteria, and any linked design docs **before** claiming.
- A polecat MUST NOT claim a bead they don't intend to work on
  now. "Holding" a bead by claiming it blocks the scheduler.
- A polecat claiming a bead MUST verify the work fits within
  their rig's scope. A `minerals` polecat doesn't claim a
  `wasteland` bead. If the routing is wrong, surface it for
  the mayor.

### Starting work

- The first action after claiming is to **reproduce the problem
  or confirm the goal locally**. For a bug fix: reproduce the
  bug. For a feature: confirm the user-facing shape matches
  the bead's description.
- If reproduction or goal-confirmation fails — the bug doesn't
  reproduce, or the goal is ambiguous — **escalate** (see
  below). Don't proceed on guesses.
- Branch off `main`, named per §18.

### Doing the work

- **Stay within the bead's scope.** A polecat MUST NOT bundle
  unrelated changes into the same PR.
- **Tests are not optional** (cross-ref §9). If a polecat finds
  themselves wanting to skip tests "because the change is
  small," that's the moment to write the test.
- **Documentation tracks the code.** If the change shifts a
  rule in CONTRACT.md, update CONTRACT.md in the same PR. If
  the change affects an OpenAPI surface, regenerate the spec
  and frontend client (per §10).
- **Commit early and often** within the feature branch — squash
  on merge if the history is messy (per §18).

### Signaling ready

- Before signaling `gt done`, a polecat MUST verify the
  **definition of done** below. All criteria must be met;
  partial completion is not "done."
- The PR description summarizes what changed and why, follows
  the template from §18, and references the bead.

## Definition of done

A bead is "done" when **all** of the following are true:

- **(a) Every acceptance criterion in the bead is met.** If a
  criterion turned out to be wrong or impossible, the bead was
  modified through escalation; the polecat does not silently
  drop criteria.
- **(b) Unit tests cover new business logic** (services,
  helpers, domain validation). Per §9.
- **(c) Integration tests cover new HTTP endpoints and storage-
  touching code paths** end-to-end against real Postgres +
  MinIO. Per §9.
- **(d) The full test suite passes locally** — both
  `make test` and `make test-integration`.
- **(e) Linters pass locally** — `make fmt-check`, `make vet`,
  `make lint`, plus the frontend equivalents if the change
  touches `frontend/`.
- **(f) OpenAPI spec is regenerated** if the API surface
  changed, AND the frontend API client is regenerated and
  committed (per §10).
- **(g) Documentation is updated** where rules changed:
  - CONTRACT.md if a contract rule moved
  - `docs/design/` if a deferred-to-v2 item just landed (add a
    "Superseded by CONTRACT.md §X on YYYY-MM-DD" note rather
    than rewriting the design record)
  - In-code godoc comments for new exported types or functions
    that warrant them (per §7)
- **(h) The PR description is complete** — summary, test plan,
  bead reference. Per §18.
- **(i) `gt done` is submitted** with a clear summary of what
  shipped.

If a bead's scope made any of these impossible — e.g. a bead
explicitly says "no tests in this PR; tests come in bd-1235" —
the exception MUST be in the bead's acceptance criteria from
the start, not invented at done time.

## Escalation triggers

A polecat MUST stop and ask back — via the bead, via mail to
the mayor, or by surfacing a blocker — rather than proceeding
when:

1. **The bead's acceptance criteria are ambiguous,
   contradictory, or under-specified** for the polecat to land
   a single defensible implementation.
2. **Implementing the bead as specified would violate a
   CONTRACT.md rule.** The polecat does not silently bend the
   contract; they surface the conflict.
3. **The work would require a CONTRACT.md change.** Contract
   changes need review beyond the polecat's own judgment.
4. **The work would touch security-relevant code in a way not
   already covered by §17's defaults.** Examples: introducing a
   new auth path, changing the markdown sanitizer policy,
   adding an outbound HTTP call, relaxing CSP.
5. **The work would introduce a destructive migration**
   (per §6).
6. **The work would introduce a new top-level dependency** that
   isn't on the §16 pre-approved list.
7. **The work would remove an endpoint, change a stable API
   field, or break the operator interface** (env vars, image
   entrypoint, log shape).
8. **Reproducing the problem (for a bug fix) or confirming the
   goal (for a feature) fails or surfaces something genuinely
   different from what the bead describes.**
9. **A dependency the bead assumed exists turns out to be
   missing or broken** (e.g. another bead's work is incomplete
   and this bead can't proceed without it).
10. **The polecat's own judgment is genuinely uncertain** about
    a load-bearing implementation choice (data model shape,
    API contract, breaking-change classification). Better to
    ask once than ship the wrong shape.

The bar is "would a reviewer object?" — not "is the polecat
capable of making the call?" Capable polecats still escalate
when the call has stakes beyond the polecat's own bead.

A polecat MUST NOT escalate to dodge work or to delay action
that's within their authority. The list above describes
structural triggers, not "I'd prefer not to think about this."

## Scope discipline

- **In scope**: the bead's acceptance criteria; tests for code
  the bead touches; small style fixes in files the bead already
  edits; obvious bug fixes encountered while implementing (with
  a brief note in the PR).
- **Out of scope**: features not in the bead; refactors of
  unrelated code; "drive-by cleanup" of code the bead doesn't
  touch; doc updates outside the bead's surface area; bundling
  another bead's work into this PR.

If a polecat encounters something out of scope that genuinely
should be addressed, the right move is to **file a bead for it**
— not to silently fix it in the current PR.

## Common anti-patterns

- **Scope creep**: "while I was in there, I also fixed X."
  Either X is in scope (rare for unrelated fixes) or X is its
  own bead.
- **Test deferral**: "I'll add tests in a follow-up." If the
  bead permitted this, fine. Otherwise, it's a violation of
  the definition of done.
- **Doc drift**: changing a behavior without updating
  CONTRACT.md or the relevant design doc. The contract becomes
  lies; the next polecat trusts the lies.
- **Premature `gt done`**: signaling done before the
  definition's criteria are all met, in hopes that "the
  reviewer will catch it." The reviewer's job is to catch real
  issues, not to do the polecat's done-checklist.
- **Silent CONTRACT.md violation**: implementing something the
  contract forbids because "the rule is wrong here." If the
  rule is wrong, the rule changes through a contract-amending
  PR; until then, the rule applies.
- **Ignoring red CI**: re-running, retrying, or "just merge
  anyway." Red CI is a real signal; treat it as one.

## What this section is NOT

This is the rulebook for working a bead. It doesn't cover:

- The mechanics of Gas Town tooling (`gt hook`, `bd update`,
  etc.) — those live in the Gas Town docs
- The actual contents of acceptance criteria — that's
  bead-specific
- How to write code — see §7
- How to test code — see §9
- How to land a PR — see §18

# §20 — References & glossary

## Design records (the "why" behind this contract)

CONTRACT.md is the operational rulebook. The reasoning lives in
the design records below. When a rule's intent is unclear,
follow the cross-reference into the design doc.

| Design doc | Subject |
|---|---|
| [`docs/design/01-scope.md`](docs/design/01-scope.md) | v1 cut line: what ships, what's deferred |
| [`docs/design/02-domain-model.md`](docs/design/02-domain-model.md) | Schema shape, type_data discipline, normalized provenance |
| [`docs/design/03-files-and-photos.md`](docs/design/03-files-and-photos.md) | Storage layout, EXIF allowlist, variant generation |
| [`docs/design/04-api-shape.md`](docs/design/04-api-shape.md) | REST conventions, error envelope, OpenAPI |
| [`docs/design/05-auth-slot.md`](docs/design/05-auth-slot.md) | Auth middleware shape, route grouping, stub user |
| [`docs/design/06-dev-prod-config.md`](docs/design/06-dev-prod-config.md) | Env vars, migrations subcommand, dev/prod parity |
| [`docs/design/07-build-embed-observability.md`](docs/design/07-build-embed-observability.md) | Multi-stage Dockerfile, slog logging, healthz/readyz |

The agenda bead that drove the design session: `hq-8h4`.

## In-repo references

| File | Purpose |
|---|---|
| `Makefile` | Build, run, test, lint, migrate, image targets (per §3) |
| `Dockerfile` | Multi-stage container image (per §4, design §7) |
| `docker-compose.yml` | Dev Postgres + MinIO services (per §3, §15) |
| `go.mod`, `go.sum` | Go dependency manifest (per §16) |
| `frontend/package.json`, `frontend/package-lock.json` | npm dependencies (per §16) |
| `migrations/` | golang-migrate SQL files (per §6) |
| `.github/workflows/` | CI workflow definitions (per §5) |
| `.gitignore`, `.dockerignore` | Exclusion patterns (per §2) |

## Project glossary

- **Specimen** — A mineral, rock, or meteorite in the
  collection. The core domain entity. Owns photos, a
  description, and an observation journal.
- **Journal entry** — A timestamped, append-only-by-creation
  note attached to a specimen. The body is editable markdown
  (for spelling fixes); the `created_at` is immutable. Each
  entry can carry zero or more attached files.
- **Photo** — An image attached to a specimen, with metadata
  (`taken_at`). Stored as an original plus generated `display`
  and `thumbnail` variants.
- **File** — A first-class storage row in `files`, referenced
  by photos, journal-entry attachments, or future file types.
  Has a UUIDv7 id, MinIO key, content type, byte size, and
  SHA256.
- **Collector** — A normalized entity representing a previous
  owner in a specimen's provenance chain. Joined to specimens
  via `specimen_collectors` with an ordering `position`.
- **type_data** — JSONB column on `specimens` holding type-
  specific fields. Marshaled from typed Go structs
  (`MineralData`, `RockData`, `MeteoriteData`).
- **Stub user** — The fixed identity v1 uses everywhere
  `author_id` is needed. Replaced by real OIDC-validated
  identities when auth ships. UUID
  `00000000-0000-0000-0000-000000000001` (a fixed constant,
  not a generated UUIDv7).
- **Visibility** — Per-specimen `private | unlisted | public`
  enum governing whether public sharing exposes the specimen.
  Column ships in v1; UX ships later.

## Gas Town terms (briefly)

These terms appear throughout this contract. Definitions are
informative; canonical definitions live in the Gas Town tooling
docs.

- **Mayor** — Town-level coordinator. Routes work to rigs,
  slings beads to polecats, makes cross-rig decisions. Doesn't
  directly write production code; coordinates the work that
  does.
- **Polecat** — An agent worker that picks up beads and lands
  changes. Persistent identity, ephemeral session — the polecat
  exists across many sessions but each session is fresh.
- **Witness** — Per-rig health monitor. Watches polecats,
  reports to the deacon.
- **Refinery** — Per-rig merge-queue processor. Lands ready
  PRs.
- **Deacon** — Town-level watchdog. Receives heartbeats,
  monitors agent health, manages cross-rig infrastructure.
- **Bead** — A unit of tracked work. Has acceptance criteria, a
  status, an assignee. Polecats claim beads, work them, signal
  done. The atomic unit of the capability ledger.
- **Sling** — The act of assigning a bead to a worker
  (typically via `gt sling`). The work then appears on the
  worker's hook.
- **Hook** — A worker's incoming work assignment. When a
  worker's hook is non-empty, that's their next assignment to
  execute.
- **Wisp** — An ephemeral, TTL-bounded record (compaction
  reports, health pings, transient notifications). Promoted to
  a permanent bead if it ages past TTL without being closed
  (signals something stuck).
- **Rig** — A scoped workspace within a town, typically
  corresponding to a project or codebase. This contract governs
  the `minerals` rig.

## External documentation

These are the upstream docs for the load-bearing dependencies.
Linked here for convenience; the project does not vendor them.

- **Go**: [go.dev](https://go.dev/doc/)
- **pgx**: [pkg.go.dev/github.com/jackc/pgx/v5](https://pkg.go.dev/github.com/jackc/pgx/v5)
- **AWS SDK Go v2**: [aws.github.io/aws-sdk-go-v2](https://aws.github.io/aws-sdk-go-v2/docs/)
- **golang-migrate**: [github.com/golang-migrate/migrate](https://github.com/golang-migrate/migrate)
- **bluemonday**: [github.com/microcosm-cc/bluemonday](https://github.com/microcosm-cc/bluemonday)
- **goldmark**: [github.com/yuin/goldmark](https://github.com/yuin/goldmark)
- **dsoprea/go-exif**: [github.com/dsoprea/go-exif](https://github.com/dsoprea/go-exif)
- **MinIO**: [min.io/docs](https://min.io/docs/minio/linux/index.html)
- **Postgres**: [postgresql.org/docs](https://www.postgresql.org/docs/)
- **Svelte**: [svelte.dev/docs](https://svelte.dev/docs)
- **Vite**: [vitejs.dev](https://vitejs.dev/)
- **Distroless**: [github.com/GoogleContainerTools/distroless](https://github.com/GoogleContainerTools/distroless)
- **CNPG (Cloud Native PG operator)**: [cloudnative-pg.io](https://cloudnative-pg.io/)
- **MinIO Operator**: [github.com/minio/operator](https://github.com/minio/operator)
- **Keycloak Operator**: [keycloak.org/operator](https://www.keycloak.org/operator)

## What this section is NOT

This is a navigation aid. It does not contain rules — every
rule in CONTRACT.md is in §1–§19. If a rule is referenced here
but not written above, it isn't actually a rule.
