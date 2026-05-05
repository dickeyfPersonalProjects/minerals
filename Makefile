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
