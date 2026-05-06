BINARY := minerals
PKG := ./cmd/minerals

.PHONY: build run test fmt vet tidy clean

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
