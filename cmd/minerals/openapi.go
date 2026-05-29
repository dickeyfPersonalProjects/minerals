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
	"time"

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
func (specStubSpecimenRepo) HasPhotoWithFile(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
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

// specStubJournalAttachmentRepo is the never-called stand-in so the
// spec advertises the journal-files routes during codegen
// (mi-720 / C-2).
type specStubJournalAttachmentRepo struct{}

func (specStubJournalAttachmentRepo) Create(context.Context, domain.Tx, domain.JournalEntryFile) error {
	return domain.ErrJournalAttachmentNotFound
}
func (specStubJournalAttachmentRepo) GetByFileID(context.Context, uuid.UUID) (domain.JournalEntryFile, error) {
	return domain.JournalEntryFile{}, domain.ErrJournalAttachmentNotFound
}
func (specStubJournalAttachmentRepo) ListByEntry(context.Context, uuid.UUID) ([]domain.JournalEntryFile, error) {
	return nil, nil
}
func (specStubJournalAttachmentRepo) Delete(context.Context, domain.Tx, uuid.UUID) error {
	return domain.ErrJournalAttachmentNotFound
}
func (specStubJournalAttachmentRepo) MaxPosition(context.Context, domain.Tx, uuid.UUID) (int, error) {
	return 0, nil
}

// specStubMineralSpeciesRepo is a never-called stand-in so the
// type-derived OpenAPI spec advertises /api/v1/mineral-species
// during codegen (mi-dtg / F-1).
type specStubMineralSpeciesRepo struct{}

func (specStubMineralSpeciesRepo) Create(context.Context, domain.Tx, domain.MineralSpecies) error {
	return domain.ErrMineralSpeciesNotFound
}
func (specStubMineralSpeciesRepo) GetByID(context.Context, uuid.UUID) (domain.MineralSpecies, error) {
	return domain.MineralSpecies{}, domain.ErrMineralSpeciesNotFound
}
func (specStubMineralSpeciesRepo) FindByName(context.Context, string) ([]domain.MineralSpecies, error) {
	return nil, nil
}
func (specStubMineralSpeciesRepo) FindByMindatID(context.Context, string) (domain.MineralSpecies, error) {
	return domain.MineralSpecies{}, domain.ErrMineralSpeciesNotFound
}

// specStubQRSheetRepo is a never-called stand-in so the
// type-derived OpenAPI spec advertises the qr-sheet routes during
// codegen (mi-c78.1 / mi-c78.2).
type specStubQRSheetRepo struct{}

func (specStubQRSheetRepo) GetByUser(context.Context, uuid.UUID) (domain.QRSheet, error) {
	return domain.QRSheet{}, domain.ErrQRSheetNotFound
}
func (specStubQRSheetRepo) Create(context.Context, domain.Tx, domain.QRSheet) error {
	return domain.ErrQRSheetNotFound
}
func (specStubQRSheetRepo) UpdateTemplate(context.Context, domain.Tx, uuid.UUID, domain.QRSheetTemplate, time.Time) error {
	return domain.ErrQRSheetNotFound
}
func (specStubQRSheetRepo) Delete(context.Context, domain.Tx, uuid.UUID) error {
	return domain.ErrQRSheetNotFound
}
func (specStubQRSheetRepo) AddSpecimen(context.Context, domain.Tx, uuid.UUID, uuid.UUID, time.Time) error {
	return domain.ErrQRSheetNotFound
}
func (specStubQRSheetRepo) RemoveSpecimen(context.Context, domain.Tx, uuid.UUID, uuid.UUID) error {
	return domain.ErrQRSheetNotFound
}
func (specStubQRSheetRepo) ListSpecimens(context.Context, uuid.UUID) ([]domain.QRSheetEntry, error) {
	return nil, nil
}

// specStubUserRepo is a never-called stand-in so the
// type-derived OpenAPI spec advertises /api/v1/profile during
// codegen (mi-2hf).
type specStubUserRepo struct{}

func (specStubUserRepo) GetBySub(context.Context, string) (domain.User, error) {
	return domain.User{}, domain.ErrUserNotFound
}
func (specStubUserRepo) GetByID(context.Context, uuid.UUID) (domain.User, error) {
	return domain.User{}, domain.ErrUserNotFound
}
func (specStubUserRepo) Create(context.Context, domain.Tx, domain.User) error {
	return domain.ErrUserNotFound
}
func (specStubUserRepo) MarkActive(context.Context, domain.Tx, uuid.UUID, string, time.Time) error {
	return domain.ErrUserNotFound
}
func (specStubUserRepo) UpdateDisplayName(context.Context, domain.Tx, uuid.UUID, string, time.Time) error {
	return domain.ErrUserNotFound
}
func (specStubUserRepo) UpdateFieldDefaults(context.Context, domain.Tx, uuid.UUID, *domain.FieldDefaults, time.Time) error {
	return domain.ErrUserNotFound
}

func (specStubUserRepo) UpdateDefaultSpecimenVisibility(context.Context, domain.Tx, uuid.UUID, *domain.Visibility, time.Time) error {
	return domain.ErrUserNotFound
}

func (specStubUserRepo) SetStatus(context.Context, domain.Tx, uuid.UUID, domain.UserStatus, time.Time) error {
	return domain.ErrUserNotFound
}

// specStubAccountEraser is a never-called stand-in so the type-derived
// OpenAPI spec advertises DELETE /api/v1/account during codegen
// (mi-nwg5).
type specStubAccountEraser struct{}

func (specStubAccountEraser) Erase(context.Context, uuid.UUID) (domain.AccountErasure, error) {
	return domain.AccountErasure{}, domain.ErrUserNotFound
}

// specStubSpecimenCollectorRepo is a never-called stand-in so the
// type-derived OpenAPI spec advertises the chain routes during
// codegen (mi-zv3 / C-3).
type specStubSpecimenCollectorRepo struct{}

func (specStubSpecimenCollectorRepo) GetChain(context.Context, domain.Tx, uuid.UUID) ([]domain.SpecimenCollectorLink, error) {
	return nil, nil
}
func (specStubSpecimenCollectorRepo) ReplaceChain(context.Context, domain.Tx, uuid.UUID, []uuid.UUID) error {
	return nil
}

// specStubSettingsRepo is a never-called stand-in so the type-derived
// OpenAPI spec advertises the GET/PUT /api/v1/admin/registration routes
// during codegen (mi-pkn2).
type specStubSettingsRepo struct{}

func (specStubSettingsRepo) RegistrationEnabled(context.Context) (bool, bool, error) {
	return false, false, nil
}
func (specStubSettingsRepo) SetRegistrationEnabled(context.Context, bool, uuid.UUID) error {
	return nil
}

// specStubAdminRepo is a never-called stand-in so the type-derived
// OpenAPI spec advertises the admin see-all routes — /api/v1/admin/users
// and /api/v1/admin/published-content — during codegen (mi-n5av /
// mi-gtkp). The published-content feed is the surface the console's
// moderation panel (mi-jjzc) acts on, so the frontend client needs it
// typed.
type specStubAdminRepo struct{}

func (specStubAdminRepo) ListUsers(context.Context, domain.Page) ([]domain.AdminUser, domain.Cursor, error) {
	return nil, "", nil
}
func (specStubAdminRepo) ListPublishedContent(context.Context, domain.Page) ([]domain.AdminContent, domain.Cursor, error) {
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
	slog.SetDefault(slog.New(slog.DiscardHandler))

	handler := api.New(api.Deps{
		Collectors: specStubCollectorRepo{},
		Photos: &api.PhotoServiceDeps{
			Photos:         specStubPhotoRepo{},
			Files:          specStubFileRepo{},
			Storage:        specStubStorage{},
			MaxUploadBytes: 100 * 1024 * 1024,
			RunInTx: func(_ context.Context, fn func(tx domain.Tx) error) error {
				return fn(nil)
			},
		},
		Specimens: specStubSpecimenRepo{},
		Journal: &api.JournalServiceDeps{
			Entries: specStubJournalRepo{},
		},
		SpecimenCollectors: specStubSpecimenCollectorRepo{},
		MineralSpecies: &api.MineralSpeciesServiceDeps{
			Repo: specStubMineralSpeciesRepo{},
		},
		QRSheets: specStubQRSheetRepo{},
		Account:  &api.AccountServiceDeps{Eraser: specStubAccountEraser{}},
		Users:    specStubUserRepo{},
		Admin:    specStubAdminRepo{},
		Settings: specStubSettingsRepo{},
		AdminSuspend: &api.AdminSuspendDeps{
			Users: specStubUserRepo{},
		},
		JournalFiles: &api.JournalFileServiceDeps{
			Entries:        specStubJournalRepo{},
			Attachments:    specStubJournalAttachmentRepo{},
			Files:          specStubFileRepo{},
			Storage:        specStubStorage{},
			MaxUploadBytes: 100 * 1024 * 1024,
			RunInTx: func(_ context.Context, fn func(tx domain.Tx) error) error {
				return fn(nil)
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/openapi.json", nil)
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
