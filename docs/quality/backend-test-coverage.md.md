# Backend test coverage — quality review (Q-1)

> Scope: Go backend under `cmd/`, `internal/`. Excludes `frontend/`.
> Date: 2026-05-09. Bead: `mi-fmj`.

## TL;DR

The backend has **23 test files vs 34 source files**. Surface coverage looks
healthy in `internal/api`, `internal/db`, and `internal/storage/exif`, but
three packages have **zero tests** (`internal/storage`, `internal/web`,
plus `cmd/minerals` outside the migrate subcommand) and the
`internal/api` suite uses **fake in-memory repos** when CONTRACT.md §9
explicitly mandates real-Postgres + real-MinIO integration tests for HTTP
handlers. CI runs no coverage report, no `-race`, no `govulncheck`. Two
contract-vs-reality drifts: `make test-cover` (referenced in §9) is not
in the Makefile, and `internal/storage/storagetest/` (referenced in §9)
does not exist.

---

## 1. Current state

### Module shape

- Module: `github.com/dickeyfPersonalProjects/minerals`, **Go 1.25**.
- Backend layout (`internal/`): `api`, `auth`, `config`, `db`, `domain`,
  `markdown`, `migrations`, `storage`, `storage/exif`, `storage/imageproc`,
  `web`. Plus `cmd/minerals` (binary entrypoint + migrate / openapi
  subcommands).
- Test framework: **standard library `testing`** only. No
  `testify`, no `ginkgo`, no `gomega`. Table-driven style, manual
  `t.Fatalf` / `t.Errorf` assertions.

### Counts (source vs test LOC, by package)

Generated from `find ... | wc -l`:

| Package | Source LOC | Test LOC | Test funcs |
|---|---:|---:|---:|
| `cmd/minerals` | 873 | 141 | 1 |
| `internal/api` | 3518 | 2960 | 72 |
| `internal/auth` | 97 | 102 | 6 |
| `internal/config` | 124 | 136 | 7 |
| `internal/db` | 1751 | 2065 | 61 |
| `internal/domain` | 366 | 87 | 5 |
| `internal/markdown` | 94 | 152 | 9 |
| `internal/migrations` | — | (in-test only) | 1 |
| `internal/storage` | 214 | **0** | **0** |
| `internal/storage/exif` | 555 | 347 | 6 |
| `internal/storage/imageproc` | 136 | 113 | 4 |
| `internal/web` | 77 | **0** | **0** |

### Test categorization

CONTRACT §9 splits tests into two buckets, both enforced via build tags:

- **Unit tests** — default tag, run by `make test` and `go test ./...`.
  No external dependencies.
- **Integration tests** — `//go:build integration`, run by
  `make test-integration`. Real Postgres + real MinIO required (CI
  spins both up as services).

Files carrying `//go:build integration`:

```
cmd/minerals/migrate_test.go
internal/db/collector_postgres_integration_test.go
internal/db/file_postgres_integration_test.go
internal/db/journal_entry_file_postgres_integration_test.go
internal/db/journal_entry_postgres_integration_test.go
internal/db/photo_postgres_integration_test.go
internal/db/specimen_collector_postgres_integration_test.go
internal/db/specimen_postgres_integration_test.go
internal/migrations/migrations_integration_test.go
```

Notably absent from the integration list: anything under
`internal/api`, `internal/storage`, `internal/auth` end-to-end.

### Build / test infrastructure

`Makefile` targets exercised by `gt done` and CI:

- `make build` — `go build`
- `make test` — `go test ./...` (unit only)
- `make test-integration` — `go test -tags integration ./...`
- `make vet` / `make lint` (`golangci-lint run`) / `make fmt-check`

`.golangci.yml`:

```yaml
version: "2"
run:
  timeout: 5m
linters:
  default: standard
  exclusions:
    paths:
      - frontend
```

The `default: standard` preset enables: `errcheck`, `govet`,
`ineffassign`, `staticcheck`, `unused` (per golangci-lint v2 docs).

CI gates (`.github/workflows/pr.yml` + `main.yml`):

1. `make fmt-check`
2. `make vet`
3. `golangci-lint v2.12.2`
4. `make test` (unit)
5. `make test-integration` *(only if `migrations/*.up.sql` is present —
   true today, so it does run)*
6. `compose-smoke` job (separate runner — `docker compose up -d`,
   `pg_isready`, `MinIO /health/live`, `GET /healthz` polling).

What CI does **not** run:

- `go test -race ./...`
- coverage collection / report (`-coverprofile`)
- `govulncheck` or any SCA scan
- mutation testing
- fuzz tests (`go test -fuzz`)
- `golangci-lint` with anything beyond the standard preset

### Test patterns

- Repo tests (`internal/db/*_integration_test.go`) use a per-test
  schema (or the `scopedDB(t)` helper) and `authedCtx()` to inject a
  user into context. Real Postgres, no mocks.
- `internal/api/*_test.go` builds the HTTP handler in-process via
  `api.New(...)` with **hand-written fake repos**
  (`fakeSpecimenRepo`, `fakePhotoRepo`, `fakeFileRepo`, etc.) and
  drives requests through `httptest.NewRecorder` /
  `httptest.NewRequest` rather than `httptest.NewServer`. Storage
  side is faked too — there is no MinIO call in this suite.
- `internal/storage/imageproc` and `internal/storage/exif` tests are
  pure-unit: synthesize JPEG/PNG buffers in memory, assert on
  decoded dimensions / EXIF tags.
- No test calls `t.Parallel()`. No test uses `testing.Short()`.
- No mention of `-race` anywhere in repo (`grep -rn "race"` returns
  nothing in workflows).

---

## 2. Observed gaps

### G1 — Three packages have zero tests

- **`internal/storage/storage.go`** (214 LOC). Wraps the AWS SDK v2
  S3 client. Public methods: `New`, `Upload`, `UploadIfNotExists`
  (the conditional-put branch via `If-None-Match: *` and
  `ErrAlreadyExists`), `Download`, `Delete`, `EnsureBucket`,
  `HeadBucket`. The `isPreconditionFailed` and `isNotFound` error
  classifiers (lines 187–214) are non-trivial branching code that
  is exercised only indirectly via `internal/api/photos_test.go`,
  which uses a fake storage backend — meaning these helpers are
  **never executed in CI**.
- **`internal/web/web.go`** (77 LOC). The SPA fallback handler
  has three branches (`MethodGet`/`MethodHead` gate, file-exists
  path, `ErrNotExist` → `index.html` fallback, generic read
  error). None are tested. A regression that breaks deep-link
  refreshes (a common SPA failure) would ship green.
- **`cmd/minerals` outside `migrate_test.go`** (~700 LOC of
  untested code in `serve.go`, `main.go`, `openapi.go`,
  `migrations.go`). `runServe` wires the entire dependency graph;
  `verifySchemaVersion` enforces the §6 mismatch contract. Both
  are silently un-exercised.

### G2 — `internal/api` is unit-tested with fakes; CONTRACT §9 mandates integration tests

CONTRACT.md §9 explicitly says (lines 1766–1770):

> **HTTP handlers** (`internal/api/`) — integration tests that hit
> the running server (`httptest.NewServer` against a fully wired
> handler tree). Cover happy path, the error envelope shape (per
> §10), and at least one auth-rejection case for protected routes.

Today's reality: `internal/api/*_test.go` files have no `//go:build
integration` tag and use hand-rolled in-memory `fakeXxxRepo`
implementations. The §9 rule "no mocks at the storage boundary" is
violated by every handler test. Risk: handler/repo wiring bugs (a
handler that builds the wrong filter, calls the wrong repo method,
or emits a response shape the real DB never produces) ship green.

### G3 — Required integration suites are missing

CONTRACT §9 lists six "MUST have tests" categories. Three of them
have **no tests at all**:

- **File upload pipeline** (§9): "upload accepted, variants
  generated, EXIF allowlist applied, `files` row written, and the
  transactional rollback case (DB insert fails → S3 object cleaned
  up)." Today this is split across (a) `internal/api/photos_test.go`
  which fakes both DB and storage, and (b) `internal/db/*photo*_
  integration_test.go` which exercises the repo only. The
  end-to-end "DB fails, S3 object is deleted" cleanup behavior
  has **no test that touches both**.
- **Auth pipeline integration** (§9): "Integration test verifying
  the public bucket is reachable without auth and the protected
  bucket is not." There is `internal/auth/auth_test.go` (unit) but
  no integration test asserting the bucket-level rule.
- **`internal/storage`** has no test of its own (covered in G1).

### G4 — Contract-vs-reality drift in §9

- `make test-cover` is referenced at CONTRACT.md §9 line 1870 but
  the `Makefile` has no such target. A polecat reading the
  contract and trying `make test-cover` will hit
  `make: *** No rule to make target 'test-cover'`.
- `internal/storage/storagetest/` is referenced at CONTRACT.md §9
  line 1852 ("Helper functions in `internal/storage/storagetest/`
  handle this; use them") but the directory does not exist.

### G5 — No coverage measurement, anywhere

CI runs the test suite but never records `-coverprofile`. There is
no per-package coverage floor, no PR-time delta report, no
historical trend. "Are these tests actually exercising the lines I
just wrote?" is unanswerable today.

### G6 — No `-race` in CI

`go test -race` catches data races at the cost of ~2× wall time.
The HTTP handler layer (`api.New(...)` plus the goroutine inside
`runServe`'s shutdown path), the pgxpool-backed repos, and the
image-processing pipeline (parallel-safe goroutines in
`golang.org/x/image/draw` callsites) are all fertile ground for
races. None would surface today.

### G7 — No `govulncheck` / SCA

`go.mod` pulls in `aws-sdk-go-v2`, `pgx/v5`, `huma/v2`, and a
chain of `dsoprea/go-exif` transitives that are years old. There
is no automated scan for known CVEs in any of them. The §17
"security never-do list" assumes the dependency tree is clean
without any tooling that verifies the assumption.

### G8 — No test parallelism / no shuffle

No file calls `t.Parallel()`. Total wall time of `make test` is
small today, so this is a comfort-not-correctness issue — but it
also hides accidental shared-state bugs that
`go test -shuffle=on` would surface.

### G9 — No fuzz tests on attack surface

`internal/markdown` (HTML sanitizer pipeline, §17) and
`internal/storage/exif` (parses untrusted user uploads) are the
two highest-value targets for `go test -fuzz`. Neither has a
single fuzz seed corpus.

### G10 — `golangci-lint` runs only the "standard" preset

The default preset is fine but conservative: it omits
`errcheck`-equivalents for HTTP body close, security smells
(`gosec`), HTTP-context-leak detection (`noctx`), and
exhaustiveness checks (`exhaustive`). For a project that takes
errors seriously (CONTRACT §10 specifies an error envelope), the
linter could be enforcing more.

---

## 3. Recommendations

### R1 — Add `make test-cover` target [S, ≤30 min]

- **Tool**: built-in `go test -coverprofile` + `go tool cover`.
- **License**: BSD-3-Clause (Go stdlib).
- **What it catches**: closes the §9 contract drift; gives polecats
  a one-command way to see which lines they actually exercised.
- **Integration footprint**: ~6 lines added to `Makefile`:
  ```makefile
  test-cover:
  	go test -race -coverprofile=coverage.txt -covermode=atomic ./...
  	go tool cover -html=coverage.txt -o coverage.html
  	@echo "Open coverage.html in your browser."
  ```
- **Tradeoffs**: none. `coverage.txt` / `coverage.html` are
  already in `.gitignore` (CONTRACT §2 lines 317–321).

### R2 — Add `-race` to CI [S, ≤30 min]

- **Tool**: built-in Go race detector.
- **License**: BSD-3-Clause.
- **What it catches**: data races in handler concurrency, pool
  reuse, or image-processing goroutine fan-out. The kind of bug
  that ships green and pages someone at 3 AM.
- **Integration footprint**: change CI step `Unit tests` from
  `make test` to `go test -race ./...` (or add `make test-race`
  and call it). Adds ~1 min to job.
- **Tradeoffs**: ~2× test runtime; `-race` requires `CGO_ENABLED=1`
  for the test binary build, which is fine in CI (the production
  build constraint is on the runtime image, not on test
  invocation). Worth re-confirming that the CI runner builds with
  cgo by default — `setup-go@v5` does.

### R3 — Add `govulncheck` to CI [S, ≤1 h]

- **Tool**: `golang.org/x/vuln/cmd/govulncheck`
  (https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck).
- **License**: BSD-3-Clause (golang.org/x/vuln).
- **What it catches**: known CVEs in direct + transitive
  dependencies, scoped to functions actually called (lower noise
  than naive SBOM scanners).
- **Integration footprint**: one CI step per workflow:
  ```yaml
  - name: govulncheck
    run: |
      go install golang.org/x/vuln/cmd/govulncheck@latest
      govulncheck ./...
  ```
  Plus a Makefile target `make vulncheck` for local use.
- **Tradeoffs**: occasional false-positives on transitive deps
  with no upstream fix; mitigate via `-show=traces` or selective
  package allowlisting in the future.

### R4 — Build `internal/storage/storagetest/` helpers + integration tests for `internal/storage` [M, ½–1 day]

- **Tool**: stdlib `testing` + the existing real-MinIO CI service.
- **License**: stdlib.
- **What it catches**: the conditional-put rejection path
  (`UploadIfNotExists` → `ErrAlreadyExists`), the head/get/delete
  round-trips, the bucket-not-found classifier — all currently
  exercised only via fakes (G1, G3).
- **Integration footprint**:
  1. New package `internal/storage/storagetest/` with `WithBucket(t)`
     (creates a per-test bucket, returns a `*storage.Client`,
     defers cleanup).
  2. New file `internal/storage/storage_integration_test.go`
     with `//go:build integration`, ~6–8 test cases.
  3. No production code changes. Closes G1 (storage), G4
     (storagetest dir), G3 (partial).
- **Tradeoffs**: requires MinIO at test time, but CI already
  runs it for the db integration suite — zero new infra.

### R5 — Lift `internal/api` tests to integration tier per CONTRACT §9 [L, multi-day, incremental]

- **Tool**: `httptest.NewServer` + the existing scopedDB / real-
  MinIO fixtures.
- **License**: stdlib.
- **What it catches**: handler/repo wiring bugs, real error-mapping
  behavior (pgx error codes → §10 envelope), real auth-context
  propagation, real upload pipeline including DB-rollback / S3-
  cleanup (G2, G3).
- **Integration footprint**:
  1. Add `//go:build integration` variants of each existing
     `internal/api/*_test.go`. Keep the unit fakes for pure
     input-validation cases (e.g. "415 on wrong content-type"
     doesn't need real Postgres).
  2. Wire `httptest.NewServer(api.New(realDeps))` against
     `scopedDB(t)` + `storagetest.WithBucket(t)`.
  3. Add the file-upload-pipeline rollback test (the §9 hard
     requirement) and the auth public-vs-protected-bucket test.
- **Tradeoffs**: slowest integration suite line-item; do
  package-by-package over multiple PRs (specimens → photos →
  journal → collectors). Don't gold-plate by deleting the unit
  fakes outright — they're useful for the validation-only cases.

### R6 — Expand `golangci-lint` linter set [M, ½ day]

- **Tool**: `golangci-lint` (already in use), enable additional
  linters via `.golangci.yml`.
- **License**: GPL-3.0 for the tool binary itself, but it runs as
  build-time tooling, not linked into the artifact (CONTRACT §16
  exempts build tooling from the runtime allowlist; §16 already
  pre-approves `golangci-lint`).
- **What it catches** (suggested adds):
  - `errcheck` (already in standard, confirm enabled) —
    unchecked error returns.
  - `bodyclose` — HTTP response bodies left unclosed.
  - `noctx` — HTTP requests built without a context.
  - `gosec` — known-bad patterns (weak crypto, SQL via
    `fmt.Sprintf`, exec injection).
  - `errorlint` — `errors.As` / `errors.Is` misuse, fmt verbs on
    wrapped errors.
- **Integration footprint**: edit `.golangci.yml`:
  ```yaml
  linters:
    default: standard
    enable:
      - bodyclose
      - errorlint
      - gosec
      - noctx
  ```
- **Tradeoffs**: each new linter will produce a one-time backlog
  of findings; most are 1–2-line fixes. `gosec` is the noisiest;
  consider per-rule disables (`G104` is often false-positive).

### R7 — Add fuzz tests for `internal/markdown` and `internal/storage/exif` [S–M, 2–4 h]

- **Tool**: built-in `go test -fuzz` (Go 1.18+).
- **License**: stdlib.
- **What it catches**: panics, infinite loops, sanitizer escapes
  in HTML rendering; out-of-bounds reads in EXIF parsing of
  malformed user uploads. Both packages handle untrusted input
  per §17.
- **Integration footprint**:
  - Add `FuzzRender(f *testing.F)` to
    `internal/markdown/markdown_test.go` — seed with the existing
    XSS-attempt fixtures the unit tests already use.
  - Add `FuzzParseExif(f *testing.F)` to
    `internal/storage/exif/exif_test.go` — seed with the JPEGs
    the unit tests construct.
  - Add a CI step `go test -fuzz=Fuzz -fuzztime=30s ./internal/markdown ./internal/storage/exif`
    on `main` only (not PR — fuzz time amplifies CI cost).
- **Tradeoffs**: the seed corpus directory
  (`testdata/fuzz/...`) is a maintenance line item if fuzz finds
  failures. That's a feature, not a cost.

### R8 — Add `t.Parallel()` to leaf unit tests + `-shuffle=on` to CI [S, 1–2 h]

- **Tool**: built-in.
- **License**: stdlib.
- **What it catches**: order-dependent tests (rare today, but
  becomes likely once R5 expands the integration suite); cuts
  wall time on the now-larger suite.
- **Integration footprint**: add `t.Parallel()` at the top of
  every test in `internal/markdown`, `internal/auth`,
  `internal/config`, `internal/storage/imageproc`. Add
  `-shuffle=on` to `make test`.
- **Tradeoffs**: a test that *was* implicitly relying on serial
  execution will now flake — the point is to surface that.
  Integration tests must NOT become parallel without per-test
  schemas (the existing `scopedDB(t)` already gives that, so
  adding `t.Parallel()` to the integration suite is also viable
  but riskier — keep it sequential for the first pass).

### R9 — Adopt `gotestsum` for CI test output [S, ≤1 h]

- **Tool**: `gotest.tools/gotestsum`
  (https://github.com/gotestyourself/gotestsum).
- **License**: Apache-2.0.
- **What it catches**: Nothing new — but produces a JUnit XML
  report and a human-readable failure summary, which makes the
  GitHub Actions test panel show test names instead of raw
  `go test` output. Lifts ergonomics, not coverage.
- **Integration footprint**: install in CI step, replace
  `go test ./...` with
  `gotestsum --junitfile junit.xml -- -race ./...`. Upload
  `junit.xml` as a workflow artifact.
- **Tradeoffs**: another build-time tool; trivial to remove.

### R10 — Publish coverage on PRs via Codecov (or Coveralls) [M, ½ day]

- **Tool**: https://about.codecov.io/ (free for public repos)
  or https://coveralls.io/ (free for public repos).
- **License**: SaaS, proprietary; the GitHub Action wrappers
  are MIT (`codecov/codecov-action`, `coverallsapp/github-action`).
  No code is added to the runtime image.
- **What it catches**: per-PR coverage delta, per-package
  numbers, untested-line annotations inline in the PR diff.
- **Integration footprint**: depends on R1 producing
  `coverage.txt`, then add a GitHub Actions step:
  ```yaml
  - uses: codecov/codecov-action@v5
    with:
      files: coverage.txt
  ```
  No source code changes.
- **Tradeoffs**: third-party SaaS dependency; needs a token
  (public-repo scope is unauthenticated). If that's not
  acceptable, `gocover-cobertura` + the GitHub Actions
  `codecov-action` against a self-hosted endpoint works too,
  or print coverage to PR comments via a small action.
  CONTRACT §16 license rules apply only to runtime/build-time
  Go deps; SaaS CI integrations are out of scope.

---

## 4. Prioritized list

The first three are zero-or-near-zero-risk wins; do them first.
R4–R5 close the contract drifts and require the most polecat-
hours. R10 is the long-term feedback loop; do it after R1.

1. **R1 (`make test-cover`) + R2 (`-race` in CI) + R8
   (`-shuffle=on`)** — three small Makefile / workflow edits,
   close the §9 drift, and start surfacing concurrency bugs the
   suite can already catch but isn't asked to. *Low-effort,
   high-signal.*
2. **R3 (`govulncheck`)** — one new CI step, tells you when the
   AWS / pgx / huma / dsoprea chain ships a CVE. Security floor
   for an app that will eventually face the public internet (per
   §17).
3. **R4 (`storagetest` + storage integration tests)** — closes
   the §9 helper-package drift AND the only-zero-test
   storage-boundary file in the repo. Required by CONTRACT §9,
   straightforward to implement, no ambiguity.
4. **R6 (more linters)** — `bodyclose`, `noctx`, `errorlint`,
   `gosec` will surface real findings the first time they run.
   Cheap to enable; one-time backlog to fix.
5. **R5 (lift `internal/api` to integration tier)** — biggest
   contract drift to close, and only L-effort because it's done
   per-handler-package over multiple PRs. Get the file-upload
   rollback and auth bucket-rule tests in early; the rest can
   land incrementally.

R7, R9, R10 are nice-to-haves; sequence them after the top five
land.

---

## 5. Out of scope (deliberately not recommended)

- **End-to-end browser tests (Playwright, Cypress)** — already
  deferred by CONTRACT §9 line 1753–1755 ("End-to-end browser
  tests are deferred until the SPA has enough surface to warrant
  them"). Revisit when the SPA has multi-step user flows that
  the current integration suite can't reach via HTTP alone.
- **Mutation testing (`go-mutesting`, `gremlins`)** — high
  per-PR runtime cost, brittle reports, and the project hasn't
  yet exhausted easier wins (R1–R6). Reconsider once R5 is
  complete and `internal/api` coverage is genuinely high.
- **BDD frameworks (`ginkgo`, `gomega`, `goconvey`)** — the
  existing test style is plain stdlib `testing` with table-
  driven cases. Introducing a BDD layer would force a rewrite of
  the entire suite for stylistic reasons; not justified.
- **`testify`** — the team has chosen explicit `t.Fatalf` /
  `t.Errorf` calls. Adding `assert.Equal` style would split the
  codebase between two assertion conventions for marginal
  ergonomic gain.
- **Snapshot testing of HTTP responses** — easy to add, but
  brittle against benign envelope changes (`updated_at`
  timestamps, request IDs). The existing JSON-decode-and-
  field-assert pattern is fine.
- **Postman / Newman API contract tests** — Huma already derives
  the OpenAPI spec from handler signatures (mi-cy4) and the
  frontend client is regenerated from that spec. The contract is
  enforced at compile time on both sides; an external smoke
  suite would be redundant.
- **Performance / benchmark tests in CI** — no contract
  requirement and no SLO to test against yet. Add when there's
  a target.

---

## References

- CONTRACT.md §9 — Testing requirements (lines 1737–1922).
  The "MUST have tests" list at lines 1759–1786 is the
  authority for what the integration suite must cover.
- CONTRACT.md §16 — Dependencies & libraries (lines 3207–3369).
  License allowlist at 3316–3344. None of the tools recommended
  above are GPL/AGPL/source-available; `golangci-lint` already
  exists in the pre-approved table at line 3267.
- CONTRACT.md §17 — Security never-do list. Frames why
  `govulncheck` (R3), fuzz tests on the markdown sanitizer and
  EXIF parser (R7), and `gosec` / `bodyclose` / `noctx` linters
  (R6) are pulling on the same thread.
- Bead **mi-fmj** — Q-1 acceptance criteria.
