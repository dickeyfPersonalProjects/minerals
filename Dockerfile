# Multi-stage image per design §7.4 (docs/design/07-build-embed-observability.md).
# Three stages: Node builds the SPA, Go embeds dist/ + compiles the binary,
# distroless static nonroot runs it. Used for both ghcr publishing (CI) and
# local `docker compose up -d` (CONTRACT §3 standard onboarding mode).

# Stage 1: build the SPA
# Image refs are fully qualified (docker.io/library/...) so the build
# works under podman too — podman requires explicit registries unless
# /etc/containers/registries.conf has unqualified-search-registries set.
FROM docker.io/library/node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: build the Go binary, embedding dist/
FROM docker.io/library/golang:1.25-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/frontend/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/minerals \
    ./cmd/minerals

# Stage 3: distroless runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /out/minerals /minerals
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/minerals"]
CMD ["serve"]
