# Frontend bundle size analysis — quality review (Q-8)

> Scope: TypeScript + Svelte SPA under `frontend/`. Excludes the
> Go backend, Tailwind CSS authoring rules, and image asset
> handling.
> Date: 2026-05-09. Bead: `mi-gi6`.

## TL;DR

A clean `vite build` produces **one 271 kB JS chunk (76 kB gzip,
65 kB brotli) + one 35 kB CSS chunk (7 kB gzip)** — totals are
fine for a personal-app SPA today, but **CI never runs `vite
build`**, **no size budget is enforced**, and **all routes ship in
one chunk** with zero code splitting. A sourcemap-attribution scan
shows `svelte` (44%) and `zod` (18%) dominate the bundle source
bytes, with one component (`SpecimenForm.svelte`, 837 LOC) eating
most of the remaining app share. The cheapest, highest-value moves
are: (1) wire `vite build` into `pr.yml` so a broken build can't
land, (2) add `size-limit` with a brotli budget pinned slightly
above today's 65 kB, (3) add `rollup-plugin-visualizer` as an
opt-in build mode for diagnostic snapshots. R4 (route-level
dynamic-import code splitting) is the only **M**-effort item and
is what unlocks meaningful future shrinkage. All recommended tools
are MIT or Apache-2.0 (§16-clean).

---

## 1. Current state

### Build pipeline

- Bundler: **Vite 6.4.2** (`frontend/vite.config.ts`, 14 LOC).
  No `build:` block — the project relies entirely on Vite
  defaults. Defaults that apply here:
  - Minifier: **esbuild** (Vite 6 default; faster than terser,
    slightly larger output).
  - Target: `'modules'` (browsers with native ESM, ~baseline 2020).
  - CSS minify: enabled by default.
  - Source maps: **off** by default; not produced in CI builds.
  - `cssCodeSplit: true` by default — but it has no effect here
    because there are no async chunks (see code splitting below).
  - `chunkSizeWarningLimit: 500 kB` — a single 271 kB chunk does
    not trigger Vite's own warning.
- Svelte preprocessor: `vitePreprocess()` only
  (`frontend/svelte.config.js`, 4 LOC). No custom optimizations.
- CSS pipeline: **Tailwind 4** via `@tailwindcss/postcss`
  (`frontend/postcss.config.js`, 5 LOC). Tailwind 4 auto-detects
  content via the Oxide engine; no `content:` array in config.
- Type-check: `svelte-check` (`npm run check`) — runs in CI but
  separately from `vite build`.
- TypeScript: `target: ES2022`, `module: ESNext`,
  `moduleResolution: bundler`, `verbatimModuleSyntax: true`
  (`frontend/tsconfig.json`).

### Measured build output (production, no sourcemap)

```
dist/index.html                   0.39 kB │ gzip:  0.26 kB
dist/assets/index-DQqsW_ls.css   34.60 kB │ gzip:  6.84 kB
dist/assets/index-D2gAxoJJ.js   271.16 kB │ gzip: 75.90 kB
                                            brotli-11: 64.77 kB
✓ built in 2.64s, 195 modules transformed
```

Total over-the-wire (gzip): **~83 kB**. Brotli-11: **~71 kB**.
The Go binary serves the `frontend/dist/` tree from `embed.FS`
(per CONTRACT §3.2 / §4) without any `Content-Encoding`
precompression layer — the runtime relies on the HTTP server's
on-the-fly compression (or, in front of it, Cloudflare).

### Source-byte attribution (from sourcemap)

A build with `--sourcemap` yields an `index-*.js.map` whose
`sourcesContent[]` totals **842 kB** of original source compiled
into the 271 kB minified chunk. The largest contributors:

| Source | Bytes | % of total |
|---|---:|---:|
| `node_modules/svelte` (runtime + internal) | 368 405 | 43.7% |
| `node_modules/zod` | 149 360 | 17.7% |
| `node_modules/@felte/core` | 49 818 | 5.9% |
| `src/lib/SpecimenForm.svelte` | 30 674 | 3.6% |
| `src/routes/SpecimenDetail.svelte` | 30 547 | 3.6% |
| `node_modules/@felte/common` | 22 231 | 2.6% |
| `src/lib/schemas/specimen.ts` | 20 646 | 2.5% |
| `node_modules/svelte-spa-router` | 19 840 | 2.4% |
| `src/lib/SpecimenFilters.svelte` | 17 800 | 2.1% |
| `node_modules/openapi-fetch` | 17 224 | 2.0% |
| `src/lib/JournalAttachments.svelte` | 15 215 | 1.8% |
| `src/lib/PhotoUploader.svelte` | 11 390 | 1.4% |
| `src/lib/CollectorChainEditor.svelte` | 10 729 | 1.3% |
| (everything else, ~22 entries) | ≤10 573 each | 14% |

(Source bytes ≠ minified bytes: Svelte's runtime contains long
identifiers and JSDoc that minify hard. Treat the table as a
relative-share guide, not absolute output sizes.)

### Code-splitting state

- **Zero dynamic imports.** Verified: `grep -r "import("
  src/` returns no matches. All seven route components and all
  `lib/` components ship in `index-*.js` regardless of which
  route the user visits.
- **No `manualChunks` configured** in `vite.config.ts`. Rollup
  groups everything into one chunk because nothing requests
  separation.
- **`svelte-spa-router` is loaded eagerly** in `App.svelte` /
  `routes.ts`. It supports a `wrap()` helper for async-loaded
  routes, but the project doesn't use it.

### CI involvement in bundle health

Inspection of `.github/workflows/pr.yml` (frontend job, lines
103–149) and `.github/workflows/main.yml`:

- ✗ **`npm run build` is not run** on PRs or on `main`.
  CI runs `prettier --check`, `eslint`, `svelte-check`, `npm
  test`. A change that breaks `vite build` (broken import, bad
  CSS, missing module) would land green.
- ✗ **No size budget enforced.** No `size-limit`, no
  `bundlewatch`, no `bundlesize`, no GHA size-diff bot.
- ✗ **No bundle-stats artifact published** by CI for inspection
  on a PR.
- ✗ **No precompression** (gzip/brotli) of `dist/` artifacts.
  The Go server compresses on the fly (or Cloudflare does);
  static `.gz` / `.br` files are not produced or shipped.
- The CONTRACT only references bundle-size *indirectly* in §4.2:
  > "Image size stays in the 20–30 MB ballpark. Embedded SPA
  > inflates this gradually; if a single build adds more than a
  > few MB, ask why."

  That guardrail applies to the embedded `dist/` tree inside the
  Go image, not to per-PR bundle deltas, and it is enforced
  only by manual review.

### Dependency contributions to runtime weight

Cross-referencing `frontend/package.json` (`dependencies` only)
with the source-attribution table:

| Package | Runtime role | Shipped? | Notes |
|---|---|---|---|
| `svelte` (5.x) | UI runtime | yes (44%) | reactivity + DOM helpers; large because the project uses `mount` API and many components |
| `zod` (3.25.76) | runtime validation | yes (18%) | `zod` 3 ships the full builder API even when only `parse()` is used; `zod` v4 has a `zod/v4-mini` build with a smaller core |
| `felte` + `@felte/core` + `@felte/common` + `@felte/validator-zod` | form state | yes (~9% combined) | five `lib/*Form.svelte` files use it |
| `svelte-spa-router` (5.1.0) | hash routing | yes (2.4%) | small, no concern |
| `openapi-fetch` (0.17.0) | typed API client | yes (2.0%) | small, no concern |

`devDependencies` (Vite, Vitest, ESLint, Prettier, Tailwind,
TypeScript, etc.) **do not ship to the browser** — they're build-
time only. Out of scope for bundle-size analysis.

### Companion Q-wave context

- `docs/quality/frontend-vuln-scanning.md.md` (`mi-7u3`, Q-6)
  characterizes the dependency surface (5 prod, 20 dev, 396
  transitive) and recommends `audit-ci`, Dependabot, and
  signature verification. Some recommendations there
  (Dependabot grouping, lockfile-lint) are independent of bundle
  size; this review does not duplicate them.
- `docs/quality/backend-test-coverage.md.md` (`mi-fmj`) — same
  format used here.
- `docs/quality/backend-code-quality.md.md` (`mi-6qo`) and
  `docs/quality/frontend-vuln-scanning.md.md` are the prior
  two Q-wave reviews to read in parallel for tone/depth.

---

## 2. Observed gaps

Specific, unambiguous misses (paired with evidence):

1. **Bundle build is not gated by CI.** `pr.yml` does not run
   `npm run build`. A polecat could merge code that fails
   `vite build` (missing import, syntax-only-valid-in-Vitest TS,
   bad CSS) and the failure would surface only when the
   Dockerfile's frontend stage fails on `main`. This makes
   bundle-size enforcement (R2 below) impossible until R1 is in
   place — there's no build artifact to measure.

2. **No size budget enforced on a PR.** Today's brotli total is
   ~71 kB; tomorrow's could be 200 kB if a polecat imports
   `lodash` or `moment` whole and nothing in CI complains. The
   only feedback loop is a human eyeballing the `vite build`
   output (which doesn't run in CI anyway). The `chunkSizeWarning
   Limit: 500 kB` Vite default is a *warn*, not a *fail*, and far
   above today's actual chunk size.

3. **All routes ship in one chunk.** `routes.ts` statically
   imports seven route components plus their dependency tree.
   A user visiting `/specimens` (the default) downloads the full
   `SpecimenForm.svelte` (837 LOC, the bundle's largest app
   file) even though it's only used on `/specimens/new` and
   `/specimens/:id/edit`. Same for `Lightbox.svelte` (used only
   in `SpecimenDetail`) and `CollectorChainEditor.svelte` (only
   on collector edit). With dynamic imports each route would
   become its own ≤30 kB-source chunk and the initial load could
   drop ~30–40% of its app-code share. The dependency runtime
   (svelte, zod, felte) would still dominate, so the *absolute*
   savings are ~15–25 kB raw / ~5–8 kB gzip on first paint, but
   the *architectural* benefit is real: future component
   additions don't bloat the entry chunk.

4. **No bundle-stats visualization is generated.** Without
   `rollup-plugin-visualizer` or equivalent, the only way to find
   out *what's in the bundle* is the manual sourcemap analysis
   done above. Future polecats investigating a regression have
   no diagnostic surface.

5. **Zod v3 ships the full chainable-builder API.** `zod` 3.x
   accounts for ~150 kB of source bytes (~18% of the bundle).
   The project uses zod for form schemas (`schemas/specimen.ts`,
   `schemas/collector.ts`, `schemas/journal.ts`) — predominantly
   `.string()`, `.number()`, `.union()`, `.object()`, `.refine()`,
   `.transform()`, `.parse()`. `zod` v4 (released 2025) added a
   tree-shakable functional core under `zod/v4` and a stripped-
   down `zod/v4-mini` (~40 kB raw / ~7 kB gzip claim from upstream
   benchmarks) that supports the API subset in use here. The
   project is on `^3.25.76`, which is a v3 release that *backports*
   v4 ergonomics; the full v4-mini benefit needs a v4 upgrade.
   This is not "do it now" advice — see R5 tradeoffs.

6. **`SpecimenForm.svelte` is a 837-line monolith.** Not a
   bundle-size issue per se — it's a maintainability one — but
   it interacts with R4: code splitting is more effective when
   per-route components import lazily, and a 837-LOC component
   is harder to split internally (e.g., lazy-load the rare
   "mineral data" tab). Filing as a separate bead candidate.

7. **No precompression in the Go embed.** `dist/*.js` is
   served raw; the runtime compresses every request even though
   the asset is immutable across deploys. A `.br` and `.gz`
   sibling per asset would let the Go server (or a tiny middleware)
   serve precompressed bytes via `Content-Encoding`. This is a
   *deployment*-shape concern, not a bundle-size concern, and
   the Cloudflare layer already brotlis on the way out — listing
   it here for completeness, but R7 below explains why I do
   *not* recommend acting on it.

8. **`docs/quality/` lacks an index.** Three Q-wave docs now
   exist in this directory (`backend-code-quality.md.md`,
   `backend-test-coverage.md.md`, `frontend-vuln-scanning.md.md`)
   plus this one. The companion `frontend-vuln-scanning.md.md`
   review (R8) already flagged this. Not re-recommended here to
   avoid double-counting effort across the Q-wave.

### Non-gaps (deliberately)

- **Tailwind 4 purge** is not a gap. Tailwind 4 / Oxide auto-
  detects content, the CSS chunk is 35 kB raw / 7 kB gzip, and
  the build doesn't ship unused utility classes. A `safelist:`
  audit might trim a few hundred bytes; not worth the effort.
- **Svelte runtime size** is not a gap. Svelte 5's `mount` /
  reactivity runtime is the cost of using Svelte; the alternatives
  (eject Svelte, switch to Solid/Preact) are not on the table for
  a personal-app SPA. The 44% share would shrink relatively as
  the app grows; treat it as a fixed cost.
- **CSS code-splitting** is not a gap because the CSS chunk
  (7 kB gzip) is too small to bother splitting. Vite's
  `cssCodeSplit: true` would activate automatically once route-
  level chunks exist (R4).
- **Source-map shipping** is not a gap. Sourcemaps are not
  produced in production builds today; that's correct (smaller
  artifact, no IP leakage). R3 below uses sourcemaps as a
  *diagnostic* artifact only, not a shipped one.

---

## 3. Recommendations

Each entry: tool name + link, license, what it catches,
integration footprint, effort estimate (S = ≤2h, M = ½–1 day,
L = multi-day), tradeoffs.

### R1 — Run `vite build` in PR CI

- **Tool:** Built-in `vite build` (no new dependency).
  ([vitejs.dev/guide/build](https://vitejs.dev/guide/build.html))
- **License:** MIT ✓ (Vite is already a devDependency).
- **What it catches:** Any change that breaks the production
  build (TypeScript errors that pass `svelte-check` but fail
  Rollup, missing files in `dist/`, CSS-in-Svelte that compiles
  in dev but errors in `vite build`, accidentally-imported
  Node-only modules). Today this surfaces only at Docker build
  time on `main`. Also produces the artifact that R2/R3 measure.
- **Integration footprint:**
  - Add one step to the `frontend` job in `.github/workflows/pr.yml`
    after `npm test`:
    ```yaml
    - name: build
      if: steps.detect.outputs.present == 'true'
      working-directory: frontend
      run: npm run build
    ```
  - Mirror the same step in `.github/workflows/main.yml` (so
    the rebuild double-checks the merge result).
  - No source change. No `package.json` change.
- **Effort:** **S** (≤30 min including CI dry-run).
- **Tradeoffs:** Adds ~3 s to the frontend CI job (build is fast
  on a clean cache). Doubles the per-job npm-cache hit, which is
  noise. Catches a real class of failures. Prerequisite for R2
  and R3.

### R2 — `size-limit` budget gate

- **Tool:** `size-limit` with `@size-limit/file` (raw + gzip)
  and `@size-limit/preset-app` (brotli).
  ([github.com/ai/size-limit](https://github.com/ai/size-limit))
- **License:** MIT ✓.
- **What it catches:** Any PR that pushes the production bundle
  past a configured budget. The default presets measure both
  size and time-to-execute; for this app, raw / gzip / brotli
  size is enough. CI fails with a diff vs the configured
  threshold, plus a per-file breakdown.
- **Integration footprint:**
  - Add `size-limit` and `@size-limit/preset-app` (or just
    `@size-limit/file`) as devDependencies in `frontend/package.json`.
  - Add a `size-limit` block to `package.json`:
    ```json
    "size-limit": [
      { "name": "JS bundle", "path": "dist/assets/index-*.js",
        "limit": "75 kB", "gzip": false, "brotli": true },
      { "name": "CSS bundle", "path": "dist/assets/index-*.css",
        "limit": "8 kB", "gzip": false, "brotli": true }
    ]
    ```
    (Initial limits set ~10% above today's 65 kB / 6 kB brotli
    measurements, leaving room for ordinary growth without
    nuisance failures.)
  - Add `npm run size` script: `size-limit`.
  - Add a CI step in `pr.yml` after `npm run build`:
    ```yaml
    - name: size budget
      if: steps.detect.outputs.present == 'true'
      working-directory: frontend
      run: npm run size
    ```
  - Optional: `andresz1/size-limit-action` for a PR comment with
    the size delta (uses `GITHUB_TOKEN`; no secrets needed).
- **Effort:** **S** (≤1h, including budget tuning).
- **Tradeoffs:** Picks the *measurement* cadence — failures
  show up only when a PR runs CI, not while developing locally.
  Limits are arbitrary; pick conservatively, raise once with a
  visible PR when intentional. Brotli measurement requires the
  `preset-app` (or manually pulling `brotli-size`) — `@size-
  limit/file` alone gives gzip+raw only. Worth the extra
  preset.

### R3 — `rollup-plugin-visualizer` (diagnostic builds)

- **Tool:** `rollup-plugin-visualizer`.
  ([github.com/btd/rollup-plugin-visualizer](https://github.com/btd/rollup-plugin-visualizer))
- **License:** MIT ✓.
- **What it catches:** *What's in the bundle.* Generates an
  HTML treemap from the build's sourcemap, attributing every
  byte to a source file or `node_modules/<pkg>`. Vital when
  the size budget (R2) trips and a polecat needs to know
  *why* the bundle grew.
- **Integration footprint:**
  - Add `rollup-plugin-visualizer` as a devDependency.
  - Edit `frontend/vite.config.ts` to conditionally enable it
    when `process.env.ANALYZE === '1'`:
    ```ts
    plugins: [
      svelte(),
      ...(process.env.ANALYZE ? [visualizer({ filename:
        'dist/stats.html', gzipSize: true, brotliSize: true })] : []),
    ],
    ```
  - Add `npm run analyze` script:
    `ANALYZE=1 vite build && open dist/stats.html`.
  - Optional CI step: when a PR is labeled `bundle-analysis`,
    run `npm run analyze` and upload `dist/stats.html` as a
    workflow artifact.
- **Effort:** **S** (≤1h).
- **Tradeoffs:** Build with `ANALYZE=1` produces sourcemaps
  (~1.2 MB extra) and `stats.html` (~600 kB). Neither ships to
  prod (only the conditional plugin slot). The HTML treemap is
  self-contained — no external service, no upload. The `ANALYZE`
  flag means the diagnostic doesn't cost CI time on every PR;
  only when invoked.

### R4 — Route-level dynamic-import code splitting

- **Tool:** `svelte-spa-router`'s `wrap()` helper (already a
  dependency). ([github.com/ItalyPaleAle/svelte-spa-router#wrap](https://github.com/ItalyPaleAle/svelte-spa-router#wrap))
- **License:** MIT ✓ (no new dep).
- **What it catches:** First-load weight and lazy-evaluation of
  rarely-visited routes. Per `wrap({ asyncComponent: () =>
  import('./routes/SpecimenDetail.svelte') })`, each route
  becomes its own Rollup chunk and is fetched only on
  navigation. Vite's `cssCodeSplit: true` then automatically
  emits a per-route CSS chunk if the route's components have
  scoped styles.
- **Integration footprint:**
  - Edit `frontend/src/routes.ts` to wrap each route component
    in an async loader. Roughly:
    ```ts
    import { wrap } from 'svelte-spa-router/wrap';
    export const routes: RouteDefinition = {
      '/': wrap({ asyncComponent: () => import('./routes/Specimens.svelte') }),
      '/specimens': wrap({ asyncComponent: () => import('./routes/Specimens.svelte') }),
      '/specimens/new': wrap({ asyncComponent: () => import('./routes/SpecimenNew.svelte') }),
      // ...
    };
    ```
  - Verify `vite build` produces `/assets/SpecimenDetail-*.js`
    chunks alongside `index-*.js`.
  - Add a small loading fallback (`loadingComponent`) to
    `wrap()` so the user sees a placeholder during chunk load.
- **Effort:** **M** (½ day — implementation is a few lines, but
  needs a manual smoke test of every route, a test that the
  router still resolves dynamic params, and a re-measure of
  R2 budgets afterwards).
- **Tradeoffs:** Adds an extra HTTP request per route (cached
  after first visit). On a slow first paint, the user briefly
  sees the loading state before the route mounts. Splitting is
  most effective when routes have *unique* dependencies — here,
  `Lightbox.svelte` and `CollectorChainEditor.svelte` are good
  candidates because they're heavy and route-local; the form
  routes share `SpecimenForm.svelte`, so Rollup may emit a
  shared chunk anyway. **Worth doing only after R1+R2 land**,
  so the size-effect of the split is measurable and budgeted.

### R5 — Migrate `zod` 3 → `zod` 4 (or `zod/v4-mini` subset)

- **Tool:** `zod` v4. ([zod.dev/v4](https://zod.dev/v4))
- **License:** MIT ✓.
- **What it catches:** ~10–12 kB gzip of unused chainable-API
  surface that ships today as the v3 builder. v4's `zod/v4-mini`
  exposes a tree-shakable functional API (`z.parse(schema, value)`
  instead of `schema.parse(value)`) that lets Rollup eliminate
  unused validators. v4 also introduces stricter type inference
  and several breaking changes; not a drop-in.
- **Integration footprint:**
  - `npm install zod@^4` (current pin is `^3.25.76`).
  - For the **largest** win, switch to `zod/v4-mini` and rewrite
    the three schema files (`schemas/specimen.ts`,
    `schemas/collector.ts`, `schemas/journal.ts`) plus the form
    integration (`@felte/validator-zod`) to use the functional
    API. Five files, ~600 LOC of changes.
  - For the **smallest** disruption, switch to `zod@^4`'s
    classic API (still chainable but a smaller core) and accept
    a more modest ~3–5 kB gzip win.
  - Verify `@felte/validator-zod` (current `^1.0.18`) supports
    zod 4 — at write time, `@felte/validator-zod` 1.x is pinned
    to zod 3 and would need a peer-dep bump or replacement.
- **Effort:** **M** (½–1 day for the classic-API path;
  potentially **L** for the v4-mini path because of `@felte/
  validator-zod` compatibility risk).
- **Tradeoffs:** Largest single shrink available *without*
  changing the framework, but the tail risk is real:
  `@felte/validator-zod` may not be on zod 4 yet, and chasing
  that compatibility could rabbit-hole into vendoring or
  forking the validator. **Defer until R1+R2+R3+R4 land** — at
  that point the budget enforcement and visualizer make the
  before/after size-delta visible and the upgrade testable.
  If `@felte/validator-zod` doesn't yet support zod 4, file a
  separate bead and revisit in 3–6 months.

### R6 — `knip` for unused-code / unused-export detection

- **Tool:** `knip`. ([github.com/webpro-nl/knip](https://github.com/webpro-nl/knip))
- **License:** ISC ✓.
- **What it catches:** Unused files, unused exports, unused
  dependencies in `package.json`, and unreferenced
  devDependencies. Catches dead code that ships to the bundle
  because something *imports* a file even though no caller uses
  the exported symbol. Also catches `dependencies` entries
  that should be `devDependencies` (a category that *would*
  affect bundle size if the polecat imports them by accident).
- **Integration footprint:**
  - Add `knip` as a devDependency.
  - Add `frontend/knip.json` (or a `knip` block in
    `package.json`) declaring entry points (`src/main.ts`,
    `vite.config.ts`, `vitest.config.ts`).
  - Add `npm run knip` script.
  - Optional CI step in `pr.yml` (allow-failure initially while
    the project triages findings).
- **Effort:** **S** (≤1.5h, including initial allowlist).
- **Tradeoffs:** Initial run will likely surface false
  positives (Svelte `$:` reactive declarations and store
  subscriptions confuse static analysis) — budget time to
  configure the ignore patterns. Some overlap with `eslint`'s
  `no-unused-vars` (already in place via `tseslint`), but
  `knip` operates at the *module* level where ESLint operates
  at the *symbol* level — they're complementary.

### R7 — `docs/quality/README.md` index

- **Tool:** None (Markdown only).
- **License:** N/A.
- **What it catches:** A documentation-discoverability gap
  flagged by the Q-6 review. As of this Q-8 review there are
  four reviews in `docs/quality/` and no map. Listed here for
  cross-Q-wave continuity but **not re-recommended** to avoid
  double-counting; defer to whoever lands the Q-6 R8 first.
- **Integration footprint:** New file `docs/quality/README.md`
  with one row per Q-wave doc (id, bead, topic, date, link).
- **Effort:** **S** (≤30 min).
- **Tradeoffs:** None — pure docs.

### R8 — (NOT recommended) precompressed assets via `vite-plugin-compression2`

- **Tool:** `vite-plugin-compression2`.
  ([github.com/nonzzz/vite-plugin-compression](https://github.com/nonzzz/vite-plugin-compression))
- **License:** MIT ✓.
- **What it would do:** Emit `dist/assets/*.js.br` and
  `*.gz` siblings at build time so the Go server (or
  Cloudflare) can serve them with `Content-Encoding: br/gzip`
  without compressing on the fly.
- **Why deferring:** The Go server today does on-the-fly
  gzip via `httpgzip`/`compress` middleware (per CONTRACT
  §14 / §3.2 expectation), and Cloudflare brotli-compresses
  at the edge regardless of origin (per §17 threat-model
  assumptions). Adding precompressed siblings adds two files
  per asset, doubles the `embed.FS` size for those entries,
  and the perceptible savings (CPU on the Go server, ~71 kB
  brotlied once at build time vs once per cold cache hit) are
  marginal for a personal-app scale. Reconsider if the project
  ever moves to a CDN-less direct-from-pod serving model.
- **Effort if revisited:** **S** (≤2h including Go-side
  middleware to pick the precompressed sibling).

---

## 4. Prioritized list

In implementation order. R1+R2+R3 land together as one PR — they
are mutually reinforcing and all **S**.

1. **R1 — `vite build` in CI.** Without this, every other
   recommendation is impossible. Trivial, ~30 min.
2. **R2 — `size-limit` budget.** Locks in today's ~71 kB brotli
   total so future regressions block at the PR. Also produces a
   visible budget-aware CI artifact. ~1h.
3. **R3 — `rollup-plugin-visualizer` (opt-in).** When R2 trips,
   this is the diagnostic. Adds zero cost to default CI. ~1h.
4. **R4 — Route-level code splitting.** Architectural payoff
   (chunk-per-route) plus ~5–8 kB gzip savings on first paint.
   Sequence after R1+R2 so the size-delta is measurable. ~½ day.
5. **R6 — `knip` for unused-code detection.** Cheap insurance,
   catches a class of accidental bundle bloat (e.g., a polecat
   marks something `dependencies` that should be `devDependencies`).
   ~1.5h.
6. **R5 — `zod` 4 / `zod/v4-mini`.** Largest single dependency
   shrink available, but blocked on `@felte/validator-zod` 4.x
   support. Defer until that lands; revisit in 3–6 months. **M**
   to **L** depending on path.
7. **R7 — `docs/quality/README.md`.** Already covered by Q-6
   (`mi-7u3`); whoever lands first carries it.

R8 is explicitly **not** recommended.

---

## 5. Out of scope (deliberately not recommended)

- **`bundlesize`** (https://github.com/siddharthkp/bundlesize) —
  archived since 2020. License-clean but unmaintained; `size-limit`
  (R2) is the modern replacement.
- **`bundlewatch`** (https://github.com/bundlewatch/bundlewatch) —
  alive but bundlewatch.io's free tier ties to a hosted dashboard,
  and the CLI alone duplicates `size-limit`. Pick one; pick R2.
- **`source-map-explorer`** — works only for sourcemap-ed bundles
  and stops at file boundaries, not package boundaries.
  `rollup-plugin-visualizer` (R3) is more informative for our
  Vite/Rollup pipeline.
- **`webpack-bundle-analyzer`** — Webpack-specific. We use Vite/
  Rollup. Not applicable.
- **Switching minifier from esbuild to terser** — terser produces
  ~3–5% smaller output at ~10× the build time. Not worth the
  CI-time hit at this bundle size. Reconsider if the JS chunk
  exceeds ~500 kB raw.
- **Building with `target: 'es2017'` or lower** — would *grow*
  the bundle (transpiled async/await, optional chaining). The
  user base for a personal-app SPA is the project owner's own
  browser; ES2022 is fine.
- **Eject Svelte for Solid / Preact / "vanilla TS"** — out of
  scope and would erase the entire frontend. The Svelte 44%
  share is the cost of using Svelte; pay it.
- **Server-side rendering / static prerendering** (SvelteKit,
  Astro) — CONTRACT §3.2 / §4.2 commit to a single Go binary
  with an embedded SPA served from `embed.FS`. SSR would require
  a separate Node runtime in the image, breaking the 20–30 MB
  image-size guardrail (CONTRACT §4.2 line 829). Hard no.
- **Image-asset optimization** (`sharp`, `imagemin`, `vite-imagetools`)
  — there are no static image assets in `frontend/src/` (verified:
  no `.png`, `.jpg`, `.webp` outside `dist/`). User-uploaded
  images are handled by the backend's `internal/storage/imageproc`
  pipeline, not the frontend bundle. Out of scope.
- **HTTP/2 push / preload-link generation** (`@vite-pwa/preload`,
  `vite-plugin-preload`) — Cloudflare and modern browsers (per
  §17 threat-model) handle preload via `103 Early Hints` or
  `<link rel="modulepreload">` that Vite already emits. No work
  needed.
- **`@swc/core`-based minifier** (`vite-plugin-minify-html`,
  `unplugin-swc`) — esbuild is the Vite default and produces
  near-identical output for this codebase size. Switching adds
  a build dep with marginal payoff.
- **CDN-only loading of svelte / zod** — would trade a same-
  origin asset (cached by the SPA's own service worker / browser
  cache) for a third-party dependency at runtime. Violates the
  same-origin posture in CONTRACT §10 and adds a supply-chain
  vector covered by the Q-6 review.

---

## References

- CONTRACT.md §4 — Build & release; §4.2 image-size guardrail
  (line 829: "Image size stays in the 20–30 MB ballpark").
- CONTRACT.md §5 — CI workflows; §5.1 PR validation gates.
- CONTRACT.md §16 — Dependencies & libraries; license allowlist
  (lines 3316–3344). Every R1–R7 tool is MIT or ISC.
- `frontend/vite.config.ts`, `frontend/svelte.config.js`,
  `frontend/postcss.config.js`, `frontend/tsconfig.json` —
  current build configuration.
- `frontend/package.json`, `frontend/package-lock.json` —
  current dependency state (5 prod, 20 dev, 396 transitive).
- `frontend/src/routes.ts` — static-import route map (target
  of R4).
- `.github/workflows/pr.yml` (lines 103–149) and
  `.github/workflows/main.yml` — current CI; build is missing.
- Bead **mi-gi6** — Q-8 acceptance criteria.
- Companion reviews:
  - `docs/quality/frontend-vuln-scanning.md.md` (Q-6 / `mi-7u3`)
    — dependency surface analysis.
  - `docs/quality/backend-test-coverage.md.md` (Q-1 / `mi-fmj`).
  - `docs/quality/backend-code-quality.md.md` (Q-3 / `mi-6qo`).
- Measurements: `vite build` invoked locally on commit
  `9765301` worktree, Node 22, npm 10. JS minified 271,157 B,
  brotli-11 64,769 B (Node `zlib.brotliCompressSync`). CSS
  34,596 B raw, brotli-11 5,858 B. Sourcemap-attribution scan
  via `index-*.js.map`'s `sourcesContent[]` summed by
  `node_modules/<pkg>` prefix.
