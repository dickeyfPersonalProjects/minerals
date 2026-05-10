# Frontend code quality / static analysis — quality review (Q-4)

> **Scope.** This doc is an analysis of the Svelte/TypeScript frontend's
> current code-quality and static-analysis tooling and a prioritized
> list of additions to consider. **No production code changes** are
> proposed here; landing any of these recommendations is a follow-up
> bead per the Q-wave plan.
>
> Audience: mayor coordinating Q-wave follow-ups, polecats picking up
> the resulting beads.
>
> Date: 2026-05-10. Bead: `mi-689`.
>
> **Adjacent Q-wave docs (deliberately out-of-scope here, do not
> duplicate):**
> - Q-2 frontend test coverage — `frontend-test-coverage.md`
> - Q-6 frontend dependency vuln scanning — `frontend-vuln-scanning.md`
> - Q-7 frontend accessibility — `frontend-accessibility.md`
> - Q-8 frontend bundle size — `frontend-bundle-size.md`

## TL;DR

The frontend has the load-bearing static-analysis gates wired —
`prettier --check`, `eslint`, `svelte-check`, `vitest run` — and
TypeScript is configured at near-maximum strictness (`strict`,
`noUncheckedIndexedAccess`, `noUnusedLocals`, `noUnusedParameters`).
The codebase is clean: zero `: any`, zero `@ts-ignore`, zero
`console.log`, zero `TODO/FIXME`, exactly one `eslint-disable`
(justified). The gaps that yield the most return for the least churn:

1. **`main.yml` skips `svelte-check`** — `pr.yml` runs all four gates
   but `main.yml` only runs prettier + eslint + tests, so a type
   error landing on `main` outside a PR (rebase, merge-queue race) is
   silent. One CI line fixes it.
2. **No `vite build` in CI** — a build-breaking change (Svelte
   compile error, Vite plugin regression) lands without a signal.
   `svelte-check` is a strong proxy but not a substitute.
3. **ESLint config enables almost no rules beyond defaults** — no
   `eslint-plugin-unicorn`, no rule against `as unknown as`
   double-casts, no `no-floating-promises`, no Svelte a11y plugin
   wired explicitly. `typescript-eslint` is included via `recommended`
   but `recommended-type-checked` (which catches floating promises,
   unsafe argument types, etc.) is not.
4. **No dead-code / unused-export detection** — `noUnusedLocals` only
   catches in-file unused symbols. Unused exports across files (the
   common rot in a young SPA) need `knip` or `ts-prune`.
5. **No size/complexity guardrails** — `lib/SpecimenForm.svelte`
   (837 LOC) and `routes/SpecimenDetail.svelte` (800 LOC) are the
   two largest files in the repo and both are growing. A soft
   complexity budget (eslint `complexity` / `max-lines`) would make
   the next refactor trigger explicit instead of by-feel.

Everything else in §3 is incremental polish.

---

## 1. Current state

### What's wired today

| Concern               | Tool / mechanism                            | Where           | Runs in `pr.yml` | Runs in `main.yml` |
|-----------------------|---------------------------------------------|-----------------|:---------------:|:------------------:|
| Formatting            | `prettier@3.4` + `prettier-plugin-svelte`   | `.prettierrc`   | ✓ | ✓ |
| Linting (JS/TS)       | `eslint@9` flat config                      | `eslint.config.js` | ✓ | ✓ |
| Linting (Svelte)      | `eslint-plugin-svelte@2.46` (`flat/recommended`) | `eslint.config.js` | ✓ | ✓ |
| TS lint              | `typescript-eslint@8` (`recommended`)        | `eslint.config.js` | ✓ | ✓ |
| Type-check           | `svelte-check@4.1` against `tsconfig.json`   | `package.json`  | ✓ | ✗ |
| Unit tests           | `vitest@3.2` (jsdom env)                     | `vitest.config.ts` | ✓ | ✓ |
| Build verification   | `vite build`                                 | (none)          | ✗ | ✗ |
| Generated client guard | `eslint.config.js` ignores `schema.d.ts`   | `eslint.config.js` | n/a | n/a |

### TypeScript strictness — actual settings

`frontend/tsconfig.json` is already tight:

```jsonc
{
  "strict": true,                       // implies the noImplicit* + strictNull* family
  "noUnusedLocals": true,
  "noUnusedParameters": true,
  "noFallthroughCasesInSwitch": true,
  "noUncheckedIndexedAccess": true,     // strong — beyond what most "strict" projects ship
  "verbatimModuleSyntax": true,
  "isolatedModules": true,
  "forceConsistentCasingInFileNames": true,
  "skipLibCheck": true
}
```

Notably **enabled**: `noUncheckedIndexedAccess` (forces `arr[i]` to
be `T | undefined`) — the single highest-signal compiler flag and
the one most projects skip.

Notably **not enabled** (intentionally or not):
- `exactOptionalPropertyTypes` — distinguishes `{x?: T}` from
  `{x?: T | undefined}`. Catches a real class of "I passed
  `undefined` where the field should be omitted" bugs.
- `noImplicitOverride` — pairs well with class hierarchies; not
  load-bearing in a Svelte SPA but free.
- `noPropertyAccessFromIndexSignature` — forces `obj['key']` over
  `obj.key` for index-signature types.

### ESLint config — what's actually enabled

`frontend/eslint.config.js` (flat config, 36 LOC):

```js
[
  js.configs.recommended,                  // ~70 stable JS rules
  ...tseslint.configs.recommended,         // ~50 TS rules; NOT recommended-type-checked
  ...svelte.configs['flat/recommended'],   // Svelte plugin defaults
  prettier,                                // disable formatting rules that fight Prettier
  ...svelte.configs['flat/prettier'],
  // language options; .svelte file parser wiring
  { ignores: ['dist/', 'node_modules/', '.svelte-kit/', 'src/lib/api/schema.d.ts'] }
]
```

No `rules: { ... }` block — the project uses the plugin defaults
verbatim. That's a deliberate choice (low maintenance, conventional
defaults), and it's working: only **one** `eslint-disable` exists
in `src/` (`SpecimenDetail.svelte:648`, justified — sanitized
markdown HTML).

### Codebase shape (frontend/src/)

- **50 source files** (20 `.svelte`, 30 `.ts`), **12,401 LOC** total
  (includes the 2,771-LOC generated `lib/api/schema.d.ts`).
- **Hand-written source ≈ 9,630 LOC**: 5,092 LOC across 20 Svelte
  components, ~1,600 LOC across 12 hand-written `.ts` modules,
  ~3,561 LOC across 18 `.test.ts` files (Q-2 reviewed test
  coverage in detail; see that doc).
- **Largest files** (LOC):
  - `lib/SpecimenForm.svelte` — 837
  - `routes/SpecimenDetail.svelte` — 800
  - `lib/schemas/specimen.ts` — 532
  - `lib/SpecimenFilters.svelte` — 501
  - `lib/JournalAttachments.svelte` — 449
- **Type escape hatches** (`as unknown as ...`): 18 occurrences,
  all but one in test files where they're conventional for stubbing
  DOM types (`FileList`, `DataTransfer`, `MediaQueryList`). Two
  production occurrences (`PhotoUploader.svelte:101`,
  `JournalAttachments.svelte:141`, `schemas/specimen.ts:307`) are
  narrowly-scoped and look intentional.
- **Zero** `: any`, **zero** `@ts-ignore` / `@ts-expect-error`,
  **zero** `console.log`, **zero** `TODO`/`FIXME`/`HACK`/`XXX`
  comments, **one** `eslint-disable` (`svelte/no-at-html-tags` on a
  sanitized-markdown render path).

### CI gate parity — `pr.yml` vs `main.yml`

There is a real divergence between the two workflows:

| Gate                    | `pr.yml` `frontend` job | `main.yml` `Frontend gates` step |
|-------------------------|:-----------------------:|:--------------------------------:|
| `prettier --check`      | ✓                       | ✓                                |
| `npx eslint .`          | ✓                       | ✓                                |
| `npm run check` (svelte-check / typecheck) | ✓ | ✗ |
| `npm test` (vitest)     | ✓                       | ✓                                |
| `vite build`            | ✗                       | ✗                                |

A type error landing on `main` outside the PR path (force-push,
admin merge, MQ race) would not be caught. Q-8 (bundle size)
identified the same `vite build` gap from the build-output angle.

### What CI does NOT currently do

- No `vite build` (build verification — see above).
- No coverage measurement (Q-2 covers this).
- No vulnerability scan on dependencies (Q-6 covers this).
- No `npm run check` on `main` (typecheck gap — see above).
- No license audit on the npm dep tree (CONTRACT.md §16 allowlist
  is enforced by reviewer discipline only).
- No dead-code / unused-export check.
- No size / cyclomatic-complexity guardrails.
- No automated a11y signal (Q-7 covers this).
- No bundle-size budget (Q-8 covers this).

---

## 2. Observed gaps

Each gap below is concrete: a category of bug or violation the
current toolchain will not catch.

### 2.1 Type-check gate missing on `main`

`main.yml` does not run `npm run check`. A typecheck failure that
slips into `main` (rebase, merge-queue, admin merge) currently
goes undetected until the next PR runs. Cost of the missing check:
identical to the cost on PR (~10s).

### 2.2 No build verification anywhere

`vite build` is never invoked in CI. `svelte-check` covers most TS
errors but not Svelte compile errors that only fire at build time
(template syntax in `<script>` interpolations, missing assets,
Vite plugin regressions). This is the same gap Q-8 flagged from
the bundle-size side; flagging it here in the static-analysis
context for completeness.

### 2.3 `typescript-eslint` runs in non-type-aware mode

`tseslint.configs.recommended` is the **fast** (syntactic-only)
preset. It does not catch:

- `no-floating-promises` — async functions whose promise is
  ignored. Real footgun in the `lib/api/wrapper.ts` and
  `routes/*.svelte` code where a stray `apiCall()` (without
  `await`) silently swallows errors.
- `no-misused-promises` — passing an `async` handler where a
  sync handler is expected (e.g., as `onclick`). Svelte's runes
  era makes this easier to do.
- `await-thenable` — `await` on a non-promise.
- `no-unsafe-argument` / `no-unsafe-assignment` — catches
  values typed `any` flowing into typed APIs.
- `unbound-method` — destructuring a method off `this` and losing
  binding.

Switching from `recommended` to `recommended-type-checked`
unlocks all of the above. It requires a `parserOptions:
{ project: './tsconfig.json' }` line and ~2-3× ESLint runtime
(still well under 30s here).

### 2.4 No Svelte a11y rules wired explicitly

Q-7 identified this from the a11y angle; flagging here so the
static-analysis bead doesn't double-fix it. `eslint-plugin-svelte`
ships a11y rules under `svelte/a11y-*` but they're not enabled by
the `flat/recommended` preset — they're a separate `flat/all` /
explicit-enable opt-in. Svelte 5 dropped the compiler-level a11y
warnings, so today missing-`alt`-on-`<img>` etc. land silently.

**Cross-reference**: this is Q-7's recommendation. Q-4 should
defer to Q-7 for the a11y rule set; this doc only notes the
overlap so reviewers don't see it as missing.

### 2.5 No dead-code / unused-export detection

`noUnusedLocals` (tsconfig) catches **in-file** unused symbols.
It does not catch:
- A function `export`ed from `lib/foo.ts` but no longer imported
  anywhere.
- A whole `.ts` or `.svelte` file no longer reachable from any
  entry point.
- Unused npm dependencies (in `package.json` but not imported).

Young SPAs accumulate this rot fast. Two tools cover it:
`knip` (broader, also flags unused deps) or `ts-prune`
(narrower, exports only).

### 2.6 No size / complexity budget

ESLint defaults do not set `max-lines`, `max-lines-per-function`,
`complexity`, `max-depth`, or `max-params`. The two largest
components — `SpecimenForm.svelte` (837) and `SpecimenDetail.svelte`
(800) — are organically growing as the schema grows; without a
budget the next "let me just add one more field" refactor
trigger is by-feel rather than mechanical. A soft budget (warn,
not error) on `max-lines` flags the trigger without stopping the
work.

### 2.7 Type-narrowing escape hatches

18 `as unknown as T` double-casts. Most are in tests (legitimate
DOM stub pattern) but the production occurrences in
`PhotoUploader.svelte`, `JournalAttachments.svelte`, and
`schemas/specimen.ts` would benefit from a custom rule limiting
double-casts to test files only. ESLint plugins that handle this:
`@typescript-eslint/consistent-type-assertions` with `assertionStyle:
'as'` and `objectLiteralTypeAssertions: 'never'` — a partial fix.
A custom rule via `eslint-plugin-no-unsafe-cast` or a
codeowners-level check is the complete fix.

### 2.8 No license audit on npm deps

Frontend has 5 direct prod + 20 direct dev + 396 transitive deps
(per Q-6's count). CONTRACT.md §16 has an explicit license
allowlist (MIT, BSD-2/3, Apache-2.0, ISC, MPL-2.0, public domain)
and forbidden list (GPL/AGPL/BSL/SSPL family). Today, enforcement
relies on a polecat reading `package-lock.json` diffs. A
transitive on `GPL-3.0` would not trip any gate.

Q-6 covers the **vulnerability** side of npm scanning (CVEs); the
**license** side falls in Q-4's scope as a static-analysis gate.

### 2.9 No formatting parity on Markdown / YAML / JSON in `frontend/`

`prettier --check .` in `frontend/` covers `.ts`, `.svelte`,
`.css`, `.json`, `.md` by default, but the project's CI only
runs Prettier in `frontend/`. Repo-root `*.md`, `*.yml` (the
GHA workflows themselves), and `kustomize/*.yaml` are not
formatted. Out of scope for Q-4 (Q-3 backend is similar) — flagged
here only so the recommendation list doesn't need to address it.

### 2.10 Generated-file drift detection

`frontend/src/lib/api/schema.d.ts` is generated by `make
gen-api-client` from `frontend/src/lib/api/openapi.json`. ESLint
ignores it. Nothing in CI verifies the generated file is
**up-to-date** with the source (i.e., that a polecat regenerated
after editing the spec). Drift means the typed client lies.

The fix is mechanical: a CI step that runs `make gen-api-client`
and `git diff --exit-code` on the result. Mentioned here because
it lives in the static-analysis envelope; cheap to land.

### 2.11 No JSDoc / structured comment lint

A young SPA with five Svelte components in the 500+ LOC range will
benefit from a structured "what does this prop do" convention on
exported APIs (component props, exported `lib/*.ts` functions). No
gate today flags an undocumented exported prop. Low-priority polish.

---

## 3. Recommendations

Each recommendation lists: tool, link, license (CONTRACT.md §16
compliance), what it catches, integration footprint, effort, and
known downsides.

### 3.1 Add `npm run check` to `main.yml`

- **Tool**: `svelte-check@4.1` (already in `devDependencies`).
- **License**: MIT — **allowed** (§16).
- **Catches**: typecheck regressions on `main` outside the PR
  path. Closes the parity gap with `pr.yml`.
- **Integration footprint**: one line in `main.yml`'s `Frontend
  gates` block:
  ```yaml
  npm ci
  npx prettier --check .
  npx eslint .
  npm run check          # ← add
  npm test
  ```
- **Effort**: **S** (≤2h) — single-line edit, no new dep.
- **Downsides**: adds ~10s to `main.yml`. None real; this is a
  trivial gate-parity fix.

### 3.2 Add `vite build` verification step to `pr.yml` and `main.yml`

- **Tool**: `vite@6` (already in `devDependencies`).
- **License**: MIT — **allowed** (§16).
- **Catches**: Svelte compile errors, Vite plugin regressions,
  asset-resolution errors, env-var mis-references — anything
  that fires only at build time, not at typecheck time.
- **Integration footprint**: one line per workflow:
  ```yaml
  - name: vite build
    if: steps.detect.outputs.present == 'true'
    working-directory: frontend
    run: npm run build
  ```
- **Effort**: **S** — two CI lines.
- **Downsides**: adds ~30-60s per workflow run. Q-8 has the same
  recommendation from the size-budget angle; if landed via Q-8,
  Q-4 doesn't need to land it again. Track once.

### 3.3 Switch `typescript-eslint` to `recommended-type-checked`

- **Tool**: `typescript-eslint@8` (already in `devDependencies`).
- **License**: MIT — **allowed** (§16).
- **Catches**: floating promises, misused promises (`async` in
  sync slots), unsafe `any` flow, `await` on non-thenables, lost
  `this` binding. Real classes of bug invisible to the current
  syntactic-only preset.
- **Integration footprint**: edit `eslint.config.js`:
  ```js
  ...tseslint.configs.recommendedTypeChecked,   // was: recommended
  // and add:
  {
    languageOptions: {
      parserOptions: {
        project: './tsconfig.json',
        extraFileExtensions: ['.svelte'],
      },
    },
  }
  ```
- **Effort**: **M** (½–1 day) — config edit is small; the work is
  triaging the initial findings and fixing or `// eslint-disable`-
  ing each one with a reason. Project has zero `eslint-disable`s
  today, so the audit pass is the cost.
- **Downsides**: ~2-3× ESLint runtime (still <30s). Type-aware
  rules require parsing the `tsconfig.json` project, which can
  surprise contributors with `unable to resolve project`-class
  errors if the parser config is misconfigured. The
  Svelte-template parser interaction is the trickiest piece —
  `extraFileExtensions` is required for the `.svelte` files to
  resolve into the type-aware run.

### 3.4 Add `knip` for dead-code / unused-export detection

- **Tool**: `knip` — <https://github.com/webpro-nl/knip>
- **License**: ISC — **allowed** (§16).
- **Catches**: unused exports (across files), unused files (no
  importer), unused npm dependencies (in `package.json` but never
  imported), unused devDependencies, duplicate exports, unlisted
  binaries. Strict superset of `ts-prune` (which is now
  unmaintained per its own README).
- **Integration footprint**:
  - `npm i -D knip` (one new devDep).
  - `frontend/knip.json` declaring entry points: `src/main.ts`,
    `src/test-setup.ts`, the test files.
  - One CI step (`npx knip`) in the `frontend` job.
- **Effort**: **M** — initial config takes 1-2h; triaging the
  first wave of findings in this codebase is fast (the project is
  young and clean) — likely a handful of stale exports.
- **Downsides**: `knip` is opinionated and occasionally flags
  intentional dynamic imports or framework-magic exports.
  Configurable via `knip.json` `ignore` arrays. Maintenance
  signal is good (active 2024-2026 development, ~10k GitHub stars).

### 3.5 Tighten ESLint with size/complexity warn-budgets

- **Tool**: `eslint@9` (already in `devDependencies`); built-in
  rules — no plugin needed.
- **License**: MIT — **allowed** (§16).
- **Catches**: drift toward unmaintainable file/function size.
- **Integration footprint**: add a `rules:` block to
  `eslint.config.js`:
  ```js
  {
    files: ['src/**/*.{ts,svelte}'],
    rules: {
      'max-lines': ['warn', { max: 600, skipBlankLines: true, skipComments: true }],
      'max-lines-per-function': ['warn', { max: 120, skipBlankLines: true }],
      'complexity': ['warn', 12],
      'max-depth': ['warn', 4],
      'max-params': ['warn', 5],
    },
  }
  ```
  Set as `warn`, not `error` — the goal is reviewer-visibility,
  not blocking PRs. Pick thresholds slightly above current
  values so today's code passes (`SpecimenForm.svelte` at 837 LOC
  would warn even at `max: 600`, which is the point — that file
  is a known refactor candidate).
- **Effort**: **S** — config edit + one round of threshold
  tuning.
- **Downsides**: complexity/size budgets famously cause drive-by
  refactors that don't improve code; setting `warn` not `error`
  and picking sensible thresholds avoids the worst of it. Svelte
  files have inflated LOC counts because template + script + style
  share one file; the threshold needs to account for that.

### 3.6 Add `eslint-plugin-unicorn` (selective)

- **Tool**: `eslint-plugin-unicorn` — <https://github.com/sindresorhus/eslint-plugin-unicorn>
- **License**: MIT — **allowed** (§16).
- **Catches**: a broad set of code-smell rules. Highest-value in
  this codebase:
  - `prefer-node-protocol` (use `node:fs` not `fs`).
  - `no-array-for-each` (controversial; skip).
  - `prefer-string-replace-all`, `prefer-array-some`, `prefer-includes`.
  - `error-message` (always pass a message to `Error()`).
  - `no-useless-undefined`.
  - `prefer-top-level-await`.
  - `no-array-callback-reference` (catches `arr.map(parseInt)`-
    style bugs).
- **Integration footprint**: add to `eslint.config.js`. The plugin
  ships a `recommended` preset but it's opinionated; I'd
  cherry-pick rules instead.
- **Effort**: **M** — config edit + triage. Cherry-pick takes 1-2h.
- **Downsides**: `unicorn`'s default preset is very opinionated
  (auto-fixes that change idioms); cherry-picking is the safe
  path. Some rules overlap with `typescript-eslint`.

### 3.7 Add `prettier --check` for the repo root

- **Tool**: `prettier@3.4` (already a frontend devDep, not yet
  invoked outside `frontend/`).
- **License**: MIT — **allowed** (§16).
- **Catches**: drift in repo-root markdown, GHA workflow YAML,
  `kustomize/*.yaml`, root-level JSON.
- **Integration footprint**: a tiny `.prettierrc` at repo root
  + a `make fmt-check-repo` target invoking `npx --yes
  prettier@3.4 --check '*.{md,yml,yaml,json}' '.github/**/*.yml'
  'kustomize/**/*.{yml,yaml}'`. Wire into `pr.yml`.
- **Effort**: **S** — config + Makefile target + CI line.
- **Downsides**: adds a node step to a backend-PR that doesn't
  touch frontend. Cost: ~5s per CI run. Could be skipped via
  path filter if it becomes annoying.

### 3.8 Add `license-checker` (or `license-checker-rseidelsohn`) CI gate

- **Tool**: `license-checker-rseidelsohn` —
  <https://github.com/RSeidelsohn/license-checker-rseidelsohn>
  (maintained fork of the unmaintained `license-checker`).
- **License**: BSD-3-Clause — **allowed** (§16).
- **Catches**: any direct or transitive npm dependency on a
  license outside the §16 allowlist. The `--onlyAllow` flag
  takes a `;`-separated SPDX list; a non-allowed license fails
  the run.
- **Integration footprint**: one CI step in `pr.yml`:
  ```yaml
  - name: license check
    if: steps.detect.outputs.present == 'true'
    working-directory: frontend
    run: |
      npx --yes license-checker-rseidelsohn \
        --production --excludePrivatePackages \
        --onlyAllow 'MIT;BSD-2-Clause;BSD-3-Clause;Apache-2.0;ISC;MPL-2.0;Unlicense;CC0-1.0;0BSD'
  ```
- **Effort**: **M** — initial run typically reveals a handful of
  packages with non-SPDX or `UNKNOWN` licenses needing manual
  override (`--customPath`). Steady-state cost is ~5s.
- **Downsides**: SPDX coverage is good for major packages but
  some niche transitives ship without a recognized LICENSE file
  and need manual override. Cost is paid once.

### 3.9 Add `enforce-up-to-date` check for the OpenAPI client

- **Tool**: `make` + `git diff --exit-code` (no new dep).
- **License**: n/a.
- **Catches**: stale `frontend/src/lib/api/schema.d.ts` (i.e.
  polecat edited `openapi.json` but forgot `make gen-api-client`).
- **Integration footprint**: add to `pr.yml` `frontend` job:
  ```yaml
  - name: api client up-to-date
    if: steps.detect.outputs.present == 'true'
    run: |
      make gen-api-client
      git diff --exit-code -- frontend/src/lib/api/schema.d.ts
  ```
- **Effort**: **S** — three CI lines.
- **Downsides**: adds whatever `make gen-api-client` costs (~5s).
  Currently the `frontend/src/lib/api/openapi.json` is committed
  too — if that file is also generated, this gate needs to cover
  it too. Verify before landing.

### 3.10 Add `eslint-plugin-svelte` a11y rules (Q-7 cross-ref)

- **Tool**: `eslint-plugin-svelte@2.46` (already a devDep).
- **License**: MIT — **allowed** (§16).
- **Catches**: missing `alt`, button-as-div, click-without-key
  handlers, etc. Listed for completeness; **deferred to Q-7**
  (`frontend-accessibility.md`) which owns the recommendation.

### 3.11 Add `@typescript-eslint/consistent-type-assertions` with strict config

- **Tool**: `typescript-eslint` (already a devDep).
- **License**: MIT — **allowed** (§16).
- **Catches**: ad-hoc `<T>x` and object-literal `as` assertions.
- **Integration footprint**: rule entry in `eslint.config.js`:
  ```js
  '@typescript-eslint/consistent-type-assertions': ['error', {
    assertionStyle: 'as',
    objectLiteralTypeAssertions: 'never',
  }],
  ```
  Plus a separate override allowing `as unknown as` only in
  `**/*.test.ts` files.
- **Effort**: **S** — rule edit; codebase already conforms.
- **Downsides**: doesn't directly stop the `as unknown as T`
  double-cast pattern; that needs a custom rule or PR-review
  discipline. This is the easy half.

### 3.12 Tighten `tsconfig.json` with `exactOptionalPropertyTypes`

- **Tool**: `typescript@5.7` (already a devDep).
- **License**: Apache-2.0 — **allowed** (§16).
- **Catches**: code that passes `undefined` to an optional field
  vs. omitting the field entirely. Real distinction in form-state
  payloads (`patchSpecimen({field: undefined})` ≠ `patchSpecimen({})`
  in API semantics).
- **Integration footprint**: one tsconfig flag.
- **Effort**: **M** — flag flip is one line; the audit/fix pass on
  the resulting type errors is the work. `lib/schemas/specimen.ts`
  has marshalling code that will need close attention.
- **Downsides**: famously noisy on first enable. Has caused some
  TS users to revert it. Worth the squeeze in a young codebase
  where the noise is bounded.

### 3.13 Add `madge` for cycle detection

- **Tool**: `madge` — <https://github.com/pahen/madge>
- **License**: MIT — **allowed** (§16).
- **Catches**: import cycles between modules. `lib/` → `routes/`
  cycles in particular tend to fan out and complicate refactors.
- **Integration footprint**: CI step:
  `npx madge --circular --extensions ts,svelte src/`.
- **Effort**: **S** — one CI line.
- **Downsides**: in this codebase the value is small — the import
  graph is shallow and clean. List low-priority.

---

## 4. Prioritized list

The top recommendations, in order of value-per-effort:

1. **`svelte-check` on `main.yml` (3.1)** — closes a real CI
   parity gap. **S effort, high value.**
2. **`recommended-type-checked` for `typescript-eslint` (3.3)** —
   unlocks `no-floating-promises` and `no-misused-promises`,
   which are the two highest-signal rules missing today. **M
   effort (audit pass), high value.**
3. **`vite build` in CI (3.2)** — overlaps with Q-8; track once,
   land via whichever bead lands first. **S effort, medium-high
   value.**
4. **`knip` for dead-code / unused-export detection (3.4)** —
   keeps the SPA from accreting rot as it grows. **M effort,
   medium value (compounding).**
5. **OpenAPI client up-to-date check (3.9)** — cheap, prevents
   a real silent-divergence bug. **S effort, medium value.**
6. **`license-checker-rseidelsohn` (3.8)** — automates the §16
   allowlist for the npm side. **M effort (initial), high value
   (long-term).**

Items 7+ (size/complexity budget, `unicorn` cherry-pick,
`exactOptionalPropertyTypes`, repo-root prettier, `madge`,
consistent-type-assertions) are nice-to-haves; recommend
deferring until the items above have landed.

---

## 5. Out of scope (deliberately not recommended)

### 5.1 SonarQube / SonarCloud

Heavy operationally, requires hosted infra or an external
account. The signal/noise ratio at a young single-operator
project is poor — most findings are duplicates of what `eslint`
+ `typescript-eslint` already catch. Defer until a SaaS-scale
QA need exists.

### 5.2 CodeClimate / Codacy / DeepSource

Same category as SonarCloud — managed-SaaS code-quality
dashboards. Useful for teams; for a single-operator project
they're overhead without proportional return. The license
posture is also non-OSI (proprietary SaaS terms).

### 5.3 `xo` / `gts` (preset linter packages)

Opinionated linter bundles. Replacing the existing flat-config
with one of these would be a step backwards in customizability;
the current config is already cleanly composed of upstream
plugins.

### 5.4 `husky` / `simple-git-hooks` (Git hooks)

Backend Q-3 listed `lefthook`/`pre-commit` for the same purpose.
Same recommendation logic applies on the frontend side — but it
should land **once for the whole repo**, not per-stack. Defer to
the Q-3 follow-up rather than duplicating here.

### 5.5 Stylelint

The project uses Tailwind 4 via `@tailwindcss/postcss` and has
**no hand-written CSS** beyond a tiny `app.css`. Stylelint adds
config + a rule debate without a meaningful surface to lint.
Re-open if/when hand-written CSS grows beyond ~200 LOC.

### 5.6 `pnpm` / `yarn` / Corepack

Package-manager swap is not a code-quality concern; it's a
reproducibility/perf one. Out of scope for Q-4. The current `npm
ci` against a committed lockfile is sufficient for v1.

### 5.7 `tsc --noEmit` as a separate gate

`svelte-check --tsconfig` already invokes the TypeScript checker
under the hood with Svelte-aware semantics. Adding a separate
`tsc --noEmit` gate would duplicate work. Skip.

### 5.8 `eslint-plugin-import`

Useful in deeper monorepos (cycle detection, ordering, missing
extensions). At this scale, `madge` (3.13) covers cycles and the
remaining rules largely overlap with what `typescript-eslint`
and `unicorn` already handle. Skip unless a specific pain point
emerges.

### 5.9 Mutation testing (`stryker-mutator`)

Real value but heavy operationally — full-suite mutation runs
are minutes-to-hours, and the noise/signal ratio at a young
project is poor. Defer until coverage measurement (Q-2) is
landed and the team has bandwidth for triage.

### 5.10 `eslint-plugin-security`

Largely Node.js-server-focused (`eval`, `child_process`,
filesystem traversal). For an SPA the rules largely don't apply.
The XSS-relevant patterns (`innerHTML`, `dangerouslySetInnerHTML`-
equivalents) are already covered by `svelte/no-at-html-tags` (and
the one `eslint-disable` for that rule is intentional, on a
sanitized-markdown render path). Skip.

---

## Appendix — verification notes

Each tool listed above was verified to exist and to ship under
the license stated, against publicly-known information as of
2026-05-10. Tools already present in `frontend/devDependencies`
(`eslint`, `prettier`, `typescript`, `typescript-eslint`,
`svelte-check`, `vite`, `eslint-plugin-svelte`) are governed by
their own upstream licenses, which I have called out individually
for each recommendation. A polecat landing any of these in a
follow-up bead should re-verify the license and the upstream
maintenance signal (last release, open issue volume) at landing
time — that's the minimum-viable due diligence per CONTRACT.md
§16's "When to add a library" rule. `ts-prune` was deliberately
not recommended (the project's README declares it unmaintained
and points users at `knip`).
