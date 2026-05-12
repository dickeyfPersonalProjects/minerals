// Package domain holds the core types, enums, and repository
// interfaces that anchor the v1 minerals app. Implementations live in
// internal/db (per CONTRACT.md §11). No SQL here, no HTTP here.
package domain

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SpecimenType is the discriminator for a specimens row.
type SpecimenType string

// Allowed SpecimenType values, matching the CHECK constraint in the
// specimens table.
const (
	SpecimenMineral   SpecimenType = "mineral"
	SpecimenRock      SpecimenType = "rock"
	SpecimenMeteorite SpecimenType = "meteorite"
	SpecimenFossil    SpecimenType = "fossil"
)

// Visibility controls who can read a specimen. v1 enforces only the
// stub-user-can-read-everything path; public sharing lands later.
type Visibility string

// Allowed Visibility values, matching the CHECK constraint in the
// specimens table.
const (
	VisibilityPrivate  Visibility = "private"
	VisibilityUnlisted Visibility = "unlisted"
	VisibilityPublic   Visibility = "public"
)

// NewID returns a fresh UUIDv7 for a new database row. Per
// CONTRACT.md §11 every PK we generate uses UUIDv7. The function
// panics on RNG failure (the only error uuid.NewV7 returns).
func NewID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Errorf("uuid v7 generation: %w", err))
	}
	return id
}

// Tx abstracts pgxpool.Pool and pgx.Tx so the same repo method can run
// inside or outside a transaction. Service-layer code passes whichever
// it has on hand.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Sentinel errors returned from repo boundaries (per §11). Handlers
// branch on these via errors.Is, never on pgx internals.
var (
	ErrSpecimenNotFound          = fmt.Errorf("specimen not found")
	ErrSpecimenConflict          = fmt.Errorf("specimen conflict")
	ErrSpecimenReferenced        = fmt.Errorf("specimen referenced")
	ErrSpecimenTypeImmutable     = fmt.Errorf("specimen type immutable")
	ErrSpecimenTypeDataInvalid   = fmt.Errorf("specimen type_data invalid")
	ErrPhotoNotFound             = fmt.Errorf("photo not found")
	ErrPhotoConflict             = fmt.Errorf("photo conflict")
	ErrJournalEntryNotFound      = fmt.Errorf("journal entry not found")
	ErrJournalEntryConflict      = fmt.Errorf("journal entry conflict")
	ErrJournalAttachmentNotFound = fmt.Errorf("journal attachment not found")
	ErrFileNotFound              = fmt.Errorf("file not found")
	ErrFileConflict              = fmt.Errorf("file conflict")
	ErrCollectorNotFound         = fmt.Errorf("collector not found")
	ErrCollectorConflict         = fmt.Errorf("collector conflict")
	ErrCollectorReferenced       = fmt.Errorf("collector referenced")
	ErrMineralSpeciesNotFound    = fmt.Errorf("mineral species not found")
	ErrMineralSpeciesConflict    = fmt.Errorf("mineral species conflict")
	ErrQRSheetNotFound           = fmt.Errorf("qr sheet not found")
	ErrQRSheetConflict           = fmt.Errorf("qr sheet conflict")
	ErrQRSheetSpecimenNotFound   = fmt.Errorf("qr sheet specimen not found")
	ErrQRSheetTemplateInvalid    = fmt.Errorf("qr sheet template invalid")
)

// Page is the cursor-pagination request shape (per §10).
type Page struct {
	Limit  int
	Cursor string
}

// Cursor is the opaque pagination cursor returned by list queries.
type Cursor string

// CollectorFilter holds the v1 list filters for collectors. Only
// Query (free-form name search via ILIKE) is supported in v1.
type CollectorFilter struct {
	Query string
}

// SpecimenFilter holds the v1 list filters (per design §4.4).
type SpecimenFilter struct {
	Type             *SpecimenType
	Visibility       *Visibility
	CollectorID      *uuid.UUID
	Query            string
	HasCatalogNumber *bool
	AcquiredAfter    *time.Time
	AcquiredBefore   *time.Time
}

// Locality is the structured side of specimens.locality. All fields
// are optional; the free-form mirror lives in Specimen.LocalityText.
type Locality struct {
	Country  string  `json:"country,omitempty"`
	Region   string  `json:"region,omitempty"`
	Site     string  `json:"site,omitempty"`
	Lat      float64 `json:"lat,omitempty"`
	Lon      float64 `json:"lon,omitempty"`
	MindatID string  `json:"mindat_id,omitempty"`
}

// Dimensions is the structured side of specimens.dimensions.
type Dimensions struct {
	LengthMM float64 `json:"length_mm,omitempty"`
	WidthMM  float64 `json:"width_mm,omitempty"`
	HeightMM float64 `json:"height_mm,omitempty"`
}

// Specimen mirrors the schema in design §2.
type Specimen struct {
	ID            uuid.UUID
	Type          SpecimenType
	CatalogNumber *string
	Name          string
	Description   string
	Visibility    Visibility
	AuthorID      uuid.UUID
	AcquiredAt    *time.Time
	AcquiredFrom  *string
	PriceCents    *int64
	SourceNotes   *string
	LocalityText  *string
	Locality      *Locality
	MassG         *float64
	Dimensions    *Dimensions
	TypeData      []byte // raw JSON; service unmarshals into MineralData/RockData/MeteoriteData/FossilData
	// MainImageID is the optional file id (NOT photo id) of the
	// photo the user has designated as this specimen's primary
	// image (mi-m8q). NULL means fall back to the first photo by
	// position. The DB enforces ON DELETE SET NULL against files(id),
	// so deleting the underlying file reverts to the fallback.
	MainImageID *uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PhotoKind discriminates the lighting condition a photo was taken
// under (mi-5b6, hq-6lrd). The vocabulary is closed: 'visible'
// (ordinary daylight / studio), three wavelength-specific UV
// variants — 'uv_sw' (shortwave, 254 nm), 'uv_mw' (midwave,
// ~312 nm), 'uv_lw' (longwave, ~365 nm) — and 'other' (anything
// else: IR, polarised, etc.). Existing rows default to 'visible'.
type PhotoKind string

// Allowed PhotoKind values, matching the photo_kind enum in the
// photos table (migrations 0005 + 0007).
const (
	PhotoKindVisible PhotoKind = "visible"
	PhotoKindUVSW    PhotoKind = "uv_sw"
	PhotoKindUVMW    PhotoKind = "uv_mw"
	PhotoKindUVLW    PhotoKind = "uv_lw"
	PhotoKindOther   PhotoKind = "other"
)

// IsValid reports whether k is one of the photo_kind enum values.
// The zero value ("") returns false; callers default to
// PhotoKindVisible explicitly when no kind is supplied.
func (k PhotoKind) IsValid() bool {
	switch k {
	case PhotoKindVisible, PhotoKindUVSW, PhotoKindUVMW, PhotoKindUVLW, PhotoKindOther:
		return true
	}
	return false
}

// Photo mirrors design §2.
type Photo struct {
	ID         uuid.UUID
	SpecimenID uuid.UUID
	FileID     uuid.UUID
	Kind       PhotoKind
	TakenAt    *time.Time
	Position   int
	CreatedAt  time.Time
}

// JournalEntry mirrors design §2.
type JournalEntry struct {
	ID         uuid.UUID
	SpecimenID uuid.UUID
	AuthorID   uuid.UUID
	BodyMD     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// JournalEntryFile mirrors design §2 — the join row between a
// journal entry and a stored file. Position controls display order
// within an entry; created_at is set at attachment time.
type JournalEntryFile struct {
	EntryID   uuid.UUID
	FileID    uuid.UUID
	Position  int
	CreatedAt time.Time
}

// Collector mirrors design §2.
type Collector struct {
	ID        uuid.UUID
	Name      string
	Notes     *string
	AuthorID  uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// File mirrors design §2.
type File struct {
	ID          uuid.UUID
	S3Key       string
	ContentType string
	ByteSize    int64
	SHA256      string
	UploadedAt  time.Time
	UploadedBy  uuid.UUID
}

// MineralData is the typed shape stored in specimens.type_data when
// type='mineral'. Optional fields use pointers so omitempty round-trips
// cleanly.
//
// UV fluorescence (mi-qas) is stored as three per-wavelength color
// lists. A nil slice means "not fluorescent under this wavelength"
// (or unknown); a populated slice carries one or more validated
// color strings from ValidFluorescenceColors. Multi-color specimens
// (e.g. red-and-green calcite) hold multiple entries per wavelength.
type MineralData struct {
	ChemicalFormula *string  `json:"chemical_formula,omitempty"`
	MineralSpecies  []string `json:"mineral_species,omitempty"`
	CrystalSystem   *string  `json:"crystal_system,omitempty"`
	MohsHardness    *float64 `json:"mohs_hardness,omitempty"`
	Color           *string  `json:"color,omitempty"`
	Luster          *string  `json:"luster,omitempty"`
	FluorescenceSW  []string `json:"fluorescence_sw,omitempty"`
	FluorescenceMW  []string `json:"fluorescence_mw,omitempty"`
	FluorescenceLW  []string `json:"fluorescence_lw,omitempty"`
	Radioactive     *bool    `json:"radioactive,omitempty"`
	Magnetic        *bool    `json:"magnetic,omitempty"`
	ReactsToAcid    *bool    `json:"reacts_to_acid,omitempty"`
	MindatID        *string  `json:"mindat_id,omitempty"`
}

// RockData is the typed shape stored in specimens.type_data when
// type='rock'.
type RockData struct {
	RockType         *string `json:"rock_type,omitempty"` // igneous|sedimentary|metamorphic
	Composition      *string `json:"composition,omitempty"`
	FormationContext *string `json:"formation_context,omitempty"`
}

// MeteoriteData is the typed shape stored in specimens.type_data when
// type='meteorite'.
type MeteoriteData struct {
	Classification    *string    `json:"classification,omitempty"`
	FallOrFind        *string    `json:"fall_or_find,omitempty"` // fall|find
	FallOrFindDate    *time.Time `json:"fall_or_find_date,omitempty"`
	OfficialName      *string    `json:"official_name,omitempty"`
	TotalKnownWeightG *float64   `json:"total_known_weight_g,omitempty"`
	MetbullRef        *string    `json:"metbull_ref,omitempty"`
}

// FossilData is the typed shape stored in specimens.type_data when
// type='fossil'. All fields are optional; the zero value of each
// field is omitted from the JSONB payload via omitempty.
type FossilData struct {
	Taxon            *string `json:"taxon,omitempty"`             // e.g. "Tyrannosaurus rex"
	TaxonomicGroup   *string `json:"taxonomic_group,omitempty"`   // e.g. "Dinosauria"
	GeologicPeriod   *string `json:"geologic_period,omitempty"`   // e.g. "Cretaceous"
	Formation        *string `json:"formation,omitempty"`         // e.g. "Hell Creek Formation"
	Locality         *string `json:"locality,omitempty"`          // stratigraphic context (complements specimen locality)
	PreservationType *string `json:"preservation_type,omitempty"` // e.g. "Cast", "Mold", "Permineralized", "Compression"
	Completeness     *string `json:"completeness,omitempty"`      // e.g. "Complete", "Partial", "Fragment"
	Prepared         *bool   `json:"prepared,omitempty"`          // whether prep work has been done
	PrepNotes        *string `json:"prep_notes,omitempty"`        // free-form prep description
}

// validRockTypes enumerates the v1 RockData.RockType vocabulary
// (per design §2's "Type-specific data shapes").
var validRockTypes = map[string]struct{}{
	"igneous": {}, "sedimentary": {}, "metamorphic": {},
}

// validFallOrFind enumerates the v1 MeteoriteData.FallOrFind vocabulary.
var validFallOrFind = map[string]struct{}{
	"fall": {}, "find": {},
}

// ValidFluorescenceColors is the closed vocabulary of UV fluorescence
// colors accepted on MineralData.FluorescenceSW/MW/LW (mi-qas). The
// list is drawn from the Henkel Glossary of Fluorescent Minerals and
// the Fluorescent Mineral Society vocabulary — only colors that
// genuinely occur in mineral UV fluorescence are included. "Black"
// and "Brown" are intentionally excluded (non-fluorescent shows as
// null, not "Black"); generic display-color names like "Cyan", "Teal",
// "Magenta" are excluded in favor of the mineralogical vocabulary
// (e.g. "Blue-green").
var ValidFluorescenceColors = map[string]struct{}{
	"Red":             {},
	"Orange":          {},
	"Yellow":          {},
	"Green":           {},
	"Blue":            {},
	"Violet":          {},
	"Pink":            {},
	"White":           {},
	"Cream":           {},
	"Blue-green":      {},
	"Blue-violet":     {},
	"Red-orange":      {},
	"Orange-yellow":   {},
	"Greenish-yellow": {},
	"Cherry red":      {},
}

// fluorescenceColorList returns ValidFluorescenceColors as a
// deterministic comma-joined string for use in error messages.
func fluorescenceColorList() string {
	out := make([]string, 0, len(ValidFluorescenceColors))
	for c := range ValidFluorescenceColors {
		out = append(out, c)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// Validate checks MineralData invariants beyond JSON-schema shape.
// Empty pointers (the v1 default) are always valid — the struct is a
// sparse bag of optional fields.
func (m MineralData) Validate() error {
	if m.MohsHardness != nil {
		if *m.MohsHardness < 0 || *m.MohsHardness > 10 {
			return fmt.Errorf("%w: mohs_hardness must be in [0,10]", ErrSpecimenTypeDataInvalid)
		}
	}
	for _, group := range []struct {
		field  string
		colors []string
	}{
		{"fluorescence_sw", m.FluorescenceSW},
		{"fluorescence_mw", m.FluorescenceMW},
		{"fluorescence_lw", m.FluorescenceLW},
	} {
		for _, c := range group.colors {
			if _, ok := ValidFluorescenceColors[c]; !ok {
				return fmt.Errorf("%w: %s color %q is not a recognized UV fluorescence color; valid: %s",
					ErrSpecimenTypeDataInvalid, group.field, c, fluorescenceColorList())
			}
		}
	}
	return nil
}

// Validate checks RockData invariants. Empty pointers are valid.
func (r RockData) Validate() error {
	if r.RockType != nil {
		if _, ok := validRockTypes[*r.RockType]; !ok {
			return fmt.Errorf("%w: rock_type must be one of igneous|sedimentary|metamorphic",
				ErrSpecimenTypeDataInvalid)
		}
	}
	return nil
}

// Validate checks MeteoriteData invariants. Empty pointers are valid.
func (m MeteoriteData) Validate() error {
	if m.FallOrFind != nil {
		if _, ok := validFallOrFind[*m.FallOrFind]; !ok {
			return fmt.Errorf("%w: fall_or_find must be one of fall|find",
				ErrSpecimenTypeDataInvalid)
		}
	}
	if m.TotalKnownWeightG != nil && *m.TotalKnownWeightG < 0 {
		return fmt.Errorf("%w: total_known_weight_g must be >= 0", ErrSpecimenTypeDataInvalid)
	}
	return nil
}

// Validate checks FossilData invariants. All fields are free-form
// strings in v1 (no closed vocabulary for taxon, period, formation,
// etc.) so there is nothing to enforce beyond JSON-schema shape; the
// method exists for parity with the other type-data structs and to
// give the API layer a single dispatch surface.
func (FossilData) Validate() error { return nil }

// SpecimenRepo is the consumer-side interface for specimens persistence.
type SpecimenRepo interface {
	Create(ctx context.Context, tx Tx, s Specimen) error
	GetByID(ctx context.Context, id uuid.UUID) (Specimen, error)
	Update(ctx context.Context, tx Tx, s Specimen) error
	Delete(ctx context.Context, tx Tx, id uuid.UUID) error
	List(ctx context.Context, filter SpecimenFilter, page Page) ([]Specimen, Cursor, error)
	// HasPhotoWithFile reports whether the specimen has any photo
	// whose file_id matches fileID. The PATCH handler calls this
	// before writing main_image_id (mi-m8q): the file must belong
	// to a photo on this specimen, otherwise the request is
	// rejected with 422.
	HasPhotoWithFile(ctx context.Context, specimenID, fileID uuid.UUID) (bool, error)
}

// PhotoRepo is the consumer-side interface for photos persistence.
type PhotoRepo interface {
	Create(ctx context.Context, tx Tx, p Photo) error
	GetByID(ctx context.Context, id uuid.UUID) (Photo, error)
	Update(ctx context.Context, tx Tx, p Photo) error
	Delete(ctx context.Context, tx Tx, id uuid.UUID) error
	ListBySpecimen(ctx context.Context, specimenID uuid.UUID, page Page) ([]Photo, Cursor, error)
	// MaxPosition returns the largest `position` value currently in
	// use on the specimen's photos, or 0 if there are none. The
	// service layer uses this to default a new photo's position to
	// max+1 (per §12 — manual ordering, no auto-shuffle).
	MaxPosition(ctx context.Context, tx Tx, specimenID uuid.UUID) (int, error)
}

// JournalEntryRepo is the consumer-side interface for journal_entries
// persistence.
type JournalEntryRepo interface {
	Create(ctx context.Context, tx Tx, e JournalEntry) error
	GetByID(ctx context.Context, id uuid.UUID) (JournalEntry, error)
	Update(ctx context.Context, tx Tx, e JournalEntry) error
	Delete(ctx context.Context, tx Tx, id uuid.UUID) error
	ListBySpecimen(ctx context.Context, specimenID uuid.UUID, page Page) ([]JournalEntry, Cursor, error)
}

// JournalEntryFileRepo is the consumer-side interface for the
// journal_entry_files join table (mi-720 / C-2). Attachments are
// listed in (position ASC, created_at ASC, file_id ASC) order. The
// repo does NOT delete the underlying files row — the service layer
// removes both rows in a single transaction, then best-effort cleans
// up the MinIO object (per CONTRACT.md §12).
type JournalEntryFileRepo interface {
	Create(ctx context.Context, tx Tx, j JournalEntryFile) error
	GetByFileID(ctx context.Context, fileID uuid.UUID) (JournalEntryFile, error)
	ListByEntry(ctx context.Context, entryID uuid.UUID) ([]JournalEntryFile, error)
	Delete(ctx context.Context, tx Tx, fileID uuid.UUID) error
	// MaxPosition returns the largest `position` value currently in
	// use among the entry's attachments, or 0 if there are none. The
	// service layer uses this to default a new attachment's position
	// to max+1 (matching the photos pattern — manual ordering, no
	// auto-shuffle).
	MaxPosition(ctx context.Context, tx Tx, entryID uuid.UUID) (int, error)
}

// FileRepo is the consumer-side interface for files persistence.
type FileRepo interface {
	Create(ctx context.Context, tx Tx, f File) error
	GetByID(ctx context.Context, id uuid.UUID) (File, error)
	Delete(ctx context.Context, tx Tx, id uuid.UUID) error
}

// CollectorRepo is the consumer-side interface for collectors
// persistence.
type CollectorRepo interface {
	Create(ctx context.Context, tx Tx, c Collector) error
	GetByID(ctx context.Context, id uuid.UUID) (Collector, error)
	Update(ctx context.Context, tx Tx, c Collector) error
	Delete(ctx context.Context, tx Tx, id uuid.UUID) error
	List(ctx context.Context, filter CollectorFilter, page Page) ([]Collector, Cursor, error)
}

// SpecimenCollectorLink is one row of a specimen's collector chain
// joined with the collector it points at. position is 1-indexed and
// matches the array order the user submitted in the PUT body.
type SpecimenCollectorLink struct {
	Collector Collector
	Position  int
}

// MineralSpeciesSource is the provenance discriminator for a row in
// the mineral_species table (mi-dtg / F-1). 'mindat' rows are
// populated by the Mindat lookup pipeline; 'user' rows are entered
// manually when no Mindat key is configured or when Mindat returns
// nothing for the search.
type MineralSpeciesSource string

const (
	// MineralSpeciesSourceMindat marks a row imported from the Mindat API.
	MineralSpeciesSourceMindat MineralSpeciesSource = "mindat"
	// MineralSpeciesSourceUser marks a row entered manually by a user.
	MineralSpeciesSourceUser MineralSpeciesSource = "user"
)

// MineralSpecies is the canonical row backing the mineral lookup
// surface (F-1). The Data field carries the MineralData JSON shape
// (per design §2) — pre-populated from Mindat or hand-entered.
//
// Attribution is set when source='mindat' to satisfy Mindat's
// CC-BY-NC-SA 4.0 terms; the frontend renders it next to the
// mineral fields when present.
type MineralSpecies struct {
	ID          uuid.UUID
	Name        string
	Source      MineralSpeciesSource
	MindatID    *string
	Data        []byte // raw JSON; service unmarshals into MineralData
	Attribution *string
	AuthorID    uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MineralSpeciesRepo is the consumer-side interface for the
// mineral_species table (mi-dtg / F-1).
type MineralSpeciesRepo interface {
	// Create inserts a new mineral_species row. Returns
	// ErrMineralSpeciesConflict on (name) or (mindat_id) unique
	// violation.
	Create(ctx context.Context, tx Tx, s MineralSpecies) error
	// GetByID returns the row identified by id, or
	// ErrMineralSpeciesNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (MineralSpecies, error)
	// FindByName performs an ILIKE substring search on `name`. The
	// caller-supplied query is escaped of LIKE metacharacters
	// before wrapping with `%`. Returns rows ordered by
	// (lower(name) ASC, id ASC) so the result is stable across
	// callers; capped at MaxListLimit rows.
	FindByName(ctx context.Context, q string) ([]MineralSpecies, error)
	// FindByMindatID returns the row whose mindat_id matches, or
	// ErrMineralSpeciesNotFound. Only meaningful for source='mindat'
	// rows; user-entered rows have a NULL mindat_id and are
	// unreachable through this method.
	FindByMindatID(ctx context.Context, mindatID string) (MineralSpecies, error)
}

// QRSheetTemplate is the discriminator for the avery-style label
// templates the printing pipeline knows about (mi-c78 epic). The
// vocabulary is fixed at compile time — there are no user-defined
// templates in v1 — so the API layer validates against
// QRSheetTemplateCapacity() before the value reaches the database.
type QRSheetTemplate string

// QRSheetTemplateCapacity returns the number of stickers per page for
// the named template, or (0, false) if the template is unknown to v1.
// Used by the API layer to validate POST/PATCH bodies AND by the GET
// handler to compute page count = ceil(specimen_count / capacity).
func QRSheetTemplateCapacity(t QRSheetTemplate) (int, bool) {
	switch t {
	case "avery-5160":
		return 30, true
	case "avery-5163":
		return 10, true
	case "avery-5164":
		return 6, true
	case "avery-22806":
		return 12, true
	case "avery-l7160":
		return 21, true
	}
	return 0, false
}

// QRSheet is the per-user persistent working set of specimens whose
// QR labels are queued for printing (mi-c78.1). v1 enforces one
// active sheet per user via UNIQUE(user_id) on the table.
type QRSheet struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Template  QRSheetTemplate
	CreatedAt time.Time
	UpdatedAt time.Time
}

// QRSheetEntry is one row of a sheet's specimens-with-thumbnail join,
// returned by QRSheetRepo.ListSpecimens. The repo joins specimens
// (for the display name) and photos (for the lowest-positioned
// thumbnail). FirstPhotoID is nil for specimens with no photos; the
// API layer turns a non-nil FirstPhotoID into a /api/v1/photos/{id}/thumb
// URL on its way out.
type QRSheetEntry struct {
	SpecimenID   uuid.UUID
	SpecimenName string
	Position     int
	AddedAt      time.Time
	FirstPhotoID *uuid.UUID
}

// QRSheetRepo is the consumer-side interface for the qr_sheets and
// qr_sheet_specimens tables (mi-c78.1). Every method is user-scoped:
// the API never trusts a sheet id from the client, it identifies the
// active sheet by the request's user.
type QRSheetRepo interface {
	// GetByUser returns the user's active sheet, or
	// ErrQRSheetNotFound when none exists.
	GetByUser(ctx context.Context, userID uuid.UUID) (QRSheet, error)
	// Create inserts a new sheet. Returns ErrQRSheetConflict when the
	// user already has one (UNIQUE(user_id) violation).
	Create(ctx context.Context, tx Tx, s QRSheet) error
	// UpdateTemplate writes the template on the user's sheet and
	// bumps updated_at. Returns ErrQRSheetNotFound when the user has
	// no sheet.
	UpdateTemplate(ctx context.Context, tx Tx, userID uuid.UUID, template QRSheetTemplate, updatedAt time.Time) error
	// Delete removes the user's sheet (cascading to qr_sheet_specimens
	// rows). Returns ErrQRSheetNotFound when none exists.
	Delete(ctx context.Context, tx Tx, userID uuid.UUID) error
	// AddSpecimen appends a specimen to the end of the user's sheet.
	// Idempotent: when the specimen is already on the sheet, the call
	// succeeds without changing position. Returns ErrQRSheetNotFound
	// when the user has no sheet and ErrSpecimenNotFound when
	// specimen_id doesn't exist.
	AddSpecimen(ctx context.Context, tx Tx, userID, specimenID uuid.UUID, addedAt time.Time) error
	// RemoveSpecimen drops a specimen from the user's sheet and
	// repacks positions so there are no gaps. Returns
	// ErrQRSheetNotFound when the user has no sheet and
	// ErrQRSheetSpecimenNotFound when the specimen isn't on it.
	RemoveSpecimen(ctx context.Context, tx Tx, userID, specimenID uuid.UUID) error
	// ListSpecimens returns the sheet's specimens (joined with their
	// display name and lowest-positioned photo id, if any) ordered
	// by position ascending.
	ListSpecimens(ctx context.Context, sheetID uuid.UUID) ([]QRSheetEntry, error)
}

// SpecimenCollectorRepo is the consumer-side interface for the
// specimen↔collector join table (mi-zv3 / C-3). The chain is edited
// atomically via ReplaceChain — there is no per-link API surface.
type SpecimenCollectorRepo interface {
	// GetChain returns every link for specimen_id ordered by
	// position ascending. An unknown specimen_id returns an empty
	// slice (the API layer probes specimen existence separately so
	// 404 vs empty-chain stays unambiguous).
	GetChain(ctx context.Context, tx Tx, specimenID uuid.UUID) ([]SpecimenCollectorLink, error)
	// ReplaceChain atomically replaces every row for specimen_id
	// with the supplied collector_ids in order; the array index
	// becomes the position (1-indexed). Returns ErrCollectorNotFound
	// if any id is missing — no partial replace.
	ReplaceChain(ctx context.Context, tx Tx, specimenID uuid.UUID, collectorIDs []uuid.UUID) error
}
