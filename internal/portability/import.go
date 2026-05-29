package portability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage/imageproc"
)

// maxReportedDetails caps the per-error detail list so a pathological
// archive (thousands of broken references) can't produce an unbounded
// response or log line. The count of suppressed items is appended.
const maxReportedDetails = 50

// ObjectStore is the slice of the storage client the import engine uses
// for the post-commit binary upload. *storage.Client satisfies it.
type ObjectStore interface {
	UploadIfNotExists(ctx context.Context, key string, body io.Reader, contentType string) error
	Upload(ctx context.Context, key string, body io.Reader, contentType string) error
	Delete(ctx context.Context, key string) error
}

// CatalogLookup returns the set of catalog_numbers already in use by the
// importer, so the engine can detect collisions during the dry-run
// before any write. Implemented in internal/db over the pool; stubbed in
// tests.
type CatalogLookup func(ctx context.Context, authorID uuid.UUID) (map[string]struct{}, error)

// Deps wires the import engine. Every repo is the consumer-side domain
// interface so the engine is unit-testable with in-memory fakes; the
// repos' Create methods re-home each row to auth.FromContext(ctx) by
// design, which is exactly the importer-ownership semantics import needs.
type Deps struct {
	Collectors         domain.CollectorRepo
	Files              domain.FileRepo
	Specimens          domain.SpecimenRepo
	Photos             domain.PhotoRepo
	Journal            domain.JournalEntryRepo
	JournalFiles       domain.JournalEntryFileRepo
	SpecimenCollectors domain.SpecimenCollectorRepo
	QRSheets           domain.QRSheetRepo
	Storage            ObjectStore
	// RunInTx runs fn inside one DB transaction. The whole archive
	// commits atomically (design §4.2 — strict all-or-nothing v1).
	RunInTx func(ctx context.Context, fn func(tx domain.Tx) error) error
	// CatalogNumbers reports the importer's existing catalog numbers for
	// collision detection. Optional: nil disables pre-flight detection
	// (the DB unique constraint still protects against clobbering, but
	// the commit would then fail rather than suffix-and-continue).
	CatalogNumbers CatalogLookup
}

// Importer runs the two-phase import (validate → commit) of a portable
// archive, re-homing every row to the caller and remapping all IDs.
type Importer struct {
	deps Deps
}

// NewImporter constructs an Importer over deps.
func NewImporter(deps Deps) *Importer { return &Importer{deps: deps} }

// parsed is the validated, in-memory view produced by the validation
// phase and consumed by commit. It also carries the resolved
// catalog-number mapping (specimen archive-id -> catalog to write) so
// commit applies the same conflict resolution the dry-run reported.
type parsed struct {
	archive      *Archive
	collectors   []CollectorRecord
	files        []FileRecord
	specimens    []SpecimenRecord
	photos       []PhotoRecord
	journal      []JournalEntryRecord
	qrsheets     []QRSheetRecord
	fileByID     map[string]FileRecord
	photoFileIDs map[string]struct{} // file ids referenced by a photo (need image variants)
	resolvedCat  map[string]*string  // specimen archive-id -> catalog_number to persist
	skipQRSheet  bool
	conflicts    []Conflict
	warnings     []string
}

// Run executes the import. When dryRun is true it validates and returns
// the report without writing anything. Otherwise it validates, then —
// only if validation passes — commits in one transaction and uploads the
// file binaries best-effort. A *ValidationError means the archive is
// unimportable; nothing was written.
func (imp *Importer) Run(ctx context.Context, raw []byte, dryRun bool) (*Report, error) {
	userID := auth.FromContext(ctx).ID

	p, err := imp.validate(ctx, raw, userID)
	if err != nil {
		return nil, err
	}

	report := &Report{
		SchemaVersion: SchemaVersion,
		DryRun:        dryRun,
		Counts: Counts{
			Collectors:     len(p.collectors),
			Files:          len(p.files),
			Specimens:      len(p.specimens),
			Photos:         len(p.photos),
			JournalEntries: len(p.journal),
		},
		Conflicts:     p.conflicts,
		Warnings:      p.warnings,
		ImageFailures: []string{},
	}
	if len(p.qrsheets) > 0 && !p.skipQRSheet {
		report.Counts.QRSheets = 1
	}
	// Normalize nil slices so the JSON response always has arrays.
	if report.Conflicts == nil {
		report.Conflicts = []Conflict{}
	}
	if report.Warnings == nil {
		report.Warnings = []string{}
	}

	if dryRun {
		return report, nil
	}

	failures, err := imp.commit(ctx, userID, p)
	if err != nil {
		return nil, err
	}
	report.Committed = true
	report.ImageFailures = failures
	return report, nil
}

// validate parses and structurally validates the archive, detects
// catalog-number conflicts, and resolves them — without writing. The
// returned parsed view is reused by commit so parsing happens once.
func (imp *Importer) validate(ctx context.Context, raw []byte, userID uuid.UUID) (*parsed, error) {
	a, err := OpenArchive(raw)
	if err != nil {
		return nil, err
	}
	mf := a.Manifest()
	if mf.Application != Application {
		return nil, &ValidationError{
			Code:    CodeMalformedArchive,
			Message: fmt.Sprintf("archive application %q is not %q", mf.Application, Application),
		}
	}
	if mf.SchemaVersion <= 0 || mf.SchemaVersion > SchemaVersion {
		return nil, &ValidationError{
			Code: CodeIncompatibleSchema,
			Message: fmt.Sprintf(
				"archive schema version %d is not supported by this server (max %d)",
				mf.SchemaVersion, SchemaVersion),
		}
	}

	p := &parsed{
		archive:      a,
		fileByID:     map[string]FileRecord{},
		photoFileIDs: map[string]struct{}{},
		resolvedCat:  map[string]*string{},
	}
	if p.collectors, err = decodeJSONL[CollectorRecord](a, CollectorsPath); err != nil {
		return nil, err
	}
	if p.files, err = decodeJSONL[FileRecord](a, FilesPath); err != nil {
		return nil, err
	}
	if p.specimens, err = decodeJSONL[SpecimenRecord](a, SpecimensPath); err != nil {
		return nil, err
	}
	if p.photos, err = decodeJSONL[PhotoRecord](a, PhotosPath); err != nil {
		return nil, err
	}
	if p.journal, err = decodeJSONL[JournalEntryRecord](a, JournalEntriesPath); err != nil {
		return nil, err
	}
	if p.qrsheets, err = decodeJSONL[QRSheetRecord](a, QRSheetsPath); err != nil {
		return nil, err
	}

	if err := imp.checkStructure(p); err != nil {
		return nil, err
	}
	if err := imp.verifyBinaries(p); err != nil {
		return nil, err
	}
	if err := imp.resolveConflicts(ctx, userID, p); err != nil {
		return nil, err
	}
	return p, nil
}

// checkStructure enforces id uniqueness, enum/field validity, and
// referential integrity within the archive. It accumulates problems and
// returns a single ValidationError listing them (capped).
func (imp *Importer) checkStructure(p *parsed) error {
	var ref, inv detailSet

	collectorIDs := map[string]struct{}{}
	for _, c := range p.collectors {
		if !validUUID(c.ID) {
			inv.add(fmt.Sprintf("collector id %q is not a UUID", c.ID))
			continue
		}
		if dup(collectorIDs, c.ID) {
			inv.add(fmt.Sprintf("duplicate collector id %q", c.ID))
		}
		if strings.TrimSpace(c.Name) == "" {
			inv.add(fmt.Sprintf("collector %q has an empty name", c.ID))
		}
	}

	fileIDs := map[string]struct{}{}
	for _, f := range p.files {
		if !validUUID(f.ID) {
			inv.add(fmt.Sprintf("file id %q is not a UUID", f.ID))
			continue
		}
		if dup(fileIDs, f.ID) {
			inv.add(fmt.Sprintf("duplicate file id %q", f.ID))
		}
		p.fileByID[f.ID] = f
	}

	specimenIDs := map[string]struct{}{}
	for _, s := range p.specimens {
		if !validUUID(s.ID) {
			inv.add(fmt.Sprintf("specimen id %q is not a UUID", s.ID))
			continue
		}
		if dup(specimenIDs, s.ID) {
			inv.add(fmt.Sprintf("duplicate specimen id %q", s.ID))
		}
		if !validSpecimenType(s.Type) {
			inv.add(fmt.Sprintf("specimen %q has invalid type %q", s.ID, s.Type))
		}
		if !validVisibility(s.Visibility) {
			inv.add(fmt.Sprintf("specimen %q has invalid visibility %q", s.ID, s.Visibility))
		}
		for label, v := range map[string]*string{
			"visibility_price":         s.VisibilityPrice,
			"visibility_acquired_from": s.VisibilityAcquiredFrom,
			"visibility_images":        s.VisibilityImages,
		} {
			if v != nil && !validVisibility(*v) {
				inv.add(fmt.Sprintf("specimen %q has invalid %s %q", s.ID, label, *v))
			}
		}
		if validSpecimenType(s.Type) {
			if _, err := validateTypeData(domain.SpecimenType(s.Type), s.TypeData); err != nil {
				inv.add(fmt.Sprintf("specimen %q type_data invalid: %v", s.ID, err))
			}
		}
		if s.MainImageFileID != nil {
			if _, ok := p.fileByID[*s.MainImageFileID]; !ok {
				ref.add(fmt.Sprintf("specimen %q main_image_file_id %q not in archive", s.ID, *s.MainImageFileID))
			}
		}
		for _, cid := range s.CollectorIDs {
			if _, ok := collectorIDs[cid]; !ok {
				ref.add(fmt.Sprintf("specimen %q references unknown collector %q", s.ID, cid))
			}
		}
	}

	photoIDs := map[string]struct{}{}
	for _, ph := range p.photos {
		if !validUUID(ph.ID) {
			inv.add(fmt.Sprintf("photo id %q is not a UUID", ph.ID))
			continue
		}
		if dup(photoIDs, ph.ID) {
			inv.add(fmt.Sprintf("duplicate photo id %q", ph.ID))
		}
		if ph.Kind != "" && !domain.PhotoKind(ph.Kind).IsValid() {
			inv.add(fmt.Sprintf("photo %q has invalid kind %q", ph.ID, ph.Kind))
		}
		if ph.Visibility != nil && !validVisibility(*ph.Visibility) {
			inv.add(fmt.Sprintf("photo %q has invalid visibility %q", ph.ID, *ph.Visibility))
		}
		if _, ok := specimenIDs[ph.SpecimenID]; !ok {
			ref.add(fmt.Sprintf("photo %q references unknown specimen %q", ph.ID, ph.SpecimenID))
		}
		if _, ok := fileIDs[ph.FileID]; !ok {
			ref.add(fmt.Sprintf("photo %q references unknown file %q", ph.ID, ph.FileID))
		} else {
			p.photoFileIDs[ph.FileID] = struct{}{}
		}
	}

	entryIDs := map[string]struct{}{}
	for _, e := range p.journal {
		if !validUUID(e.ID) {
			inv.add(fmt.Sprintf("journal entry id %q is not a UUID", e.ID))
			continue
		}
		if dup(entryIDs, e.ID) {
			inv.add(fmt.Sprintf("duplicate journal entry id %q", e.ID))
		}
		if _, ok := specimenIDs[e.SpecimenID]; !ok {
			ref.add(fmt.Sprintf("journal entry %q references unknown specimen %q", e.ID, e.SpecimenID))
		}
		for _, at := range e.Attachments {
			if _, ok := fileIDs[at.FileID]; !ok {
				ref.add(fmt.Sprintf("journal entry %q references unknown file %q", e.ID, at.FileID))
			}
		}
	}

	if len(p.qrsheets) > 1 {
		inv.add(fmt.Sprintf("archive carries %d QR sheets; at most one is allowed", len(p.qrsheets)))
	}
	for _, q := range p.qrsheets {
		if !validUUID(q.ID) {
			inv.add(fmt.Sprintf("qr sheet id %q is not a UUID", q.ID))
		}
		if _, ok := domain.QRSheetTemplateCapacity(domain.QRSheetTemplate(q.Template)); !ok {
			inv.add(fmt.Sprintf("qr sheet %q has unknown template %q", q.ID, q.Template))
		}
		for _, m := range q.Specimens {
			if _, ok := specimenIDs[m.SpecimenID]; !ok {
				ref.add(fmt.Sprintf("qr sheet %q references unknown specimen %q", q.ID, m.SpecimenID))
			}
		}
	}

	if !inv.empty() {
		return &ValidationError{Code: CodeInvalidRecord, Message: "archive contains invalid records", Details: inv.list()}
	}
	if !ref.empty() {
		return &ValidationError{Code: CodeReference, Message: "archive contains broken cross-references", Details: ref.list()}
	}
	return nil
}

// verifyBinaries checks that every file's binary is present in the
// archive and matches its declared size and hash.
func (imp *Importer) verifyBinaries(p *parsed) error {
	var bad detailSet
	for _, f := range p.files {
		path := f.Path
		if path == "" {
			path = FileBinaryPrefix + f.ID
		}
		if _, err := p.archive.OpenFileBinary(path, f.SHA256, f.ByteSize); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				bad.add(ve.Message)
				continue
			}
			bad.add(err.Error())
		}
	}
	if !bad.empty() {
		return &ValidationError{Code: CodeIntegrity, Message: "archive file binaries failed verification", Details: bad.list()}
	}
	return nil
}

// resolveConflicts loads the importer's existing catalog numbers and
// resolves every colliding imported catalog_number to a unique suffixed
// value, recording each as a Conflict. It also flags whether a QR sheet
// already exists (in which case the archive's sheet is skipped, never
// merged — import must not modify pre-existing data).
func (imp *Importer) resolveConflicts(ctx context.Context, userID uuid.UUID, p *parsed) error {
	taken := map[string]struct{}{}
	if imp.deps.CatalogNumbers != nil {
		existing, err := imp.deps.CatalogNumbers(ctx, userID)
		if err != nil {
			return fmt.Errorf("import: load existing catalog numbers: %w", err)
		}
		for k := range existing {
			taken[k] = struct{}{}
		}
	}
	for _, s := range p.specimens {
		if s.CatalogNumber == nil || strings.TrimSpace(*s.CatalogNumber) == "" {
			p.resolvedCat[s.ID] = s.CatalogNumber
			continue
		}
		orig := *s.CatalogNumber
		if _, clash := taken[orig]; !clash {
			taken[orig] = struct{}{}
			v := orig
			p.resolvedCat[s.ID] = &v
			continue
		}
		resolved := uniqueCatalog(orig, taken)
		taken[resolved] = struct{}{}
		v := resolved
		p.resolvedCat[s.ID] = &v
		p.conflicts = append(p.conflicts, Conflict{
			Kind:       ConflictCatalogNumber,
			SpecimenID: s.ID,
			Detail:     fmt.Sprintf("catalog number %q already in use; imported as %q", orig, resolved),
		})
	}

	if len(p.qrsheets) > 0 && imp.deps.QRSheets != nil {
		if _, err := imp.deps.QRSheets.GetByUser(ctx, userID); err == nil {
			p.skipQRSheet = true
			p.warnings = append(p.warnings,
				"a QR sheet already exists for your account; the archived QR sheet was skipped to avoid modifying existing data")
		} else if !errors.Is(err, domain.ErrQRSheetNotFound) {
			return fmt.Errorf("import: probe existing qr sheet: %w", err)
		}
	}
	return nil
}

// commit writes the whole archive in one transaction (re-homing via the
// repos' auth-context author resolution, remapping every id), then
// uploads the file binaries best-effort. It returns the list of binaries
// whose upload failed (DB rows still committed — re-importable).
func (imp *Importer) commit(ctx context.Context, userID uuid.UUID, p *parsed) ([]string, error) {
	collectorMap := map[string]uuid.UUID{}
	fileMap := map[string]uuid.UUID{}
	specimenMap := map[string]uuid.UUID{}

	txErr := imp.deps.RunInTx(ctx, func(tx domain.Tx) error {
		// 1. collectors (no archive dependencies).
		for _, c := range p.collectors {
			id := domain.NewID()
			collectorMap[c.ID] = id
			if err := imp.deps.Collectors.Create(ctx, tx, domain.Collector{
				ID: id, Name: c.Name, Notes: c.Notes,
				CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
			}); err != nil {
				return fmt.Errorf("create collector: %w", err)
			}
		}

		// 2. files — before specimens, because specimens.main_image_id
		//    is an FK into files. S3 key is freshly namespaced on the id.
		for _, f := range p.files {
			id := domain.NewID()
			fileMap[f.ID] = id
			if err := imp.deps.Files.Create(ctx, tx, domain.File{
				ID: id, S3Key: FileBinaryPrefix + id.String(),
				ContentType: f.ContentType, ByteSize: f.ByteSize,
				SHA256: f.SHA256, UploadedAt: f.UploadedAt,
			}); err != nil {
				return fmt.Errorf("create file: %w", err)
			}
		}

		// 3. specimens — remap main_image_file_id, apply resolved
		//    (conflict-free) catalog number, preserve all other fields.
		for _, s := range p.specimens {
			id := domain.NewID()
			specimenMap[s.ID] = id
			var mainImg *uuid.UUID
			if s.MainImageFileID != nil {
				if nid, ok := fileMap[*s.MainImageFileID]; ok {
					mainImg = &nid
				}
			}
			typeData, err := validateTypeData(domain.SpecimenType(s.Type), s.TypeData)
			if err != nil {
				return fmt.Errorf("specimen type_data: %w", err)
			}
			spec := domain.Specimen{
				ID:                     id,
				Type:                   domain.SpecimenType(s.Type),
				CatalogNumber:          p.resolvedCat[s.ID],
				Name:                   s.Name,
				Description:            s.Description,
				Visibility:             domain.Visibility(s.Visibility),
				AcquiredAt:             s.AcquiredAt,
				AcquiredFrom:           s.AcquiredFrom,
				PriceCents:             s.PriceCents,
				SourceNotes:            s.SourceNotes,
				LocalityText:           s.LocalityText,
				Locality:               decodeLocality(s.Locality),
				MassG:                  s.MassG,
				Dimensions:             decodeDimensions(s.Dimensions),
				TypeData:               typeData,
				MainImageID:            mainImg,
				VisibilityPrice:        visPtr(s.VisibilityPrice),
				VisibilityAcquiredFrom: visPtr(s.VisibilityAcquiredFrom),
				VisibilityImages:       visPtr(s.VisibilityImages),
				Tagged:                 s.Tagged,
				CreatedAt:              s.CreatedAt,
				UpdatedAt:              s.UpdatedAt,
			}
			if err := imp.deps.Specimens.Create(ctx, tx, spec); err != nil {
				return fmt.Errorf("create specimen: %w", err)
			}
		}

		// 4. photos — remap specimen_id + file_id.
		for _, ph := range p.photos {
			kind := domain.PhotoKind(ph.Kind)
			if kind == "" {
				kind = domain.PhotoKindVisible
			}
			if err := imp.deps.Photos.Create(ctx, tx, domain.Photo{
				ID:         domain.NewID(),
				SpecimenID: specimenMap[ph.SpecimenID],
				FileID:     fileMap[ph.FileID],
				Kind:       kind,
				TakenAt:    ph.TakenAt,
				Position:   ph.Position,
				Visibility: visPtr(ph.Visibility),
				CreatedAt:  ph.CreatedAt,
			}); err != nil {
				return fmt.Errorf("create photo: %w", err)
			}
		}

		// 5. journal entries + their attachment links.
		for _, e := range p.journal {
			entryID := domain.NewID()
			if err := imp.deps.Journal.Create(ctx, tx, domain.JournalEntry{
				ID: entryID, SpecimenID: specimenMap[e.SpecimenID],
				BodyMD: e.BodyMD, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
			}); err != nil {
				return fmt.Errorf("create journal entry: %w", err)
			}
			for _, at := range e.Attachments {
				if err := imp.deps.JournalFiles.Create(ctx, tx, domain.JournalEntryFile{
					EntryID: entryID, FileID: fileMap[at.FileID],
					Position: at.Position, CreatedAt: at.CreatedAt,
				}); err != nil {
					return fmt.Errorf("create journal attachment: %w", err)
				}
			}
		}

		// 6. specimen↔collector chains (ordered, position = index+1).
		for _, s := range p.specimens {
			if len(s.CollectorIDs) == 0 {
				continue
			}
			ids := make([]uuid.UUID, 0, len(s.CollectorIDs))
			for _, cid := range s.CollectorIDs {
				ids = append(ids, collectorMap[cid])
			}
			if err := imp.deps.SpecimenCollectors.ReplaceChain(ctx, tx, specimenMap[s.ID], ids); err != nil {
				return fmt.Errorf("create specimen collector chain: %w", err)
			}
		}

		// 7. QR sheet (one per user) + membership — only when the
		//    importer has none (resolveConflicts set skipQRSheet otherwise).
		if len(p.qrsheets) > 0 && !p.skipQRSheet {
			q := p.qrsheets[0]
			if err := imp.deps.QRSheets.Create(ctx, tx, domain.QRSheet{
				ID: domain.NewID(), UserID: userID,
				Template:  domain.QRSheetTemplate(q.Template),
				CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt,
			}); err != nil {
				return fmt.Errorf("create qr sheet: %w", err)
			}
			members := append([]QRSheetSpecimen(nil), q.Specimens...)
			sort.SliceStable(members, func(i, j int) bool { return members[i].Position < members[j].Position })
			for _, m := range members {
				if err := imp.deps.QRSheets.AddSpecimen(ctx, tx, userID, specimenMap[m.SpecimenID], m.AddedAt); err != nil {
					return fmt.Errorf("add qr sheet specimen: %w", err)
				}
			}
		}
		return nil
	})
	if txErr != nil {
		return nil, fmt.Errorf("import: commit transaction: %w", txErr)
	}

	return imp.uploadBinaries(ctx, p, fileMap), nil
}

// uploadBinaries uploads each file's original bytes (and, for photo
// images, regenerated display/thumbnail variants) to the object store
// under the importer's freshly-namespaced key. Best-effort by design
// (§4.2): the DB rows are already committed, so a failure here only
// leaves a missing object — recorded for retry, never failing the
// import. Returns the keys that failed.
func (imp *Importer) uploadBinaries(ctx context.Context, p *parsed, fileMap map[string]uuid.UUID) []string {
	if imp.deps.Storage == nil {
		return []string{}
	}
	failures := []string{}
	for _, f := range p.files {
		newID, ok := fileMap[f.ID]
		if !ok {
			continue
		}
		path := f.Path
		if path == "" {
			path = FileBinaryPrefix + f.ID
		}
		data, err := p.archive.RawFile(path)
		if err != nil {
			failures = append(failures, path)
			slog.ErrorContext(ctx, "import: re-read file binary failed", "path", path, "err", err)
			continue
		}
		key := FileBinaryPrefix + newID.String()
		if err := imp.deps.Storage.UploadIfNotExists(ctx, key, bytes.NewReader(data), f.ContentType); err != nil {
			failures = append(failures, key)
			slog.ErrorContext(ctx, "import: upload original failed", "key", key, "err", err)
			continue
		}
		// Variants only for files backing a photo (journal attachments
		// such as PDFs are served as originals and need none).
		if _, isPhoto := p.photoFileIDs[f.ID]; !isPhoto {
			continue
		}
		variants, err := imageproc.Generate(data, f.ContentType)
		if err != nil {
			// Non-fatal: the original is stored; variants regenerate
			// later. Surface it so the gap is visible.
			slog.WarnContext(ctx, "import: variant generation failed; original stored without variants",
				"key", key, "content_type", f.ContentType, "err", err)
			failures = append(failures, key+" (variants)")
			continue
		}
		if err := imp.deps.Storage.Upload(ctx, key+".display.jpg", bytes.NewReader(variants.Display), "image/jpeg"); err != nil {
			failures = append(failures, key+".display.jpg")
			slog.ErrorContext(ctx, "import: upload display variant failed", "key", key, "err", err)
		}
		if err := imp.deps.Storage.Upload(ctx, key+".thumb.jpg", bytes.NewReader(variants.Thumbnail), "image/jpeg"); err != nil {
			failures = append(failures, key+".thumb.jpg")
			slog.ErrorContext(ctx, "import: upload thumb variant failed", "key", key, "err", err)
		}
	}
	return failures
}

// --- small helpers -------------------------------------------------------

func validUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil && s != uuid.Nil.String()
}

func dup(set map[string]struct{}, k string) bool {
	if _, ok := set[k]; ok {
		return true
	}
	set[k] = struct{}{}
	return false
}

func validVisibility(v string) bool {
	switch domain.Visibility(v) {
	case domain.VisibilityPrivate, domain.VisibilityUnlisted, domain.VisibilityPublic:
		return true
	}
	return false
}

func validSpecimenType(t string) bool {
	switch domain.SpecimenType(t) {
	case domain.SpecimenMineral, domain.SpecimenRock, domain.SpecimenMeteorite, domain.SpecimenFossil:
		return true
	}
	return false
}

func visPtr(s *string) *domain.Visibility {
	if s == nil {
		return nil
	}
	v := domain.Visibility(*s)
	return &v
}

func decodeLocality(raw json.RawMessage) *domain.Locality {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var l domain.Locality
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil
	}
	return &l
}

func decodeDimensions(raw json.RawMessage) *domain.Dimensions {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var d domain.Dimensions
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil
	}
	return &d
}

// uniqueCatalog returns orig with a deterministic " (import N)" suffix
// not present in taken.
func uniqueCatalog(orig string, taken map[string]struct{}) string {
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s (import %d)", orig, i)
		if _, ok := taken[cand]; !ok {
			return cand
		}
	}
}

// validateTypeData mirrors the specimen create path: it unmarshals the
// raw type_data into the struct selected by t, runs its Validate, and
// returns the canonical JSON. Empty/null becomes `{}`.
func validateTypeData(t domain.SpecimenType, raw json.RawMessage) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return []byte(`{}`), nil
	}
	switch t {
	case domain.SpecimenMineral:
		var d domain.MineralData
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("not a MineralData shape: %w", err)
		}
		if err := d.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(d)
	case domain.SpecimenRock:
		var d domain.RockData
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("not a RockData shape: %w", err)
		}
		if err := d.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(d)
	case domain.SpecimenMeteorite:
		var d domain.MeteoriteData
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("not a MeteoriteData shape: %w", err)
		}
		if err := d.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(d)
	case domain.SpecimenFossil:
		var d domain.FossilData
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("not a FossilData shape: %w", err)
		}
		if err := d.Validate(); err != nil {
			return nil, err
		}
		return json.Marshal(d)
	}
	return nil, fmt.Errorf("unknown specimen type %q", t)
}

// detailSet accumulates problem strings while capping the reported count.
type detailSet struct {
	items   []string
	dropped int
}

func (d *detailSet) add(s string) {
	if len(d.items) < maxReportedDetails {
		d.items = append(d.items, s)
		return
	}
	d.dropped++
}

func (d *detailSet) empty() bool { return len(d.items) == 0 && d.dropped == 0 }

func (d *detailSet) list() []string {
	if d.dropped == 0 {
		return d.items
	}
	return append(d.items, fmt.Sprintf("… and %d more", d.dropped))
}
