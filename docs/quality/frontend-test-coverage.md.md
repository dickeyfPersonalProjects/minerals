# Frontend test coverage — quality review (Q-2)

> Scope: TypeScript + Svelte frontend under `frontend/`. Excludes the
> Go backend.
> Date: 2026-05-09. Bead: `mi-ih0`.

## TL;DR

The frontend has **18 test files (3561 LOC, 153 `it()` cases)
covering 14 of 20 Svelte components and 4 of 8 hand-written TS
modules**. Form/route flows are well-exercised — `SpecimenForm`,
`SpecimenDetail`, `SpecimenFilters`, `Specimens`, `JournalAttachments`,
`PhotoUploader`, the toast middleware all have real component tests.
But there is **zero coverage measurement** (no `@vitest/coverage-*`
package, no CI step, no threshold), and the largest pure-logic
file in the repo — **`lib/schemas/specimen.ts` (532 LOC, 17
exported / internal helpers)** — has **no direct unit test**: its
marshalling functions (`formToCreateBody`, `formToPatchBody`,
`specimenToFormValues`, `resetTypeDataDefaults`) are exercised only
indirectly through form-submit assertions. The cheapest, highest-
signal moves are: (1) wire `@vitest/coverage-v8` and report a number
on every PR, (2) add direct tests for the specimen schema marshalling
helpers, (3) add component tests for `Lightbox` (keyboard nav,
index wraparound) and `SpecimenCard` (thumb fallback / abort).
All three are S- or M-effort, MIT/Apache-2.0 licensed, and add no
runtime dependencies.

---

## 1. Current state

### Codebase shape

- `frontend/`: Vite 6 + Svelte 5 + TypeScript 5.7, Vitest 3.2 test
  runner, jsdom 25 environment.
- **20 Svelte components** (5092 LOC), **8 hand-written TS modules**
  (977 LOC, excluding the 2771-LOC generated `lib/api/schema.d.ts`
  and the 43-LOC `test-setup.ts`).
- **18 test files** (3561 LOC, 153 `it()` cases inside 32
  `describe()` blocks).
- Test-to-source ratio: 3561 / (5092 + 977) ≈ **0.59 test LOC per
  source LOC** — comfortably above the backend's 1:1.7 ratio for
  Go, but the surface is also thinner.

### Test framework and configuration

`frontend/package.json` devDependencies:

| Package | Purpose |
|---|---|
| `vitest` `^3.2.4` | Test runner |
| `@testing-library/svelte` `^5.3.1` | Svelte 5 component renderer |
| `@testing-library/jest-dom` `^6.6.3` | DOM matchers (`toBeInTheDocument` etc.) |
| `jsdom` `^25.0.1` | DOM environment |

Scripts (only the test-relevant ones):

```json
"check": "svelte-check --tsconfig ./tsconfig.json",
"lint": "eslint .",
"test": "vitest run"
```

`frontend/vitest.config.ts`:

```ts
plugins: [svelte(), svelteTesting()],
test: { environment: 'jsdom', globals: true, setupFiles: ['./src/test-setup.ts'] }
```

`src/test-setup.ts` polyfills two jsdom gaps:
- `Element.prototype.animate` (Svelte 5 transitions call this)
- `window.matchMedia` (theme bootstrap reads this)

There is no `coverage` block in `vitest.config.ts`, no
`@vitest/coverage-v8` / `@vitest/coverage-istanbul` devDependency,
no `--coverage` flag in any script.

### Test categorization (Svelte/TS)

CONTRACT §9 (lines 1895–1906) splits frontend tests into:

- **Unit tests** for utility modules — pure functions, formatting,
  the time helpers from §8.
- **Component tests** for critical UI flows — specimen create
  form, photo upload widget. Svelte Testing Library + Vitest.
- **E2E / browser tests deferred.**

The repo follows this split but does not surface it (no `*.unit.ts`
/ `*.component.ts` naming, no separate config). That's fine for
v1 — there's no infrastructure asymmetry between the two categories
the way the Go side has between unit and integration.

### Coverage by file

**Svelte components — tested** (14 of 20):

| Component | Component LOC | Test LOC | `it()` cases |
|---|---:|---:|---:|
| `lib/SpecimenForm.svelte` | 837 | 220 | 9 |
| `lib/SpecimenFilters.svelte` | 501 | 272 | 18 |
| `lib/JournalAttachments.svelte` | 449 | 239 | 12 |
| `lib/PhotoUploader.svelte` | 337 | 186 | 9 |
| `lib/CollectorChainEditor.svelte` | 325 | 270 | 13 |
| `lib/JournalEntryForm.svelte` | 129 | 112 | 6 |
| `lib/ConfirmModal.svelte` | 145 | 114 | 7 |
| `lib/Toaster.svelte` | 73 | 66 | 4 |
| `routes/SpecimenDetail.svelte` | 800 | 651 | 27 |
| `routes/Specimens.svelte` | 238 | 238 | 11 |
| `routes/SpecimenEdit.svelte` | 229 | 231 | 9 |
| `routes/SpecimenNew.svelte` | 70 | 103 | 4 |
| `routes/CollectorEdit.svelte` | 130 | 144 | 5 |
| `routes/Collectors.svelte` | 301 | 315 | 12 |

**Svelte components — untested** (6 of 20):

| Component | LOC | Triviality assessment |
|---|---:|---|
| `App.svelte` | 11 | Trivial wiring (`<Layout><Router/></Layout><Toaster/>`); §9 exempt. |
| `lib/Layout.svelte` | 56 | Header + footer template + `<a use:link>`; mostly markup. Borderline-trivial. |
| `lib/ThemeToggle.svelte` | 56 | One click handler; the underlying `theme.ts` is well-tested in `theme.test.ts`. Borderline-trivial. |
| `lib/SpecimenCard.svelte` | 129 | **Non-trivial**: lazy `$effect` fetches first photo via API, abort on unmount, `thumbFailed` fallback. |
| `lib/Lightbox.svelte` | 132 | **Non-trivial**: `$state` index, prev/next wraparound, keyboard handler (`Escape` / `ArrowLeft` / `ArrowRight`), focus trap, `onDelete` callback, autoclose-when-empty `$effect`. |
| `lib/CollectorForm.svelte` | 144 | **Non-trivial**: felte form with `duplicate` / `error` submit-result branches and `nameTakenError` field-scoped error path — same shape as the well-tested `SpecimenForm` and `JournalEntryForm`. |

**TS modules — tested** (4 of 8):

| Module | Source LOC | Test LOC | What it covers |
|---|---:|---:|---|
| `lib/time.ts` | 40 | 50 | `formatDate`, `formatDateTime` rendering & locale defaults. |
| `lib/theme.ts` | 120 | 147 | `themeStore`, `toggleTheme`, persistence, `prefers-color-scheme` resolution. |
| `lib/toasts.ts` | 76 | 86 | Toast store CRUD, dismiss, dedupe. |
| `lib/api/wrapper.ts` | 71 | 117 | `envelopeMessage`, the auto-toast middleware (5 `it()` covering 2xx pass-through, suppress header, JSON / non-JSON envelopes, network error). |

**TS modules — untested** (4 of 8):

| Module | LOC | Triviality assessment |
|---|---:|---|
| `routes.ts` | 24 | Plain route table; §9 exempt (data, no behavior). |
| `main.ts` | 22 | Mount entrypoint (`new App({ target })`); §9 exempt (framework boot). |
| `lib/api/index.ts` | 14 | One-liner: `installToastMiddleware()` + `createClient`. §9 exempt. |
| `lib/schemas/specimen.ts` | **532** | **Non-trivial.** `formToCreateBody`, `formToPatchBody`, `specimenToFormValues`, `resetTypeDataDefaults`, `parseOptionalFloat`, `priceDollarsToCents`, `toRfc3339`, `toDateInputValue`, `buildDimensions`, `buildLocality`, `buildTypeData`, `dimsEqual`, `localityEqual`, `typeDataEqual`, `arraysEqual` — 17 functions, all branching, all exercised only indirectly via `SpecimenForm.test.ts` form-submit assertions. |
| `lib/schemas/journal.ts` | 24 | Tiny — one Zod schema + empty constructor. Validation shape exercised via `JournalEntryForm.test.ts`. Borderline-trivial. |
| `lib/schemas/collector.ts` | 11 | Tiny — one Zod schema. Borderline-trivial. |

### CI gates

`.github/workflows/pr.yml` frontend job (`name: Frontend`):

```yaml
- npm ci
- npx prettier --check .
- npx eslint .
- npm run check          # svelte-check (typecheck)
- npm test               # vitest run
```

`.github/workflows/main.yml` frontend job: identical to PR. **No
`--coverage`, no JUnit reporter, no annotations.** Test failures
appear as raw vitest output in the workflow log, not as inline
PR annotations.

`Makefile` frontend targets: `fmt-frontend`, `fmt-check-frontend`,
`lint-frontend`. **No `test-frontend` or `test-cover-frontend`
target** — the CI `npm test` step is the only invocation path.

### Test patterns in use

- **API mocking**: every route/component test that talks to the
  backend hoists `vi.fn()` stubs and replaces `../lib/api`'s
  `client` (`vi.mock('../lib/api', () => ({ client: { GET: ..., ... } }))`).
  The `withFetch` helper in `wrapper.test.ts` is the only place
  that drives the real `openapi-fetch` client against a stubbed
  global `fetch`. Pattern is consistent.
- **Render + `screen.getByTestId(...)`**: components consistently
  expose `data-testid` attributes (`specimen-form`, `name-error`,
  `journal-cancel-button`, etc.) and tests query against them.
- **`fireEvent` + `waitFor`**: standard async pattern.
- **`afterEach(() => cleanup())`** is sometimes called, sometimes
  delegated to `@testing-library/svelte` defaults. Mostly fine
  but slightly inconsistent.
- **`vi.spyOn(window, 'confirm')`**: native confirm dialogs are
  mocked at the window level (used in `Collectors.test.ts`,
  `JournalAttachments.test.ts`).
- **No `t.skip` / `it.todo`** — verified via grep.
- **No `vi.useFakeTimers()`** — every test exercises real
  microtasks; no time freezing today.

### Contract context

- CONTRACT.md §9 (lines 1895–1906) — "**Frontend tests**" section.
  Sets the bar: unit tests for utility modules, component tests
  for critical UI flows, **E2E deferred**. Today's suite meets the
  bar; this review is about *measuring* it and closing the
  schema-marshalling gap §9 implicitly demands ("don't test
  trivial Svelte template renderings, … do test branching
  logic").
- CONTRACT.md §9 line 1796–1810 — exemptions: trivial getters,
  pure data definitions, generated code (`lib/api/schema.d.ts` is
  exempt by name).
- CONTRACT.md §16 (lines 3316–3344) — license allowlist: MIT,
  BSD, Apache-2.0, ISC, MPL-2.0, public domain. Forbidden:
  GPL/AGPL family, BSL/SSPL/Elastic/Confluent, custom/unknown.
  Every recommended tool below clears this bar.
- CONTRACT.md §17 — no test-related rules; security scanning
  separately covered by Q-6 (`docs/quality/frontend-vuln-scanning.md.md`).

---

## 2. Observed gaps

Specific, unambiguous misses:

### G1 — No coverage measurement, anywhere

Vitest has first-class coverage support via `@vitest/coverage-v8`
or `@vitest/coverage-istanbul`. Neither is installed, neither is
configured, no CI step requests it. Concretely:

- `frontend/package.json` has no `coverage` devDependency.
- `vitest.config.ts` has no `test.coverage` block.
- `pr.yml` has no `--coverage` flag and no upload step.
- No `coverage-summary.json` artifact, no Codecov / Coveralls
  integration.

"Did the test for `SpecimenForm` actually exercise the catalog-
number-conflict branch?" is unanswerable without running coverage
locally. New code can ship with zero exercising tests as long as
the file *has* a sibling `*.test.ts`.

### G2 — `lib/schemas/specimen.ts` marshalling helpers are untested

The file is 532 LOC and exports / declares **17 functions**, all
with branching:

- `formToCreateBody(values)` — converts form values to API create
  body, with conditional `dimensions` / `locality` / `type_data`
  inclusion.
- `formToPatchBody(initial, values)` — diff-style: only includes
  fields that changed. Uses `dimsEqual`, `localityEqual`,
  `typeDataEqual`, `arraysEqual` to compute diffs.
- `specimenToFormValues(s)` — converts API `SpecimenView` back to
  form state, including `priceCents` → dollars-string and
  RFC3339 → date-input.
- `resetTypeDataDefaults(values, type)` — clears the wrong
  type's fields when the user toggles type radio.
- `parseOptionalFloat`, `priceDollarsToCents`, `toRfc3339`,
  `toDateInputValue`, `buildDimensions`, `buildLocality`,
  `buildTypeData`, `dimsEqual`, `localityEqual`, `typeDataEqual`,
  `arraysEqual` — all pure helpers with branching.

`SpecimenForm.test.ts` covers user-facing flows but never:
- Asserts that `formToCreateBody({ ..., mass_g: '' })` produces
  `mass_g: null` (vs the string `''`).
- Verifies that `formToPatchBody` omits unchanged fields (the
  whole point of PATCH semantics).
- Round-trips `specimenToFormValues(formToCreateBody(x))` to
  catch impedance-mismatch regressions.

These are exactly the "branching pure functions" §9 lists as
MUST-have-tests. They're trivially testable in plain Vitest with
zero DOM scaffolding.

### G3 — `Lightbox.svelte` is untested and has non-trivial behavior

132 LOC with: keyboard navigation (`Escape` closes, `ArrowLeft`
prev, `ArrowRight` next), index wraparound (`(index - 1 + photos.length) % photos.length`),
auto-close `$effect` when `photos.length` becomes 0, optional
`onDelete` callback. None of this is exercised. A regression that
flips the wraparound modulo arithmetic, breaks the keyboard
handler, or fails to close on empty would ship green.

### G4 — `SpecimenCard.svelte` is untested

129 LOC with a per-mount `$effect` that:
- Issues `client.GET('/api/v1/specimens/{id}/photos')` to fetch
  the first photo URL for the card thumbnail.
- Aborts via `AbortController` on unmount.
- Falls back to a `thumbFailed` placeholder when the request
  errors or returns zero photos.

The `Specimens.svelte` route test renders the list, but mocks the
API at the client level — meaning the per-card `$effect` is hit
but the abort path and the `thumbFailed` fallback are never
asserted. The card is the user-visible primary entry point on the
specimens list; a regression here is loud.

### G5 — `CollectorForm.svelte` is untested

144 LOC of felte+Zod form with three `CollectorFormSubmitResult`
branches (`ok`, `duplicate`, `error`) and a field-scoped
`nameTakenError` path. The structurally-identical
`JournalEntryForm.svelte` (129 LOC, same submit-result pattern)
*is* tested with 6 cases; `CollectorForm` is not. The duplicate /
field-scoped path is exercised at the **route** level via
`CollectorEdit.test.ts` and `Collectors.test.ts`, but the form
component itself has no dedicated test, so a refactor (e.g.
swapping `felte` for `superforms`) wouldn't be caught.

### G6 — No accessibility assertions in tests

The components use semantic HTML (`<button aria-label=...>`,
`role="alert"`, `aria-invalid`, `aria-describedby`) and the tests
reference some of these attributes implicitly via
`screen.getByRole('radio', { name: /rock/i })`. But:

- No test runs `axe-core` (`vitest-axe` / `@axe-core/svelte`)
  against rendered output.
- No test asserts focus management on dialog open/close
  (`ConfirmModal`, `Lightbox`).
- No test asserts color-contrast or computed-style invariants
  (jsdom can't do contrast, but axe checks structural a11y).

A regression that drops the `aria-label` on `ThemeToggle`, breaks
the modal's focus trap, or yields disconnected `for`/`id`
associations on form labels would not fail any test.

### G7 — No CI test annotations / JUnit output

Vitest emits raw text into the GitHub Actions log; it does **not**
write a JUnit XML or use the GitHub Actions reporter (`--reporter=github-actions`
is built into Vitest 3.x). A failing test surfaces as a job
failure with no inline annotation on the offending line in the
PR diff. Polecats triaging a CI failure must scroll the log
rather than seeing the diagnosis next to the code.

### G8 — No mutation- or property-based testing

Two related blind spots:

- **Mutation testing** (Stryker-JS) would surface assertions that
  pass against the implementation but don't actually constrain
  behavior — a common failure mode for tests written after the
  fact. Today, `SpecimenForm.test.ts` could be passing while a
  refactor broke `formToCreateBody` and the test would still pass
  because it asserts on `firstCall[0].name` only.
- **Property-based testing** (`fast-check`, `zod-fast-check`)
  would shake the Zod schemas (`specimenFormSchema`,
  `mineralDataSchema`, etc.) with adversarial inputs derived from
  the schema itself. The hand-written test cases cover known
  shapes; property tests cover the long tail.

### G9 — `npm test` is the only invocation path

There is no `make test-frontend` or `make test-cover-frontend`
target. A polecat working on a backend-and-frontend PR has to
remember `cd frontend && npm test` separately from
`make test`. Easy to forget at `gt done` time.

### G10 — E2E browser tests genuinely deferred (acknowledge, not fix)

CONTRACT §9 explicitly defers Playwright/Cypress until "the SPA
has enough surface to warrant them." With seven routes and the
file-upload + journal flows already non-trivial, the threshold is
arguably here, but this remains the project's stated stance. R9
below documents the eventual path.

---

## 3. Recommendations

Each recommendation includes name, link, license, what it catches,
integration footprint, effort, and tradeoffs.

### R1 — `@vitest/coverage-v8` + report on every PR

- **Tool**: `@vitest/coverage-v8`
  ([github.com/vitest-dev/vitest/tree/main/packages/coverage-v8](https://github.com/vitest-dev/vitest/tree/main/packages/coverage-v8)).
  V8's native coverage; no instrumentation, no transform overhead.
- **License**: MIT ✓.
- **What it catches**: Closes G1. Per-file / per-line coverage,
  consumable as `coverage-summary.json` + HTML viewer. Exposes
  the actual gap behind G2 (specimen schema marshalling) without
  having to grep for it.
- **Integration footprint**:
  - Add `@vitest/coverage-v8` as a devDependency.
  - Add to `vitest.config.ts`:
    ```ts
    test: {
      ...,
      coverage: {
        provider: 'v8',
        reporter: ['text', 'html', 'json-summary'],
        include: ['src/**/*.{ts,svelte}'],
        exclude: ['src/**/*.test.ts', 'src/lib/api/schema.d.ts',
                  'src/test-setup.ts', 'src/main.ts', 'src/routes.ts'],
      },
    }
    ```
  - Add `npm run test:cover` script: `vitest run --coverage`.
  - Add a CI step in `pr.yml` and `main.yml` after the existing
    `npm test`: print summary, optionally upload `coverage/`.
- **Effort**: **S** (≤1h).
- **Tradeoffs**: V8 coverage is faster than istanbul but has
  small caveats around branch reporting on TypeScript decorators
  / async iterators (none used here, so n/a). Coverage in CI
  adds ~5–10 s to the frontend job; negligible.

### R2 — Coverage thresholds with a soft floor, not a hard bar

- **Tool**: Vitest's built-in `coverage.thresholds` config.
- **License**: MIT ✓ (Vitest itself).
- **What it catches**: Prevents coverage from regressing silently.
  Set as a *floor* (start at the current measured number rounded
  down) rather than an aspirational ceiling, so the threshold
  catches *new* untested branches without forcing a backfill PR.
- **Integration footprint**: extend the `coverage` block in R1:
  ```ts
  thresholds: {
    lines: 70, statements: 70, functions: 70, branches: 60,
    autoUpdate: false,
  }
  ```
  Tune the numbers from the first R1 run. The CONTRACT does not
  specify a threshold, so this is a polecat-level convention,
  not a §9 rule.
- **Effort**: **S** (≤30 min once R1 is in).
- **Tradeoffs**: Thresholds invite gaming (write tests until
  coverage clears the bar; don't necessarily exercise the
  branches that matter). Mitigate by also adopting R8 (mutation
  testing) once R1+R2 bed in.

### R3 — Direct unit tests for `lib/schemas/specimen.ts`

- **Tool**: Existing Vitest setup; new file
  `frontend/src/lib/schemas/specimen.test.ts`.
- **License**: N/A (test code in-repo).
- **What it catches**: Closes G2. Round-trip
  `specimenToFormValues(s)` → `formToCreateBody(x)` /
  `formToPatchBody(initial, x)` against fixtures of each `type`
  (`mineral`, `rock`, `meteorite`); assert `formToPatchBody`
  emits an empty patch when nothing changed; verify the
  type-data swap behavior of `resetTypeDataDefaults`.
- **Integration footprint**: One new test file. ~150 LOC. No
  config changes. Pure-function tests need no DOM, no mocks, run
  in milliseconds.
- **Effort**: **M** (½ day — the surface is wide; ~17 functions).
- **Tradeoffs**: None structurally. The schema file changes most
  often when the API adds fields, so the tests will need
  updating in lockstep — that's the point.

### R4 — Component test for `Lightbox.svelte`

- **Tool**: Existing Svelte Testing Library + Vitest.
- **License**: MIT ✓ (already in).
- **What it catches**: Closes G3. Cases:
  - Renders the start photo at `startIndex`.
  - `ArrowRight` advances; `ArrowLeft` retreats; both wrap.
  - `Escape` calls `onClose`.
  - When `photos.length === 0`, `index` clamps and `onClose`
    fires (the auto-close `$effect`).
  - `onDelete` is called with `current.id`.
- **Integration footprint**: One new file
  `frontend/src/lib/Lightbox.test.ts`, ~120 LOC. Uses
  `fireEvent.keyDown(window, { key: 'ArrowRight' })` for the
  document-level keyboard handler; pattern not currently used in
  the suite but trivial to introduce.
- **Effort**: **S** (≤2h).
- **Tradeoffs**: Focus assertions in jsdom are imperfect — jsdom
  doesn't do real layout, so `:focus-visible` and tab-order
  can't be tested. Assert on `document.activeElement` instead;
  it's good enough for the keyboard-trap cases here.

### R5 — Component test for `SpecimenCard.svelte`

- **Tool**: Existing setup; uses the same `vi.mock('./api')`
  pattern as `JournalAttachments.test.ts`.
- **License**: MIT ✓.
- **What it catches**: Closes G4. Cases:
  - Renders the thumbnail when the API returns ≥1 photo.
  - Falls back to the placeholder when the API returns zero
    photos.
  - Falls back to the placeholder when the API returns an
    error.
  - Aborts the in-flight request on unmount (assert that the
    `AbortController.signal.aborted` is `true` after
    `cleanup()`).
- **Integration footprint**: One new file ~80 LOC.
- **Effort**: **S** (≤1.5h).
- **Tradeoffs**: The abort assertion needs the test to grab the
  `signal` argument out of the mocked `client.GET` call. Pattern
  is straightforward but not used elsewhere yet; codify in a
  helper if it appears more than twice.

### R6 — Component test for `CollectorForm.svelte`

- **Tool**: Existing setup; mirrors `JournalEntryForm.test.ts`.
- **License**: MIT ✓.
- **What it catches**: Closes G5. Cases:
  - Required-name validation surfaces the field error.
  - Submit calls `onSubmit` with trimmed values when valid.
  - Banner-error path on `kind: 'error'`.
  - Field-scoped `nameTakenError` on `kind: 'duplicate'`.
- **Integration footprint**: One new file ~80 LOC; same shape
  as `JournalEntryForm.test.ts`.
- **Effort**: **S** (≤1h).
- **Tradeoffs**: Some duplication with the route-level
  `CollectorEdit.test.ts` / `Collectors.test.ts` cases. That's
  fine — the form-level test pins the form's contract regardless
  of how the routes change.

### R7 — `vitest-axe` for in-test accessibility checks

- **Tool**: `vitest-axe`
  ([github.com/chaance/vitest-axe](https://github.com/chaance/vitest-axe))
  wrapping `axe-core`.
- **License**: `vitest-axe` MIT ✓; `axe-core` MPL-2.0 ✓ (allowlist).
- **What it catches**: Closes G6 (partially). Structural a11y
  issues axe-core flags: missing `<label for>` associations,
  invalid ARIA attribute values, contrast issues that *can* be
  computed in jsdom (limited subset), buttons without accessible
  names, etc.
- **Integration footprint**:
  - Add `vitest-axe` and `axe-core` as devDependencies.
  - Extend `test-setup.ts` to register `expect.extend(matchers)`.
  - Add `expect(container).toHaveNoViolations()` calls to the
    largest forms (`SpecimenForm`, `CollectorForm`, `Specimens`).
- **Effort**: **S–M** (≤3h: tooling install + 4–6 assertions).
- **Tradeoffs**: jsdom can't do real layout, so axe-core skips
  some color-contrast / sizing rules under it; the bigger win
  comes when paired with R9 (Playwright + axe in a real browser).
  The structural-a11y win is real even in jsdom.

### R8 — Property-based tests for Zod schemas via `fast-check` + `zod-fast-check`

- **Tool**: `fast-check`
  ([github.com/dubzzz/fast-check](https://github.com/dubzzz/fast-check))
  + `zod-fast-check` ([github.com/Cellule/zod-fast-check](https://github.com/Cellule/zod-fast-check)).
- **License**: `fast-check` MIT ✓; `zod-fast-check` MIT ✓.
- **What it catches**: Closes G8 (partially). For each Zod
  schema, derive an `Arbitrary` and assert that a round-trip
  through `specimenToFormValues` / `formToCreateBody` is
  idempotent. Catches accidental regressions where, e.g., a
  whitespace-only `name` survives a round-trip.
- **Integration footprint**:
  - Two devDependencies, one new file
    `frontend/src/lib/schemas/specimen.property.test.ts` (~60 LOC),
    one configuration of run-count (default 100 cases is fine).
- **Effort**: **S** (≤2h).
- **Tradeoffs**: First-time setup; `zod-fast-check` is a small
  community project (~9k downloads/wk on npm as of 2026). If it
  ever bit-rots, hand-rolling the arbitraries against the schema
  is straightforward.

### R9 — Playwright E2E suite (deferred per §9, document the path)

- **Tool**: Playwright
  ([github.com/microsoft/playwright](https://github.com/microsoft/playwright)).
- **License**: Apache-2.0 ✓.
- **What it catches**: Real-browser flows the jsdom suite can't:
  drag-and-drop in `PhotoUploader`, lightbox focus trap with
  *real* `:focus-visible`, theme switch under `prefers-color-
  scheme`, form keyboard navigation, real network behavior in
  the toast middleware.
- **Integration footprint** (when adopted):
  - `frontend/playwright.config.ts` + `frontend/e2e/` directory.
  - A new GHA workflow `e2e.yml` that runs `docker compose up`,
    waits for `:8080/healthz`, runs `npx playwright test`.
  - `make e2e` Make target for local invocation.
- **Effort**: **L** (multi-day — first-time wiring + several
  flow tests).
- **Tradeoffs**: CONTRACT §9 explicitly defers this; not in
  scope to adopt now. Document so the next polecat to revisit
  the threshold (after E-3 search/filter and Mindat-lookup F-1
  land) doesn't re-litigate the tool choice.

### R10 — Mutation testing with Stryker (defer; cite as future)

- **Tool**: Stryker-JS
  ([github.com/stryker-mutator/stryker-js](https://github.com/stryker-mutator/stryker-js)).
- **License**: Apache-2.0 ✓.
- **What it catches**: Closes G8 (the other half). Generates
  AST mutations and reruns the suite per mutation; failures-to-
  detect surface tests that don't actually pin behavior.
- **Integration footprint** (when adopted):
  - `stryker.config.json` + `@stryker-mutator/core` + the
    Vitest runner plugin.
  - A *scheduled* GHA workflow (weekly or on-demand) — not
    per-PR, because mutation runs are 10–30× slower than the
    test suite.
- **Effort**: **M** (≤1 day to wire; ongoing triage cost).
- **Tradeoffs**: Costly. Adopt only after R1–R3 + R6 are bedded
  in and there's something to measure. For now it's documented
  as the natural escalation step beyond R2's coverage threshold.

### R11 — `make test-frontend` + `make test-cover-frontend` targets

- **Tool**: GNU make (already in use).
- **License**: N/A.
- **What it catches**: Closes G9. Polecats running quality gates
  before `gt done` can run a single `make` invocation per layer.
- **Integration footprint**: ~6 lines in `Makefile`:
  ```makefile
  test-frontend:
  	cd frontend && npm test

  test-cover-frontend:
  	cd frontend && npm run test:cover
  ```
  Plus mention in README. No CI changes (CI already runs
  `npm test` directly).
- **Effort**: **S** (≤15 min).
- **Tradeoffs**: None. Strictly ergonomic.

### R12 — `--reporter=github-actions` for inline PR annotations

- **Tool**: Built-in Vitest reporter.
- **License**: MIT ✓.
- **What it catches**: Closes G7. Failing tests become inline
  PR annotations on the offending line, rather than text in the
  workflow log.
- **Integration footprint**: One CI flag in `pr.yml` /
  `main.yml`:
  ```yaml
  - name: tests
    working-directory: frontend
    run: npx vitest run --reporter=github-actions --reporter=default
  ```
  (Multiple reporters allowed; keep `default` for human-readable
  log + `github-actions` for annotations.)
- **Effort**: **S** (≤15 min).
- **Tradeoffs**: None.

---

## 4. Prioritized list

Implement R1 + R3 + R11 + R12 in one PR — they're complementary
and all S/M. R4–R6 add direct component coverage. R7–R8 close the
quality-of-tests gap. R9 + R10 are flagged for later.

1. **R1 (`@vitest/coverage-v8`)** — closes the #1 gap (no
   measurement). Without this, every other recommendation
   ships blind. Implement first.
2. **R3 (specimen schema marshalling tests)** — closes the
   single biggest pure-logic untested file in the repo;
   directly satisfies CONTRACT §9 ("do test branching logic").
3. **R11 (`make test-frontend` + `test-cover-frontend`)** —
   trivial Make additions, fold into the same PR as R1.
4. **R12 (CI annotations)** — one flag, dramatic ergonomic win.
5. **R4 (`Lightbox` test)** — closes the riskiest untested
   component (keyboard nav + state machine).
6. **R5 (`SpecimenCard` test)** — closes the next-riskiest
   untested component (network effect + abort).
7. **R6 (`CollectorForm` test)** — symmetry with the other
   well-tested forms; cheap.
8. **R7 (`vitest-axe`)** — first structural a11y safety net,
   independent of the rest.
9. **R2 (coverage thresholds)** — adopt after R1+R3+R4+R5+R6
   land; baseline from those runs.
10. **R8 (property tests)** — once the unit tests in R3 exist,
    `fast-check` adds long-tail coverage at low marginal cost.
11. **R9 (Playwright)** — defer per §9; document path so the
    revisit isn't a tool-selection rerun.
12. **R10 (Stryker)** — defer; depends on R1+R2 bedding in.

---

## 5. Out of scope (deliberately not recommended)

- **Cypress** — comparable to Playwright (R9); MIT-licensed
  open core. Excluded because Playwright has overtaken Cypress
  for new SPA E2E projects in 2025–2026 (better Svelte support,
  faster runtime, official cross-browser parity). Adopting
  Cypress means choosing a less-maintained tool with no
  technical advantage here.
- **Jest** — replaced by Vitest already; reverting would lose
  the Vite-native test pipeline. Vitest's API is Jest-compatible
  enough that test code wouldn't change much, but the build
  benefits would.
- **`@testing-library/user-event`** — a higher-level wrapper
  over `fireEvent` that simulates real user gestures. Worth
  adopting as ergonomics improve, but the existing `fireEvent`
  patterns are not buggy; mention as a future ergonomic upgrade,
  not as a Q-2 gap.
- **Storybook + Storybook test runner** — Storybook is great for
  visual catalog work, but introduces a parallel build pipeline
  and `.stories.svelte` files, which isn't justified at 20-
  component scale. Revisit when there's a design-system layer.
- **Visual regression testing (Chromatic, Percy, BackstopJS)** —
  Chromatic is a paid SaaS; Percy is paid SaaS post-acquisition;
  BackstopJS is MIT but maintenance has slowed (last release
  2024). All require a hosted-image baseline. Defer until the
  UI is more visually load-bearing.
- **`@testing-library/cypress`** — N/A once Playwright is the
  E2E choice (R9).
- **Dom Testing Library "everything"** — `@testing-library/svelte`
  is the project's chosen wrapper; sticking to it keeps the
  query API consistent.
- **Snapshot testing** — Vitest supports it (`toMatchSnapshot`),
  but UI snapshots are notoriously brittle against benign style
  refactors. The current `data-testid` + property-asserting
  pattern is more durable.
- **Karma / browser-launcher test runners** — Vitest's
  experimental browser mode (`@vitest/browser`) covers the
  same use case more cleanly. If the jsdom polyfills in
  `test-setup.ts` ever multiply, revisit `@vitest/browser` (MIT)
  *before* reaching for Karma.
- **`tsd` for type-level tests** — could pin the shape of
  exported types, but `svelte-check` + the OpenAPI codegen
  already enforce contract typing end-to-end. Net signal too
  low.
- **`madge` / dependency-cycle detection in tests** — useful in
  larger codebases; the import graph here is shallow and
  one-directional already.

---

## References

- CONTRACT.md §9 — Testing requirements (lines 1737–1922),
  esp. "Frontend tests" subsection 1895–1906 and the
  exemption rules 1792–1810.
- CONTRACT.md §16 — Dependencies & libraries (lines 3207–3369),
  esp. license allowlist 3316–3344. Every R1–R12 tool above is
  MIT, Apache-2.0, or MPL-2.0.
- `frontend/package.json`, `frontend/vitest.config.ts`,
  `frontend/src/test-setup.ts` — current test framework wiring.
- `.github/workflows/pr.yml`, `.github/workflows/main.yml` —
  current frontend CI gates; integration points for R1, R12.
- `frontend/src/lib/schemas/specimen.ts` — the 532-LOC pure-
  logic file that's the focus of R3.
- Bead **mi-ih0** — Q-2 acceptance criteria.
- Companion review: `docs/quality/backend-test-coverage.md.md`
  (Q-1 / `mi-fmj`) — same Q-wave; format mirrored here.
- Companion review: `docs/quality/frontend-vuln-scanning.md.md`
  (Q-6 / `mi-7u3`) — same Q-wave, frontend half.
