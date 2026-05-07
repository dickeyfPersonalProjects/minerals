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

// specStubPhotoRepo, specStubFileRepo, specStubStorage are never-called
// stand-ins so the type-derived OpenAPI spec advertises the photos
// routes during codegen (mi-jpu / B-3).
type specStubPhotoRepo struct{}

func (specStubPhotoRepo) Create(context.Context, domain.Tx, domain.Photo) error {
	return domain.ErrPhotoNotFound
}
func (specStubPhotoRepo) GetByID(context.Context, uuid.UUID) (domain.Photo, error) {
	return domain.Photo{}, domain.ErrPhotoNotFound
}
func (specStubPhotoRepo) Update(context.Context, domain.Tx, domain.Photo) error {
	return domain.ErrPhotoNotFound
}
func (specStubPhotoRepo) Delete(context.Context, domain.Tx, uuid.UUID) error {
	return domain.ErrPhotoNotFound
}
func (specStubPhotoRepo) ListBySpecimen(context.Context, uuid.UUID, domain.Page) ([]domain.Photo, domain.Cursor, error) {
	return nil, "", nil
}
func (specStubPhotoRepo) MaxPosition(context.Context, domain.Tx, uuid.UUID) (int, error) {
	return 0, nil
}

type specStubFileRepo struct{}

func (specStubFileRepo) Create(context.Context, domain.Tx, domain.File) error {
	return domain.ErrFileNotFound
}
func (specStubFileRepo) GetByID(context.Context, uuid.UUID) (domain.File, error) {
	return domain.File{}, domain.ErrFileNotFound
}
func (specStubFileRepo) Delete(context.Context, domain.Tx, uuid.UUID) error {
	return domain.ErrFileNotFound
}

type specStubStorage struct{}

func (specStubStorage) Upload(context.Context, string, io.Reader, string) error { return nil }
func (specStubStorage) UploadIfNotExists(context.Context, string, io.Reader, string) error {
	return nil
}
func (specStubStorage) Download(context.Context, string) (io.ReadCloser, http.Header, error) {
	return nil, nil, nil
}
func (specStubStorage) Delete(context.Context, string) error { return nil }

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

// specStubSpecimenRepo mirrors specStubCollectorRepo for specimens —
// a never-called stand-in so the spec advertises /api/v1/specimens
// routes during `make gen-api-client` codegen (mi-quf / B-2).
type specStubSpecimenRepo struct{}

func (specStubSpecimenRepo) Create(context.Context, domain.Tx, domain.Specimen) error {
	return domain.ErrSpecimenNotFound
}
func (specStubSpecimenRepo) GetByID(context.Context, uuid.UUID) (domain.Specimen, error) {
	return domain.Specimen{}, domain.ErrSpecimenNotFound
}
func (specStubSpecimenRepo) Update(context.Context, domain.Tx, domain.Specimen) error {
	return domain.ErrSpecimenNotFound
}
func (specStubSpecimenRepo) Delete(context.Context, domain.Tx, uuid.UUID) error {
	return domain.ErrSpecimenNotFound
}
func (specStubSpecimenRepo) List(context.Context, domain.SpecimenFilter, domain.Page) ([]domain.Specimen, domain.Cursor, error) {
	return nil, "", nil
}

// specStubJournalRepo is the never-called stand-in so the spec
// advertises /api/v1/journal and /api/v1/specimens/{id}/journal
// during `make gen-api-client` codegen (mi-y6b / C-1).
type specStubJournalRepo struct{}

func (specStubJournalRepo) Create(context.Context, domain.Tx, domain.JournalEntry) error {
	return domain.ErrJournalEntryNotFound
}
func (specStubJournalRepo) GetByID(context.Context, uuid.UUID) (domain.JournalEntry, error) {
	return domain.JournalEntry{}, domain.ErrJournalEntryNotFound
}
func (specStubJournalRepo) Update(context.Context, domain.Tx, domain.JournalEntry) error {
	return domain.ErrJournalEntryNotFound
}
func (specStubJournalRepo) Delete(context.Context, domain.Tx, uuid.UUID) error {
	return domain.ErrJournalEntryNotFound
}
func (specStubJournalRepo) ListBySpecimen(context.Context, uuid.UUID, domain.Page) ([]domain.JournalEntry, domain.Cursor, error) {
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

	handler := api.New(api.Deps{
		Collectors: specStubCollectorRepo{},
		Photos: &api.PhotoServiceDeps{
			Photos:         specStubPhotoRepo{},
			Files:          specStubFileRepo{},
			Storage:        specStubStorage{},
			MaxUploadBytes: 100 * 1024 * 1024,
			RunInTx: func(ctx context.Context, fn func(tx domain.Tx) error) error {
				return fn(nil)
			},
		},
		Specimens: specStubSpecimenRepo{},
		Journal: &api.JournalServiceDeps{
			Entries: specStubJournalRepo{},
		},
	})
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
