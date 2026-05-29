// POST /api/v1/import — restore a user's collection from a portable
// export archive (mi-dkuu.2). The endpoint is the re-homing inverse of
// the export: it validates an uploaded ZIP (schema version, structural
// integrity, file-binary hashes, intra-archive referential integrity),
// then — unless ?dryRun=true — recreates every entity owned by the
// caller in one transaction, regenerating all IDs and rewriting the
// cross-references, and uploads the file binaries best-effort. The
// archive format and the two-phase engine live in internal/portability.
package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/portability"
)

// ImportServiceDeps wires POST /api/v1/import. Every repo is the
// consumer-side domain interface (so tests pass in-memory fakes);
// Storage, RunInTx, and CatalogNumbers complete the import engine.
// MaxUploadBytes bounds the uploaded archive. The endpoint is registered
// only when the required collaborators are present.
type ImportServiceDeps struct {
	Collectors         domain.CollectorRepo
	Files              domain.FileRepo
	Specimens          domain.SpecimenRepo
	Photos             domain.PhotoRepo
	Journal            domain.JournalEntryRepo
	JournalFiles       domain.JournalEntryFileRepo
	SpecimenCollectors domain.SpecimenCollectorRepo
	QRSheets           domain.QRSheetRepo
	Storage            portability.ObjectStore
	RunInTx            TxRunner
	// CatalogNumbers reports the importer's existing catalog numbers so
	// the engine can resolve collisions before the commit. Optional.
	CatalogNumbers portability.CatalogLookup
	MaxUploadBytes int64
}

// ready reports whether the deps carry everything the import engine
// needs to run. A missing required collaborator leaves the route
// unregistered (matching the nil-deps pattern of the other services).
func (d *ImportServiceDeps) ready() bool {
	return d != nil &&
		d.Collectors != nil && d.Files != nil && d.Specimens != nil &&
		d.Photos != nil && d.Journal != nil && d.JournalFiles != nil &&
		d.SpecimenCollectors != nil && d.QRSheets != nil &&
		d.Storage != nil && d.RunInTx != nil
}

// importService backs the import endpoint.
type importService struct {
	importer       *portability.Importer
	maxUploadBytes int64
}

// importForm is the multipart body: a single `file` field carrying the
// export ZIP.
type importForm struct {
	// No contentType constraint: browsers and multipart clients often
	// send a ZIP as application/octet-stream. The engine validates the
	// payload authoritatively by parsing the ZIP and its manifest, so a
	// declared MIME type here would only reject legitimate uploads.
	File huma.FormFile `form:"file" required:"true" doc:"The minerals export ZIP archive to import."`
}

// importInput is the POST /api/v1/import request: a multipart upload plus
// the dryRun query toggle.
type importInput struct {
	DryRun  bool `query:"dryRun" doc:"When true, validate the archive and return the report without writing anything (design §4.1)."`
	RawBody huma.MultipartFormFiles[importForm]
}

// importOutput carries the import report (counts, conflicts, warnings,
// and any best-effort image-upload failures).
type importOutput struct {
	Body portability.Report
}

// registerImportOperations registers POST /api/v1/import when the deps
// are fully wired. Uses the Protected() chain — only an active account
// can import into its own collection.
func registerImportOperations(api huma.API, mws authMiddlewares, deps *ImportServiceDeps) {
	if !deps.ready() {
		return
	}
	if deps.MaxUploadBytes <= 0 {
		deps.MaxUploadBytes = 100 * 1024 * 1024
	}
	s := &importService{
		maxUploadBytes: deps.MaxUploadBytes,
		importer: portability.NewImporter(portability.Deps{
			Collectors:         deps.Collectors,
			Files:              deps.Files,
			Specimens:          deps.Specimens,
			Photos:             deps.Photos,
			Journal:            deps.Journal,
			JournalFiles:       deps.JournalFiles,
			SpecimenCollectors: deps.SpecimenCollectors,
			QRSheets:           deps.QRSheets,
			Storage:            deps.Storage,
			RunInTx:            func(ctx context.Context, fn func(tx domain.Tx) error) error { return deps.RunInTx(ctx, fn) },
			CatalogNumbers:     deps.CatalogNumbers,
		}),
	}

	huma.Register(api, huma.Operation{
		OperationID: "import-user-data",
		Method:      http.MethodPost,
		Path:        "/api/v1/import",
		Summary:     "Import a collection from an export archive",
		Description: "Multipart upload (`file` form field) of a minerals export ZIP. Two-phase: the archive is always validated first (schema version, structural integrity, file-binary SHA-256, intra-archive referential integrity); with `?dryRun=true` the validation report is returned and nothing is written. On commit every entity is recreated owned by the caller in a single transaction with regenerated IDs, catalog-number collisions are imported as new specimens with a suffixed number (existing rows are never modified), and file binaries are uploaded best-effort (failures are listed for retry). Returns the import report.",
		Tags:        []string{"account"},
		Errors: []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusRequestEntityTooLarge,
			http.StatusUnprocessableEntity,
			http.StatusInternalServerError,
		},
		Middlewares: append(huma.Middlewares{s.maxBytesMiddleware}, mws.Protected()...),
	}, s.doImport)
}

// maxBytesMiddleware caps the request body so an oversized archive fails
// with 413 before the handler buffers it.
func (s *importService) maxBytesMiddleware(ctx huma.Context, next func(huma.Context)) {
	r, w := humago.Unwrap(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadBytes)
	next(ctx)
}

func (s *importService) doImport(ctx context.Context, in *importInput) (*importOutput, error) {
	u := auth.FromContext(ctx)
	if u.Sub == "" || u.ID == uuid.Nil {
		// Defensive — the Protected() chain should already 401 first.
		return nil, newAPIError(http.StatusUnauthorized, "unauthorized", "authentication required", nil)
	}

	// huma has already spilled the multipart body to /tmp by the time we
	// run; schedule cleanup of the on-disk form first thing so every
	// return path unlinks it (matching the photo-upload hygiene).
	form := in.RawBody.Data()
	defer func() {
		if in.RawBody.Form != nil {
			_ = in.RawBody.Form.RemoveAll()
		}
	}()
	defer func() { _ = form.File.Close() }()

	raw, err := io.ReadAll(form.File.File)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, newAPIError(http.StatusRequestEntityTooLarge, "payload_too_large",
				"the uploaded archive exceeds the configured size limit",
				map[string]any{"max_bytes": mbe.Limit})
		}
		return nil, newAPIError(http.StatusBadRequest, "bad_request", "failed to read the uploaded archive", nil)
	}

	report, err := s.importer.Run(ctx, raw, in.DryRun)
	if err != nil {
		var ve *portability.ValidationError
		if errors.As(err, &ve) {
			return nil, newAPIError(validationStatus(ve.Code), ve.Code, ve.Message, validationDetails(ve))
		}
		slog.ErrorContext(ctx, "import: commit failed", "user_id", u.ID, "err", err)
		return nil, newAPIError(http.StatusInternalServerError, "internal_error", "import failed", nil)
	}

	slog.InfoContext(ctx, "user data import",
		"user_id", u.ID, "dry_run", report.DryRun, "committed", report.Committed,
		"specimens", report.Counts.Specimens, "photos", report.Counts.Photos,
		"collectors", report.Counts.Collectors, "journal_entries", report.Counts.JournalEntries,
		"conflicts", len(report.Conflicts), "image_failures", len(report.ImageFailures),
	)
	return &importOutput{Body: *report}, nil
}

// validationStatus maps a portability validation code to its HTTP status.
// A malformed container (unparseable upload) is a 400; everything else is
// a well-formed-but-rejectable archive → 422.
func validationStatus(code string) int {
	if code == portability.CodeMalformedArchive {
		return http.StatusBadRequest
	}
	return http.StatusUnprocessableEntity
}

// validationDetails surfaces the per-problem list (if any) under a
// stable details key.
func validationDetails(ve *portability.ValidationError) map[string]any {
	if len(ve.Details) == 0 {
		return nil
	}
	return map[string]any{"problems": ve.Details}
}
