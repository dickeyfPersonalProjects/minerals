# Backend dependency vulnerability scanning — quality review (Q-5)

> Scope: Go backend rooted at the repo root (`go.mod`, `cmd/`,
> `internal/`, `migrations/`, `Dockerfile`). Excludes the
> TypeScript/Svelte frontend (covered by Q-6,
> `docs/quality/frontend-vuln-scanning.md`).
> Date: 2026-05-10. Bead: `mi-6xy`.

## TL;DR

The Go backend has **10 direct + 32 indirect dependencies** in
`go.mod` (195-line `go.sum`), ships in a distroless container
image to `ghcr.io`, and has **zero vulnerability scanning** —
no `govulncheck` in CI or Make, no Dependabot config, no image
scan, no SBOM, no signature/provenance verification. CONTRACT
§17 line 1038 explicitly defers "Security scanning (CodeQL,
Trivy, dependency CVE checks)" to v2 with the note "cheap to
add when motivated"; this review is that motivation. The
cheapest, highest-signal moves are: (1) wire `govulncheck` into
`make` + the `pr.yml` and `main.yml` backend jobs, (2) commit
`.github/dependabot.yml` for `gomod`, `docker`, and
`github-actions` ecosystems, (3) add Trivy filesystem + image
scans to the existing `image` job. All three are S-effort,
free, and use Apache-2.0 / BSD-3-Clause tools that fit the §16
license allowlist.

---

## 1. Current state

### Codebase shape

- Go 1.25 module `github.com/dickeyfPersonalProjects/minerals`
  (per `go.mod` line 3). Toolchain in CI uses
  `go-version-file: go.mod`.
- **57 Go source files** across `cmd/minerals/` (entry point,
  subcommands incl. `serve`, `migrate`, `openapi`) and
  `internal/{api,auth,config,db,domain,markdown,migrations,storage,web}`.
- Single binary, distroless runtime
  (`gcr.io/distroless/static-debian12:nonroot`),
  `CGO_ENABLED=0` — pure-Go closure (CONTRACT §16 lines
  3209–3220).

### Dependency surface (from `go.mod`)

**Direct (`require ( ... )` block, 10 modules):**

| Module | Version | Purpose |
|---|---|---|
| `github.com/aws/aws-sdk-go-v2` | `v1.41.7` | AWS SDK core |
| `github.com/aws/aws-sdk-go-v2/config` | `v1.32.17` | AWS config loader |
| `github.com/aws/aws-sdk-go-v2/credentials` | `v1.19.16` | AWS credentials providers |
| `github.com/aws/aws-sdk-go-v2/service/s3` | `v1.100.1` | S3/MinIO client (object storage) |
| `github.com/aws/smithy-go` | `v1.25.1` | AWS SDK runtime |
| `github.com/danielgtaylor/huma/v2` | `v2.37.3` | OpenAPI 3.1 framework (locked in by mi-cy4) |
| `github.com/golang-migrate/migrate/v4` | `v4.19.1` | DB migrations |
| `github.com/google/uuid` | `v1.6.0` | UUIDv7 |
| `github.com/jackc/pgx/v5` | `v5.9.2` | Postgres driver + pool |
| `github.com/oklog/ulid/v2` | `v2.1.1` | Request ID generation |

**Indirect (`// indirect` block, 32 modules):** AWS-SDK
sub-modules, EXIF parsing (`dsoprea/go-exif/v3`), HTML
sanitizer (`microcosm-cc/bluemonday`), CSS parser
(`gorilla/css`), Markdown (`yuin/goldmark`),
`golang.org/x/{image,net,sync,text}`, `gopkg.in/yaml.v2`,
`lib/pq` (pulled in transitively via `golang-migrate`), etc.

**Lockfile (`go.sum`):** **195 lines**, 42 module entries,
hash-pinned. `go.mod` + `go.sum` are committed; `go mod tidy`
is the only sanctioned update path (CONTRACT §16 lines
3290–3298).

### Build/runtime artifact surface

- **Container image** built from `Dockerfile` (3-stage:
  `node:22-alpine` for SPA build → `golang:1.25-alpine` for
  Go build → `gcr.io/distroless/static-debian12:nonroot`
  for runtime). Pushed to
  `ghcr.io/dickeyfpersonalprojects/minerals` from
  `.github/workflows/main.yml` on every `main` push (tags:
  `latest`, `staging`, `sha-<short>`).
- **No `vendor/` directory** (CONTRACT §16 lines 3346–3354 —
  not vendoring is intentional in v1).

### Scanning / advisory tooling actually in place

**None.** Concretely:

- `Makefile` targets: `build`, `run`, `test`, `fmt`, `vet`,
  `tidy`, `clean`, `lint` (`golangci-lint run`), `fmt-check`,
  `migrate-up/down/version/create`, `test-integration`,
  frontend targets, `compose-*`, `gen-api-client`,
  `openapi-spec`. **No `vuln`, `audit`, `scan`, or
  equivalent target.**
- `.github/workflows/pr.yml` Backend job: `setup-go` →
  `make fmt-check` → `make vet` → `golangci-lint-action@v8`
  → `make test` → `make test-integration` (gated on
  migrations existing). **No `govulncheck` step, no Trivy
  step, no OSV step.**
- `.github/workflows/main.yml` Tests job: identical
  pre-image gates, plus a `compose-smoke` job (lifecycle
  validation) and an `image` job that builds + pushes the
  Docker image with no scanning of the resulting layers.
- `.golangci.yml`: `default: standard`, `timeout: 5m`,
  `exclusions.paths: [frontend]`. **No `gosec` linter
  enabled** (and `gosec` is SAST, not deps SCA — orthogonal,
  flagged in §3 only as adjacent).
- `.github/dependabot.yml`: **does not exist.**
- No `osv-scanner.toml`, no `trivy.yaml`, no `.trivyignore`,
  no `.snyk`, no `.gosec*`, no `vuln.yaml`.
- No SBOM produced or published. No `cosign` / Sigstore
  attestations on the image.
- Image consumers have no way to verify the GHCR registry
  served the same bytes that CI built.

### Contract context

- **CONTRACT §17 (deferred-to-v2 list)** lines 1037–1042:
  *"Security scanning (CodeQL, Trivy, dependency CVE
  checks) — Deferred to v2; cheap to add when motivated"*
  and *"Automated dependency updates (Dependabot or
  Renovate config)"* — both deferred but explicitly
  marked as cheap. Same section line 990: *"Run security
  scans (CodeQL, Trivy, Snyk). Deferred to v2; cheap to
  add when motivated."* This review is the activation
  authority.
- **CONTRACT §16 License policy** (lines 3316–3344):
  allowlist is MIT, BSD-2/3, Apache-2.0, ISC, MPL-2.0,
  public domain. Forbidden: GPL/LGPL/AGPL,
  BSL/SSPL/Elastic/Confluent, custom, unknown. Any
  recommended scanner that ships as a runtime / CI binary
  must clear this bar.
- **CONTRACT §16 Pure-Go requirement** (lines 3209–3220):
  the runtime build is `CGO_ENABLED=0` on
  `distroless/static-debian12:nonroot`. CI scanners are
  build-time tooling, so they can use cgo or system
  packages — but this matters because if a CVE in a
  transitive forces an upgrade and the upgrade requires
  cgo, the polecat MUST escalate (lines 3216–3220).
  Vulnerability scanning has to surface findings *with
  enough context* for that decision.
- **CONTRACT §16 Major version bumps** (lines 3309–3312):
  major bumps are contract changes. Auto-merge of
  Dependabot major-bump PRs MUST NOT be enabled — see R2.

---

## 2. Observed gaps

Specific, unambiguous misses:

1. **No CVE detection on the Go dependency graph.** A new CVE
   on `pgx`, `aws-sdk-go-v2`, `huma/v2`, `golang-migrate`,
   `golang.org/x/net`, `bluemonday`, or any of the 42 modules
   in `go.sum` lands silently. The backend serves authenticated
   API traffic and renders user-supplied Markdown / HTML
   (`yuin/goldmark` + `bluemonday` per §17), so an upstream
   sanitizer-bypass or HTTP-request-smuggling CVE is in scope,
   not theoretical. Today nothing — automated or human —
   would notice within reasonable latency.
2. **No automated dependency updates.** With 42 transitives
   and 10 directs, CONTRACT §16's manual `go get -u@latest`
   policy (lines 3302–3304) is the only mechanism, and there
   is no ritual that runs it on a cadence. A vulnerable
   version pin can sit unfixed indefinitely.
3. **No image / filesystem CVE scan.** The published image
   `ghcr.io/dickeyfpersonalprojects/minerals` has its base
   layer (`distroless/static-debian12:nonroot`) and the
   embedded Go binary. Distroless static is small but not
   zero — the binary itself is scannable for known-vulnerable
   Go modules, and base-layer CVEs (glibc, ca-certificates)
   apply if Distroless updates lag.
4. **No SBOM.** Without an SBOM (CycloneDX or SPDX) attached
   to the image, downstream consumers can't run their own
   scans against our artifact. This blocks any future
   external review (auditor, partner, security reviewer)
   that would expect an SBOM as table stakes.
5. **No image signature / provenance.** The registry could
   serve a substituted image and nothing in the deploy chain
   would detect it. Cosign / Sigstore is explicitly deferred
   per CONTRACT §17 line 994–995, but flagged here because
   it is on the same "cheap to add" list as the items above
   and shares one CI step.
6. **No advisory-allowlist mechanism.** When a real CVE
   lands and the fix isn't yet available upstream (or the
   CVE is non-exploitable in our reachable code), there's no
   place to record "we've reviewed this, suppressed until
   X." Any adopted scanner must support a suppression file
   so the gate doesn't become all-or-nothing.
7. **No license audit on the Go closure.** §16 forbids GPL /
   AGPL / BSL / SSPL / unknown licenses. Today this is
   trust-on-first-use during PR review of `go.sum` diffs;
   nothing enforces the allowlist on the transitive closure.
   Low-probability but high-blast-radius: an AGPL transitive
   could land via an indirect upgrade and ship in our
   image without notice.
8. **Quality-doc index missing.** `docs/quality/` holds
   seven Q-wave reviews (six others plus this one) and has
   no `README.md`. Readers landing in the directory have no
   map. Cross-cutting with Q-6 (which flagged the same gap)
   — implementation should land in whichever PR clears it
   first. Out of scope for the *vuln-scanning* deliverable
   per the bead, but flagged so a follow-up doesn't drop it.

Non-gaps (deliberately): the **direct dependency licenses** all
appear allowlist-compatible from a manifest read (`pgx` MIT,
`aws-sdk-go-v2` Apache-2.0, `huma/v2` MIT, `migrate` MIT,
`uuid` BSD-3, `ulid/v2` Apache-2.0, `goldmark` MIT,
`bluemonday` BSD-3, `dsoprea/go-exif/v3` MIT,
`golang.org/x/...` BSD-3). Transitive coverage isn't verified —
that's R5 below.

---

## 3. Recommendations

Each recommendation includes name, link, license, what it
catches, integration footprint, effort, and tradeoffs.

### R1 — `govulncheck` in CI + Make

- **Tool:** `govulncheck`
  ([github.com/golang/vuln](https://github.com/golang/vuln);
  module `golang.org/x/vuln/cmd/govulncheck`).
- **License:** **BSD-3-Clause** ✓ (allowlist-compatible per
  §16; the `golang.org/x/...` family is the same license
  as the deps already in use).
- **What it catches:** Vulnerabilities in the Go stdlib
  *and* third-party modules from the official Go vuln
  database (`pkg.go.dev/vuln`). Key advantage over
  generic SCA: govulncheck does **call-graph analysis**
  and reports only CVEs whose vulnerable functions are
  *reachable* from `cmd/minerals`, not every CVE in any
  module in `go.sum`. This dramatically reduces noise vs.
  R5 / R6.
- **Integration footprint:**
  - Add a `make vuln` target:
    `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
    (no devDep entry needed — `go run` resolves and
    caches the tool). Alternative: pin the version in a
    new `tools.go` build-tagged file (Go's recommended
    "tool dependencies" pattern, [pkg.go.dev/cmd/go#hdr-Modules_and_vendoring](https://pkg.go.dev/cmd/go#hdr-Modules_and_vendoring)).
  - Add a CI step in `pr.yml` and `main.yml` Backend
    jobs after `make test`:
    `- name: govulncheck` /
    `  run: make vuln`.
  - No `osv-scanner.toml`-style file initially — add a
    suppression mechanism (see Tradeoffs) only when a
    real triage demands it.
- **Effort:** **S** (≤2h, including triage of any initial
  findings). The `golang.org/x/net` indirect at `v0.50.0`
  is recent — first run will probably be clean or near-
  clean.
- **Tradeoffs:** No native suppression / allowlist file
  format — if a finding is non-exploitable but unfixed
  upstream, the only options are (a) wait for the fix,
  (b) bump to a non-vulnerable version, (c) wrap the
  govulncheck invocation in a script that filters
  specific GHSA IDs (anti-pattern; brittle). A more
  robust suppression story is one of the reasons R5
  (OSV-Scanner) is the right second layer. Govulncheck
  is also Go-only — does not scan the base image's
  system packages (R3 covers that).

### R2 — Dependabot config for `gomod` + `docker` + `github-actions`

- **Tool:** Dependabot
  ([docs.github.com/.../dependabot-version-updates](https://docs.github.com/en/code-security/dependabot/dependabot-version-updates/configuration-options-for-the-dependabot.yml-file)).
- **License:** N/A — first-party GitHub feature; no binary
  enters the repo. Free for public and private repos.
- **What it catches:** Out-of-date dependencies (open PRs to
  bump), and Dependabot Alerts surface CVEs from the GitHub
  Advisory Database independently of CI runs (so an advisory
  published Sunday gets an Alert Monday morning even if no
  PR runs CI that week). Three ecosystems matter for the
  backend:
  - `gomod` rooted at `/` — covers the 10 direct + 32
    indirect Go modules.
  - `docker` rooted at `/` — covers `Dockerfile` base
    images (the Node, Go, and Distroless tags) — currently
    `node:22-alpine`, `golang:1.25-alpine`, and
    `gcr.io/distroless/static-debian12:nonroot`. None are
    digest-pinned, so semver bumps on digest-only
    `:nonroot` tags won't be caught, but Distroless
    *labelled* updates (e.g., `:debian13-nonroot`) will be.
  - `github-actions` rooted at `/` — covers the
    pinned-by-major actions in `.github/workflows/*.yml`
    (`actions/checkout@v4`, `actions/setup-go@v5`,
    `golangci/golangci-lint-action@v8`, etc.).
- **Integration footprint:**
  - One file: `.github/dependabot.yml` with three `updates:`
    blocks, weekly cadence, `groups:` for minor+patch,
    `ignore:` empty initially, no auto-merge enabled.
  - Per CONTRACT §16 lines 3309–3312, **major-version
    bumps stay manual** — a Dependabot PR that bumps a
    major version is a contract change requiring its own
    review. The config should not auto-merge anything;
    it just opens PRs.
- **Effort:** **S** (≤30 min — small file, reviewable in
  one read). Pairs naturally with R1 — once R1 is wired,
  Dependabot's bump PRs run through it for free.
- **Tradeoffs:** Generates PR noise (one PR per direct
  dep by default; transitives only bump via direct
  upgrades). Mitigate with `groups: { go-minor-and-patch:
  { update-types: [minor, patch] } }`. Dependabot can be
  disabled at any time without rollback work.

### R3 — Trivy filesystem + image scan

- **Tool:** Trivy
  ([github.com/aquasecurity/trivy](https://github.com/aquasecurity/trivy)).
- **License:** **Apache-2.0** ✓.
- **What it catches:** Two distinct gates from one binary:
  - **Filesystem mode** (`trivy fs .`): scans `go.mod` /
    `go.sum` for known vulns (overlap with R1, but Trivy
    pulls from multiple advisory sources — GHSA, OSV,
    distro) **and** detects misconfigurations in
    `Dockerfile`, `kustomize/`, and `docker-compose.yml`
    (e.g., images without digest pins, root-by-default
    containers, `latest` tag usage). Trivy IaC scanning
    is the underrated half here.
  - **Image mode** (`trivy image
    ghcr.io/.../minerals:sha-<short>`): scans the built
    image for OS-package CVEs in the base layer. With
    Distroless static, the surface is tiny (ca-certificates,
    tzdata) — but Trivy will surface a `tzdata` CVE the
    moment Distroless lags upstream Debian.
- **Integration footprint:**
  - Use `aquasecurity/trivy-action`
    ([github.com/aquasecurity/trivy-action](https://github.com/aquasecurity/trivy-action))
    in `pr.yml` (filesystem mode, blocks merge on HIGH/
    CRITICAL) and in `main.yml`'s `image` job *after* the
    push step (image mode, blocks if a base CVE survives
    publishing).
  - One config file `trivy.yaml` at repo root for shared
    severity thresholds (`severity: HIGH,CRITICAL`,
    `exit-code: 1`, `ignore-unfixed: true` to suppress
    "no fix available yet").
  - Optional `.trivyignore` for triaged CVEs with a
    one-line justification per entry.
- **Effort:** **S** (≤2h — the Action does most of the
  work; the budget is for triaging the first run).
- **Tradeoffs:** Distroless static is so small that the
  *image* scan will rarely fire — most signal will come
  from the *filesystem* scan. That overlaps with R1 for
  the Go side; the unique value is IaC scanning of
  `Dockerfile` / `kustomize/` / `docker-compose.yml`.
  Filesystem scans run on the runner without registry
  auth; image scans need registry pull credentials —
  in CI, the existing `GITHUB_TOKEN` covers this for
  the same-repo image.

### R4 — `.github/dependabot.yml` Alerts (GHSA-driven)

- **Tool:** Dependabot Alerts (a separate feature from R2's
  version updates; both share the config file but the
  alerting is opt-in via repo settings, not the YAML).
- **License:** N/A — first-party GitHub feature.
- **What it catches:** GHSA-database advisories on the
  declared `gomod` ecosystem, surfaced in the repo's
  Security tab and via webhook / email. Critical
  difference from R2: alerts fire on *advisory
  publication*, not on Dependabot's update cadence. A
  CVE published Sunday creates an alert within hours,
  even if R2's weekly cron hasn't fired yet.
- **Integration footprint:**
  - Repo Settings → Code security → enable "Dependabot
    alerts" + "Dependabot security updates".
  - No file changes beyond R2's `.github/dependabot.yml`.
  - No CI changes — alerts are out-of-band.
- **Effort:** **S** (≤15 min — it's a checkbox).
- **Tradeoffs:** Alerts can be noisy if a transitive has
  a published CVE that doesn't actually affect us — but
  the Security tab supports per-alert dismissal with
  reason. Free for public repos; on private repos
  Dependabot security updates require GitHub Advanced
  Security only above the free tier (Dependabot alerts
  themselves are free on private repos).

### R5 — OSV-Scanner as a deeper SCA gate

- **Tool:** OSV-Scanner
  ([github.com/google/osv-scanner](https://github.com/google/osv-scanner)).
- **License:** **Apache-2.0** ✓.
- **What it catches:** Vulnerabilities cross-referenced from
  the OSV.dev database (broader than GHSA: includes Go vuln
  DB, RustSec, OSS-Fuzz findings, CVE feeds). Useful
  supplement because:
  - Same binary scans **both `go.sum` and
    `frontend/package-lock.json`** in one pass — paired
    with Q-6's R5 (which makes the same recommendation
    for the frontend), one tool covers both halves of
    the repo.
  - It supports a native `osv-scanner.toml` suppression
    file, so unfixed-upstream CVEs can be triaged with
    expiration dates — cleaner than govulncheck's
    no-suppression model.
  - It's not coupled to the GitHub Advisory push timing.
- **Integration footprint:**
  - Use `google/osv-scanner-action`
    ([github.com/google/osv-scanner-action](https://github.com/google/osv-scanner-action))
    in a new `.github/workflows/osv.yml` (PR + main +
    weekly cron), or run the binary in an existing
    workflow step.
  - One config file `osv-scanner.toml` at repo root for
    suppression entries (initially empty).
  - No source code changes; no Make change strictly
    required (but a `make scan-osv` target is a natural
    addition).
- **Effort:** **M** (½ day — first-time Action wiring,
  triage of initial findings, decision on whether to
  block on findings vs. report-only).
- **Tradeoffs:** Some duplication with R1 (Go modules)
  and Q-6 R1 (npm modules). The dedup is the point —
  govulncheck's reachability analysis filters Go
  noise; OSV is the second layer that catches what
  reachability misses (e.g., a CVE in a transitive
  whose vulnerable function is called from a code path
  govulncheck didn't see because of build-tag exclusion).
  Do **not** adopt OSV-Scanner *instead of* R1: govulncheck's
  reachability is unique to the Go ecosystem and worth
  keeping as the noise-floor gate.

### R6 — `go-licenses` license enforcement

- **Tool:** `go-licenses`
  ([github.com/google/go-licenses](https://github.com/google/go-licenses)).
- **License:** **Apache-2.0** ✓.
- **What it catches:** The transitive license closure of
  the Go binary. CONTRACT §16 forbids GPL / AGPL / BSL /
  SSPL / unknown licenses; today nothing enforces this on
  *transitives*. `go-licenses check` exits non-zero if any
  module's license isn't on a configured allowlist.
- **Integration footprint:**
  - Add a `make license-check` target:
    `go run github.com/google/go-licenses@latest check
    --allowed_licenses=MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0,Unlicense,CC0-1.0
    ./cmd/minerals`.
  - Add a CI step in `pr.yml` Backend job after
    `make vet`.
  - Optional: a `disallowed_types` file documenting which
    licenses are forbidden (useful for human review even
    if `--allowed_licenses` is the gate).
- **Effort:** **M** (½ day — the tool's classification is
  best-effort and may report `Unknown` on modules with
  non-standard `LICENSE` filenames, requiring manual
  override entries).
- **Tradeoffs:** `go-licenses` is in maintenance mode —
  Google maintains it but it's not a rapid-development
  project. Acceptable because the use case (license
  classification) is itself slow-moving. Alternative is
  `go-license-detector`
  ([github.com/go-enry/go-license-detector](https://github.com/go-enry/go-license-detector),
  Apache-2.0) which has more recent commits but fewer CI
  ergonomics — sequence as a fallback if `go-licenses`
  causes friction.

### R7 — SBOM generation with Syft (paired with the image build)

- **Tool:** Syft
  ([github.com/anchore/syft](https://github.com/anchore/syft)).
- **License:** **Apache-2.0** ✓.
- **What it catches:** Not vulnerabilities directly — Syft
  produces an SBOM (CycloneDX or SPDX) for the built
  artifact. The SBOM is then consumable by Grype, Trivy,
  or any external scanner without needing access to our
  build environment. CONTRACT §17 line 996 explicitly
  defers SBOM publishing; this rec activates that line.
- **Integration footprint:**
  - Add a step to `.github/workflows/main.yml`'s `image`
    job after the `docker/build-push-action`:
    `anchore/sbom-action@v0` with `image:
    ${{ env.IMAGE_NAME }}:sha-<short>`,
    `format: cyclonedx-json`, `output-file:
    sbom.cdx.json`. The Action attaches the SBOM as a
    workflow artifact and (optionally) signs it with
    cosign.
  - No Make target required — SBOM is a CI-only
    artifact.
- **Effort:** **S** (≤1h — single Action wiring).
- **Tradeoffs:** SBOM only earns its keep if something
  consumes it. Today nothing does — so R7 is enabling
  rather than gating. Pair with R3 (Trivy can ingest
  SBOM as input) or with future cosign attestation work.
  Optional; sequence after R1–R5.

### R8 — OpenSSF Scorecard for supply-chain hygiene posture

- **Tool:** Scorecard
  ([github.com/ossf/scorecard](https://github.com/ossf/scorecard)).
- **License:** **Apache-2.0** ✓.
- **What it catches:** Not vulnerabilities — *meta* security
  posture: branch protection, signed releases, fuzzing
  presence, pinned actions, dangerous-workflow patterns.
  Score is published as a badge.
- **Integration footprint:** One workflow file
  (`.github/workflows/scorecard.yml`) using
  `ossf/scorecard-action`. Runs on schedule, not on every
  PR. Pairs with the same Q-6 R6 recommendation — one
  Scorecard config covers both Go and frontend halves of
  the repo.
- **Effort:** **S** (≤1h).
- **Tradeoffs:** Not strictly a "vuln scanner"; it grades
  us on practices. Low signal in the short term, useful
  long-term as the project grows. Optional — sequence
  after R1–R5.

### R9 — `gosec` (adjacent: SAST, not SCA) — *suggest, don't bundle*

- **Tool:** gosec
  ([github.com/securego/gosec](https://github.com/securego/gosec)).
- **License:** **Apache-2.0** ✓.
- **What it catches:** Code-pattern security issues in our
  *own* Go source — hard-coded credentials, weak crypto,
  SQL string interpolation, file-permission anti-patterns,
  `unsafe` usage, missing TLS verification. Orthogonal to
  dependency scanning (this review's scope), but flagged
  here because the same activation moment in §17 ("cheap
  to add when motivated") covers it.
- **Integration footprint:** Easiest path — enable the
  `gosec` linter in `.golangci.yml` (`linters: enable:
  [gosec]`), no separate binary required. The
  golangci-lint pipeline already runs in CI. Configure
  per-rule severity in `linters-settings.gosec` if the
  default is too noisy.
- **Effort:** **S** (≤1h to enable; **M** if first run
  finds genuine issues that need fixing).
- **Tradeoffs:** Out of scope for *this* bead's title
  (SCA, not SAST). Including as R9 because it's the
  cheapest **adjacent** add and it's natural to bundle
  into a "security gates" PR after R1–R3. **Does not
  block** the SCA recommendations; sequence
  independently.

---

## 4. Prioritized list

In order of value-per-effort. Implement R1+R2+R4 in one PR —
they're mutually reinforcing, all S-effort, and they close
the three biggest gaps at once.

1. **R1 (`govulncheck` in CI)** — closes the #1 gap (no Go
   CVE detection), reachability analysis keeps noise low,
   uses official Go tooling that's already understood by
   anyone who reads pkg.go.dev/vuln. First gate to land.
2. **R2 (Dependabot for `gomod` + `docker` + `github-actions`)** —
   independent, free, automatic ongoing remediation pipeline.
   The `dependabot.yml` is also prerequisite-light: even if
   R1 is delayed, R2 alone gets us PRs and (with R4) Alerts.
3. **R3 (Trivy filesystem + image scan)** — covers the
   gaps R1 doesn't: IaC misconfiguration in `Dockerfile` /
   `kustomize/` / `docker-compose.yml`, and base-layer CVEs
   in the published image. One Action does both.
4. **R4 (Dependabot Alerts)** — checkbox-tier; pairs with R2
   so it's natural to enable in the same PR as the YAML.
5. **R5 (OSV-Scanner)** — second-layer SCA, covers Go *and*
   frontend, has a native suppression model that R1 lacks.
   Useful even after R1+R3 are in place.

R6 (license enforcement), R7 (SBOM), R8 (Scorecard), R9
(gosec) are nice-to-haves; sequence after the top five.
None block production.

---

## 5. Out of scope (deliberately not recommended)

- **Snyk** — proprietary, gated free tier; not OSS. The
  free tier's rate limits and account-coupling create
  friction that the OSS alternatives (R1 + R3 + R5) cover.
  Reconsider only if the team adopts Snyk for a paid use
  case (license scanning, container scanning) and the Go
  scanning becomes a free side-effect.
- **Mend / Renovate** — capable, but the OSS Renovate
  edition is **AGPL-3.0** (forbidden under §16, lines
  3329–3332). Mend's hosted Renovate service avoids
  redistribution but also avoids transparency. Dependabot
  (R2) is first-party, free, and license-clean — pick the
  one that doesn't require a license-policy footnote.
  (Same call as Q-6's R2.)
- **Sonatype Nancy** ([github.com/sonatype-nexus-community/nancy](https://github.com/sonatype-nexus-community/nancy),
  Apache-2.0). Considered as an OSS-Index-driven SCA. Not
  recommended because govulncheck (R1) is the official
  Go-team tool with reachability analysis, and OSV-Scanner
  (R5) covers the breadth dimension Nancy was historically
  good for. Adopting Nancy would be a third overlapping
  layer with no unique signal.
- **CodeQL for Go** — useful for application-code SAST,
  not dependency CVE detection (the use case here). gosec
  (R9) covers the same surface for Go at a fraction of
  the runtime. CodeQL warrants its own bead in the
  security-review wave (CONTRACT §17). Out of scope for
  this Q-5 review.
- **Cosign image signing** — explicitly deferred per
  CONTRACT §17 line 994–995. The dependency-scan
  recommendations above don't depend on signing being
  in place. Sequence cosign with R7 (SBOM) when image
  trust becomes a deploy-side requirement.
- **Multi-arch builds** (line 1040) — orthogonal to
  vulnerability scanning; called out only because R3
  (Trivy image scan) would need to scan each arch
  separately if multi-arch lands. Not a blocker; a
  matrix step in `main.yml` covers it.
- **`go list -m -u all` reports** — not a recommendation
  because Dependabot (R2) supersedes it as a polling
  mechanism. Useful for one-off audits; not a CI gate.
- **GitHub Actions pinning by SHA** — strong supply-chain
  hygiene practice, but a separate concern from CVE
  scanning. Pair with R8 (Scorecard) which grades on
  this; don't bundle into the SCA PR.
- **GovOSS / FOSSA** — proprietary license-scanning SaaS;
  go-licenses (R6) handles the contract requirement
  without third-party data sharing.

---

## References

- **CONTRACT.md §16** — Dependencies & libraries (lines
  3207–3369), esp. license allowlist 3316–3344. Every
  R1–R9 tool above is Apache-2.0, BSD-3-Clause, or
  first-party-GitHub.
- **CONTRACT.md §17** — Security never-do list. Lines
  990–996 ("Run security scans (CodeQL, Trivy, Snyk).
  Deferred to v2; cheap to add when motivated"; same for
  SBOM and signing) and lines 1037–1042 ("Security
  scanning ... Deferred to v2; cheap to add when
  motivated"; "Automated dependency updates") are the
  bead authority for activating these gates now.
- **`go.mod`, `go.sum`** — current declared and resolved
  dependency state (counts in §1).
- **`Dockerfile`** — three-stage build; R3 image scan
  targets the published `gcr.io/distroless/static-debian12:nonroot`
  layer.
- **`.github/workflows/pr.yml`, `.github/workflows/main.yml`** —
  current CI gates; integration points for R1, R3, R5,
  R7, R8.
- **`.golangci.yml`** — current lint config; integration
  point for R9 (`gosec`).
- **Bead `mi-6xy`** — Q-5 acceptance criteria.
- **Companion review:** `docs/quality/frontend-vuln-scanning.md`
  (Q-6 / `mi-7u3`) — same Q-wave; same shape and tone.
  R5 (OSV-Scanner) and R8 (Scorecard) overlap with that
  doc's R5 / R6 — a single PR can clear both at once.
