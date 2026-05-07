package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// specStubCollectorRepo is a never-called stand-in so the type-derived
// OpenAPI spec advertises the collectors routes during codegen. The
// `openapi` subcommand only triggers spec marshalling — handlers
// are never dispatched against it.
type specStubCollectorRepo struct{}

func (specStubCollectorRepo) Create(context.Context, domain.Tx, domain.Collector) error {
	return domain.ErrCollectorNotFound
}
func (specStubCollectorRepo) GetByID(context.Context, uuid.UUID) (domain.Collector, error) {
	return domain.Collector{}, domain.ErrCollectorNotFound
}
func (specStubCollectorRepo) Update(context.Context, domain.Tx, domain.Collector) error {
	return domain.ErrCollectorNotFound
}
func (specStubCollectorRepo) Delete(context.Context, domain.Tx, uuid.UUID) error {
	return domain.ErrCollectorNotFound
}
func (specStubCollectorRepo) List(context.Context, domain.CollectorFilter, domain.Page) ([]domain.Collector, domain.Cursor, error) {
	return nil, "", nil
}

// runOpenAPI writes the type-derived OpenAPI spec served by the
// running server at /api/v1/openapi.json to stdout. The frontend
// codegen Makefile target consumes the output. Uses an in-process
// httptest recorder so the helper has no runtime dependencies on
// Postgres, MinIO, or migrations.
func runOpenAPI(args []string) error {
	if len(args) > 0 {
		return errors.New("openapi: takes no arguments")
	}

	// Silence the request log so stdout stays clean JSON.
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	handler := api.New(api.Deps{Collectors: specStubCollectorRepo{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		return fmt.Errorf("openapi: spec endpoint returned %d", rec.Code)
	}
	if _, err := os.Stdout.Write(rec.Body.Bytes()); err != nil {
		return fmt.Errorf("openapi: write stdout: %w", err)
	}
	if _, err := os.Stdout.Write([]byte("\n")); err != nil {
		return fmt.Errorf("openapi: write stdout: %w", err)
	}
	return nil
}
