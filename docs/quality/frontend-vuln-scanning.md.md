# Frontend dependency vulnerability scanning — quality review (Q-6)

> Scope: TypeScript + Svelte frontend under `frontend/`. Excludes the
> Go backend.
> Date: 2026-05-09. Bead: `mi-7u3`.

## TL;DR

The frontend has **5 direct production dependencies, 20 dev
dependencies, and 396 lockfile entries** (transitive closure), and
**zero vulnerability scanning** anywhere — no `npm audit` in CI, no
Dependabot config, no SCA tool, no signature verification. CONTRACT
§17 explicitly deferred security scanning to v2 ("cheap to add when
motivated"); this review is that motivation. The cheapest, highest-
signal moves are: (1) wire `audit-ci` into `make` + the `pr.yml`
frontend job, (2) commit `.github/dependabot.yml` for the npm
ecosystem, (3) add `npm audit signatures` to verify package
provenance. All three are S-effort, free, and use OSI-permissive
tools that fit the §16 license allowlist.

---

## 1. Current state

### Codebase shape

- `frontend/`: Vite 6 + Svelte 5 + TypeScript 5.7, Vitest test
  runner, Tailwind 4, Felte/Zod for forms.
- **50 source files** (20 `.svelte`, 30 `.ts`), **18 test files**
  (`*.test.ts`).
- One generated artifact tracked in repo: `frontend/src/lib/api/schema.d.ts`
  (rewritten by `make gen-api-client` per CONTRACT §2).

### Dependency surface (from `frontend/package.json`)

**Direct production (`dependencies`, 5 packages):**

| Package | Version | Purpose |
|---|---|---|
| `@felte/validator-zod` | `^1.0.18` | Zod validation glue for Felte forms |
| `felte` | `^1.3.0` | Form state/validation |
| `openapi-fetch` | `^0.17.0` | Typed API client (paired with codegen) |
| `svelte-spa-router` | `^5.1.0` | Hash-based client-side router |
| `zod` | `^3.25.76` | Runtime schema validation |

**Direct dev (`devDependencies`, 20 packages):** Vite, Vitest,
ESLint 9, TypeScript 5.7, `svelte-check`, Prettier, Tailwind,
Testing Library, jsdom, `openapi-typescript`, etc.

**Lockfile (`frontend/package-lock.json`):** **396 packages** in
`packages` map, **5651 LOC**. Lockfile present and tracked
(per CONTRACT §16 vendoring stance: lockfile committed, no
`vendor/` directory).

### Scanning / advisory tooling actually in place

**None.** Concretely:

- `frontend/package.json` scripts: `dev`, `build`, `preview`,
  `check` (svelte-check), `lint` (eslint), `format`, `format:check`,
  `test` (vitest). **No `audit` script.**
- `Makefile` frontend targets: `fmt-frontend`, `fmt-check-frontend`,
  `lint-frontend`. **No audit/scan target.**
- `.github/workflows/pr.yml` frontend job: `npm ci` → `prettier --check`
  → `eslint` → `svelte-check` → `npm test`. **No `npm audit` step,
  no SCA step.**
- `.github/workflows/main.yml` frontend gates: identical to PR.
  **No scan.**
- `.github/dependabot.yml`: **does not exist.**
- `.github/workflows/` does not configure CodeQL, Trivy,
  OSV-Scanner, Snyk, or Socket.
- ESLint config: `js.configs.recommended` + `tseslint.recommended`
  + `svelte.configs.flat/recommended` + `prettier`. **No
  `eslint-plugin-security`** (and that's a code-pattern linter
  anyway — orthogonal to dependency scanning).
- No `eslint-plugin-no-unsanitized`, no `lockfile-lint` config, no
  `retire.js` config.
- No `.nsprc` / `audit-ci.json` / `audit-ci.jsonc` advisory
  allowlist file.

### Contract context

- CONTRACT.md §17 ("Deferred to v2") line 1038: **"Security scanning
  (CodeQL, Trivy, dependency CVE checks)"** — explicitly deferred,
  acknowledged as cheap to add.
- CONTRACT.md §17 line 1040: **"Automated dependency updates
  (Dependabot or Renovate config)"** — also deferred.
- CONTRACT.md §16 "License policy" (lines 3316–3344): allowlist is
  MIT, BSD-2/3, Apache-2.0, ISC, MPL-2.0, public domain. Forbidden:
  GPL/LGPL/AGPL, BSL/SSPL/Elastic/Confluent, custom, unknown. Any
  recommended scanner that ships as a runtime/CI binary must clear
  this bar.
- CONTRACT.md §16 line 3348: lockfile is the source of truth; `npm
  ci` is presumed (CI uses it, see `pr.yml`).

---

## 2. Observed gaps

Specific, unambiguous misses:

1. **No CVE detection on the dependency graph.** A new CVE on `vite`,
   `zod`, `svelte`, or any of their transitives lands silently.
   The frontend is on track for public Cloudflare exposure (§17),
   so XSS / prototype-pollution / ReDoS in a transitive is in scope,
   not theoretical. There is currently no human or automated process
   that would notice.
2. **No automated dependency updates.** With 396 transitives, manual
   `npm outdated` polling is the only mechanism, and there is no
   ritual that runs it. A vulnerable version pin can sit unfixed
   indefinitely.
3. **No package-provenance / signature verification.** `npm ci` in
   `pr.yml` and `main.yml` runs over the public registry without
   `npm audit signatures`, so a compromised registry response or a
   malicious release of an existing package (typosquatting,
   account takeover) is not caught at install time.
4. **No lockfile integrity policy.** Nothing prevents a polecat from
   committing a lockfile entry that resolves to a non-registry URL
   (e.g., a forked GitHub tarball, a malicious CDN). `npm ci`
   verifies the lockfile against the manifest but not against an
   allowlist of resolution sources.
5. **No advisory-allowlist mechanism.** When a real CVE lands and the
   fix isn't yet available upstream (or the CVE is non-exploitable
   in our usage), there's no place to record "we've reviewed this,
   ignored until X" — so any future audit step would be all-or-
   nothing.
6. **`docs/quality/` lacks an index.** `backend-test-coverage.md.md`
   sits alongside this doc with no `README.md` linking the Q-wave
   reviews. Minor, but readers landing in `docs/quality/` have no
   map.

Non-gaps (deliberately): the frontend has *no transitive on a
forbidden license that I could see* in the `package.json` direct
list — every direct dep listed is MIT or Apache-2.0 from the
ecosystem. A real license audit (R5 below) would confirm
transitively, but this isn't a current emergency.

---

## 3. Recommendations

Each recommendation includes name, link, license, what it catches,
integration footprint, effort, and tradeoffs.

### R1 — `npm audit` + `audit-ci` in CI

- **Tool:** `audit-ci` ([github.com/IBM/audit-ci](https://github.com/IBM/audit-ci))
  wrapping the built-in `npm audit`.
- **License:** Apache-2.0 ✓ (allowlist-compatible per §16).
- **What it catches:** Any advisory in the GitHub Advisory Database
  (the source `npm audit` queries) for our direct or transitive
  deps, gated by configurable severity threshold. `audit-ci` adds
  CI-friendly behavior: deterministic exit codes, JSON allowlist
  for known-accepted advisories, retry-on-network-error.
- **Integration footprint:**
  - Add `audit-ci` as a devDependency.
  - Add `npm run audit` script: `audit-ci --moderate`.
  - Add `make audit-frontend` target.
  - Add a CI step to `pr.yml` and `main.yml` frontend jobs after
    `npm ci`.
  - Add `frontend/audit-ci.jsonc` for the allowlist (initially
    empty).
- **Effort:** **S** (≤2h, including allowlist plumbing).
- **Tradeoffs:** `npm audit` over-reports dev-only advisories
  that don't affect runtime (Vite plugins, build tooling). Mitigate
  with `--moderate` threshold and `--report-type=important` flag,
  or split prod-only with `--production`. The first run will
  almost certainly surface advisories — budget time to triage.

### R2 — Dependabot config for npm + GitHub Actions

- **Tool:** Dependabot
  ([docs.github.com/.../dependabot-version-updates](https://docs.github.com/en/code-security/dependabot/dependabot-version-updates/configuration-options-for-the-dependabot.yml-file)).
- **License:** N/A — first-party GitHub feature; no binary enters
  the repo. Free for public and private repos.
- **What it catches:** Out-of-date dependencies (open PRs to bump),
  and Dependabot Alerts surface CVEs from the GitHub Advisory
  Database independently of CI runs (which is faster than R1: an
  advisory published Sunday gets an alert Monday morning, even if
  no PR runs CI that week).
- **Integration footprint:**
  - One file: `.github/dependabot.yml`.
  - Two ecosystems: `npm` (rooted at `/frontend`) and
    `github-actions` (rooted at `/`).
  - Weekly cadence is enough for this project's scale.
- **Effort:** **S** (≤30 min — small file, reviewable in one read).
- **Tradeoffs:** Generates PR noise (one PR per bump, by default).
  Mitigate with `groups:` to bundle minor/patch updates, and
  `ignore:` for known-incompatible majors. Dependabot can be
  disabled at any time without rollback work.

### R3 — `npm audit signatures` (provenance verification)

- **Tool:** Built-in npm CLI subcommand
  ([docs.npmjs.com/cli/v10/commands/npm-audit](https://docs.npmjs.com/cli/v10/commands/npm-audit)).
- **License:** Artistic-2.0 (npm CLI itself, OSI-approved). No new
  binary added to the repo.
- **What it catches:** Tampered or unsigned packages. Verifies the
  ECDSA registry signature on every downloaded package, plus
  Sigstore provenance attestations where the publisher uses them.
  This is the only line of defense against the registry serving a
  swapped tarball.
- **Integration footprint:**
  - One CI step in `pr.yml` and `main.yml` after `npm ci`:
    `working-directory: frontend; run: npm audit signatures`.
  - Optional Make target `audit-signatures-frontend`.
- **Effort:** **S** (≤30 min).
- **Tradeoffs:** Not all npm publishers have provenance attestations
  yet; signatures alone are weaker than provenance. Some packages
  fail signature verification due to publisher misconfiguration —
  expect to run it once locally first and decide whether to fail
  CI on missing signatures or only on bad signatures (`--audit-
  level=high` flag tunes this).

### R4 — `lockfile-lint` allowlist of resolution sources

- **Tool:** `lockfile-lint`
  ([github.com/lirantal/lockfile-lint](https://github.com/lirantal/lockfile-lint)).
- **License:** Apache-2.0 ✓.
- **What it catches:** Lockfile entries resolving to anything
  other than the official npm registry. Defends against a polecat
  (or compromised contributor) committing a `package-lock.json`
  that pulls a malicious tarball from a non-registry URL, GitHub
  fork, or attacker-controlled CDN. Also enforces HTTPS-only
  resolution.
- **Integration footprint:**
  - Add `lockfile-lint` as a devDependency.
  - Add `frontend/.lockfile-lintrc.json` with
    `{ "path": "package-lock.json", "type": "npm",
       "validate-https": true,
       "allowed-hosts": ["npm"], "validate-package-names": true,
       "validate-checksum": true }`.
  - Add `npm run audit:lockfile` script and a CI step.
- **Effort:** **S** (≤1h).
- **Tradeoffs:** Slightly redundant with `npm ci`'s integrity
  check, but the integrity check is per-tarball; this enforces
  *which sources are allowed*. Low runtime cost.

### R5 — OSV-Scanner as a deeper SCA gate

- **Tool:** OSV-Scanner
  ([github.com/google/osv-scanner](https://github.com/google/osv-scanner)).
- **License:** Apache-2.0 ✓.
- **What it catches:** Vulnerabilities cross-referenced from the
  OSV.dev database (broader than GitHub Advisory: includes Go
  vuln DB, RustSec, OSS-Fuzz findings, etc., though for npm the
  overlap with R1 is large). Useful supplement because:
  - It also covers the **Go side** (`go.mod`) in one scan, so
    one tool for both halves of the repo.
  - It has a "guided remediation" mode for npm lockfiles that
    suggests minimal upgrades.
  - It's not coupled to GitHub's Advisory Database push timing.
- **Integration footprint:**
  - Use the official GitHub Action
    `google/osv-scanner-action/.github/workflows/osv-scanner-pr.yml`
    in a new `.github/workflows/osv.yml`, or run the binary in
    a step.
  - No source code changes; no Make change strictly required
    (but a `make scan-osv` target that runs the local binary
    is a natural addition).
- **Effort:** **M** (½ day — first-time Action wiring, triage of
  initial findings).
- **Tradeoffs:** Some duplication with R1 for npm-specific
  findings. The dedup is fine — OSV catches things npm audit
  misses (notably non-GHSA-tagged CVEs) and vice versa, and CI
  cost is ~30s per run. Do **not** adopt OSV-Scanner *instead of*
  R1: `npm audit` + `audit-ci` integrate with the npm advisory
  flow developers already use locally; OSV is the second layer.

### R6 — OpenSSF Scorecard for supply-chain hygiene posture

- **Tool:** Scorecard ([github.com/ossf/scorecard](https://github.com/ossf/scorecard)).
- **License:** Apache-2.0 ✓.
- **What it catches:** Not vulnerabilities — *meta* security
  posture: branch protection, signed releases, fuzzing presence,
  pinned actions, etc. Score is published as a badge.
- **Integration footprint:** One workflow file
  (`.github/workflows/scorecard.yml`) using
  `ossf/scorecard-action`. Runs on schedule, not on every PR.
- **Effort:** **S** (≤1h).
- **Tradeoffs:** Not strictly a "vuln scanner"; it grades us on
  practices. Low signal in the short term, useful long-term as
  the project grows. Optional — sequence after R1–R3.

### R7 — Retire.js for known-vulnerable JS bundled in source

- **Tool:** Retire.js
  ([github.com/RetireJS/retire.js](https://github.com/RetireJS/retire.js)).
- **License:** Apache-2.0 ✓.
- **What it catches:** Vendored / inlined JS libraries (jQuery,
  AngularJS, etc.) with known vulnerabilities — the case where a
  developer pasted a CDN script into a Svelte component, bypassing
  `package.json`. Retire scans **source files**, not just lockfiles.
- **Integration footprint:** A devDependency, an `npm run audit:retire`
  script, and a CI step. Or run via the official GitHub Action.
- **Effort:** **S** (≤1h).
- **Tradeoffs:** Low signal *today* — the codebase has no vendored
  JS (verified: no `static/`, no `public/vendor/`, no inline `<script
  src="https://cdn...">` in any `.svelte` or `index.html`). So R7 is
  a *guard* against future regressions, not a present finding.
  Adopt only if the team plans to allow vendored JS; otherwise skip.

### R8 — `docs/quality/README.md` index

- **Tool:** None (just a Markdown file).
- **License:** N/A.
- **What it catches:** Not a vuln, but a docs-discoverability gap.
  Two Q-wave reviews now live in `docs/quality/`; readers should
  see them as a set with one-line descriptions and date stamps.
- **Integration footprint:** New file
  `docs/quality/README.md` with table of: Q-id → bead → topic →
  date → file. Add a one-line link to top of each Q-wave doc.
- **Effort:** **S** (≤30 min).
- **Tradeoffs:** Pure docs; no code risk. Trivial to defer to a
  later polecat if scope creep matters.

---

## 4. Prioritized list

In order of value-per-effort. Implement R1+R2+R3 in one PR — they're
mutually reinforcing and all S-effort.

1. **R1 (`audit-ci` in CI)** — closes the #1 gap (no CVE detection),
   uses tooling already familiar to npm developers (`npm audit`),
   surfaces real findings the first time it runs.
2. **R2 (Dependabot config)** — independent, free, automatic
   ongoing remediation pipeline. The `dependabot.yml` is also
   prerequisite-light: even if R1 is delayed, R2 alone gets us
   Alerts for free.
3. **R3 (`npm audit signatures`)** — closes the registry-tampering
   blind spot, costs one CI step.
4. **R5 (OSV-Scanner)** — second-layer SCA, also covers Go, useful
   even after R1 is in place.
5. **R4 (`lockfile-lint`)** — narrowest gap, but cheapest insurance
   against a mis-resolved lockfile entry. Defer until R1–R3 are
   bedded in to avoid PR-size creep.

R6, R7, R8 are nice-to-haves; sequence after the top five.

---

## 5. Out of scope (deliberately not recommended)

- **Snyk** — proprietary, gated free tier; not OSS. The free tier's
  rate limits and account-coupling create friction that the OSS
  alternatives (R1 + R5) cover. Reconsider only if the team adopts
  Snyk for a paid use case (license scanning, container scanning)
  and the npm scanning becomes a free side-effect.
- **Socket.dev / `@socketsecurity/cli`** — actively maintained and
  very good at catching malicious-package signals (post-install
  scripts, network access, typosquatting). **Excluded because the
  CLI repo declares no LICENSE file** (verified via GitHub API:
  `license: null` on `SocketDev/socket-cli` as of 2026-05-09). Per
  CONTRACT §16, "anything custom or unknown" is forbidden, and
  introducing a binary with no declared license — even just in CI —
  violates the section's spirit. Revisit if Socket publishes an
  explicit OSI-allowlisted LICENSE.
- **Renovate** — capable, but the OSS edition is **AGPL-3.0**
  (forbidden under §16, lines 3329–3332). Mend's hosted Renovate
  service avoids redistribution but also avoids transparency.
  Dependabot (R2) is first-party, free, and license-clean — pick
  the one that doesn't require a license-policy footnote.
- **CodeQL for npm** — useful for application-code SAST, not
  dependency CVE detection (the use case here). Worth its own
  bead in the security-review wave (CONTRACT §17). Out of scope
  for this Q-6 review.
- **Trivy** — strong for container images and OS packages. Will be
  the right answer when the Dockerfile-published image
  (`ghcr.io/.../minerals`) gets scanning, which is a separate
  bead (image scanning, not frontend deps). Out of scope here.
- **`npm audit fix --force`** — explicitly anti-recommended. Forced
  fixes can downgrade or change major versions silently; the team
  should review and apply each upgrade rather than letting CI write
  PRs.
- **SBOM generation (CycloneDX, Syft)** — per CONTRACT §17 line
  1039, "Image signing (cosign) and SBOM publishing" is deferred.
  The dependency-scan recommendations above don't depend on having
  an SBOM. Sequence SBOM with image signing, not with frontend
  vuln scanning.
- **Mocking / sandbox audit (`socket-npm install` wrapping)** —
  same license-unknown issue as Socket CLI itself.

---

## References

- CONTRACT.md §16 — Dependencies & libraries (lines 3207–3369),
  esp. license allowlist 3316–3344. Every R1–R8 tool above is
  Apache-2.0 or first-party-GitHub.
- CONTRACT.md §17 — Security never-do list. Line 1038 ("Security
  scanning ... Deferred to v2; cheap to add when motivated") is the
  bead authority for activating these gates now. Line 1040
  authorizes Dependabot/Renovate.
- `frontend/package.json`, `frontend/package-lock.json` — current
  declared and resolved dependency state (counts in §1).
- `.github/workflows/pr.yml`, `.github/workflows/main.yml` — current
  CI gates; integration points for R1/R3/R5.
- Bead **mi-7u3** — Q-6 acceptance criteria.
- Companion review: `docs/quality/backend-test-coverage.md.md`
  (Q-1 / `mi-fmj`) — same Q-wave; sets the docs format used here.
