// Export HTTP surface (mi-dkuu.1). Implements the user-data export
// half of the export/import design (docs/export-import-design.md):
//
//	GET /api/v1/export   (authenticated; streams a ZIP archive)
//
// The archive is a versioned, self-describing ZIP of the caller's
// own data — every specimen, collector, journal entry (with
// attachments), QR sheet, and photo, plus the backing image and
// attachment binaries. It is the read-only inverse of the GDPR
// account-erasure enumeration (account.go / db.AccountErasePostgres):
// erasure deletes exactly this set of rows + object keys; export
// serializes them for portability, backup, and GDPR data-access.
//
// Archive layout (schemaVersion 1):
//
//	minerals-export.zip
//	├── manifest.json                       archive metadata + image map
//	├── data/
//	│   ├── collectors.jsonl
//	│   ├── specimens.jsonl                 incl. ordered collectorIds chain
//	│   ├── journal_entries.jsonl           incl. attachments[] refs
//	│   └── qrsheets.jsonl                  incl. specimen membership
//	├── images/<specimenId>/<imageId>.<ext> specimen photo originals
//	└── files/journal/<entryId>/<fileId>.<ext>  journal attachment binaries
//
// Streaming model: all metadata (text rows) is enumerated up front in
// the handler so an enumeration failure surfaces as a proper HTTP
// error envelope BEFORE any bytes are written; the large image and
// attachment binaries are streamed straight from object storage into
// the zip in the response body callback, never fully buffered. A
// storage read that fails mid-stream can only be logged (the 200 +
// headers are already committed) — the same tradeoff the photo
// download path accepts.
//
// v1 is synchronous and all-or-nothing (full collection only; no
// subset). The async-job path (a status poll + signed download link)
// is a conditional follow-up (design §6.5) for collections too large
// for a single request.
package api

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// exportSchemaVersion gates import compatibility (design §2.2). Bump
// on any breaking change to the archive format; the import endpoint
// (sub-bead 3) validates this before writing anything and refuses
// unknown/newer majors with a clear error.
const exportSchemaVersion = 1

// exportListPageSize is the keyset page size used to walk each
// owner-scoped list query. MaxListLimit (db.MaxListLimit) caps the
// per-call ceiling; we page until the cursor drains.
const exportListPageSize = 200

// ExportStorage is the subset of *storage.Client the export service
// uses: read one object by key. Defining it here keeps tests free of
// a real MinIO/S3 connection (mirrors PhotoStorage / JournalFileStorage).
type ExportStorage interface {
	Download(ctx context.Context, key string) (io.ReadCloser, http.Header, error)
}

// ExportServiceDeps carries everything the export handler needs. The
// service is registered when ExportServiceDeps is non-nil in api.Deps
// and every required collaborator is wired; a nil dep leaves
// GET /api/v1/export unregistered (the unit-test path).
type ExportServiceDeps struct {
	Specimens          domain.SpecimenRepo
	Photos             domain.PhotoRepo
	JournalEntries     domain.JournalEntryRepo
	JournalFiles       domain.JournalEntryFileRepo
	Collectors         domain.CollectorRepo
	SpecimenCollectors domain.SpecimenCollectorRepo
	QRSheets           domain.QRSheetRepo
	Files              domain.FileRepo
	Storage            ExportStorage
}

func (d ExportServiceDeps) ready() bool {
	return d.Specimens != nil && d.Photos != nil && d.JournalEntries != nil &&
		d.JournalFiles != nil && d.Collectors != nil && d.SpecimenCollectors != nil &&
		d.QRSheets != nil && d.Files != nil && d.Storage != nil
}

// exportService backs GET /api/v1/export.
type exportService struct {
	deps ExportServiceDeps
}

// registerExportOperations registers GET /api/v1/export when the
// export deps are fully wired. The endpoint uses the Protected()
// chain: the caller must be a fully set-up (active) account, and the
// resolved application user id scopes every query to the caller's own
// rows.
func registerExportOperations(api huma.API, mws authMiddlewares, deps *ExportServiceDeps) {
	if deps == nil || !deps.ready() {
		return
	}
	s := &exportService{deps: *deps}

	huma.Register(api, huma.Operation{
		OperationID: "export-data",
		Method:      http.MethodGet,
		Path:        "/api/v1/export",
		Summary:     "Export the caller's full collection as a ZIP archive",
		Description: "Streams a versioned ZIP archive of the caller's own data: every " +
			"specimen (with type-specific fields and per-field visibility), collector, " +
			"journal entry (with attachments), QR sheet, and photo, plus the backing " +
			"image and attachment binaries. Strictly author-scoped — never another " +
			"user's data. The archive is self-describing (manifest.json carries the " +
			"schema version, counts, and the file↔specimen map with content hashes) so " +
			"it can be re-imported to recreate the collection. v1 is synchronous and " +
			"all-or-nothing.",
		Tags:   []string{"export"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		// io.Copy from object storage can outlast the default huma
		// timeout for large collections; the binaries stream straight
		// through, so leave cancellation to the request context.
		Middlewares: mws.Protected(),
	}, s.export)
}

// dataExportOutput streams the ZIP through a body callback (the same
// raw-body pattern the system endpoints use in server.go). Returning
// the callback rather than a typed body lets the handler do all the
// fallible enumeration up front — a failure there becomes a JSON
// error envelope — while the body callback only writes already-
// gathered metadata and streams binaries.
type dataExportOutput struct {
	Body func(huma.Context)
}

// exportManifest is the archive-level metadata written to manifest.json
// (design §2.2). camelCase mirrors the design example.
type exportManifest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Application   string              `json:"application"`
	ExportedAt    time.Time           `json:"exportedAt"`
	ExportedBy    string              `json:"exportedBy"` // keycloak sub of the exporter
	Counts        exportCounts        `json:"counts"`
	Images        []exportImageRecord `json:"images"`
}

type exportCounts struct {
	Collectors     int `json:"collectors"`
	Specimens      int `json:"specimens"`
	JournalEntries int `json:"journalEntries"`
	QRSheets       int `json:"qrsheets"`
	Images         int `json:"images"`
	JournalFiles   int `json:"journalFiles"`
}

// exportImageRecord is one entry in manifest.images[] — the
// authoritative file↔specimen mapping and integrity record (content
// hash) for a specimen photo, carrying the full photo metadata so it
// round-trips (kind, position, visibility, taken_at).
type exportImageRecord struct {
	ImageID     uuid.UUID          `json:"imageId"` // photos.id
	SpecimenID  uuid.UUID          `json:"specimenId"`
	FileID      uuid.UUID          `json:"fileId"`
	Path        string             `json:"path"` // path of the binary inside the archive
	Kind        domain.PhotoKind   `json:"kind"`
	Position    int                `json:"position"`
	Visibility  *domain.Visibility `json:"visibility,omitempty"`
	TakenAt     *time.Time         `json:"takenAt,omitempty"`
	ContentHash string             `json:"contentHash"` // "sha256:<hex>"
	ContentType string             `json:"contentType"`
	ByteSize    int64              `json:"byteSize"`
	CreatedAt   time.Time          `json:"createdAt"`

	// key is the object-storage key for the original bytes. Not
	// serialized — used only to stream the binary into the zip.
	key string `json:"-"`
}

// exportCollectorRecord is one line of data/collectors.jsonl.
// author_id is omitted: export is the caller's own data and import
// re-homes to the importer (design §2.3).
type exportCollectorRecord struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Notes     *string   `json:"notes,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// exportSpecimenRecord is one line of data/specimens.jsonl. It carries
// every persisted, user-owned field including per-field visibility
// overrides and the raw type_data JSON, plus the ordered collector
// chain (collectorIds) so the specimen↔collector links round-trip.
// author_id is omitted (re-homed on import).
type exportSpecimenRecord struct {
	ID            uuid.UUID           `json:"id"`
	Type          domain.SpecimenType `json:"type"`
	CatalogNumber *string             `json:"catalogNumber,omitempty"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	Visibility    domain.Visibility   `json:"visibility"`
	AcquiredAt    *time.Time          `json:"acquiredAt,omitempty"`
	AcquiredFrom  *string             `json:"acquiredFrom,omitempty"`
	PriceCents    *int64              `json:"priceCents,omitempty"`
	SourceNotes   *string             `json:"sourceNotes,omitempty"`
	LocalityText  *string             `json:"localityText,omitempty"`
	Locality      *domain.Locality    `json:"locality,omitempty"`
	MassG         *float64            `json:"massG,omitempty"`
	Dimensions    *domain.Dimensions  `json:"dimensions,omitempty"`
	// TypeData is the raw type-specific JSON (mineral/rock/meteorite/
	// fossil). Embedded verbatim so it round-trips without the export
	// needing to know the type discriminant. Null when unset.
	TypeData json.RawMessage `json:"typeData,omitempty"`
	// MainImageID references the photo's file id designated as the
	// primary image. Remapped on import alongside the file ids.
	MainImageID            *uuid.UUID         `json:"mainImageId,omitempty"`
	VisibilityPrice        *domain.Visibility `json:"visibilityPrice,omitempty"`
	VisibilityAcquiredFrom *domain.Visibility `json:"visibilityAcquiredFrom,omitempty"`
	VisibilityImages       *domain.Visibility `json:"visibilityImages,omitempty"`
	Tagged                 bool               `json:"tagged"`
	// CollectorIds is the ordered specimen_collectors chain (1-indexed
	// position == slice order). Each id references a collectors row in
	// data/collectors.jsonl; remapped on import.
	CollectorIDs []uuid.UUID `json:"collectorIds"`
	CreatedAt    time.Time   `json:"createdAt"`
	UpdatedAt    time.Time   `json:"updatedAt"`
}

// exportJournalRecord is one line of data/journal_entries.jsonl. The
// markdown body and ordered attachment references round-trip; the
// rendered HTML is intentionally not exported (it is derived from
// BodyMD via the §17 pipeline on import). author_id is omitted.
type exportJournalRecord struct {
	ID          uuid.UUID                `json:"id"`
	SpecimenID  uuid.UUID                `json:"specimenId"`
	BodyMD      string                   `json:"bodyMd"`
	Attachments []exportAttachmentRecord `json:"attachments"`
	CreatedAt   time.Time                `json:"createdAt"`
	UpdatedAt   time.Time                `json:"updatedAt"`
}

// exportAttachmentRecord references a journal attachment binary
// shipped under files/journal/<entryId>/<fileId>.<ext>, with its
// integrity hash so import can verify and re-upload it.
type exportAttachmentRecord struct {
	FileID      uuid.UUID `json:"fileId"`
	Position    int       `json:"position"`
	Path        string    `json:"path"`
	ContentHash string    `json:"contentHash"` // "sha256:<hex>"
	ContentType string    `json:"contentType"`
	ByteSize    int64     `json:"byteSize"`
	CreatedAt   time.Time `json:"createdAt"`

	// key / entryID are streaming bookkeeping, not serialized.
	key     string    `json:"-"`
	entryID uuid.UUID `json:"-"`
}

// exportQRSheetRecord is the single line of data/qrsheets.jsonl (v1
// enforces one active sheet per user). Membership is embedded as an
// ordered list of specimen ids (design §2.1). user_id is omitted.
type exportQRSheetRecord struct {
	ID        uuid.UUID              `json:"id"`
	Template  domain.QRSheetTemplate `json:"template"`
	Specimens []exportQRSheetMember  `json:"specimens"`
	CreatedAt time.Time              `json:"createdAt"`
	UpdatedAt time.Time              `json:"updatedAt"`
}

type exportQRSheetMember struct {
	SpecimenID uuid.UUID `json:"specimenId"`
	Position   int       `json:"position"`
	AddedAt    time.Time `json:"addedAt"`
}

// exportArchive is the fully-enumerated, in-memory metadata for one
// export. Only text rows live here; image and attachment binaries are
// streamed from object storage at write time, never buffered.
type exportArchive struct {
	manifest    exportManifest
	collectors  []exportCollectorRecord
	specimens   []exportSpecimenRecord
	journal     []exportJournalRecord
	qrsheets    []exportQRSheetRecord
	attachments []exportAttachmentRecord // flat list of binaries to stream
}

func (s *exportService) export(ctx context.Context, _ *struct{}) (*dataExportOutput, error) {
	u := auth.FromContext(ctx)
	if u.Sub == "" || u.ID == uuid.Nil {
		// Defensive — the Protected() chain should already have
		// surfaced this as 401 before reaching the handler.
		return nil, newAPIError(http.StatusUnauthorized,
			"unauthorized", "authentication required", nil)
	}

	arch, err := s.gather(ctx, u)
	if err != nil {
		slog.ErrorContext(ctx, "export: gather failed", "user_id", u.ID, "err", err)
		return nil, newAPIError(http.StatusInternalServerError,
			"internal_error", "failed to assemble export", nil)
	}

	filename := fmt.Sprintf("minerals-export-%s.zip", arch.manifest.ExportedAt.UTC().Format("20060102"))

	return &dataExportOutput{
		Body: func(c huma.Context) {
			c.SetHeader("Content-Type", "application/zip")
			c.SetHeader("Content-Disposition",
				fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", filename, filename))
			c.SetHeader("Cache-Control", "no-store")
			s.stream(c.Context(), c.BodyWriter(), arch)
		},
	}, nil
}

// gather enumerates every owned entity for the caller into an
// in-memory archive. All errors here precede any response bytes, so
// they become a clean HTTP error envelope.
func (s *exportService) gather(ctx context.Context, u auth.User) (*exportArchive, error) {
	arch := &exportArchive{}

	// Collectors — owner-scoped defensively in-app: CollectorRepo.List
	// has no owner filter and is unrestricted for admin callers, so we
	// never trust the query scope alone for "my data".
	cols, err := s.listAllCollectors(ctx)
	if err != nil {
		return nil, fmt.Errorf("collectors: %w", err)
	}
	for _, c := range cols {
		if c.AuthorID != u.ID {
			continue
		}
		arch.collectors = append(arch.collectors, exportCollectorRecord{
			ID: c.ID, Name: c.Name, Notes: c.Notes,
			CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		})
	}

	// Specimens — OwnerID adds an explicit author_id = caller clause
	// (applied unconditionally, even for admins), and we re-check
	// AuthorID in-app as a belt-and-suspenders guard.
	specs, err := s.listAllSpecimens(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("specimens: %w", err)
	}
	for _, sp := range specs {
		if sp.AuthorID != u.ID {
			continue
		}
		rec, err := s.specimenRecord(ctx, sp)
		if err != nil {
			return nil, fmt.Errorf("specimen %s: %w", sp.ID, err)
		}
		arch.specimens = append(arch.specimens, rec)

		// Photos for this specimen → manifest.images[] + binaries.
		if err := s.collectPhotos(ctx, sp.ID, &arch.manifest.Images); err != nil {
			return nil, fmt.Errorf("specimen %s photos: %w", sp.ID, err)
		}

		// Journal entries for this specimen → journal records +
		// attachment binaries.
		if err := s.collectJournal(ctx, sp.ID, arch); err != nil {
			return nil, fmt.Errorf("specimen %s journal: %w", sp.ID, err)
		}
	}

	// QR sheet — explicitly user-scoped; at most one per user.
	if rec, ok, err := s.qrSheetRecord(ctx, u.ID); err != nil {
		return nil, fmt.Errorf("qrsheet: %w", err)
	} else if ok {
		arch.qrsheets = append(arch.qrsheets, rec)
	}

	arch.manifest = exportManifest{
		SchemaVersion: exportSchemaVersion,
		Application:   "minerals",
		ExportedAt:    time.Now().UTC(),
		ExportedBy:    u.Sub,
		Images:        arch.manifest.Images,
		Counts: exportCounts{
			Collectors:     len(arch.collectors),
			Specimens:      len(arch.specimens),
			JournalEntries: len(arch.journal),
			QRSheets:       len(arch.qrsheets),
			Images:         len(arch.manifest.Images),
			JournalFiles:   len(arch.attachments),
		},
	}
	return arch, nil
}

// specimenRecord converts a domain specimen into its archive record,
// fetching the ordered collector chain.
func (s *exportService) specimenRecord(ctx context.Context, sp domain.Specimen) (exportSpecimenRecord, error) {
	links, err := s.deps.SpecimenCollectors.GetChain(ctx, nil, sp.ID)
	if err != nil {
		return exportSpecimenRecord{}, fmt.Errorf("collector chain: %w", err)
	}
	collectorIDs := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		collectorIDs = append(collectorIDs, l.Collector.ID)
	}
	var typeData json.RawMessage
	if len(sp.TypeData) > 0 {
		typeData = json.RawMessage(sp.TypeData)
	}
	return exportSpecimenRecord{
		ID:                     sp.ID,
		Type:                   sp.Type,
		CatalogNumber:          sp.CatalogNumber,
		Name:                   sp.Name,
		Description:            sp.Description,
		Visibility:             sp.Visibility,
		AcquiredAt:             sp.AcquiredAt,
		AcquiredFrom:           sp.AcquiredFrom,
		PriceCents:             sp.PriceCents,
		SourceNotes:            sp.SourceNotes,
		LocalityText:           sp.LocalityText,
		Locality:               sp.Locality,
		MassG:                  sp.MassG,
		Dimensions:             sp.Dimensions,
		TypeData:               typeData,
		MainImageID:            sp.MainImageID,
		VisibilityPrice:        sp.VisibilityPrice,
		VisibilityAcquiredFrom: sp.VisibilityAcquiredFrom,
		VisibilityImages:       sp.VisibilityImages,
		Tagged:                 sp.Tagged,
		CollectorIDs:           collectorIDs,
		CreatedAt:              sp.CreatedAt,
		UpdatedAt:              sp.UpdatedAt,
	}, nil
}

// collectPhotos appends every photo of a specimen to the manifest
// image list, resolving each photo's backing file for the integrity
// hash, content type, and object key.
func (s *exportService) collectPhotos(ctx context.Context, specimenID uuid.UUID, out *[]exportImageRecord) error {
	var cursor domain.Cursor
	for {
		photos, next, err := s.deps.Photos.ListBySpecimen(ctx, specimenID, domain.Page{
			Limit: exportListPageSize, Cursor: string(cursor),
		})
		if err != nil {
			return err
		}
		for _, p := range photos {
			f, err := s.deps.Files.GetByID(ctx, p.FileID)
			if err != nil {
				return fmt.Errorf("photo %s file: %w", p.ID, err)
			}
			ext := extensionForContentType(f.ContentType)
			path := fmt.Sprintf("images/%s/%s%s", specimenID, p.ID, ext)
			*out = append(*out, exportImageRecord{
				ImageID:     p.ID,
				SpecimenID:  specimenID,
				FileID:      p.FileID,
				Path:        path,
				Kind:        photoKindOrDefault(p.Kind),
				Position:    p.Position,
				Visibility:  p.Visibility,
				TakenAt:     p.TakenAt,
				ContentHash: "sha256:" + f.SHA256,
				ContentType: f.ContentType,
				ByteSize:    f.ByteSize,
				CreatedAt:   p.CreatedAt,
				key:         f.S3Key,
			})
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

// collectJournal appends every journal entry of a specimen (with its
// attachment references) to the archive, and registers each
// attachment binary for streaming.
func (s *exportService) collectJournal(ctx context.Context, specimenID uuid.UUID, arch *exportArchive) error {
	var cursor domain.Cursor
	for {
		entries, next, err := s.deps.JournalEntries.ListBySpecimen(ctx, specimenID, domain.Page{
			Limit: exportListPageSize, Cursor: string(cursor),
		})
		if err != nil {
			return err
		}
		for _, e := range entries {
			rec := exportJournalRecord{
				ID:          e.ID,
				SpecimenID:  e.SpecimenID,
				BodyMD:      e.BodyMD,
				Attachments: []exportAttachmentRecord{},
				CreatedAt:   e.CreatedAt,
				UpdatedAt:   e.UpdatedAt,
			}
			links, err := s.deps.JournalFiles.ListByEntry(ctx, e.ID)
			if err != nil {
				return fmt.Errorf("entry %s attachments: %w", e.ID, err)
			}
			for _, l := range links {
				f, err := s.deps.Files.GetByID(ctx, l.FileID)
				if err != nil {
					return fmt.Errorf("attachment %s file: %w", l.FileID, err)
				}
				ext := extensionForJournalContentType(f.ContentType)
				path := fmt.Sprintf("files/journal/%s/%s%s", e.ID, l.FileID, ext)
				att := exportAttachmentRecord{
					FileID:      l.FileID,
					Position:    l.Position,
					Path:        path,
					ContentHash: "sha256:" + f.SHA256,
					ContentType: f.ContentType,
					ByteSize:    f.ByteSize,
					CreatedAt:   l.CreatedAt,
					key:         f.S3Key,
					entryID:     e.ID,
				}
				rec.Attachments = append(rec.Attachments, att)
				arch.attachments = append(arch.attachments, att)
			}
			arch.journal = append(arch.journal, rec)
		}
		if next == "" {
			return nil
		}
		cursor = next
	}
}

// qrSheetRecord loads the caller's QR sheet and its membership. ok is
// false (with nil error) when the user has no sheet.
func (s *exportService) qrSheetRecord(ctx context.Context, userID uuid.UUID) (exportQRSheetRecord, bool, error) {
	sheet, err := s.deps.QRSheets.GetByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrQRSheetNotFound) {
			return exportQRSheetRecord{}, false, nil
		}
		return exportQRSheetRecord{}, false, err
	}
	members, err := s.deps.QRSheets.ListSpecimens(ctx, sheet.ID)
	if err != nil {
		return exportQRSheetRecord{}, false, fmt.Errorf("members: %w", err)
	}
	out := exportQRSheetRecord{
		ID:        sheet.ID,
		Template:  sheet.Template,
		Specimens: make([]exportQRSheetMember, 0, len(members)),
		CreatedAt: sheet.CreatedAt,
		UpdatedAt: sheet.UpdatedAt,
	}
	for _, m := range members {
		out.Specimens = append(out.Specimens, exportQRSheetMember{
			SpecimenID: m.SpecimenID, Position: m.Position, AddedAt: m.AddedAt,
		})
	}
	return out, true, nil
}

// listAllCollectors walks the collector list to exhaustion. Scoping
// to the caller happens in gather (in-app AuthorID filter).
func (s *exportService) listAllCollectors(ctx context.Context) ([]domain.Collector, error) {
	var out []domain.Collector
	var cursor domain.Cursor
	for {
		batch, next, err := s.deps.Collectors.List(ctx, domain.CollectorFilter{}, domain.Page{
			Limit: exportListPageSize, Cursor: string(cursor),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

// listAllSpecimens walks the owner-scoped specimen list to exhaustion.
func (s *exportService) listAllSpecimens(ctx context.Context, ownerID uuid.UUID) ([]domain.Specimen, error) {
	var out []domain.Specimen
	var cursor domain.Cursor
	for {
		batch, next, err := s.deps.Specimens.List(ctx, domain.SpecimenFilter{OwnerID: &ownerID}, domain.Page{
			Limit: exportListPageSize, Cursor: string(cursor),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

// stream writes the assembled archive to w as a ZIP. manifest.json is
// written first (design §2.2 — import reads the schema version before
// processing), then the JSONL data entries, then the image and
// attachment binaries streamed straight from object storage. Errors
// after the first write can only be logged: the 200 status + headers
// are already committed.
func (s *exportService) stream(ctx context.Context, w io.Writer, arch *exportArchive) {
	zw := zip.NewWriter(w)
	defer func() {
		if err := zw.Close(); err != nil {
			slog.ErrorContext(ctx, "export: zip close failed", "err", err)
		}
	}()

	if err := writeZipJSON(zw, "manifest.json", arch.manifest); err != nil {
		slog.ErrorContext(ctx, "export: write manifest failed", "err", err)
		return
	}

	if err := writeZipJSONL(zw, "data/collectors.jsonl", arch.collectors); err != nil {
		slog.ErrorContext(ctx, "export: write collectors failed", "err", err)
		return
	}
	if err := writeZipJSONL(zw, "data/specimens.jsonl", arch.specimens); err != nil {
		slog.ErrorContext(ctx, "export: write specimens failed", "err", err)
		return
	}
	if err := writeZipJSONL(zw, "data/journal_entries.jsonl", arch.journal); err != nil {
		slog.ErrorContext(ctx, "export: write journal failed", "err", err)
		return
	}
	if err := writeZipJSONL(zw, "data/qrsheets.jsonl", arch.qrsheets); err != nil {
		slog.ErrorContext(ctx, "export: write qrsheets failed", "err", err)
		return
	}

	for _, img := range arch.manifest.Images {
		if err := s.streamObject(ctx, zw, img.Path, img.key); err != nil {
			slog.ErrorContext(ctx, "export: stream image failed",
				"image_id", img.ImageID, "key", img.key, "err", err)
			return
		}
	}
	for _, att := range arch.attachments {
		if err := s.streamObject(ctx, zw, att.Path, att.key); err != nil {
			slog.ErrorContext(ctx, "export: stream attachment failed",
				"file_id", att.FileID, "key", att.key, "err", err)
			return
		}
	}
}

// streamObject copies one object from storage into a zip entry. The
// entry is stored (not re-deflated) — image/jpeg, webp, png and PDFs
// are already compressed, so deflate would burn CPU for no gain.
func (s *exportService) streamObject(ctx context.Context, zw *zip.Writer, path, key string) error {
	wr, err := zw.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Store})
	if err != nil {
		return err
	}
	body, _, err := s.deps.Storage.Download(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	if _, err := io.Copy(wr, body); err != nil {
		return err
	}
	return nil
}

// writeZipJSON writes one pretty-printed JSON document as a zip entry.
func writeZipJSON(zw *zip.Writer, name string, v any) error {
	wr, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(wr)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeZipJSONL writes a slice as newline-delimited JSON (one object
// per line) into a zip entry. An empty slice yields an empty file —
// the entry is always present so import can rely on its existence.
func writeZipJSONL[T any](zw *zip.Writer, name string, items []T) error {
	wr, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(wr) // Encode appends a trailing '\n' per item — exactly JSONL.
	for i := range items {
		if err := enc.Encode(items[i]); err != nil {
			return err
		}
	}
	return nil
}

// photoKindOrDefault mirrors toPhotoView: fakes that don't round-trip
// through Postgres can leave Kind empty; emit the v1 default so the
// archive shape is always populated.
func photoKindOrDefault(k domain.PhotoKind) domain.PhotoKind {
	if k == "" {
		return domain.PhotoKindVisible
	}
	return k
}
