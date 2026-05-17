BINARY := minerals
PKG := ./cmd/minerals

# ── Tool versions (must match .github/workflows/{pr,main}.yml) ───────
# Pin tool versions so polecats and CI run identical gates. When CI
# updates a version, update these too — the auto-install logic below
# enforces the pin by reinstalling on mismatch.
GOLANGCI_LINT_VERSION ?= v2.12.2
GOTESTSUM_VERSION     ?= v1.13.0
GO_LICENSES_VERSION   ?= latest
GOVULNCHECK_VERSION   ?= latest

GOBIN := $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build run test test-cover fmt vet tidy clean fmt-frontend fmt-check-frontend lint-frontend

build:
	go build -o bin/$(BINARY) $(PKG)

run:
	go run $(PKG)

# Race detector + shuffled execution matches CI (.github/workflows/pr.yml
# `Unit tests` step). Gotestsum drives `go test` so we get the same JUnit
# artefact CI uploads; junit.xml is gitignored. Auto-installs gotestsum
# at the pinned version when missing.
test: install-gotestsum
	gotestsum --junitfile junit.xml -- -race -shuffle=on ./...

# Coverage with race detector. Outputs coverage.txt (atomic mode for
# safe accumulation across parallel goroutines) and an HTML report.
# Both files are gitignored — see .gitignore.
test-cover:
	go test -race -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/

# --- mi-bi6: backend skeleton + migrate/lint targets ----------------
.PHONY: lint fmt-check migrate-up migrate-down migrate-version migrate-create test-integration license-check vulncheck

lint: install-golangci-lint
	golangci-lint run

fmt-check:
	@diff -u <(echo -n) <(gofmt -l .) || (echo "gofmt diffs above; run 'make fmt'"; exit 1)

migrate-up:
	go run $(PKG) migrate up

# Usage: make migrate-down N=2 (default 1)
N ?= 1
migrate-down:
	go run $(PKG) migrate down $(N)

migrate-version:
	go run $(PKG) migrate version

# Usage: make migrate-create NAME=add_visibility_index
migrate-create:
	@if [ -z "$(NAME)" ]; then echo "NAME=... is required"; exit 1; fi
	go run $(PKG) migrate create NAME=$(NAME)

test-integration:
	go test -tags integration ./...

# Mechanical enforcement of the CONTRACT §16 license allowlist (mi-q7n).
# --ignore skips first-party packages (our own module has no LICENSE file at
# the repo root); their *dependencies* are still checked, which is the point.
# No upstream modules currently require an override; if a transitive dep ever
# ships without a SPDX-recognized LICENSE, add a documented entry here rather
# than widening --ignore.
LICENSE_ALLOWLIST := MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MPL-2.0,Unlicense,CC0-1.0
LICENSE_IGNORE := github.com/dickeyfPersonalProjects/minerals

license-check: install-go-licenses
	go-licenses check ./... \
		--allowed_licenses=$(LICENSE_ALLOWLIST) \
		--ignore=$(LICENSE_IGNORE)

# SCA against known CVEs in direct + transitive Go deps. Scoped to the
# reachable callgraph, so it's lower-noise than naive SBOM scanners
# (per mi-xql / Q-1 R3 / CONTRACT §16).
vulncheck: install-govulncheck
	govulncheck ./...

# ── Tool install targets ─────────────────────────────────────────────
# Each ensures the pinned version is present in $(GOBIN). Idempotent:
# silent when already correct. The pinned-version targets (golangci-lint,
# gotestsum) detect mismatches and reinstall; @latest targets only check
# for presence.
.PHONY: install-golangci-lint install-gotestsum install-go-licenses install-govulncheck tools

# Install all build/test tools at their pinned versions. Useful for a
# one-shot environment bootstrap.
tools: install-golangci-lint install-gotestsum install-go-licenses install-govulncheck

install-golangci-lint:
	@want="$(GOLANGCI_LINT_VERSION)"; \
	have=""; \
	if command -v golangci-lint >/dev/null 2>&1; then \
		have=$$(golangci-lint version 2>&1 | sed -n 's/.*has version v\{0,1\}\([0-9][0-9.]*\).*/v\1/p' | head -1); \
	fi; \
	if [ "$$have" != "$$want" ]; then \
		echo "Installing golangci-lint@$$want (have: $${have:-none})..."; \
		GOBIN="$(GOBIN)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$$want; \
	fi

install-gotestsum:
	@want="$(GOTESTSUM_VERSION)"; \
	have=""; \
	if command -v gotestsum >/dev/null 2>&1; then \
		have=$$(gotestsum --version 2>&1 | sed -n 's/.*gotestsum version v\{0,1\}\([0-9][0-9.]*\).*/v\1/p' | head -1); \
	fi; \
	if [ "$$have" != "$$want" ]; then \
		echo "Installing gotestsum@$$want (have: $${have:-none})..."; \
		GOBIN="$(GOBIN)" go install gotest.tools/gotestsum@$$want; \
	fi

install-go-licenses:
	@command -v go-licenses >/dev/null 2>&1 || { \
		echo "Installing go-licenses@$(GO_LICENSES_VERSION)..."; \
		GOBIN="$(GOBIN)" go install github.com/google/go-licenses@$(GO_LICENSES_VERSION); \
	}

install-govulncheck:
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "Installing govulncheck@$(GOVULNCHECK_VERSION)..."; \
		GOBIN="$(GOBIN)" go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	}

# ── Frontend (mi-p5h) ─────────────────────────────────────────────
.PHONY: test-frontend test-cover-frontend check-frontend install-frontend

# Idempotent npm dependency install — mirrors the pinned-tool pattern
# used for Go tools above. Runs `npm ci` only when node_modules is
# missing or older than package-lock.json. Closes the stale-deps
# footgun where `make ci-quick` would surface bogus "rule not found"
# errors after a package.json bump landed without a local reinstall
# (mi-zmg8 — eslint 9→10 upgrade added svelte/prefer-svelte-reactivity).
install-frontend:
	@if [ ! -f frontend/node_modules/.package-lock.json ] || \
	    [ frontend/package-lock.json -nt frontend/node_modules/.package-lock.json ]; then \
		echo "Installing frontend deps (npm ci)..."; \
		cd frontend && npm ci; \
	fi

fmt-frontend: install-frontend
	cd frontend && npx prettier --write .

fmt-check-frontend: install-frontend
	cd frontend && npx prettier --check .

lint-frontend: install-frontend
	cd frontend && npx eslint .

# Svelte/TypeScript typecheck — mirrors CI's `svelte-check (typecheck)`
# step (.github/workflows/pr.yml).
check-frontend: install-frontend
	cd frontend && npm run check

test-frontend: install-frontend
	cd frontend && npm test

test-cover-frontend: install-frontend
	cd frontend && npm run test:cover

# ── CI parity targets (mi-c0v) ────────────────────────────────────
# The two-tier model that lets polecats reproduce CI locally:
#
#   ci-quick  — 1:1 mirror of CI's Frontend + Backend lint/typecheck/test
#               jobs. Run before `gt done` / opening a PR. Skips only the
#               slow gates (vuln scan, license audit, coverage). Wired
#               into the pre-push hook via lefthook.
#
#   ci-local  — full parity with .github/workflows/pr.yml. Adds the slow
#               gates on top of ci-quick: govulncheck, SPDX license
#               audit, and frontend tests with coverage.
#
# Integration tests and compose-smoke are intentionally excluded — they
# need running Postgres/MinIO and aren't part of the gate "union" called
# out in the bead. Run `make test-integration` separately when needed.
.PHONY: ci-quick ci-local

ci-quick: fmt-check vet lint test fmt-check-frontend lint-frontend check-frontend
	@echo "✓ Quick gates passed"

ci-local: fmt-check vet lint license-check test vulncheck fmt-check-frontend lint-frontend check-frontend test-cover-frontend
	@echo "✓ All CI gates passed"

# ── API client codegen (mi-cy4) ───────────────────────────────────
# Dumps the type-derived OpenAPI spec from the in-process server and
# regenerates the typed frontend client at frontend/src/lib/api/.
# Generated files are committed (per CONTRACT.md §2: "generated but
# tracked"). Run after backend handler signatures change.
.PHONY: gen-api-client openapi-spec

OPENAPI_SPEC := frontend/src/lib/api/openapi.json

openapi-spec:
	@mkdir -p $(dir $(OPENAPI_SPEC))
	go run $(PKG) openapi > $(OPENAPI_SPEC)

gen-api-client: openapi-spec install-frontend
	cd frontend && npx openapi-typescript src/lib/api/openapi.json -o src/lib/api/schema.d.ts

# ── Compose lifecycle (mi-8ky) ────────────────────────────────────
# Two operating modes per CONTRACT §3:
#   compose-up   — full stack (postgres + minio + migrate + app on :8080),
#                  the standard onboarding flow.
#   compose-deps — deps only (postgres + minio); pair with `make run`
#                  and `cd frontend && npm run dev` for hot-reload dev.
.PHONY: compose-up compose-deps compose-down compose-down-v

compose-up:
	docker compose up -d

compose-deps:
	docker compose up -d postgres minio

compose-down:
	docker compose down

compose-down-v:
	docker compose down -v

# ── Local git hooks (mi-cyb) ──────────────────────────────────────
# Installs lefthook into $GOPATH/bin (or $GOBIN) if missing, then wires
# up the hooks defined in lefthook.yml. Opt-in per clone — CI runs the
# same gates independently, so skipping this only loses local
# convenience. See docs/quality/backend-code-quality.md §3.12.
.PHONY: hooks-install

hooks-install:
	@command -v lefthook >/dev/null 2>&1 || go install github.com/evilmartians/lefthook@latest
	lefthook install
