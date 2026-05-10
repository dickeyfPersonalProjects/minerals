BINARY := minerals
PKG := ./cmd/minerals

.PHONY: build run test fmt vet tidy clean fmt-frontend fmt-check-frontend lint-frontend

build:
	go build -o bin/$(BINARY) $(PKG)

run:
	go run $(PKG)

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/

# --- mi-bi6: backend skeleton + migrate/lint targets ----------------
.PHONY: lint fmt-check migrate-up migrate-down migrate-version migrate-create test-integration

lint:
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

# ── Frontend (mi-p5h) ─────────────────────────────────────────────
.PHONY: test-frontend test-cover-frontend

fmt-frontend:
	cd frontend && npx prettier --write .

fmt-check-frontend:
	cd frontend && npx prettier --check .

lint-frontend:
	cd frontend && npx eslint .

test-frontend:
	cd frontend && npm test

test-cover-frontend:
	cd frontend && npm run test:cover

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

gen-api-client: openapi-spec
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
