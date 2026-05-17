// Photos HTTP surface (mi-jpu / B-3). Implements the §12 upload +
// download + variant pipeline:
//
//	POST   /api/v1/specimens/{id}/photos      (multipart upload)
//	GET    /api/v1/specimens/{id}/photos      (list with cursor pagination)
//	GET    /api/v1/photos/{id}                (download original)
//	GET    /api/v1/photos/{id}/display        (download display variant)
//	GET    /api/v1/photos/{id}/thumb          (download thumbnail variant)
//	PATCH  /api/v1/photos/{id}                (update taken_at / position)
//	DELETE /api/v1/photos/{id}                (remove DB rows + S3 objects)
//
// Pipeline order is canonical (CONTRACT.md §12): reject early, parse,
// EXIF-filter, generate variants, SHA-256, write to MinIO with
// If-None-Match, then DB transaction. On DB failure the just-written
// MinIO objects are best-effort cleaned up. Reverse order is
// forbidden.
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
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage/exif"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage/imageproc"
)

// Allowed inner Content-Type values for photo uploads (per CONTRACT.md
// §12). HEIC is intentionally absent: pure-Go HEIC support is
// immature and §16 forbids cgo. A follow-up bead reopens HEIC for v1.1.
var allowedPhotoContentTypes = []string{
	"image/jpeg",
	"image/png",
	"image/webp",
}

// Variant key suffixes (per §12 storage layout).
const (
	variantDisplaySuffix = ".display.jpg"
	variantThumbSuffix   = ".thumb.jpg"
)

// PhotoStorage is the subset of *storage.Client the photos service
// uses. Defining it here keeps tests free of an actual MinIO/S3
// connection.
type PhotoStorage interface {
	UploadIfNotExists(ctx context.Context, key string, body io.Reader, contentType string) error
	Upload(ctx context.Context, key string, body io.Reader, contentType string) error
	Download(ctx context.Context, key string) (io.ReadCloser, http.Header, error)
	Delete(ctx context.Context, key string) error
}

// TxRunner abstracts db.RunInTx for tests. Production wires
// pool-backed db.RunInTx; tests pass a no-op runner that just calls
// fn with a nil pgx.Tx (the in-memory fakes don't care).
type TxRunner func(ctx context.Context, fn func(tx domain.Tx) error) error

// PhotoView is the wire shape of a photo resource.
type PhotoView struct {
	ID          uuid.UUID  `json:"id" doc:"UUIDv7 primary key."`
	SpecimenID  uuid.UUID  `json:"specimen_id" doc:"Owning specimen."`
	FileID      uuid.UUID  `json:"file_id" doc:"UUID of the underlying files row."`
	ContentType string     `json:"content_type" doc:"Content-Type stored on the original file."`
	ByteSize    int64      `json:"byte_size" doc:"Byte size of the stored original (post EXIF filter)."`
	SHA256      string     `json:"sha256" doc:"Hex SHA-256 of the stored original bytes."`
	Kind        string     `json:"kind" enum:"visible,uv_sw,uv_mw,uv_lw,other" doc:"Lighting condition the photo was taken under: visible, uv_sw (shortwave UV), uv_mw (midwave UV), uv_lw (longwave UV), or other."`
	TakenAt     *time.Time `json:"taken_at" doc:"When the photo was taken; defaulted from EXIF DateTimeOriginal when not provided."`
	Position    int        `json:"position" doc:"Manual ordering; 1-indexed within the specimen's photos."`
	CreatedAt   time.Time  `json:"created_at" doc:"RFC 3339 creation timestamp."`
}

func toPhotoView(p domain.Photo, f domain.File) PhotoView {
	kind := string(p.Kind)
	if kind == "" {
		// Fakes that don't round-trip through Postgres can omit the
		// default; emit the v1 default explicitly so the wire shape
		// is always populated.
		kind = string(domain.PhotoKindVisible)
	}
	return PhotoView{
		ID:          p.ID,
		SpecimenID:  p.SpecimenID,
		FileID:      p.FileID,
		ContentType: f.ContentType,
		ByteSize:    f.ByteSize,
		SHA256:      f.SHA256,
		Kind:        kind,
		TakenAt:     p.TakenAt,
		Position:    p.Position,
		CreatedAt:   p.CreatedAt,
	}
}

// PhotoServiceDeps carries everything the photo handlers need. The
// service is registered when PhotoServiceDeps is non-nil in api.Deps.
type PhotoServiceDeps struct {
	Photos  domain.PhotoRepo
	Files   domain.FileRepo
	Storage PhotoStorage
	RunInTx TxRunner
	// Specimens resolves the parent specimen whose author_id and
	// visibility drive a photo's authorization (photos carry neither
	// of their own — CONTRACT.md §13 v2). Required for per-resource
	// enforcement; nil disables it (the unit-test path, alongside a
	// nil Enforcer).
	Specimens domain.SpecimenRepo
	// Users resolves the parent specimen's owner so the per-field
	// visibility resolver (mi-fo8) can consult FieldDefaults when
	// deciding which photos to redact from a list response or a
	// download. nil disables redaction — the unit-test path,
	// alongside a nil enforcer; redaction layers on top of, and
	// requires, the §13 v2 enforcer.
	Users          domain.UserRepo
	MaxUploadBytes int64
}

// PhotoService wires huma operations against a PhotoServiceDeps.
type PhotoService struct {
	deps  PhotoServiceDeps
	authz authzGuard
}

func registerPhotoOperations(api huma.API, mux *http.ServeMux, authMW authMiddlewares, guard authzGuard, deps *PhotoServiceDeps) {
	if deps == nil {
		return
	}
	if deps.Photos == nil || deps.Files == nil || deps.Storage == nil || deps.RunInTx == nil {
		return
	}
	if deps.MaxUploadBytes <= 0 {
		// Belt-and-suspenders: a zero limit would silently mean "no
		// limit" via http.MaxBytesReader. Default to 100 MiB (the §15
		// default) when the caller forgot to wire it.
		deps.MaxUploadBytes = 100 * 1024 * 1024
	}

	s := &PhotoService{deps: *deps, authz: guard}
	mws := authMW.Protected()
	optionalMWs := authMW.Optional()

	huma.Register(api, huma.Operation{
		OperationID:   "upload-photo",
		Method:        http.MethodPost,
		Path:          "/api/v1/specimens/{id}/photos",
		Summary:       "Upload a photo for a specimen",
		Description:   "Multipart upload (`file` form field). Server-side EXIF allowlist filter (drops GPS, XMP, MakerNotes per CONTRACT.md §12), display + thumbnail variant generation, transactional MinIO + Postgres write.",
		Tags:          []string{"photos"},
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
		OperationID: "list-specimen-photos",
		Method:      http.MethodGet,
		Path:        "/api/v1/specimens/{id}/photos",
		Summary:     "List a specimen's photos",
		Description: "Cursor-paginated list ordered by (position, created_at) ascending — the manual ordering the user controls. " +
			"Returns 404 when the caller cannot see the parent specimen — sub-resource visibility is inherited (CONTRACT.md §13 v2).",
		Tags:        []string{"photos"},
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound},
		Middlewares: optionalMWs,
	}, s.list)

	huma.Register(api, huma.Operation{
		OperationID: "patch-photo",
		Method:      http.MethodPatch,
		Path:        "/api/v1/photos/{id}",
		Summary:     "Update a photo's taken_at and/or position",
		Tags:        []string{"photos"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.patch)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-photo",
		Method:        http.MethodDelete,
		Path:          "/api/v1/photos/{id}",
		Summary:       "Delete a photo",
		Description:   "Removes the photos row, the files row, and all three MinIO objects (original + display + thumbnail). Best-effort MinIO cleanup; DB transaction is the source of truth.",
		Tags:          []string{"photos"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares:   mws,
	}, s.delete)

	// Download endpoints stream raw image bytes — bypass huma's
	// JSON-by-default response shaping by registering them directly
	// on the mux (still under the protected route bucket via the
	// auth.Auth + auth.RequireUser chain wrapping the catch-all
	// /api/v1/ handler in api.New). Auth is enforced here because the
	// route is registered to a specific path that wins over the
	// catch-all.
	registerPhotoDownloadRoutes(mux, s, authMW.verifier)
}

// maxBytesMiddleware wraps the request body in http.MaxBytesReader so
// uploads that exceed s.deps.MaxUploadBytes fail with 413 before the
// handler reads anything (per CONTRACT.md §12).
func (s *PhotoService) maxBytesMiddleware(ctx huma.Context, next func(huma.Context)) {
	r, w := humago.Unwrap(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, s.deps.MaxUploadBytes)
	next(ctx)
}

// enforcePhoto runs the §13 v2 per-resource check for a photo
// operation, deriving the photo's owner and visibility from its
// parent specimen (photos carry neither of their own). A no-op when
// authorization is disabled or no specimen repo is wired — the
// unit-test path, alongside a nil Enforcer.
func (s *PhotoService) enforcePhoto(
	ctx context.Context, specimenID, photoID uuid.UUID, act string,
) error {
	if !s.authz.active() || s.deps.Specimens == nil {
		return nil
	}
	sp, err := s.deps.Specimens.GetByID(ctx, specimenID)
	if err != nil {
		if errors.Is(err, domain.ErrSpecimenNotFound) {
			return newAPIError(http.StatusNotFound, "specimen_not_found",
				"no such specimen", map[string]any{"field": "id"})
		}
		return newAPIError(http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
	}
	return s.authz.check(ctx, photoResource(photoID, sp), act)
}

// canSeePhotoHTTP is the per-image visibility check for the raw
// download routes (mi-fo8 / mi-9ww). It runs the photo-specific
// resolution chain — image override, specimen VisibilityImages,
// specimen overall, owner default, system default — and writes a
// 404 envelope when the caller cannot see the resolved Visibility.
// A nil enforcer or missing specimen repo makes the check a no-op,
// matching the rest of the redaction surface and authzGuard's seam.
//
// A 404 (not 403) is deliberate: per CONTRACT.md §13 v2 a viewer
// who is not allowed to see a photo MUST NOT be able to distinguish
// "redacted" from "never existed."
func (s *PhotoService) canSeePhotoHTTP(w http.ResponseWriter, r *http.Request, p domain.Photo) bool {
	if !s.authz.active() || s.deps.Specimens == nil {
		return true
	}
	sp, err := s.deps.Specimens.GetByID(r.Context(), p.SpecimenID)
	if err != nil {
		if errors.Is(err, domain.ErrSpecimenNotFound) {
			writeError(w, http.StatusNotFound, "photo_not_found", "no such photo", nil)
			return false
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return false
	}
	red := newRedactor(s.deps.Users, s.authz)
	owner := red.loadOwner(r.Context(), sp.AuthorID)
	if red.canSeePhoto(r.Context(), sp, owner, p) {
		return true
	}
	writeError(w, http.StatusNotFound, "photo_not_found", "no such photo", nil)
	return false
}

// enforcePhotoHTTP is the net/http analogue of enforcePhoto for the
// raw download routes. It writes a §10 envelope and returns false
// when access is denied (or the parent specimen is missing). The
// view action is a detail-style read (CONTRACT.md §13 v2) — a
// forbidden outcome is rewritten as 404 so existence is never
// leaked, matching what GET /photos/{id} would produce for a
// missing row. Non-view actions (edit, delete) keep 403 semantics.
func (s *PhotoService) enforcePhotoHTTP(
	w http.ResponseWriter, r *http.Request, specimenID, photoID uuid.UUID, act string,
) bool {
	if !s.authz.active() || s.deps.Specimens == nil {
		return true
	}
	sp, err := s.deps.Specimens.GetByID(r.Context(), specimenID)
	if err != nil {
		if errors.Is(err, domain.ErrSpecimenNotFound) {
			writeError(w, http.StatusNotFound, "photo_not_found", "no such photo", nil)
			return false
		}
		writeError(w, http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
		return false
	}
	res := photoResource(photoID, sp)
	if act == actView {
		return s.authz.checkViewHTTP(w, r, res, "photo_not_found", "no such photo")
	}
	return s.authz.checkHTTP(w, r, res, act)
}

type uploadPhotoForm struct {
	File    huma.FormFile `form:"file" required:"true" doc:"The image file to upload."`
	TakenAt string        `form:"taken_at" required:"false" doc:"Optional ISO 8601 timestamp; defaults to EXIF DateTimeOriginal when present."`
	Kind    string        `form:"kind" required:"false" doc:"Optional photo kind: visible, uv_sw, uv_mw, uv_lw, or other. Defaults to visible."`
}

type uploadPhotoInput struct {
	SpecimenID string `path:"id"`
	RawBody    huma.MultipartFormFiles[uploadPhotoForm]
}

type uploadPhotoOutput struct {
	Location string `header:"Location"`
	Body     PhotoView
}

func (s *PhotoService) upload(ctx context.Context, in *uploadPhotoInput) (*uploadPhotoOutput, error) {
	// CONTRACT.md §17: huma decodes the multipart form before this
	// handler runs, so by the time we get here any body larger than
	// huma's MultipartMaxMemory (8 KiB default) has already spilled to
	// /tmp. Schedule cleanup first thing so every early return — UUID
	// parse, content-type rejection, oversize, downstream errors —
	// unlinks the tempfile. RemoveAll deletes the on-disk spillover;
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

	specimenID, err := parseUUID(in.SpecimenID, "id")
	if err != nil {
		return nil, err
	}
	// CONTRACT.md §13 v2: adding a photo is editing the specimen's
	// gallery — the caller must own (or be admin of) the specimen.
	if err := s.enforcePhoto(ctx, specimenID, uuid.Nil, actCreate); err != nil {
		return nil, err
	}

	innerCT := strings.ToLower(strings.TrimSpace(form.File.ContentType))
	if !isAllowedPhotoContentType(innerCT) {
		return nil, newAPIError(http.StatusUnsupportedMediaType, "unsupported_media_type",
			"file content-type is not allowed",
			map[string]any{"allowed": allowedPhotoContentTypes, "got": innerCT})
	}

	data, err := io.ReadAll(form.File.File)
	if err != nil {
		// http.MaxBytesReader returns *http.MaxBytesError with the
		// limit; the body may also be truncated by other transport
		// errors. Treat MaxBytesError as 413; everything else is 400.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, newAPIError(http.StatusRequestEntityTooLarge, "payload_too_large",
				"upload exceeds the configured size limit",
				map[string]any{"max_bytes": mbe.Limit})
		}
		return nil, newAPIError(http.StatusBadRequest, "bad_request",
			"failed to read upload body", nil)
	}

	// Kind defaults to 'visible' (per CONTRACT.md §12 / mi-5b6) when
	// the form field is absent or empty. An invalid value is a 400
	// rather than a silent coerce so callers learn about typos.
	kind := domain.PhotoKindVisible
	if raw := strings.ToLower(strings.TrimSpace(form.Kind)); raw != "" {
		k := domain.PhotoKind(raw)
		if !k.IsValid() {
			return nil, newAPIError(http.StatusBadRequest, "invalid_kind",
				"kind must be one of visible, uv_sw, uv_mw, uv_lw, other",
				map[string]any{"field": "kind"})
		}
		kind = k
	}

	// Default taken_at: caller-supplied value wins; fall back to EXIF.
	var takenAt *time.Time
	if raw := strings.TrimSpace(form.TakenAt); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, newAPIError(http.StatusBadRequest, "invalid_taken_at",
				"taken_at must be RFC 3339",
				map[string]any{"field": "taken_at"})
		}
		t = t.UTC()
		takenAt = &t
	}
	if takenAt == nil {
		if t := exif.ExtractDateTimeOriginal(data, innerCT); t != nil {
			u := t.UTC()
			takenAt = &u
		}
	}

	// EXIF allowlist filter on the bytes we're about to store.
	filtered, err := exif.Filter(data, innerCT)
	if err != nil {
		// EXIF parse failure is non-fatal for storage of the raw
		// bytes — strip nothing and fall through. Log loudly so we
		// can audit if a malformed file got through.
		slog.WarnContext(ctx, "exif filter failed; storing raw bytes",
			"err", err, "content_type", innerCT)
		filtered = data
	}

	sum := sha256.Sum256(filtered)
	sha256Hex := hex.EncodeToString(sum[:])

	variants, err := imageproc.Generate(filtered, innerCT)
	if err != nil {
		// Decoding failures here are user-input errors — the bytes
		// claim a valid content type but aren't decodable.
		return nil, newAPIError(http.StatusBadRequest, "image_decode_failed",
			"the uploaded file could not be decoded as the declared content type",
			map[string]any{"content_type": innerCT})
	}

	fileID := domain.NewID()
	originalKey := "files/" + fileID.String()
	displayKey := originalKey + variantDisplaySuffix
	thumbKey := originalKey + variantThumbSuffix

	// MinIO writes (per §12 step 6): original with If-None-Match,
	// then variants. On any failure mid-flight, undo successful
	// writes before returning.
	written := []string{}
	cleanup := func() {
		for _, k := range written {
			if err := s.deps.Storage.Delete(ctx, k); err != nil {
				slog.ErrorContext(ctx, "minio cleanup failed", "key", k, "err", err)
			}
		}
	}

	if err := s.deps.Storage.UploadIfNotExists(ctx, originalKey, bytesReader(filtered), innerCT); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			return nil, newAPIError(http.StatusConflict, "file_id_collision",
				"object key collision; retry the upload", nil)
		}
		return nil, newAPIError(http.StatusInternalServerError, "storage_write_failed",
			"failed to write original to object storage", nil)
	}
	written = append(written, originalKey)

	if err := s.deps.Storage.Upload(ctx, displayKey, bytesReader(variants.Display), "image/jpeg"); err != nil {
		cleanup()
		return nil, newAPIError(http.StatusInternalServerError, "storage_write_failed",
			"failed to write display variant", nil)
	}
	written = append(written, displayKey)

	if err := s.deps.Storage.Upload(ctx, thumbKey, bytesReader(variants.Thumbnail), "image/jpeg"); err != nil {
		cleanup()
		return nil, newAPIError(http.StatusInternalServerError, "storage_write_failed",
			"failed to write thumbnail variant", nil)
	}
	written = append(written, thumbKey)

	// DB transaction: insert files + photos atomically. Rollback on
	// any failure cleans up the just-written MinIO objects.
	now := time.Now().UTC()
	file := domain.File{
		ID:          fileID,
		S3Key:       originalKey,
		ContentType: innerCT,
		ByteSize:    int64(len(filtered)),
		SHA256:      sha256Hex,
		UploadedBy:  auth.FromContext(ctx).ID,
		UploadedAt:  now,
	}
	photo := domain.Photo{
		ID:         domain.NewID(),
		SpecimenID: specimenID,
		FileID:     fileID,
		Kind:       kind,
		TakenAt:    takenAt,
		CreatedAt:  now,
	}

	txErr := s.deps.RunInTx(ctx, func(tx domain.Tx) error {
		if err := s.deps.Files.Create(ctx, tx, file); err != nil {
			return err
		}
		// Default position to max+1 for the specimen.
		maxPos, err := s.deps.Photos.MaxPosition(ctx, tx, specimenID)
		if err != nil {
			return err
		}
		photo.Position = maxPos + 1
		return s.deps.Photos.Create(ctx, tx, photo)
	})
	if txErr != nil {
		cleanup()
		switch {
		case errors.Is(txErr, domain.ErrPhotoNotFound):
			// FK violation on specimen_id (specimen doesn't exist).
			return nil, newAPIError(http.StatusNotFound, "specimen_not_found",
				"no such specimen",
				map[string]any{"field": "id"})
		case errors.Is(txErr, domain.ErrFileConflict):
			return nil, newAPIError(http.StatusConflict, "file_conflict",
				"file with that key already exists", nil)
		}
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to commit upload", nil)
	}

	out := &uploadPhotoOutput{
		Location: "/api/v1/photos/" + photo.ID.String(),
		Body:     toPhotoView(photo, file),
	}
	return out, nil
}

func isAllowedPhotoContentType(ct string) bool {
	for _, allowed := range allowedPhotoContentTypes {
		if ct == allowed {
			return true
		}
	}
	return false
}

func bytesReader(b []byte) *strings.Reader {
	return strings.NewReader(string(b))
}

type listPhotosInput struct {
	SpecimenID string `path:"id"`
	Limit      int    `query:"limit" minimum:"1" maximum:"200" doc:"Page size (1-200; defaults to 50)."`
	Cursor     string `query:"cursor" doc:"Opaque pagination cursor returned by the previous page."`
}

type listPhotosOutput struct {
	Body photoListBody
}

type photoListBody struct {
	Items      []PhotoView `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

func (s *PhotoService) list(ctx context.Context, in *listPhotosInput) (*listPhotosOutput, error) {
	specimenID, err := parseUUID(in.SpecimenID, "id")
	if err != nil {
		return nil, err
	}
	// CONTRACT.md §13 v2: resolve and visibility-check the parent
	// before evaluating the sub-list. If the caller cannot see the
	// parent, return 404 without touching the photos table. The
	// loaded specimen is reused for the per-field visibility
	// redaction below (mi-fo8 / mi-9ww), which drives the per-photo
	// drop predicate.
	var sp domain.Specimen
	if s.deps.Specimens != nil {
		got, sperr := s.deps.Specimens.GetByID(ctx, specimenID)
		if sperr != nil {
			return nil, mapSpecimenError(sperr)
		}
		if err := s.authz.checkView(ctx, specimenResource(got),
			"specimen_not_found", "no such specimen"); err != nil {
			return nil, err
		}
		sp = got
	}
	page := domain.Page{Limit: in.Limit, Cursor: in.Cursor}
	rows, cursor, err := s.deps.Photos.ListBySpecimen(ctx, specimenID, page)
	if err != nil {
		return nil, mapPhotoListError(err)
	}

	// Per-field visibility redaction (mi-fo8 / mi-9ww): drop photos
	// the caller may not see. Done after the page is loaded so cursor
	// pagination still walks the underlying ordering; the response
	// can therefore contain fewer items than the page size. The
	// caller MUST NOT learn how many photos were redacted (no count
	// is exposed) — that is a deliberate privacy property.
	red := newRedactor(s.deps.Users, s.authz)
	visible := red.filterPhotos(ctx, sp, rows)

	items := make([]PhotoView, 0, len(visible))
	for _, p := range visible {
		f, err := s.deps.Files.GetByID(ctx, p.FileID)
		if err != nil {
			return nil, newAPIError(http.StatusInternalServerError, "internal_error",
				"failed to load photo file", nil)
		}
		items = append(items, toPhotoView(p, f))
	}
	body := photoListBody{Items: items}
	if cursor != "" {
		c := string(cursor)
		body.NextCursor = &c
	}
	return &listPhotosOutput{Body: body}, nil
}

type patchPhotoInput struct {
	ID   string `path:"id"`
	Body patchPhotoBody
}

type patchPhotoBody struct {
	TakenAt  *time.Time `json:"taken_at,omitempty" doc:"New taken_at; pass null to clear, omit to leave unchanged."`
	Position *int       `json:"position,omitempty" doc:"New manual ordering position; omit to leave unchanged."`
	Kind     *string    `json:"kind,omitempty" enum:"visible,uv_sw,uv_mw,uv_lw,other" doc:"New photo kind; omit to leave unchanged."`
}

type patchPhotoOutput struct {
	Body PhotoView
}

func (s *PhotoService) patch(ctx context.Context, in *patchPhotoInput) (*patchPhotoOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	current, err := s.deps.Photos.GetByID(ctx, id)
	if err != nil {
		return nil, mapPhotoError(err)
	}
	if err := s.enforcePhoto(ctx, current.SpecimenID, current.ID, actEdit); err != nil {
		return nil, err
	}

	if in.Body.TakenAt != nil {
		t := in.Body.TakenAt.UTC()
		current.TakenAt = &t
	}
	if in.Body.Position != nil {
		if *in.Body.Position < 1 {
			return nil, newAPIError(http.StatusBadRequest, "invalid_position",
				"position must be >= 1",
				map[string]any{"field": "position"})
		}
		current.Position = *in.Body.Position
	}
	if in.Body.Kind != nil {
		k := domain.PhotoKind(strings.ToLower(strings.TrimSpace(*in.Body.Kind)))
		if !k.IsValid() {
			return nil, newAPIError(http.StatusBadRequest, "invalid_kind",
				"kind must be one of visible, uv_sw, uv_mw, uv_lw, other",
				map[string]any{"field": "kind"})
		}
		current.Kind = k
	}

	if err := s.deps.Photos.Update(ctx, nil, current); err != nil {
		return nil, mapPhotoError(err)
	}
	f, err := s.deps.Files.GetByID(ctx, current.FileID)
	if err != nil {
		return nil, mapFileError(err)
	}
	return &patchPhotoOutput{Body: toPhotoView(current, f)}, nil
}

type deletePhotoInput struct {
	ID string `path:"id"`
}

type deletePhotoOutput struct{}

func (s *PhotoService) delete(ctx context.Context, in *deletePhotoInput) (*deletePhotoOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	current, err := s.deps.Photos.GetByID(ctx, id)
	if err != nil {
		return nil, mapPhotoError(err)
	}
	if err := s.enforcePhoto(ctx, current.SpecimenID, current.ID, actDelete); err != nil {
		return nil, err
	}

	originalKey := "files/" + current.FileID.String()
	displayKey := originalKey + variantDisplaySuffix
	thumbKey := originalKey + variantThumbSuffix

	// Remove DB rows first (in a transaction); then best-effort
	// delete MinIO objects. Per CONTRACT.md §12 the DB is the source
	// of truth; if MinIO cleanup fails the orphan-cleanup job picks
	// it up later (deferred for v1).
	txErr := s.deps.RunInTx(ctx, func(tx domain.Tx) error {
		if err := s.deps.Photos.Delete(ctx, tx, current.ID); err != nil {
			return err
		}
		if err := s.deps.Files.Delete(ctx, tx, current.FileID); err != nil {
			return err
		}
		return nil
	})
	if txErr != nil {
		return nil, mapPhotoError(txErr)
	}

	for _, k := range []string{originalKey, displayKey, thumbKey} {
		if err := s.deps.Storage.Delete(ctx, k); err != nil {
			slog.ErrorContext(ctx, "minio orphan after delete", "key", k, "err", err)
		}
	}
	return &deletePhotoOutput{}, nil
}

func registerPhotoDownloadRoutes(mux *http.ServeMux, s *PhotoService, verifier auth.TokenVerifier) {
	// CONTRACT.md §13 v2: photo bytes are a detail-style read of a
	// visibility-scoped resource. Anonymous callers are admitted —
	// the parent-specimen visibility check (enforcePhotoHTTP) does
	// the gating and translates a denial into 404 so existence is
	// never leaked. An invalid token still 401s.
	wrap := func(h http.Handler) http.Handler {
		return Chain(h, auth.OptionalAuth(verifier))
	}
	// Per the bead acceptance criteria (mi-jpu) GET /api/v1/photos/{id}
	// returns the original bytes — not a JSON metadata view. Photo
	// metadata is exposed via the upload response, the list endpoint,
	// and the patch endpoint's response body.
	mux.Handle("GET /api/v1/photos/{id}", wrap(http.HandlerFunc(s.downloadOriginal)))
	mux.Handle("GET /api/v1/photos/{id}/display", wrap(http.HandlerFunc(s.downloadDisplay)))
	mux.Handle("GET /api/v1/photos/{id}/thumb", wrap(http.HandlerFunc(s.downloadThumb)))
}

// downloadOriginal serves the original bytes. Per CONTRACT.md §12 we
// set Content-Type from stored files.content_type (never sniffed),
// emit ETag: "{sha256}", and honour If-None-Match for 304s.
func (s *PhotoService) downloadOriginal(w http.ResponseWriter, r *http.Request) {
	s.serveDownload(w, r, "")
}

func (s *PhotoService) downloadDisplay(w http.ResponseWriter, r *http.Request) {
	s.serveDownload(w, r, variantDisplaySuffix)
}

func (s *PhotoService) downloadThumb(w http.ResponseWriter, r *http.Request) {
	s.serveDownload(w, r, variantThumbSuffix)
}

func (s *PhotoService) serveDownload(w http.ResponseWriter, r *http.Request, variantSuffix string) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a UUID",
			map[string]any{"field": "id"})
		return
	}
	ctx := r.Context()
	p, err := s.deps.Photos.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrPhotoNotFound) {
			writeError(w, http.StatusNotFound, "photo_not_found", "no such photo", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}
	// CONTRACT.md §13 v2: viewing a photo's bytes requires view
	// access to its parent specimen (public/unlisted shortcut applies).
	if !s.enforcePhotoHTTP(w, r, p.SpecimenID, p.ID, actView) {
		return
	}
	// mi-fo8 / mi-9ww: per-photo visibility resolution. A photo the
	// parent-visibility check admitted may still be hidden by an
	// image-level override or the specimen's VisibilityImages chain.
	// Render the redacted state as 404 so existence is not leaked
	// (matches what GET would have produced for a missing photo).
	if !s.canSeePhotoHTTP(w, r, p) {
		return
	}
	f, err := s.deps.Files.GetByID(ctx, p.FileID)
	if err != nil {
		if errors.Is(err, domain.ErrFileNotFound) {
			writeError(w, http.StatusNotFound, "file_not_found", "no such file", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	contentType := f.ContentType
	if variantSuffix != "" {
		contentType = "image/jpeg"
	}
	key := f.S3Key + variantSuffix

	// ETag derived from the underlying SHA-256 of the original. For
	// the original the ETag is the bytes' hash directly; for variants
	// the ETag is "{sha256}-{variant}" so an unchanged original maps
	// to a stable variant ETag (variants are deterministic from the
	// original). Per CONTRACT.md §12 we honour If-None-Match.
	etag := `"` + f.SHA256
	if variantSuffix != "" {
		etag += "-" + strings.TrimPrefix(strings.TrimSuffix(variantSuffix, ".jpg"), ".")
	}
	etag += `"`

	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body, _, err := s.deps.Storage.Download(ctx, key)
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

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", buildContentDisposition(p.ID, contentType, variantSuffix))

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, body); err != nil {
		// Streaming error after WriteHeader — we can only log.
		slog.ErrorContext(ctx, "download stream error", "err", err, "key", key)
	}
}

// buildContentDisposition produces the §17 disposition header.
// Filenames use the photo UUID + an extension derived from the
// content type; user-supplied filenames never appear in object keys
// or response headers (per §12 / §17). RFC 6266 encoding via the
// `filename*=UTF-8”...` form is used so unicode-safe even though we
// only emit ASCII UUIDs.
func buildContentDisposition(photoID uuid.UUID, contentType, variantSuffix string) string {
	ext := extensionForContentType(contentType)
	name := photoID.String()
	if variantSuffix != "" {
		// Variants get a suffix in the filename so a saved file is
		// distinguishable from the original.
		name += strings.TrimSuffix(variantSuffix, ".jpg")
	}
	filename := name + ext
	disposition := "inline"
	if !strings.HasPrefix(contentType, "image/") {
		disposition = "attachment"
	}
	return fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s",
		disposition, filename, url.PathEscape(filename))
}

func extensionForContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

// mapPhotoError translates photo repo sentinels into §10 envelope
// errors. Other errors become opaque 500.
func mapPhotoError(err error) error {
	if errors.Is(err, domain.ErrPhotoNotFound) {
		return newAPIError(http.StatusNotFound, "photo_not_found",
			"no such photo", nil)
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}

func mapFileError(err error) error {
	if errors.Is(err, domain.ErrFileNotFound) {
		return newAPIError(http.StatusNotFound, "file_not_found",
			"no such file", nil)
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}

func mapPhotoListError(err error) error {
	if strings.Contains(err.Error(), "cursor:") {
		return newAPIError(http.StatusBadRequest, "invalid_cursor",
			"cursor is malformed", nil)
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}

// Compile-time guard: db.PhotoPostgres satisfies the (cursor-aware)
// PhotoRepo interface signature.
var _ domain.PhotoRepo = (*db.PhotoPostgres)(nil)
