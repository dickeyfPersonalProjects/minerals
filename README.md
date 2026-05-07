# Minerals Collection

A web app for cataloging a personal collection of minerals, rocks, and
meteorites. v1 is a single-overseer, locally-hosted SPA over a Go API
backed by Postgres + MinIO; see `docs/design/01-scope.md` for the v1
cut line and `CONTRACT.md` for the operational rulebook.

## Quickstart

Prerequisites (per CONTRACT.md §3): Go 1.23+, Node 22+, Docker with
Compose v2, `make`, `git`.

```bash
# 1. Clone
git clone <repo-url> minerals && cd minerals

# 2. Start dev services (Postgres + MinIO)
docker compose up -d

# 3. Apply migrations
make migrate-up

# 4. Install frontend dependencies
cd frontend && npm ci && cd ..
```

Then run the app in two terminals:

```bash
# Terminal 1 — Go backend on :8080
make run

# Terminal 2 — Vite dev server on :5173 (HMR + proxy to :8080)
cd frontend && npm run dev
```

Open <http://localhost:5173>. The page should display "Backend is up"
once the Svelte smoke test fetches `/healthz` through the Vite proxy.

To verify the backend directly:

```bash
curl -fsS http://localhost:8080/healthz   # → "ok"
```

## Where to go next

- **`CONTRACT.md`** — operational rulebook: layout, dev workflow, CI,
  migrations, code review rules, env vars, and the rest.
- **`docs/design/01-scope.md` … `07-build-embed-observability.md`** —
  frozen design decisions and rationale for v1.
