# Backend code quality / static analysis — review (Q-3)

> **Scope.** This doc is an analysis of the Go backend's current quality
> tooling and a prioritized list of additions to consider. **No
> production code changes** are proposed here; landing any of these
> recommendations is a follow-up bead per the Q-wave plan.
>
> Audience: mayor coordinating Q-wave follow-ups, polecats picking up
> the resulting beads.

## TL;DR

The codebase already has the load-bearing gates wired (`gofmt`,
`go vet`, `golangci-lint` standard set, unit + integration tests, CI
on PR and main). The gaps that yield the most return for the least
churn:

1. **Add `gosec` and `errorlint`** to `golangci-lint` — security and
   error-wrapping coverage that the standard set doesn't provide.
2. **Add `govulncheck`** as a CI step — official Go vulnerability
   scanner against `go.mod`/`go.sum`. Free, fast, no false positives.
3. **Add `make test-cover` and a coverage step in CI** — CONTRACT.md
   §9 already documents the target, but the Makefile doesn't define
   it (broken cross-reference).
4. **Add `-race` to the unit test target** — the cost is one Make
   flag and ~2x runtime; the win is real-deal data-race detection.
5. **Add `go-licenses` as a CI gate** — automate the §16 allowlist
   check; today it's enforced by polecat discipline only.

Everything else in §3 is incremental polish.

---

## 1. Current state

### What's wired today

| Concern              | Tool / mechanism                                | Where       |
|----------------------|-------------------------------------------------|-------------|
| Formatting           | `gofmt` via `make fmt-check`                     | Makefile, CI |
| Vetting              | `go vet` via `make vet`                          | Makefile, CI |
| Linting              | `golangci-lint v2.12.2`, `default: standard`     | `.golangci.yml`, CI |
| Unit tests           | `go test ./...`                                  | Makefile, CI |
| Integration tests    | `go test -tags integration ./...`                | Makefile, CI |
| Compose smoke        | `docker compose up` health-check job             | CI |
| Frontend lint/format | `eslint`, `prettier` (PR + main)                 | CI |
| Build reproducibility| `go.mod` + `go.sum` pinned, no `replace`         | repo |

### What golangci-lint actually runs

`.golangci.yml` is minimal:

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

`default: standard` in golangci-lint v2 enables exactly five linters:
`errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`. (`gosimple`
was folded into `staticcheck` in v2, which is why it's not listed
separately.) Nothing is enabled on top of the standard set.

### Codebase shape

- **57 `.go` files**, **14,253 LOC** total (`find internal cmd -name "*.go"`).
- **Source LOC: 7,805**; **test LOC: 6,448**. Roughly 0.83 lines of test
  per line of source — strong signal that the testing culture is real,
  not aspirational.
- **23 `_test.go` files**, of which 9 are `//go:build integration`
  (`internal/db/*_integration_test.go`, `internal/migrations/*`,
  `cmd/minerals/migrate_test.go`).
- Internal packages: `internal/{api,auth,config,db,domain,markdown,
  migrations,storage,storage/exif,storage/imageproc,web}`.

### Source files with no co-located `_test.go`

Identified by neighbor-file scan (does **not** mean untested — the
behavior may be exercised via integration tests in another file):

```
internal/api/huma_auth.go
internal/api/huma_errors.go
internal/api/errors.go
internal/api/middleware.go
internal/web/web.go
internal/db/repos.go
internal/db/pool.go
internal/storage/storage.go
```

`internal/web/web.go` (the `embed.FS` host) and `internal/db/repos.go`
(repository interfaces) fall into the §9 exemption list — testing
them would re-assert framework behavior. `internal/db/pool.go` is
similar (pgx pool wiring). The api `errors.go` / `huma_errors.go` /
`middleware.go` and `internal/storage/storage.go` are real surface,
likely covered transitively by integration tests but worth
double-checking on a future pass — out of scope for this doc.

### What CI does NOT currently do

- No coverage measurement or coverage gate.
- No `-race` flag on unit tests.
- No vulnerability scan on dependencies (`govulncheck`).
- No license audit (CONTRACT.md §16 allowlist is enforced by reviewer
  discipline only).
- No SAST beyond `staticcheck` (i.e. nothing security-focused like
  `gosec` or CodeQL).
- No `goimports` check (so import grouping/sorting drift is
  technically possible — `gofmt` doesn't sort imports).
- No structured-logging linter (the project uses `slog`).

### Broken cross-reference

CONTRACT.md §9 documents:

> ```
> make test-cover           # unit tests with coverage report → coverage.html
> ```

The `Makefile` does not define `test-cover`. Either the rule is
obsolete and the contract should be updated, or the rule is missing
and should be added. Adding the rule is straightforward and aligns
with the coverage recommendation in §3.

---

## 2. Observed gaps

Each gap below is concrete: a category of bug or violation the current
toolchain will not catch.

### 2.1 Security/static-analysis gap (gosec class)

The standard linter set catches correctness bugs but not security
patterns. Things the standard set will **not** flag:

- Use of `math/rand` where `crypto/rand` is required (CONTRACT.md §17
  forbids this for security paths).
- `os/exec` invocations that pass user-influenced strings.
- Hard-coded credentials in source.
- Weak crypto (`md5`, `sha1` for security purposes).
- Insecure TLS (`InsecureSkipVerify: true`).
- File-path joins that don't sanitize traversal.
- SQL string concatenation (CONTRACT.md §11/§17 forbids).

The §17 "never-do list" is comprehensive; there is no mechanical
enforcement of any item on it.

### 2.2 Vulnerability-scanning gap

`go.mod`/`go.sum` is pinned and committed, but there is no scheduled
or per-PR check that any pinned version has a known CVE. Today: a
known-vulnerable `pgx` would land silently. Cost of the missing
check: zero seconds in steady state, ~5s on PRs.

### 2.3 License-audit gap

CONTRACT.md §16 has an explicit license allowlist. Enforcement
relies on polecat reading `go.sum` diffs and recognizing license
strings. Transitive dependencies are not realistically tracked by
hand. A new transitive on (say) GPL-3.0 would not trip any gate.

### 2.4 Coverage-visibility gap

There's no signal — local or in CI — for "does this PR drop
coverage." CONTRACT.md §9 explicitly does not mandate a coverage
percentage, but visibility is still useful: a PR adding 200 lines of
new untested code in `internal/api/` is a different review than one
adding 200 lines to a well-tested package, and reviewers can't see
the difference today.

### 2.5 Race-detector gap

Unit tests run without `-race`. The HTTP server, the `pgxpool`
wrapper, and middleware all involve shared state across goroutines;
a race would be invisible until prod load hit.

### 2.6 Error-wrapping gap

`errcheck` flags ignored errors, but it doesn't catch the more subtle
patterns:

- `if err == sentinel` instead of `errors.Is(err, sentinel)` (breaks
  with wrapped errors).
- `err.(*MyErr)` instead of `errors.As(err, &target)`.
- `fmt.Errorf("...%v", err)` instead of `fmt.Errorf("...%w", err)`
  (loses wrap).

Given the project uses `huma`'s error envelope and wraps domain
errors at the boundary, this is a real category.

### 2.7 Import-ordering gap

`gofmt` enforces formatting but does not sort or group imports.
`goimports` does. Today, an import block can drift into an arbitrary
order without any gate complaining.

### 2.8 Structured-logging gap

The project standardizes on `slog` (per CONTRACT.md §14). `slog`
calls have an easy footgun: passing an odd number of `any`
key-value args silently produces malformed log records. Nothing
detects this today.

### 2.9 Dependency-restriction gap

Some imports are forbidden in specific packages (e.g. `math/rand` in
auth/security paths; direct `database/sql` use anywhere when the
contract is `pgx`). There is no automated declaration of these
constraints, so they're enforced by review.

### 2.10 Local-loop gap (worktree pre-commit)

CONTRACT.md notes worktrees may not run pre-commit hooks, so
polecats are instructed to run `make lint && make test` manually
before every commit. There's no scaffolded `pre-commit` config to
make that one command, and a polecat skipping the step is
indistinguishable from one running it.

---

## 3. Recommendations

Each recommendation lists: tool, link, license (CONTRACT.md §16
compliance), what it catches, integration footprint, effort, and
known downsides.

### 3.1 Enable `gosec` via `golangci-lint`

- **Tool**: `gosec` — <https://github.com/securego/gosec>
- **License**: Apache-2.0 — **allowed** (§16).
- **Catches**: hardcoded creds, weak crypto, insecure TLS, command
  injection, path traversal, `math/rand` in security contexts. Maps
  almost 1:1 to CONTRACT.md §17 forbidden patterns.
- **Integration footprint**: add `gosec` to `linters.enable` in
  `.golangci.yml`. No new CI step. No new binary install — already
  bundled in `golangci-lint`.
- **Effort**: **S** (≤2h) — initial rule landed in one config edit,
  plus 1-2h to triage findings against existing code and either fix
  or `//nolint:gosec` with reason.
- **Downsides**: well-known false positives (most G104 "errors not
  checked" overlap with `errcheck`; G304 "file include via variable"
  fires on legitimate code paths). Recommend starting with `severity:
  medium` and `confidence: medium` to keep the signal/noise ratio
  acceptable.

### 3.2 Add `govulncheck` CI step

- **Tool**: `govulncheck` — <https://golang.org/x/vuln>
- **License**: BSD-3-Clause — **allowed** (§16).
- **Catches**: known CVEs in pinned dependencies. Unlike most SCA
  tools, it does **call-graph analysis** — only flags vulnerabilities
  the code actually reaches, drastically reducing false positives.
- **Integration footprint**: one CI step:
  `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
  (or pinned version). Add to `pr.yml` and `main.yml`.
- **Effort**: **S** — single workflow step.
- **Downsides**: only covers the official Go vuln database (good
  coverage for stdlib and major modules; thinner for niche
  dependencies). Not a substitute for SBOM-driven scanning if/when
  that becomes a need.

### 3.3 Add `make test-cover` + coverage step in CI

- **Tool**: stdlib `go test -coverprofile`, `go tool cover` (no
  external dep). Optionally `gocover-cobertura` for GHA reporting.
- **License**: stdlib (BSD-3-Clause); `gocover-cobertura` is
  Apache-2.0 — **allowed** (§16).
- **Catches**: regression visibility. Not a quality bar by itself,
  but enables informed code review.
- **Integration footprint**:
  - `Makefile`: add `test-cover` target invoking
    `go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out -o coverage.html`.
  - CI: run `make test-cover` and upload `coverage.out` as a build
    artifact (no third-party service required for v1; can later
    integrate Codecov if desired).
  - **Do NOT** add a hard threshold gate in v1 — CONTRACT.md §9 is
    explicit about not mandating a percentage.
- **Effort**: **S** — Makefile target + 2 CI lines.
- **Downsides**: coverage % is a famously misleading metric in
  isolation; the artifact is for reviewer awareness, not a gate.

### 3.4 Add `-race` to unit test target

- **Tool**: stdlib (`go test -race`).
- **License**: stdlib — **allowed**.
- **Catches**: data races on shared state. Particularly relevant
  given the HTTP server, pgxpool, and middleware-shared maps.
- **Integration footprint**: change `make test` (or add `make
  test-race`) to pass `-race`. Or add a dedicated CI step. Race
  detector roughly doubles test runtime and triples memory; given
  the unit-test suite is small, the cost is negligible.
- **Effort**: **S** — one Make flag.
- **Downsides**: cgo not required for race detection on Go 1.21+;
  no impact on `CGO_ENABLED=0` build. The `-race` flag is only valid
  in tests, not in the production binary build (no risk of leaking
  into prod). Cost is small but real on slow runners.

### 3.5 Add `errorlint` via `golangci-lint`

- **Tool**: `errorlint` — <https://github.com/polyfloyd/go-errorlint>
- **License**: MIT — **allowed**.
- **Catches**: `err == sentinel` instead of `errors.Is`, type
  assertions on errors instead of `errors.As`, `%v` on errors
  instead of `%w`, switching on error types without unwrap.
- **Integration footprint**: add `errorlint` to `linters.enable`.
  Bundled in `golangci-lint`.
- **Effort**: **S** — config edit + small fix-up pass.
- **Downsides**: noisier on legacy code; minimal impact here given
  the codebase is young.

### 3.6 Replace `gofmt` check with `goimports`

- **Tool**: `goimports` — <https://pkg.go.dev/golang.org/x/tools/cmd/goimports>
  (also reachable via `golangci-lint`'s `goimports` linter).
- **License**: BSD-3-Clause — **allowed**.
- **Catches**: out-of-order imports; missing/unused imports
  (auto-removable); incorrect grouping (stdlib / third-party / local).
- **Integration footprint**: add `goimports` to `linters.enable`
  in `.golangci.yml` with the project's module path as `local-prefixes`
  (so `github.com/dickeyfPersonalProjects/minerals/...` groups
  separately). Optionally rename `make fmt` to invoke `goimports
  -w` instead of `gofmt -w`.
- **Effort**: **S** — config + one-time `goimports -w` pass.
- **Downsides**: minor; `goimports` is a strict superset of `gofmt`
  for this purpose.

### 3.7 Add `go-licenses` CI gate

- **Tool**: `go-licenses` — <https://github.com/google/go-licenses>
- **License**: Apache-2.0 — **allowed**.
- **Catches**: any direct or transitive dependency on a license
  outside the §16 allowlist. Outputs CSV/JSON suitable for review.
- **Integration footprint**: CI step running `go-licenses check` with
  the §16 allowlist as `--allowed_licenses`. Fails the job on a
  forbidden license.
- **Effort**: **M** (½–1 day) — initial config + reconciling any
  unidentified-license dependencies (some Go modules ship without a
  SPDX-recognized LICENSE file and need a manual override mapping
  via `--licenses_csv` or the equivalent override config).
- **Downsides**: "unknown license" is treated as forbidden by
  default, which is correct but generates noise from poorly-tagged
  modules until overrides are filled in. Initial setup is the
  expensive part; steady-state cost is ~5s per CI run.

### 3.8 Add `sloglint` via `golangci-lint`

- **Tool**: `sloglint` — <https://github.com/go-simpler/sloglint>
- **License**: MIT — **allowed**.
- **Catches**: malformed `slog` calls (odd number of args, missing
  keys, mixed kv/attr style). Configurable rule set covers the
  common footguns called out in CONTRACT.md §14.
- **Integration footprint**: add `sloglint` to `linters.enable`.
  Bundled in `golangci-lint`. Configure `kv-only` or `attr-only` per
  team preference.
- **Effort**: **S** — config edit.
- **Downsides**: opinionated; requires a kv-vs-attr decision. The
  `kv-only` mode (string-key-then-value pairs) matches what most of
  the existing logging in `internal/api/middleware.go` uses.

### 3.9 Add `depguard` rules via `golangci-lint`

- **Tool**: `depguard` — <https://github.com/OpenPeeDeeP/depguard>
- **License**: BSD-3-Clause — **allowed**.
- **Catches**: forbidden imports per package. Can encode rules like
  "`internal/auth/` and `internal/api/` MUST NOT import `math/rand`"
  or "no package imports `database/sql` directly — must go through
  `pgx`."
- **Integration footprint**: add `depguard` to `linters.enable` and
  declare rules in `.golangci.yml`. Bundled in `golangci-lint`.
- **Effort**: **M** — translating CONTRACT.md §17 import-related
  rules into `depguard` patterns is the work. Rules are then
  permanent.
- **Downsides**: `depguard` config syntax is verbose; rule scope is
  by file path glob, which can be brittle if the package layout
  shifts.

### 3.10 Add `gocritic`, `revive`, `misspell`, `prealloc`

These are smaller wins, recommended as a single follow-up bead so
the noise-tuning happens once.

- **`gocritic`** (MIT) — broad set of style/perf/diagnostic checks;
  `opinionated` tag is opt-in, default tag is sane.
- **`revive`** (MIT) — fast, configurable replacement for the
  deprecated `golint`. Catches `exported-without-doc`,
  `unused-parameter`, etc.
- **`misspell`** (MIT) — typos in comments/identifiers/strings.
  Cheap, one config flag.
- **`prealloc`** (MIT) — flags appendable slices that could be
  preallocated. Mild perf wins, easy fixes.
- **License**: all MIT — **allowed**.
- **Integration footprint**: enable in `.golangci.yml`. All bundled.
- **Effort**: **M** (combined) — most of the cost is triaging the
  initial wave of findings.
- **Downsides**: `gocritic` and `revive` overlap with each other and
  with `staticcheck` — expect to spend ½ day disabling redundant
  checks to keep signal high.

### 3.11 Native `go test` fuzzing for parsers

- **Tool**: stdlib `testing.F` fuzz tests (Go 1.18+).
- **License**: stdlib — **allowed**.
- **Catches**: panics, infinite loops, sanitizer bypass on attacker-
  shaped input. Best targets in this codebase:
  - `internal/markdown/markdown.go` — markdown→HTML→sanitize pipeline.
    A fuzz test that asserts the sanitizer never emits forbidden
    elements is high-value (CONTRACT.md §17.1 calls out this pipeline
    as security-critical).
  - `internal/storage/exif/exif.go` — EXIF parsing on attacker-
    controlled bytes. Already has unit tests; corpus-based fuzzing
    would expand coverage.
  - `internal/db/cursor.go` — pagination cursor decode.
- **Integration footprint**: add `Fuzz*` functions next to existing
  tests; run with `go test -run=^$ -fuzz=. -fuzztime=30s` in a CI
  job (separate from the main test job since fuzz time isn't bounded
  by suite size).
- **Effort**: **M-L** — writing the harnesses + a property to assert
  is the work. Each one is ~½ day if you also seed a corpus.
- **Downsides**: fuzz CI is awkward — short fuzz times in PRs are
  mostly ceremonial; meaningful fuzzing wants minutes, ideally on a
  cron. v1 worth: low. Defer until a security incident or a parser
  rewrite makes it warranted.

### 3.12 Add a local pre-commit / pre-push helper

- **Tool**: `pre-commit` — <https://github.com/pre-commit/pre-commit>
  (Python framework; manages hook installation per-clone). Or
  `lefthook` — <https://github.com/evilmartians/lefthook> (Go-native,
  no Python dep).
- **License**: MIT (both) — **allowed**.
- **Catches**: developer skipping `make lint && make test` before
  commit. Closes the worktree-skips-hooks gap CONTRACT mentions.
- **Integration footprint**: one config file (`.pre-commit-config.yaml`
  or `lefthook.yml`) + a one-liner install command. Hooks call
  existing Make targets — no new tooling.
- **Effort**: **S** — config file + README note.
- **Downsides**: opt-in per developer (each clone runs the install
  step once); doesn't help in CI (CI already runs the gates). v1
  recommendation: `lefthook` over `pre-commit`, since it doesn't
  drag in a Python toolchain on a Go project.

### 3.13 Add CodeQL workflow (GitHub-native SAST)

- **Tool**: `github/codeql-action` — <https://github.com/github/codeql-action>
- **License**: MIT (action) — **allowed**. The CodeQL CLI itself is
  under the **CodeQL Terms and Conditions** (free for public repos
  and "Advanced Security" contexts), which is **not** OSI-approved.
  See **Downsides** — this means CodeQL is **not** unambiguously
  §16-compliant.
- **Catches**: deeper SAST than `gosec` — taint flow, source-to-sink
  reachability, broad CWE coverage. Findings appear in the GitHub
  Security tab.
- **Integration footprint**: one workflow file
  (`.github/workflows/codeql.yml`) generated by GitHub's UI.
- **Effort**: **S** (workflow itself) but **review-blocked** until the
  license question is resolved.
- **Downsides**: CodeQL is "free for public repos" but the engine
  license is GitHub's own terms, not an OSI license. **Recommend
  deferring** until the license question has been explicitly
  resolved by the project owner. If `gosec` + `govulncheck` already
  cover the threat surface, the marginal value of CodeQL is small.
  Listed here only because it's the obvious "what about CodeQL?"
  question; the answer in this project's license posture is
  "probably no, at least not by default."

---

## 4. Prioritized list

The top recommendations, in order of value-per-effort:

1. **`gosec` (3.1)** — direct mechanical enforcement of CONTRACT.md
   §17. **S effort, high value.**
2. **`govulncheck` (3.2)** — catches known CVEs with near-zero cost.
   **S effort, high value.**
3. **`make test-cover` + coverage artifact (3.3)** — fixes the
   broken CONTRACT.md cross-reference and gives reviewers signal.
   **S effort, medium value.**
4. **`-race` on unit tests (3.4)** — cheap insurance against
   concurrency bugs. **S effort, medium value.**
5. **`errorlint` + `goimports` + `sloglint` (3.5, 3.6, 3.8)** —
   bundled-in linters that match how the codebase already wants to
   be written. **Combined S-M effort, medium value.**
6. **`go-licenses` (3.7)** — automates the §16 allowlist.
   **M effort (initial), high value (long-term).**

Items 7+ (`depguard`, the small linters batch, fuzz, pre-commit,
CodeQL) are nice-to-haves; recommend deferring until the items
above have landed.

---

## 5. Out of scope (deliberately not recommended)

### 5.1 Mutation testing (e.g. `gremlins`, `go-mutesting`)

Real value but heavy operationally — full-suite mutation runs are
minutes-to-hours, and the noise/signal ratio at a young project is
poor. Defer until coverage is well-understood (post-3.3) and the
team has bandwidth for the triage cost.

### 5.2 `gocyclo` / `cyclop` (cyclomatic complexity caps)

Opinionated thresholds tend to cause drive-by refactors that don't
improve the code. Skip unless a specific pain point emerges.

### 5.3 `dupl` (duplicate code detection)

Historically noisy on Go because struct/method layouts produce
near-duplicate detection on legitimate patterns (e.g. repository
methods).

### 5.4 `golines` (line-length formatter)

`gofmt` deliberately doesn't enforce line length; `golines` does.
Imposes mechanical line breaks that often hurt readability. Skip.

### 5.5 Vendoring (§16 explicitly defers it)

Not a quality tool but sometimes proposed alongside dependency
audits. CONTRACT.md §16 already addresses this. Skip.

### 5.6 Renovate / Dependabot

Useful, but §16 explicitly defers to manual updates in v1. Re-open
if/when manual updates become toil.

### 5.7 `copyloopvar`

Caught the pre-Go-1.22 loop-variable footgun. Go 1.22+ changed loop
variable semantics, and the project is on **Go 1.25** (`go.mod`).
Effectively obsolete here.

### 5.8 SBOM generation (`syft`, `cyclonedx-gomod`)

Useful for downstream consumers / supply-chain compliance regimes.
This is a single-operator project with Cloudflare-protected ingress,
not a SaaS shipping software to third parties. Re-open if/when
distribution requirements change.

### 5.9 CodeQL (covered as 3.13)

Listed there with the license caveat. Out of scope for landing in v1
absent an explicit license decision.

---

## Appendix — verification notes

Each tool listed above was verified to exist and to ship under the
license stated, against publicly-known information as of the date
this document was written. Tools bundled inside `golangci-lint` (which
is already in §16's pre-approved list) are governed by their own
upstream licenses, which I have called out individually for each
recommendation. A polecat landing any of these in a follow-up bead
should re-verify the license and the upstream maintenance signal
(last release, open issue volume) at landing time — that's the
minimum-viable due diligence per CONTRACT.md §16's "When to add a
library" rule.
