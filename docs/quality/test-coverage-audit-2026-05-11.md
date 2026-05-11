# Test coverage audit — post Q-wave

> Scope: backend + frontend coverage after the Q-1 / Q-2 test wave landed.
> Date: 2026-05-11. Bead: `mi-5si`.
> Companion to `backend-test-coverage.md` (Q-1) and `frontend-test-coverage.md` (Q-2).

## TL;DR

The Q-wave closed most of the original gaps. **Backend total coverage is 40.6%
unit-only** — but that headline is misleading because `internal/db` (3.7%) and
`internal/storage` (0%) are integration-only by design and run green in CI with
Postgres+MinIO. The genuine remaining gaps are:

- **`internal/web` (77 LOC, 0% coverage)** — SPA fallback handler. Never
  tested; multiple branches (method gate, ErrNotExist → index.html, generic
  read error) ship blind. **Carried over from Q-1 G1.**
- **`internal/domain.QRSheetTemplateCapacity` (0%)** — pure switch with five
  branches that drags `internal/domain` to 68% (below the 70% CONTRACT
  threshold). Trivial to test.
- **`internal/api` error-mapper tails** — `mapPhotoError`, `mapListError`
  (collectors), `huma_errors.Error` at 0%; `codeForStatus` at 30.8%. The
  integration wave got `internal/api` to 69% but the §10 error-envelope
  classifiers are still mostly unexercised.
- **Frontend `ImageCropModal.svelte` (65.3% lines)** and
  **`MineralSpeciesAutocomplete.svelte` (70.8% lines, 66.7% functions)** —
  both below the 80%/85% line floor codified in `vitest.config.ts`. These are
  new components added after the Q-2 review.

Three Q-wave R-items are still unaddressed but are intentional deferrals (Q-1
R10 Codecov, Q-2 R9 Playwright, Q-2 R10 Stryker). One R-item (Q-1 R10) is a
genuine pending follow-up; the other two were explicitly deferred by §9.

---

## 1. Method

- `go test -coverprofile=coverage.txt ./...` (unit only, no DB/MinIO).
- `go tool cover -func=coverage.txt` for per-function breakdown.
- `go test -tags=integration ./...` to confirm integration suites compile and
  run with skip-on-no-DATABASE_URL.
- `npm run test:cover` (Vitest + `@vitest/coverage-v8`) for frontend.
- Cross-referenced against `docs/quality/backend-test-coverage.md` (Q-1
  `mi-fmj`) and `docs/quality/frontend-test-coverage.md` (Q-2 `mi-ih0`) to
  identify which R-items the wave addressed and which remain.
- Wave beads checked: `mi-b5n` (api integration), `mi-8c5` (component tests),
  `mi-h01` (linters), `mi-h8j` (fuzz), `mi-xql` (govulncheck), `mi-qh7`
  (storage integration), `mi-6f8` (schema unit tests), `mi-4sz` (frontend CI
  plumbing), `mi-k9t` (vitest-axe), `mi-gof` (property tests), `mi-qb3`
  (`-race`/`-shuffle`/`gotestsum`).

---

## 2. Backend coverage by package

Unit-only run (`go test ./...`, no integration tag, no DB):

| Package | Coverage | Notes |
|---|---:|---|
| `internal/auth` | **100.0%** | ✓ |
| `internal/config` | **95.3%** | ✓ |
| `internal/markdown` | **90.5%** | ✓ + fuzz harness |
| `internal/mindat` | **85.7%** | ✓ |
| `internal/storage/imageproc` | **80.0%** | ✓ |
| `internal/storage/exif` | **77.1%** | ✓ + fuzz harness |
| `internal/api` | **69.0%** | Just under 70% threshold; see §3 below |
| `internal/domain` | **68.0%** | One pure function at 0% drags the average |
| `internal/db` | 3.7% | Integration-only by design (skips without `DATABASE_URL`) |
| `internal/storage` | 0.0% | Integration-only (needs MinIO) |
| `internal/web` | **0.0%** | **Genuine gap — see §3 G1** |
| `internal/storage/storagetest` | 0.0% | Test helper, not source code |
| `cmd/minerals` | 0.0% / 7.9% w/ integration | Entry point; migrate subcommand tested |
| `migrations/` | n/a | No test files (SQL only) |

CI runs unit + integration with both services up, so the real per-package
floor in CI is significantly higher than the unit-only number. The
`storage_integration_test.go` and the nine `internal/db/*_integration_test.go`
files all execute against the GHA Postgres + MinIO services.

## 3. Backend gaps

### G1 — `internal/web` is still untested (carried from Q-1 G1)

77 LOC, 0% coverage, two functions (`FS`, `Handler`). The `Handler` body has
four branches:

1. Method gate — `GET`/`HEAD` only.
2. File-exists path — serve via `http.FileServer`.
3. `ErrNotExist` path — open `index.html`, rewrite the request, serve.
4. Generic read error — 500.

A regression that breaks deep-link refreshes (the SPA fallback contract) would
ship green today. Q-1 R5 (lift `internal/api` to integration tier) addressed
the handler tier above it, but never reached this package. The integration
suite spins MinIO and Postgres but never exercises the embedded SPA shell.

**Severity**: P3. The SPA fallback breakage is loud (the whole frontend
404s on refresh), so it would be caught in manual smoke, but blind regressions
are a comfort the rest of the codebase has.

**Effort**: S (≤1h). The handler is pure stdlib; a `httptest.NewRecorder` +
synthesized `fs.FS` (via `fstest.MapFS`) covers all four branches without
touching the embed directive.

### G2 — `internal/domain.QRSheetTemplateCapacity` at 0% (drags package to 68%)

The function is a pure switch over the five v1 Avery templates returning
`(int, bool)`. `internal/domain` is at 68% — just below the §9-implied 70%
floor — because this is the only untested function in the file (other than
`NewID` at 75%, where the uncovered line is the `uuid.New()` error path that
`google/uuid` does not actually return).

The function is called by the API layer to validate `POST /qr-sheets`
templates. A bug here is a silent contract drift between the validator and
the GET handler's `page count = ceil(specimen_count / capacity)`.

**Severity**: P3. Low blast radius (v1 has five templates, only one used in
practice) but trivial to fix.

**Effort**: S (≤15 min). Six table-driven cases, one file edit.

### G3 — `internal/api` error mappers at 0%

The wave got `internal/api` to 69%. The functions still at 0%:

- `mapPhotoError` (`photos.go:693`) — maps domain errors to §10 envelopes for
  the photo subtree.
- `mapListError` (`collectors.go:307`) — list-pagination error classifier.
- `huma_errors.Error` (`huma_errors.go:28`) — global error transformer hook.
- `huma_errors.codeForStatus` at 30.8% — only the 4xx → code-string branch
  is tested; the 5xx classifier paths are not.

These are §10 contract surface. A regression that emits the wrong envelope
shape (e.g. forgets the `code` field, or maps the wrong pgx error to
`conflict`) would ship green against the existing integration suite because
the suite asserts on status codes and body fields it expects — not on the
classifier branches the production code takes.

**Severity**: P3. CONTRACT §10 is explicit that the error envelope is part
of the API contract.

**Effort**: M (~3h). Each mapper is a 5–10-line switch over `errors.Is` /
typed errors; tests can be table-driven in a new
`internal/api/errors_test.go` and exercise the mappers directly via the
exported helpers (no HTTP scaffolding needed).

### G4 — `cmd/minerals/serve.go` and `openapi.go` at 0%

- `serve.go` (~220 LOC). `runServe` wires the entire dependency graph;
  `verifySchemaVersion` enforces the §6 schema-mismatch contract;
  `configureLogger` parses `LOG_LEVEL` + `LOG_FORMAT`. None tested.
- `openapi.go` (~700 LOC). Generates the OpenAPI spec at build time via
  `cmd/minerals openapi`. The spec is consumed by frontend codegen (mi-cy4).
  Tested only via the `make openapi-spec` step in CI succeeding; no
  assertions on its content.

**Severity**: P4. `runServe` is a process-entry point that integration tests
*could* cover but the compose-smoke job already does end-to-end (it brings
the binary up, polls `/healthz`, hits the API). `openapi.go` is in effect a
build script — its output is the test.

**Verdict**: Document as known, but not a P3 gap. The compose-smoke and the
fact that frontend codegen breaks visibly on bad OpenAPI cover the failure
modes.

---

## 4. Frontend coverage

Total: **89.13% lines, 79% branches, 83.87% functions** — comfortably above
the floor in `vitest.config.ts` (`lines: 85, statements: 85, functions: 80,
branches: 75`).

Per-file breakdown of files **below the 85% line floor** (excluding §9-exempt
trivial wiring — `App.svelte`, `Layout.svelte`, `ThemeToggle.svelte`):

| File | Lines | Branches | Functions | Notes |
|---|---:|---:|---:|---|
| `lib/ImageCropModal.svelte` | **65.3%** | 72.7% | 64.3% | **G5** — added after Q-2 review |
| `lib/MineralSpeciesAutocomplete.svelte` | **70.8%** | 69.2% | 66.7% | **G6** — added after Q-2 review |
| `routes/SpecimenEdit.svelte` | 79.3% | 67.3% | 88.9% | Just under floor |
| `routes/SpecimenNew.svelte` | 81.0% | **61.1%** | 75.0% | Branches under floor |
| `lib/JournalAttachments.svelte` | 82.3% | 67.6% | 78.9% | Branches under floor |

## 5. Frontend gaps

### G5 — `lib/ImageCropModal.svelte` at 65.3% lines / 64.3% functions

Added by `mi-wisp-9r6` (rotate controls). The Q-2 review predates this
component. Uncovered region is lines 33–55 and 208–310 — based on file shape,
likely the canvas-rendering / save path and the keyboard handler.

A regression in the cropping math (off-by-one in the canvas rectangle,
wrong rotation angle handling) would ship green.

**Severity**: P3. User-visible feature, manual smoke is the only safety net
today.

**Effort**: S (≤2h). Pattern follows `Lightbox.test.ts` (also keyboard +
state, also tested via `fireEvent.keyDown`); jsdom can render canvas via the
existing setup with no new polyfills (no `<canvas>` assertions needed — assert
on the emitted blob's MIME type and the `onSave` callback args).

### G6 — `lib/MineralSpeciesAutocomplete.svelte` at 70.8% lines / 66.7% functions

Added during the mineral species lookup work (mi-c1m / Mindat integration).
Uncovered region is lines 113–128 and 144–161 — likely the keyboard
navigation in the dropdown and the "no results / loading" UI branches.

**Severity**: P3. The component is on the `SpecimenForm`; a regression in
the keyboard navigation or empty-state would be loud, but a regression in
the debounced fetch (cancellation, error fallback) would not be.

**Effort**: S (≤2h). Mock the `client.GET` for `/api/v1/mineral-species`
the same way `JournalAttachments.test.ts` does, drive `ArrowDown` / `Enter`
through `fireEvent.keyDown`, assert on selected item.

### G7 — `routes/SpecimenNew.svelte` branch coverage at 61.1%

Just under the 75% branch floor in `vitest.config.ts`. Uncovered branches
(per the coverage output, lines 32-36 / 43-44 / 49-50) are most likely the
duplicate-catalog error path and the navigate-on-success / error-recovery
forks. The existing test (3 cases) covers happy path + 409 — there's at
least one error branch missing.

**Severity**: P4. The 409 path is tested; the residual gap is likely
network-error fallback that doesn't have a user-visible distinction.

**Effort**: S (≤30 min). One added test case.

---

## 6. Still-unaddressed R-items from the Q-wave

Cross-referenced against the original Q-1 and Q-2 R-lists:

### Q-1 (backend) status

| R | Title | Status |
|---|---|---|
| R1 | `make test-cover` | ✓ landed |
| R2 | `-race` in CI | ✓ landed (`mi-qb3`, via `gotestsum -- -race`) |
| R3 | `govulncheck` | ✓ landed (`mi-xql`) |
| R4 | `storagetest` helpers + storage integration | ✓ landed (`mi-qh7`) |
| R5 | Lift `internal/api` to integration tier | ✓ landed (`mi-b5n`) |
| R6 | Expand `golangci-lint` linters | ✓ landed (`mi-h01`, `mi-3xm`, `mi-4wm`, `mi-aqa`) |
| R7 | Fuzz harnesses | ✓ landed (`mi-h8j`) |
| R8 | `t.Parallel` + `-shuffle=on` | **partial** — `-shuffle=on` landed in `mi-qb3`; `t.Parallel()` was not added to leaf tests |
| R9 | `gotestsum` | ✓ landed (`mi-qb3`) |
| R10 | Publish coverage on PRs (Codecov / Coveralls) | **unaddressed** — coverage artifact uploaded; no PR-level delta / inline annotation |

### Q-2 (frontend) status

| R | Title | Status |
|---|---|---|
| R1 | `@vitest/coverage-v8` | ✓ landed (`mi-4sz`) |
| R2 | Coverage thresholds | ✓ landed (in `vitest.config.ts`) |
| R3 | Specimen marshalling tests | ✓ landed (`mi-6f8`) |
| R4 | `Lightbox.svelte` test | ✓ landed (`mi-8c5`) |
| R5 | `SpecimenCard.svelte` test | ✓ landed (`mi-8c5`) |
| R6 | `CollectorForm.svelte` test | ✓ landed (`mi-8c5`) |
| R7 | `vitest-axe` | ✓ landed (`mi-k9t`) |
| R8 | Property-based tests via `fast-check` | ✓ landed (#80, `mi-gof`) |
| R9 | Playwright E2E | deferred per CONTRACT §9 |
| R10 | Stryker mutation testing | deferred (depends on R1+R2 bedding in — now they have, but mutation is still high-cost) |
| R11 | `make test-frontend` + `test-cover-frontend` | ✓ landed (`mi-4sz`) |
| R12 | `--reporter=github-actions` | ✓ landed (`mi-4sz`) |

### Q-1 R8 partial — `t.Parallel()` on leaf unit tests

`mi-qb3` added `-shuffle=on` to CI but did not annotate the leaf unit tests
with `t.Parallel()`. Without `t.Parallel()`, shuffle reorders test files but
each file's tests still run sequentially — the order-dependence detector
this combo is meant to be doesn't actually fire. Adding `t.Parallel()` to
the deterministic-leaf packages (`internal/markdown`, `internal/auth`,
`internal/config`, `internal/storage/imageproc`) is the missing half.

**Severity**: P4. Comfort, not correctness. The test suite is small enough
that the wall-time win is negligible and order-dependence isn't manifest.

**Effort**: S (≤1h). Mechanical edit; verify each test doesn't share state
with another (none do).

---

## 7. Prioritized gap list

| # | Gap | Severity | Effort | Notes |
|---|---|---|---|---|
| 1 | G1 — `internal/web` SPA fallback handler | P3 | S | One new file, ~80 LOC |
| 2 | G3 — `internal/api` error mappers (mapPhotoError, mapListError, huma_errors) | P3 | M | Table-driven tests; ~150 LOC |
| 3 | G2 — `internal/domain.QRSheetTemplateCapacity` | P3 | S | 5 table cases |
| 4 | G5 — `ImageCropModal.svelte` test | P3 | S | ~100 LOC; pattern from Lightbox |
| 5 | G6 — `MineralSpeciesAutocomplete.svelte` test | P3 | S | ~80 LOC |
| 6 | G7 — `SpecimenNew.svelte` error-branch test | P4 | S | One extra case |
| 7 | Q-1 R10 — Codecov / Coveralls PR delta | P4 | S | One CI step + token decision |
| 8 | Q-1 R8 partial — `t.Parallel()` on leaf tests | P4 | S | Mechanical |
| 9 | G4 — `cmd/minerals/serve.go` runServe / verifySchemaVersion | P4 | M | Mostly covered by compose-smoke; document |

The five P3 items are the actionable list. Items 7–9 are nice-to-have or
already-covered-elsewhere; documented for visibility.

The two deferred R-items (Q-2 R9 Playwright, Q-2 R10 Stryker) remain
deferred per CONTRACT §9 and the original Q-2 ranking; no change recommended
in this audit. Revisit Playwright once the SPA grows beyond the seven-route
v1 surface; revisit Stryker once the frontend coverage floor (currently
89%) has been stable for two release cycles.

---

## 8. Out of scope

- **Increasing `internal/db` / `internal/storage` unit coverage** — these
  packages are intentionally integration-only per CONTRACT §9 (lines
  1759–1786). The 3.7% / 0% unit numbers are not gaps; they are the
  contract working as intended. CI runs the integration suite with real
  Postgres + MinIO and provides the actual coverage these packages have.
- **`cmd/minerals/main.go` and `openapi.go`** — process entry and build-
  time spec generator. The compose-smoke job and the frontend codegen
  consumer are the real tests.
- **`App.svelte`, `Layout.svelte`, `ThemeToggle.svelte`, `main.ts`,
  `routes.ts`, `lib/api/index.ts`** — §9-exempt trivial wiring per the
  Q-2 review's classification.
- **Generated code** — `lib/api/schema.d.ts` is excluded from coverage by
  config; no change needed.

---

## References

- `docs/quality/backend-test-coverage.md` (Q-1, `mi-fmj`).
- `docs/quality/frontend-test-coverage.md` (Q-2, `mi-ih0`).
- CONTRACT.md §9 — testing requirements (lines 1737–1922).
- CONTRACT.md §10 — error envelope contract.
- `vitest.config.ts` — frontend coverage thresholds.
- `.github/workflows/pr.yml` — CI gates.
- ROADMAP.md V1 "Quality & CI" section.
- Bead `mi-5si` — this audit.
