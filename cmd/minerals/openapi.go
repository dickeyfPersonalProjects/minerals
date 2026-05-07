package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
)

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

	handler := api.New(api.Deps{})
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
