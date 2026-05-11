# Minerals Collection

A web app for cataloging a personal collection of minerals, rocks, and
meteorites. v1 is a single-overseer, locally-hosted SPA over a Go API
backed by Postgres + MinIO; see `docs/design/01-scope.md` for the v1
cut line and `CONTRACT.md` for the operational rulebook.

## Quickstart

Prerequisites (per CONTRACT.md §3): Go 1.25+, Node 22+, Docker with
Compose v2, `make`, `git`.

### Standard onboarding (full stack in containers)

```bash
git clone <repo-url> minerals && cd minerals
docker compose up -d                       # postgres + minio + app
```

Open <http://localhost:8080>. The app builds from the local
`Dockerfile`, auto-applies migrations against Postgres on startup
(dev mode), then serves the embedded SPA on `:8080`. To verify the
backend directly:

```bash
curl -fsS http://localhost:8080/healthz   # → "ok"
```

### Hot-reload dev (deps in containers, app on the host)

For Vite HMR + fast Go rebuilds:

```bash
make compose-deps                          # postgres + minio only
make migrate-up                            # apply migrations against the dev DB
cd frontend && npm ci && cd ..             # one-time

# Two terminals:
make run                                   # backend on :8080
cd frontend && npm run dev                 # Vite on :5173 (proxies to :8080)
```

Open <http://localhost:5173>.

### Tear-down

```bash
make compose-down       # stop everything (volumes preserved)
make compose-down-v     # stop + wipe volumes (fresh DB / MinIO next run)
```

### Local git hooks (optional)

Enforce `make fmt-check` before commit and `make lint && make test`
before push, so the same gates CI runs catch problems on your machine
first. Opt-in per clone:

```bash
make hooks-install
```

This installs [lefthook](https://github.com/evilmartians/lefthook)
into `$GOPATH/bin` if missing and wires up the hooks in `lefthook.yml`.

## Where to go next

- **`CONTRACT.md`** — operational rulebook: layout, dev workflow, CI,
  migrations, code review rules, and the rest. §3 covers both modes
  above in more depth.
- **`CONFIG.md`** — canonical inventory of every tunable setting (env
  vars, ConfigMap keys, feature flags). The first stop when adding or
  changing a setting.
- **`docs/design/01-scope.md` … `07-build-embed-observability.md`** —
  frozen design decisions and rationale for v1.
