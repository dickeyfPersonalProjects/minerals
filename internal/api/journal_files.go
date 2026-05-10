// Journal entry attachments HTTP surface (mi-720 / C-2). Implements
// §12 file upload + download for non-image content attached to
// journal entries:
//
//	POST   /api/v1/journal/{id}/files          (multipart upload, single file)
//	GET    /api/v1/journal/{id}/files          (list entry's attachments)
//	DELETE /api/v1/journal-files/{file_id}     (remove attachment + file + object)
//	GET    /api/v1/files/{file_id}             (Go-proxied download of the original)
//
// Pipeline order mirrors photos but skips variant generation: reject
// early on Content-Type allowlist + size cap, read body, SHA-256,
// MinIO conditional put, DB transaction (files + journal_entry_files),
// best-effort MinIO cleanup on DB failure.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage"
)

// allowedJournalContentTypes is the locked v1 allowlist for journal-
// entry attachment uploads (CONTRACT.md §12). Rationale:
//   - PDFs are the dominant non-image attachment for analytical
//     reports (XRD scans, lab certificates).
//   - Images are accepted but uploaded as-is — the photo-pipeline
//     variants are intentionally skipped (variants are for the
//     specimen gallery, not journal context).
//   - Plain text + CSV + Markdown cover field notes and lab data.
//   - JSON / XML support attaching machine-readable analysis
//     output.
//
// Anything else → 415 with details.allowed listing the set.
var allowedJournalContentTypes = []string{
	"application/pdf",
	"image/jpeg",
	"image/png",
	"image/webp",
	"text/plain",
	"text/csv",
	"text/markdown",
	"application/json",
	"application/xml",
}

// JournalFileStorage is the subset of *storage.Client the journal-
// files service uses. Defining it here keeps tests free of a real
// MinIO/S3 connection.
type JournalFileStorage interface {
	UploadIfNotExists(ctx context.Context, key string, body io.Reader, contentType string) error
	Download(ctx context.Context, key string) (io.ReadCloser, http.Header, error)
	Delete(ctx context.Context, key string) error
}

// JournalFileView is the wire shape of a journal attachment. It
// folds the journal_entry_files row + the underlying files row into
// one resource — the client never has to fetch them separately.
type JournalFileView struct {
	EntryID     uuid.UUID `json:"entry_id" doc:"Owning journal entry."`
	FileID      uuid.UUID `json:"file_id" doc:"UUID of the underlying files row; also the key suffix in object storage."`
	ContentType string    `json:"content_type" doc:"Content-Type stored on the underlying file."`
	ByteSize    int64     `json:"byte_size" doc:"Byte size of the stored file."`
	SHA256      string    `json:"sha256" doc:"Hex SHA-256 of the stored bytes."`
	Position    int       `json:"position" doc:"Manual ordering; 1-indexed within the entry's attachments."`
	CreatedAt   time.Time `json:"created_at" doc:"RFC 3339 attachment timestamp."`
}

func toJournalFileView(j domain.JournalEntryFile, f domain.File) JournalFileView {
	return JournalFileView{
		EntryID:     j.EntryID,
		FileID:      j.FileID,
		ContentType: f.ContentType,
		ByteSize:    f.ByteSize,
		SHA256:      f.SHA256,
		Position:    j.Position,
		CreatedAt:   j.CreatedAt,
	}
}

// JournalFileServiceDeps carries everything the journal-files handlers
// need. The service is registered when JournalFileServiceDeps is
// non-nil in api.Deps.
type JournalFileServiceDeps struct {
	Entries        domain.JournalEntryRepo
	Attachments    domain.JournalEntryFileRepo
	Files          domain.FileRepo
	Storage        JournalFileStorage
	RunInTx        TxRunner
	MaxUploadBytes int64
}

// JournalFileService wires huma operations against a
// JournalFileServiceDeps.
type JournalFileService struct {
	deps JournalFileServiceDeps
}

func registerJournalFileOperations(api huma.API, mux *http.ServeMux, deps *JournalFileServiceDeps) {
	if deps == nil {
		return
	}
	if deps.Entries == nil || deps.Attachments == nil || deps.Files == nil ||
		deps.Storage == nil || deps.RunInTx == nil {
		return
	}
	if deps.MaxUploadBytes <= 0 {
		// Belt-and-suspenders: a zero limit would silently mean "no
		// limit" via http.MaxBytesReader. Default to 100 MiB (the §15
		// default) when the caller forgot to wire it.
		deps.MaxUploadBytes = 100 * 1024 * 1024
	}

	s := &JournalFileService{deps: *deps}
	mws := huma.Middlewares{humaAuth}

	huma.Register(api, huma.Operation{
		OperationID:   "upload-journal-file",
		Method:        http.MethodPost,
		Path:          "/api/v1/journal/{id}/files",
		Summary:       "Attach a file to a journal entry",
		Description:   "Multipart upload (`file` form field). Single file per request. Allowlist-gated content types (CONTRACT.md §12), 100 MiB default cap, transactional MinIO + Postgres write. No image variants are generated for journal attachments — the photo-pipeline variants are specimen-gallery only.",
		Tags:          []string{"journal-files"},
		DefaultStatus: http.StatusCreated,
		Errors: []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusNotFound,
			http.StatusUnsupportedMediaType,
			http.StatusRequestEntityTooLarge,
			http.StatusInternalServerError,
		},
		Middlewares: append(huma.Middlewares{s.maxBytesMiddleware}, mws...),
	}, s.upload)

	huma.Register(api, huma.Operation{
		OperationID: "list-journal-files",
		Method:      http.MethodGet,
		Path:        "/api/v1/journal/{id}/files",
		Summary:     "List a journal entry's attachments",
		Description: "Returns attachments ordered by (position ASC, created_at ASC). v1 returns the full set in one response; pagination is deferred (entries have at most a handful of attachments in practice).",
		Tags:        []string{"journal-files"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.list)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-journal-file",
		Method:        http.MethodDelete,
		Path:          "/api/v1/journal-files/{file_id}",
		Summary:       "Remove an attachment from a journal entry",
		Description:   "Deletes the journal_entry_files join row, the files row, and the MinIO object (best-effort). The DB transaction is the source of truth; if the MinIO delete fails the orphan-cleanup job picks it up later (deferred for v1).",
		Tags:          []string{"journal-files"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares:   mws,
	}, s.delete)

	// Download endpoint streams raw bytes — bypass huma's
	// JSON-by-default response shaping by registering directly on
	// the mux. Auth is enforced via the Chain wrapper because the
	// path wins over the catch-all /api/v1/.
	registerJournalFileDownloadRoute(mux, s)
}

// maxBytesMiddleware wraps the request body in http.MaxBytesReader so
// uploads that exceed s.deps.MaxUploadBytes fail with 413 before the
// handler reads anything (per CONTRACT.md §12).
func (s *JournalFileService) maxBytesMiddleware(ctx huma.Context, next func(huma.Context)) {
	r, w := humago.Unwrap(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, s.deps.MaxUploadBytes)
	next(ctx)
}

type uploadJournalFileForm struct {
	File huma.FormFile `form:"file" required:"true" doc:"The file to attach. Max size MAX_UPLOAD_BYTES (default 100 MiB)."`
}

type uploadJournalFileInput struct {
	EntryID string `path:"id" doc:"Journal entry UUID."`
	RawBody huma.MultipartFormFiles[uploadJournalFileForm]
}

type uploadJournalFileOutput struct {
	Location string `header:"Location" doc:"URL of the underlying file resource (download via GET /api/v1/files/{file_id})."`
	Body     JournalFileView
}

func (s *JournalFileService) upload(ctx context.Context, in *uploadJournalFileInput) (*uploadJournalFileOutput, error) {
	// CONTRACT.md §17: huma decodes the multipart form before this
	// handler runs, so by the time we get here any body larger than
	// huma's MultipartMaxMemory (8 KiB default) has already spilled to
	// /tmp. Schedule cleanup first thing so every early return — UUID
	// parse, entry lookup, content-type rejection, oversize — unlinks
	// the tempfile. RemoveAll deletes the on-disk spillover;
	// File.Close releases the *os.File handle on top of it.
	// readOnlyRootFilesystem only permits writes to /tmp, and
	// "best-effort, but expected" cleanup is the §17 rule there.
	form := in.RawBody.Data()
	defer func() {
		if in.RawBody.Form != nil {
			_ = in.RawBody.Form.RemoveAll()
		}
	}()
	defer func() { _ = form.File.Close() }()

	entryID, err := parseUUID(in.EntryID, "id")
	if err != nil {
		return nil, err
	}

	// Verify the entry exists before doing any storage work. Without
	// this check the FK violation would only surface after we've
	// already written the original to MinIO and rolled back the DB
	// — wasteful when the caller used a stale entry id.
	if _, err := s.deps.Entries.GetByID(ctx, entryID); err != nil {
		if errors.Is(err, domain.ErrJournalEntryNotFound) {
			return nil, newAPIError(http.StatusNotFound, "journal_entry_not_found",
				"no such journal entry",
				map[string]any{"field": "id"})
		}
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to look up journal entry", nil)
	}

	innerCT := strings.ToLower(strings.TrimSpace(form.File.ContentType))
	if !isAllowedJournalContentType(innerCT) {
		return nil, newAPIError(http.StatusUnsupportedMediaType, "unsupported_media_type",
			"file content-type is not allowed",
			map[string]any{"allowed": allowedJournalContentTypes, "got": innerCT})
	}

	data, err := io.ReadAll(form.File.File)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, newAPIError(http.StatusRequestEntityTooLarge, "payload_too_large",
				"upload exceeds the configured size limit",
				map[string]any{"max_bytes": mbe.Limit})
		}
		return nil, newAPIError(http.StatusBadRequest, "bad_request",
			"failed to read upload body", nil)
	}

	sum := sha256.Sum256(data)
	sha256Hex := hex.EncodeToString(sum[:])

	fileID := domain.NewID()
	originalKey := "files/" + fileID.String()

	// MinIO write (per §12 step 6): only the original — no variants
	// for journal attachments. On any failure mid-flight, undo
	// successful writes before returning.
	written := []string{}
	cleanup := func() {
		for _, k := range written {
			if err := s.deps.Storage.Delete(ctx, k); err != nil {
				slog.ErrorContext(ctx, "minio cleanup failed", "key", k, "err", err)
			}
		}
	}

	if err := s.deps.Storage.UploadIfNotExists(ctx, originalKey, bytesReader(data), innerCT); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			return nil, newAPIError(http.StatusConflict, "file_id_collision",
				"object key collision; retry the upload", nil)
		}
		return nil, newAPIError(http.StatusInternalServerError, "storage_write_failed",
			"failed to write file to object storage", nil)
	}
	written = append(written, originalKey)

	// DB transaction: insert files + journal_entry_files atomically.
	// Rollback on any failure cleans up the just-written MinIO object.
	now := time.Now().UTC()
	file := domain.File{
		ID:          fileID,
		S3Key:       originalKey,
		ContentType: innerCT,
		ByteSize:    int64(len(data)),
		SHA256:      sha256Hex,
		UploadedBy:  auth.FromContext(ctx).ID,
		UploadedAt:  now,
	}
	attachment := domain.JournalEntryFile{
		EntryID:   entryID,
		FileID:    fileID,
		CreatedAt: now,
	}

	txErr := s.deps.RunInTx(ctx, func(tx domain.Tx) error {
		if err := s.deps.Files.Create(ctx, tx, file); err != nil {
			return err
		}
		max, err := s.deps.Attachments.MaxPosition(ctx, tx, entryID)
		if err != nil {
			return err
		}
		attachment.Position = max + 1
		return s.deps.Attachments.Create(ctx, tx, attachment)
	})
	if txErr != nil {
		cleanup()
		switch {
		case errors.Is(txErr, domain.ErrJournalEntryNotFound):
			return nil, newAPIError(http.StatusNotFound, "journal_entry_not_found",
				"no such journal entry",
				map[string]any{"field": "id"})
		case errors.Is(txErr, domain.ErrFileConflict):
			return nil, newAPIError(http.StatusConflict, "file_conflict",
				"file with that key already exists", nil)
		}
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to commit upload", nil)
	}

	out := &uploadJournalFileOutput{
		Location: "/api/v1/files/" + fileID.String(),
		Body:     toJournalFileView(attachment, file),
	}
	return out, nil
}

type listJournalFilesInput struct {
	EntryID string `path:"id" doc:"Journal entry UUID."`
}

type listJournalFilesOutput struct {
	Body journalFileListBody
}

type journalFileListBody struct {
	Items []JournalFileView `json:"items" doc:"Attachments ordered by (position, created_at)."`
}

func (s *JournalFileService) list(ctx context.Context, in *listJournalFilesInput) (*listJournalFilesOutput, error) {
	entryID, err := parseUUID(in.EntryID, "id")
	if err != nil {
		return nil, err
	}
	if _, err := s.deps.Entries.GetByID(ctx, entryID); err != nil {
		if errors.Is(err, domain.ErrJournalEntryNotFound) {
			return nil, newAPIError(http.StatusNotFound, "journal_entry_not_found",
				"no such journal entry", map[string]any{"field": "id"})
		}
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to look up journal entry", nil)
	}

	rows, err := s.deps.Attachments.ListByEntry(ctx, entryID)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to list attachments", nil)
	}

	items := make([]JournalFileView, 0, len(rows))
	for _, j := range rows {
		f, err := s.deps.Files.GetByID(ctx, j.FileID)
		if err != nil {
			return nil, newAPIError(http.StatusInternalServerError, "internal_error",
				"failed to load attachment file", nil)
		}
		items = append(items, toJournalFileView(j, f))
	}
	return &listJournalFilesOutput{Body: journalFileListBody{Items: items}}, nil
}

type deleteJournalFileInput struct {
	FileID string `path:"file_id" doc:"File UUID of the attachment to remove."`
}

type deleteJournalFileOutput struct{}

func (s *JournalFileService) delete(ctx context.Context, in *deleteJournalFileInput) (*deleteJournalFileOutput, error) {
	fileID, err := parseUUID(in.FileID, "file_id")
	if err != nil {
		return nil, err
	}
	// Resolve the attachment first so we can clean up the object
	// after the DB rows go away.
	attachment, err := s.deps.Attachments.GetByFileID(ctx, fileID)
	if err != nil {
		if errors.Is(err, domain.ErrJournalAttachmentNotFound) {
			return nil, newAPIError(http.StatusNotFound, "journal_attachment_not_found",
				"no such journal attachment", map[string]any{"field": "file_id"})
		}
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to look up attachment", nil)
	}
	_ = attachment // entry id available if a future audit log wants it.
	originalKey := "files/" + fileID.String()

	txErr := s.deps.RunInTx(ctx, func(tx domain.Tx) error {
		if err := s.deps.Attachments.Delete(ctx, tx, fileID); err != nil {
			return err
		}
		if err := s.deps.Files.Delete(ctx, tx, fileID); err != nil {
			return err
		}
		return nil
	})
	if txErr != nil {
		switch {
		case errors.Is(txErr, domain.ErrJournalAttachmentNotFound):
			return nil, newAPIError(http.StatusNotFound, "journal_attachment_not_found",
				"no such journal attachment", map[string]any{"field": "file_id"})
		case errors.Is(txErr, domain.ErrFileNotFound):
			return nil, newAPIError(http.StatusNotFound, "file_not_found",
				"no such file", map[string]any{"field": "file_id"})
		}
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to delete attachment", nil)
	}

	if err := s.deps.Storage.Delete(ctx, originalKey); err != nil {
		slog.ErrorContext(ctx, "minio orphan after attachment delete",
			"key", originalKey, "err", err)
	}
	return &deleteJournalFileOutput{}, nil
}

func isAllowedJournalContentType(ct string) bool {
	for _, allowed := range allowedJournalContentTypes {
		if ct == allowed {
			return true
		}
	}
	return false
}

func registerJournalFileDownloadRoute(mux *http.ServeMux, s *JournalFileService) {
	wrap := func(h http.Handler) http.Handler {
		return Chain(h, auth.Auth, auth.RequireUser)
	}
	// GET /api/v1/files/{file_id} is the canonical Go-proxied
	// download path for any file (CONTRACT.md §12 download flow).
	// Photos use /api/v1/photos/{id} for the variant-aware path;
	// this endpoint serves only the original bytes by file_id and
	// is what journal attachments expose.
	mux.Handle("GET /api/v1/files/{file_id}", wrap(http.HandlerFunc(s.downloadOriginal)))
}

// downloadOriginal serves the original bytes by file_id. Per
// CONTRACT.md §12 we set Content-Type from stored files.content_type
// (never sniffed), emit ETag: "{sha256}", and honour If-None-Match.
// Disposition is `inline` for image/* and `attachment` for everything
// else — non-image journal attachments (PDF, CSV, JSON, …) prompt a
// download instead of rendering inline.
func (s *JournalFileService) downloadOriginal(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("file_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "file_id must be a UUID",
			map[string]any{"field": "file_id"})
		return
	}
	ctx := r.Context()
	f, err := s.deps.Files.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "file_not_found", "no such file", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
		return
	}

	etag := `"` + f.SHA256 + `"`
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body, _, err := s.deps.Storage.Download(ctx, f.S3Key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "object_not_found",
				"object missing from storage", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_read_failed",
			"failed to read object from storage", nil)
		return
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", buildFileContentDisposition(f.ID, f.ContentType))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", f.ByteSize))

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, body); err != nil {
		slog.ErrorContext(ctx, "download stream error", "err", err, "key", f.S3Key)
	}
}

// buildFileContentDisposition produces a §17 disposition header for
// a file_id-keyed download. Non-image content types render as
// `attachment` so browsers prompt a download; images render `inline`
// so they can preview in-app. Filenames are derived from the file
// UUID + an extension for the content type — no user-supplied
// filename ever appears in the response.
func buildFileContentDisposition(fileID uuid.UUID, contentType string) string {
	ext := extensionForJournalContentType(contentType)
	filename := fileID.String() + ext
	disposition := "attachment"
	if strings.HasPrefix(contentType, "image/") {
		disposition = "inline"
	}
	return fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s",
		disposition, filename, url.PathEscape(filename))
}

func extensionForJournalContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "text/markdown":
		return ".md"
	case "application/json":
		return ".json"
	case "application/xml":
		return ".xml"
	default:
		return ""
	}
}
