# Frontend accessibility — review (Q-7)

> **Scope.** This doc is an analysis of the Svelte/TypeScript SPA's
> current accessibility posture and a prioritized list of additions
> to consider. **No production code changes** are proposed here;
> landing any of these recommendations is a follow-up bead per the
> Q-wave plan.
>
> Audience: mayor coordinating Q-wave follow-ups, polecats picking
> up the resulting beads.

## TL;DR

The SPA is hand-built with a careful eye for a11y in places —
ConfirmModal traps focus, the form layer wires up `aria-invalid` /
`aria-describedby`, the toast region uses `aria-live` with
severity-aware roles, and icon-only buttons have `aria-label`s. The
gaps fall into one structural category and several point fixes:

1. **No automated a11y signal in CI** — no axe-core, no Lighthouse,
   and the `svelte/a11y-*` ESLint rules aren't explicitly enabled.
   Svelte 5 dropped its compiler-level a11y warnings, so today a
   regression like a missing `alt` or a button-as-div lands silently.
2. **Replace hand-rolled modals with `<dialog>`** — ConfirmModal and
   Lightbox don't restore focus to the trigger on close, and the
   custom focus-trap doesn't match what the browser gives you for
   free with `dialog.showModal()`.
3. **Two `window.confirm()` holdovers** in destructive flows
   (Collectors delete, JournalAttachments delete) bypass the
   project's accessible ConfirmModal — UX/a11y inconsistency.
4. **Navigation a11y missing** — no skip-to-main link, no per-route
   `<title>` updates, no `aria-current="page"` on nav, no live-region
   route announcer. Keyboard and screen-reader users navigate the
   SPA blind.
5. **No reduced-motion respect** — `animate-pulse`, `fly` toast
   transitions, and CSS `transition-*` classes ignore
   `prefers-reduced-motion`.

Everything else in §3 is incremental polish.

---

## 1. Current state

### Stack and gates wired today

| Concern              | Tool / mechanism                                  | Where      |
|----------------------|---------------------------------------------------|------------|
| Framework            | `svelte@5.16` (runes mode)                        | repo       |
| Build                | `vite@6`                                          | repo       |
| CSS                  | `tailwindcss@4` + custom OKLCH tokens             | `app.css`  |
| Type-check           | `svelte-check` (`tsconfig.json`)                  | CI (PR)    |
| Lint                 | `eslint@9` + `eslint-plugin-svelte@2.46`          | CI (PR/main) |
| Format               | `prettier@3` + `prettier-plugin-svelte`           | CI (PR/main) |
| Unit/component tests | `vitest@3` + `@testing-library/svelte`            | CI (PR)    |
| Form layer           | `felte` + `@felte/validator-zod`                  | runtime    |
| Router               | `svelte-spa-router` (hash routing)                | runtime    |
| Icon strategy        | inline `<svg aria-hidden="true">` (no icon dep)   | components |

### What ESLint actually checks

`frontend/eslint.config.js` extends three flat configs:

```js
js.configs.recommended,
...tseslint.configs.recommended,
...svelte.configs['flat/recommended'],
```

`eslint-plugin-svelte`'s `flat/recommended` set in v2.46 enables a
curated subset of rules. It includes a handful of `svelte/a11y-*`
rules (e.g. `svelte/a11y-no-noninteractive-tabindex`,
`svelte/a11y-missing-attribute`, `svelte/a11y-missing-content`),
but **most of the a11y family is opt-in**. The plugin's full a11y
suite is exposed via the standalone `svelte/a11y` flat config (or
by enabling rules individually); the project doesn't pull either.

Compounding this: **Svelte 5 dropped the compiler-level a11y
warnings** that earlier versions emitted on `svelte-check` /
`vite build`. The previous safety net is gone, and the project
hasn't replaced it with explicit rule enablement.

### What hand-rolled patterns are in place (good signal)

A grep over `frontend/src/**/*.svelte` shows ~100 a11y attribute
references. The non-trivial patterns:

- **`ConfirmModal.svelte`** — `role="dialog"`, `aria-modal="true"`,
  `aria-labelledby` / `aria-describedby` wired to `<h2>` and `<p>`,
  bespoke focus-trap on `Tab`/`Shift+Tab`, default focus on Cancel
  to neutralize accidental Enter, Escape closes.
- **`Lightbox.svelte`** — `role="dialog"`, `aria-modal`, keyboard
  nav (`ArrowLeft`/`ArrowRight`/`Escape`), `aria-label`s on every
  icon-only button, `figure` + `figcaption` for image counter.
- **`Toaster.svelte`** — `aria-live="polite"`, severity-aware
  `role` (`alert` for error/warning, `status` for success/info),
  glyph icons marked `aria-hidden="true"`.
- **`SpecimenForm.svelte`** — every input has an associated
  `<label for>`, `aria-invalid` on validation state,
  `aria-describedby` pointing to error `<p>`, `<fieldset>`/`<legend>`
  for grouped controls (type, locality, physical, type-specific).
- **`SpecimenFilters.svelte`** — toggle button uses `aria-expanded`
  and `aria-controls`; chip groups use `role="group"` + `aria-label`;
  individual chips use `aria-pressed`; date inputs have `aria-label`s
  for the implicit "from"/"to" semantics.
- **`CollectorChainEditor.svelte`** — every reorder button has a
  contextual `aria-label` (`Move ${name} up`, `Remove ${name} from
  chain`).
- **`PhotoUploader.svelte`** — `role="progressbar"` per upload row;
  hidden file input is `class="sr-only"` rather than `display:none`
  (keeps it focusable/programmatically clickable).
- **`Layout.svelte`** — semantic landmarks: `<header>`, `<nav>`,
  `<main>`, `<footer>`. `<html lang="en">` set in `index.html`.
- **`SpecimenDetail.svelte`** — `<article>` for the page,
  `<section>` per content block, `<time datetime>` for journal
  timestamps, `<dl>`/`<dt>`/`<dd>` for property tables.
- **Heading hierarchy** — pages start with `<h1>` and step down
  through `<h2>` / `<h3>`. No skipped levels caught in audit.
- **Form labels** — two `sr-only` labels (`Collectors` search,
  `JournalEntryForm` body) where placeholder text is the only
  visible affordance. Correct pattern.
- **Image alt text** — meaningful `alt` for hero photos and
  cards; deliberately empty `alt=""` on gallery thumbnails because
  the surrounding `<button aria-label="View photo N">` carries the
  semantics. Correct pattern.

### Frontend codebase shape

- **24 `.svelte` files**, **17 `.ts` files** under `frontend/src`.
- 11 `*.test.ts` component-level tests (vitest + Testing Library).
- Routes: 7 (`Specimens`, `SpecimenNew/Edit/Detail`, `Collectors`,
  `CollectorEdit`, fallback). Hash router, no SSR.
- Token system: 11 OKLCH variables per theme, swap on `.dark` class.
  Comments claim WCAG AA awareness ("WCAG-AA-friendly text-on-tinted
  surface"); no automated check confirms it.

### What CI does NOT currently do

- No accessibility linting beyond what's in `flat/recommended`
  (most `svelte/a11y-*` rules are off).
- No automated axe / WAVE / Lighthouse / Pa11y run.
- No color-contrast audit on the OKLCH token pairs or on direct
  Tailwind color utilities (`text-emerald-600`, `text-red-600`,
  etc.).
- No keyboard-navigation regression test.
- No screen-reader smoke test (manual or automated).

---

## 2. Observed gaps

Each gap below is concrete: a category of bug or violation the
current toolchain will not catch.

### 2.1 No automated a11y signal in CI

The project's frontend gates today are: `prettier --check`,
`eslint`, `svelte-check`, `vitest run`. None of those exercise:

- ARIA attribute validity (e.g. `aria-modal` on a non-dialog,
  `aria-labelledby` pointing at a missing id).
- Color contrast.
- Keyboard reachability of interactive elements.
- Landmark/heading structure.
- Form-label coverage at the rendered-DOM level.

A regression — e.g. someone removes an `<h2 id="confirm-modal-title">`
that `aria-labelledby="confirm-modal-title"` still references — is
invisible until a manual smoke catches it.

### 2.2 `svelte/a11y-*` rules not explicitly enabled

`flat/recommended` enables a curated subset (see §1). The full
list of `svelte/a11y-*` rules in `eslint-plugin-svelte` 2.46
includes ~25 rules; only ~8 are active by default. Off-by-default
rules that would catch real regressions in this codebase:

- `svelte/a11y-click-events-have-key-events`
- `svelte/a11y-no-static-element-interactions`
- `svelte/a11y-label-has-associated-control`
- `svelte/a11y-no-redundant-roles`
- `svelte/a11y-role-has-required-aria-props`
- `svelte/a11y-img-redundant-alt`
- `svelte/a11y-positive-tabindex`
- `svelte/a11y-aria-attributes`
- `svelte/a11y-no-noninteractive-element-interactions`

Combined with the Svelte 5 compiler dropping its built-in a11y
warnings, this is the single biggest visibility gap.

### 2.3 Hand-rolled modals miss focus return + initial-focus edge cases

`ConfirmModal.svelte` and `Lightbox.svelte` both:

- Set `role="dialog"` + `aria-modal="true"` on a `<div>`.
- Implement a focus-trap with a `keydown` handler that wraps Tab.
- Move focus to a default control on mount.

What they don't do:

- **Capture `document.activeElement` at mount and restore it on
  unmount.** Closing a delete-confirmation dropdown with Escape or
  Cancel leaves focus in the body — keyboard and screen-reader
  users have to Tab from the top of the page back to where they
  were. (`Lightbox.svelte:48` focuses the dialog itself; nothing
  restores focus on close. Same in `ConfirmModal.svelte:71`.)
- **Use the native `<dialog>` element**, which gives focus-trap,
  Escape handling, focus-return, and screen-reader-aware modal
  semantics for free. Browser support is universal as of 2026
  (Chrome 37+, Firefox 98+, Safari 15.4+).
- **Treat the backdrop as a `<button>`**: both files render the
  click-outside-to-close affordance as a positioned `<button
  type="button" aria-label="Close dialog" tabindex="-1">`. With
  `tabindex="-1"` it's removed from the tab sequence, but it
  remains in the screen reader's accessibility tree as a
  duplicate "Close dialog" / "Close photo viewer" landmark before
  the actual dialog content. A non-button element with a
  `pointerdown` listener (or, with `<dialog>`, a `::backdrop`
  click handler) would not pollute the AT tree.

### 2.4 Two `window.confirm()` holdovers in destructive flows

The project introduced `ConfirmModal` in mi-3tp specifically to
replace `window.confirm()` for delete UX. Two call sites still
use the native dialog:

- `frontend/src/routes/Collectors.svelte:113` — collector delete
- `frontend/src/lib/JournalAttachments.svelte:262` — attachment
  delete

`window.confirm()` has its own a11y story (browser-handled, varies
per OS/AT), but the inconsistency means destructive flows behave
differently across the SPA — one Esc-to-cancel pattern in modals,
a different one in native confirms, and the focus restoration
behavior differs too. JournalAttachments.svelte:260 explicitly
calls this out: `// Native confirm() is fine for v1 per the bead.`

### 2.5 SPA navigation is silent for AT users

- **Static `<title>`** — `index.html` has `<title>Minerals</title>`;
  no route updates it. Screen readers announce the document title
  on route change, so users on every page hear "Minerals,
  Minerals, Minerals."
- **No live-region route announcer** — when a hash route changes,
  nothing in the DOM is in an `aria-live` region tied to the
  navigation. AT users get no audible cue that the page changed.
- **No skip link** — `Layout.svelte` mounts `<header>` first;
  keyboard users tab through the home link, two nav links, and the
  theme toggle on every page before reaching content. A "Skip to
  main content" link as the first focusable element is the
  standard fix (WCAG 2.4.1).
- **No `aria-current="page"`** — header nav has Specimens /
  Collectors links; the active link is not marked. The router
  exposes `router.location` (the underlying store) so this is a
  one-effect fix.

### 2.6 Loading skeletons are silent

Several pages render a "shimmer" skeleton during fetch:

```html
<div class="h-10 w-64 animate-pulse rounded bg-[var(--color-surface-2)]"></div>
```

These have no `aria-busy`, no `role="status"`, and no sr-only
"Loading…" caption. AT users see an empty page during the fetch
window. The pattern at `SpecimenDetail.svelte:390-396`,
`Specimens.svelte:165-171`, and `Collectors.svelte:181-187` is
consistent — and consistently silent.

### 2.7 No `prefers-reduced-motion` respect

Animations and transitions used:

- `animate-pulse` on every loading skeleton.
- `transition` / `transition-colors` on hover states across
  cards, chips, and buttons.
- `fly` Svelte transitions on toasts (`Toaster.svelte:55-56`).
- `transition-opacity` on the journal-entry hover-action group
  (`SpecimenDetail.svelte:612, 621`).

None of these respect `prefers-reduced-motion: reduce`. Tailwind
v4 ships `motion-safe:` and `motion-reduce:` variants for this
exact purpose; the project uses neither. Users with vestibular
disorders or migraine triggers get the full motion regardless of
their OS setting.

### 2.8 Color-contrast assumptions are unverified

The OKLCH token system encodes `--color-text` /
`--color-text-muted` against `--color-bg` / `--color-surface`.
Comments in `Toaster.svelte:25-32` claim "WCAG-AA-friendly text-
on-tinted-surface" combinations. There is no automated check
that:

- The OKLCH pairs hit AA (4.5:1 for normal text, 3:1 for large)
  in either theme.
- Direct Tailwind color utilities used as accents
  (`text-emerald-600`, `text-red-600`, `text-amber-500`,
  `text-emerald-400` in dark mode) hit AA against the surfaces
  they sit on.
- Hover/focus states don't drop below threshold on transition.

The metadata classes (`text-[var(--color-text-muted)]` at
`text-xs` or smaller) are the highest-risk surface — small,
low-contrast text scattered through every list/detail view.

### 2.9 `aria-describedby` references can dangle

`SpecimenForm.svelte` (and similar) set:

```html
<input
  id="specimen-name"
  aria-invalid={Boolean(showError('name')) || Boolean(fieldErrors.name)}
  aria-describedby="specimen-name-error"
/>
{#if showError('name')}
  <p id="specimen-name-error">…</p>
{/if}
```

When no error is present, `aria-describedby` points to a
non-existent element. WCAG/ARIA permit this (AT silently ignores
missing ids), but the pattern is fragile and noisy in dev tools.
The cleaner pattern: omit `aria-describedby` when no error, or
always render the `<p>` with a `hidden` attribute and conditional
text. (Low severity; flagged here for completeness.)

### 2.10 Indeterminate progress has wrong ARIA shape

`PhotoUploader.svelte:303-309` renders an indeterminate progress
bar:

```html
<div role="progressbar" aria-label={`Uploading ${item.name}`}>
  <div class="h-full w-1/3 animate-pulse bg-[var(--color-accent)]"></div>
</div>
```

Per ARIA 1.2, an indeterminate `progressbar` should omit
`aria-valuenow` (correct here) **and** include `aria-busy="true"`
on the surrounding container or set `aria-valuetext` to a status
phrase. As written, AT may announce "Uploading photo.jpg, 0
percent" — misleading.

### 2.11 Focus indicators are sometimes opacity-only

A handful of buttons override the default outline for visual
density and use `focus-visible:opacity-100` as the only focus cue
(`SpecimenDetail.svelte:499, 529, 612, 621`; `Toaster.svelte:67`
sets `focus-visible:outline-none` without a paired indicator
beyond `focus-visible:opacity-100`). Opacity changes from 70 →
100 are not a sufficient focus indicator under WCAG 2.4.7 (focus
must be visible) and 2.4.13 (focus appearance, AAA but a useful
target).

### 2.12 `<title>` changes / route announcements absent (cross-ref §2.5)

Already covered in §2.5; logged as its own gap because the
remediation is independent (a `<svelte:head><title>` per route
component, plus a small store-driven `<div aria-live="polite"
role="status">` in `Layout.svelte`).

### 2.13 No documented a11y baseline

CONTRACT.md doesn't have an a11y section. Comments in code refer
to "WCAG AA" informally but the contract has no rule that says
"every interactive element needs a visible focus indicator," "all
form controls need a programmatic label," etc. Polecats picking
up frontend work have no canonical reference; the implicit
baseline is "what the previous polecat happened to do."

---

## 3. Recommendations

Each recommendation lists: tool / practice, link, license
(CONTRACT.md §16 compliance where applicable), what it catches,
integration footprint, effort, and known downsides.

### 3.1 Enable explicit `svelte/a11y-*` rules in ESLint

- **Tool**: `eslint-plugin-svelte` (already installed) — add
  `svelte/a11y` flat config or enable rules individually.
  <https://sveltejs.github.io/eslint-plugin-svelte/rules/>
- **License**: MIT — **allowed** (§16; already pre-approved).
- **Catches**: missing alt, button-as-div, label-without-control,
  invalid ARIA attribute combos, `tabindex > 0`, role-with-missing-
  required-aria-props, redundant roles. ~17 additional rules
  beyond what `flat/recommended` enables.
- **Integration footprint**: append `...svelte.configs['flat/a11y']`
  (or per-rule `svelte/a11y-*: 'error'` entries) to
  `frontend/eslint.config.js`. No new dependency. Run `npx eslint
  .` once and triage findings — expect a small number given the
  hand-rolled hygiene already in place.
- **Effort**: **S** — config edit + 0-2h triage.
- **Downsides**: noisy on a few false-positive shapes (e.g.
  `svelte/a11y-click-events-have-key-events` doesn't recognize
  every keyboard handler shape; some legitimate "button wrapped
  around an entire card" patterns trip it). Use `eslint-disable`
  comments with a one-line reason where appropriate, same
  pattern as the existing `svelte/no-at-html-tags` disable in
  `SpecimenDetail.svelte:648`.

### 3.2 Add `axe-core` for component-level a11y assertions

- **Tool**: `axe-core` — <https://github.com/dequelabs/axe-core>
- **License**: MPL-2.0 — **allowed** (§16).
- **Catches**: ~90 WCAG 2.1 AA rules at the rendered-DOM level —
  ARIA validity, contrast (subject to jsdom limits — see
  downsides), keyboard reachability, label coverage, landmark
  usage, role mismatches.
- **Integration footprint**:
  - Add `axe-core` as a `devDependency` (no runtime tax).
  - Write a 10-line `expect(node).toBeAccessible()` matcher in
    `src/test-setup.ts` that wraps `axe.run(node)`.
  - Add the assertion to existing component tests where the
    component is rendered standalone (e.g. `ConfirmModal.test.ts`,
    `SpecimenForm.test.ts`, `Toaster.test.ts`, `Lightbox` —
    Lightbox doesn't have a test today; tracked discovered work
    in §2 of `backend-test-coverage.md.md` is backend-scoped, but
    a sibling frontend coverage doc would surface this).
  - There is a community `vitest-axe` package (MIT) but it has
    been quiet for several releases; the 10-line wrapper has no
    transitive risk and reads clearer in test diffs.
- **Effort**: **M** (½–1 day) — wrapper + adding to existing
  tests + fixing whatever it surfaces.
- **Downsides**: jsdom doesn't compute layout, so axe's
  contrast checks (`color-contrast` rule) report
  `incomplete` rather than `pass`/`fail` in vitest. That's a
  Lighthouse / Playwright job, not a vitest job. Plan a split:
  axe in vitest for ARIA / labels / structure; Lighthouse CI for
  contrast and full-page audits.

### 3.3 Add Lighthouse CI for full-page a11y audits

- **Tool**: `@lhci/cli` — <https://github.com/GoogleChrome/lighthouse-ci>
- **License**: Apache-2.0 — **allowed** (§16).
- **Catches**: full Lighthouse a11y category (axe-core under the
  hood + contrast against rendered styles + scrolled-to-element
  tests), plus performance and best-practices side benefits.
  Generates HTML report artifacts.
- **Integration footprint**:
  - `npx lhci autorun` against `npm run dev` in CI, configured
    via `lighthouserc.json` with `assertions: { categories:a11y:
    'error' }`.
  - Add a `frontend-a11y` job to `.github/workflows/pr.yml`
    parallel to the existing `Frontend` job.
  - One-time runtime cost: ~30-60s per run. Only runs when
    `frontend/**` changes (path filter).
- **Effort**: **M** — config + workflow + threshold tuning.
- **Downsides**: needs a running dev server (already supported by
  `lhci autorun`). Headless Chrome adds ~80MB to CI image; first
  run finds many new violations and tuning the threshold (90?
  95?) takes a pass. Don't gate too tightly until the baseline
  is clean.

> **Why not Pa11y?** `pa11y` is published under LGPL-3.0, which
> falls in the §16 forbidden GPL family. Lighthouse CI is the
> contract-compatible equivalent.

### 3.4 Migrate ConfirmModal & Lightbox to native `<dialog>`

- **Tool**: HTMLDialogElement (browser stdlib).
  <https://developer.mozilla.org/en-US/docs/Web/HTML/Element/dialog>
- **License**: stdlib — n/a.
- **Catches**: focus-trap, Escape handling, focus-return-on-close,
  AT modal-context announcement (e.g. NVDA's "dialog" role
  announcement on `showModal()`), and `::backdrop` styling that
  removes the need for a backdrop `<button>`.
- **Integration footprint**:
  - Replace `<div role="dialog" aria-modal="true">` with
    `<dialog>`; replace `dialog.focus()` with
    `dialogEl.showModal()`.
  - Drop the bespoke focus-trap (`onKey` Tab branch) — the
    browser does it natively.
  - Keep the "default focus on Cancel" behavior via
    `cancelBtn.focus()` after `showModal()`.
  - Use `dialog.addEventListener('close', …)` for the on-close
    side effects (currently inferred from `onCancel` callback).
  - For backdrop click-to-close, listen for `click` on the
    `<dialog>` itself and check `event.target === dialogEl` (the
    canonical pattern when the dialog is full-bleed and content
    is in a child).
- **Effort**: **M** — two components, two test files; the
  invariants are tested in `ConfirmModal.test.ts` /
  `Lightbox.test.ts` (today only Confirm has dedicated tests),
  so the refactor is verifiable.
- **Downsides**: the `<dialog>` close event doesn't carry a
  reason ("submit" vs. "escape"); the project would need to
  inspect `dialog.returnValue` or set a flag in the close
  handler. Minor refactor cost.

### 3.5 Add a "Skip to main content" link

- **Tool**: 6 lines of Svelte in `Layout.svelte`.
- **License**: n/a.
- **Catches**: WCAG 2.4.1 (Bypass Blocks). Today every keyboard
  user tabs through the brand link, two nav links, and the theme
  toggle on every page before reaching content.
- **Integration footprint**: insert as the first child of the
  layout root: `<a href="#main" class="sr-only
  focus:not-sr-only">Skip to main content</a>` and add `id="main"
  tabindex="-1"` to the existing `<main>`.
- **Effort**: **S** (≤30 min).
- **Downsides**: none. This is the cheapest win in the doc.

### 3.6 Per-route `<title>` and route announcer

- **Tool**: `<svelte:head>` (built-in) + a small
  `aria-live="polite"` element in `Layout.svelte`.
- **License**: n/a.
- **Catches**: WCAG 2.4.2 (Page Titled), and the SPA
  route-announcement gap. AT users get a meaningful announcement
  on route change.
- **Integration footprint**:
  - Each route component adds `<svelte:head><title>{name} ·
    Minerals</title></svelte:head>` (e.g. "Specimens · Minerals",
    "{specimen.name} · Minerals").
  - Layout adds a hidden status region:
    ```html
    <div class="sr-only" role="status" aria-live="polite">
      {$routeAnnouncement}
    </div>
    ```
    bound to a derived store of the current route's display name
    (driven by `router.location` from `svelte-spa-router`).
- **Effort**: **S** — one helper store + one head fragment per
  route (~7 routes).
- **Downsides**: needs care for the detail routes — `{specimen.
  name} · Minerals` only resolves after the fetch lands. Until
  then, fall back to "Loading specimen · Minerals" so the title
  reflects state.

### 3.7 `aria-current="page"` on header nav

- **Tool**: 3 lines in `Layout.svelte`.
- **License**: n/a.
- **Catches**: WCAG 1.3.1 (Info and Relationships) — the active
  page in a nav is conveyed only via `text-[var(--color-accent)]`
  hover today; AT users get nothing.
- **Integration footprint**: import `location` from
  `svelte-spa-router`, derive a boolean per link, set
  `aria-current={isActive ? 'page' : undefined}`. Optionally
  pair with a visible indicator (underline, weight bump).
- **Effort**: **S**.
- **Downsides**: none.

### 3.8 Replace `window.confirm()` with `ConfirmModal`

- **Tool**: existing `ConfirmModal.svelte`.
- **License**: n/a.
- **Catches**: UX/a11y consistency — every destructive action
  goes through the same focus-aware, escape-aware, AT-friendly
  dialog. Removes the two `window.confirm` holdovers in
  `Collectors.svelte:113` and `JournalAttachments.svelte:262`.
- **Integration footprint**: lift the `deleteTarget` /
  `confirmDelete` pattern from `SpecimenDetail.svelte` into the
  two affected files. Both already have the basic discriminated-
  union shape; the call-site change is small.
- **Effort**: **S** (1-2h with tests).
- **Downsides**: existing tests in `Collectors.test.ts` mock
  `window.confirm`; those will need to be rewritten to assert
  modal interactions. Net: clearer tests.

### 3.9 Restore focus to trigger on dialog close

- **Tool**: `document.activeElement` capture pattern.
- **License**: n/a.
- **Catches**: WCAG 2.4.3 (Focus Order). After closing
  ConfirmModal or Lightbox today, focus is on the body. With this
  fix, focus returns to the button that opened the dialog.
- **Integration footprint**: subsumed by §3.4 (native `<dialog>`
  does this for free). If §3.4 is deferred, a 4-line patch in
  each modal: capture `document.activeElement` in `onMount`,
  restore in `onDestroy`.
- **Effort**: **S** (or **0** if §3.4 lands).
- **Downsides**: none.

### 3.10 Annotate loading skeletons

- **Tool**: standard ARIA.
- **License**: n/a.
- **Catches**: AT silence during fetch windows.
- **Integration footprint**: wrap the existing skeleton blocks
  in an element with `role="status" aria-busy="true"` and a
  visually hidden `<span class="sr-only">Loading…</span>`. Three
  files: `Specimens.svelte`, `Collectors.svelte`,
  `SpecimenDetail.svelte`.
- **Effort**: **S** (≤30 min).
- **Downsides**: none.

### 3.11 Adopt Tailwind `motion-reduce:` variants

- **Tool**: Tailwind 4 built-in variants.
- **License**: MIT (already in use).
- **Catches**: WCAG 2.3.3 (Animation from Interactions, AAA but
  a real comfort win).
- **Integration footprint**: prefix or pair `animate-pulse` /
  `transition*` with `motion-safe:` (so they only apply when the
  user hasn't requested reduced motion), or add explicit
  `motion-reduce:animate-none` / `motion-reduce:transition-none`.
  Toast `fly` transitions need a runtime guard:
  `prefers-reduced-motion` media-query check that skips the
  transition.
- **Effort**: **S** — sweep across components, no logic changes.
- **Downsides**: negligible visual difference for users without
  the OS preference set.

### 3.12 Fix `aria-describedby` dangling on form errors

- **Tool**: standard pattern.
- **License**: n/a.
- **Catches**: noisy DOM in dev tools; latent fragility if AT
  vendors tighten "missing referent" handling.
- **Integration footprint**: render `aria-describedby` only when
  there's an error, or always render the `<p>` with empty content
  + `hidden` attribute. Apply across `SpecimenForm.svelte`,
  `JournalEntryForm.svelte`, `CollectorForm.svelte`.
- **Effort**: **S**.
- **Downsides**: none.

### 3.13 Tighten progressbar ARIA in PhotoUploader

- **Tool**: standard ARIA.
- **License**: n/a.
- **Catches**: misleading "0%" announcement.
- **Integration footprint**: add `aria-valuetext="Uploading"` to
  the progressbar div, and `aria-busy="true"` to the surrounding
  `<li>` while `status === 'uploading'`.
- **Effort**: **S** (≤15 min).
- **Downsides**: none.

### 3.14 Pair `focus-visible:outline-none` with a non-opacity indicator

- **Tool**: Tailwind utilities.
- **License**: n/a.
- **Catches**: WCAG 2.4.7 (Focus Visible). Several buttons
  currently rely on opacity transitions for focus state.
- **Integration footprint**: replace
  `focus-visible:opacity-100` with a paired ring, e.g.
  `focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]
  focus-visible:opacity-100`. Apply across
  `SpecimenDetail.svelte`, `Toaster.svelte`, and any future
  hover-action group.
- **Effort**: **S** — utility sweep.
- **Downsides**: minor visual change in focus state; preferable
  to invisibility.

### 3.15 Add a contrast-ratio unit test for theme tokens

- **Tool**: `culori` (color math) — <https://culorijs.org/>
- **License**: MIT — **allowed** (§16).
- **Catches**: regressions in the OKLCH token definitions that
  drop a text/background pair below WCAG AA. Pure-math test, no
  browser needed.
- **Integration footprint**: `tokens.test.ts` parses the OKLCH
  values from `app.css` (or a colocated `tokens.ts`), uses culori
  to compute relative luminance + contrast ratio, asserts ≥4.5:1
  for normal-text pairs, ≥3:1 for large text.
- **Effort**: **M** — initial token extraction (CSS vars are
  string-templated today; consider lifting them into a
  TypeScript module that `app.css` imports, or duplicate-source-
  of-truth with a comment that they must match).
- **Downsides**: duplicate-source-of-truth risk if the tokens
  live in two places. Mitigation: extract to TS, generate CSS
  from there, or use a small parser on the CSS file in the test.

### 3.16 Document the a11y baseline in CONTRACT.md

- **Tool**: prose.
- **License**: n/a.
- **Catches**: ambiguity about what "done" means for new UI work.
- **Integration footprint**: add a CONTRACT.md §X (or extend
  §7b) covering: required focus visibility, AA contrast as the
  minimum bar, keyboard parity for every mouse action, labels
  for every form control, semantic landmarks per page,
  reduced-motion respect, automated gates that must pass (axe in
  vitest, Lighthouse CI threshold).
- **Effort**: **S** — ~1 page of prose.
- **Downsides**: nominal ongoing maintenance cost when the
  baseline shifts.

---

## 4. Prioritized list (top 5)

1. **Enable explicit `svelte/a11y-*` ESLint rules** (§3.1).
   Cheapest catch-everything safety net, no new dependency,
   replaces the Svelte 5 compiler-warning gap. **Effort: S.**

2. **Add axe-core in vitest + Lighthouse CI** (§3.2 + §3.3). The
   axe-core unit-level coverage catches ARIA / label / role /
   structure regressions before they hit a PR; Lighthouse CI
   covers contrast and end-to-end keyboard reachability. The two
   together are the project's first proper a11y test surface.
   **Effort: M each.**

3. **Migrate ConfirmModal & Lightbox to native `<dialog>`**
   (§3.4). Removes the largest hand-rolled-correctness risk;
   gives focus-return for free; deletes ~30 lines of bespoke
   focus-trap code. **Effort: M.**

4. **Skip link + per-route title + `aria-current` + focus return**
   (§3.5 + §3.6 + §3.7 + §3.9). Bundle of small navigation a11y
   wins. None depend on each other; together they fix the SPA
   "navigate-blind" experience for AT users. **Effort: S each;
   ~½ day total.**

5. **Replace `window.confirm()` with ConfirmModal + annotate
   loading skeletons + `motion-reduce` sweep** (§3.8 + §3.10 +
   §3.11). Consistency / regression / comfort polish that
   together remove the most visible inconsistencies. **Effort:
   S each.**

§3.12 / §3.13 / §3.14 / §3.15 / §3.16 are quality polish — fold
them into the next a11y-themed PR rather than scheduling
separately.

---

## 5. Out-of-scope (deliberately not recommended)

- **Storybook + `@storybook/addon-a11y`** — the project doesn't
  use Storybook today, and adding it solely for a11y review is
  heavyweight. Vitest + Testing Library + axe-core covers the
  same surface at much lower cost. Reconsider if Storybook lands
  for design-system reasons.
- **Pa11y / `pa11y-ci`** — published under LGPL-3.0, which falls
  in the §16 forbidden license family. Lighthouse CI (Apache-2.0)
  is the contract-compatible substitute.
- **Deque axe DevTools / WAVE Pro / commercial a11y vendors** —
  paid; the open-source axe-core covers v1 needs.
- **Manual NVDA / JAWS / VoiceOver test matrix** — valuable but
  out of scope for a small project at v1. Plan it as part of the
  pre-1.0 polish pass once the automated gates are green.
- **Full WCAG 2.2 AAA push** — 2.2 AA is the realistic target;
  AAA criteria like 2.4.13 (focus appearance) are aspirational
  but not the right bar to set as a CI gate today.
- **`eslint-plugin-jsx-a11y`** — React-specific; no value for a
  Svelte codebase. Mentioned only because it's a frequent
  drive-by suggestion.
