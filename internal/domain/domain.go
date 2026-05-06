// Package domain holds the core types, enums, and repository
// interfaces that anchor the v1 minerals app. Implementations live in
// internal/db (per CONTRACT.md §11). No SQL here, no HTTP here.
package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SpecimenType is the discriminator for a specimens row.
type SpecimenType string

const (
	SpecimenMineral   SpecimenType = "mineral"
	SpecimenRock      SpecimenType = "rock"
	SpecimenMeteorite SpecimenType = "meteorite"
)

// Visibility controls who can read a specimen. v1 enforces only the
// stub-user-can-read-everything path; public sharing lands later.
type Visibility string

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
	ErrSpecimenNotFound     = fmt.Errorf("specimen not found")
	ErrSpecimenConflict     = fmt.Errorf("specimen conflict")
	ErrPhotoNotFound        = fmt.Errorf("photo not found")
	ErrPhotoConflict        = fmt.Errorf("photo conflict")
	ErrJournalEntryNotFound = fmt.Errorf("journal entry not found")
	ErrJournalEntryConflict = fmt.Errorf("journal entry conflict")
	ErrFileNotFound         = fmt.Errorf("file not found")
	ErrFileConflict         = fmt.Errorf("file conflict")
	ErrCollectorNotFound    = fmt.Errorf("collector not found")
	ErrCollectorConflict    = fmt.Errorf("collector conflict")
)

// Page is the cursor-pagination request shape (per §10).
type Page struct {
	Limit  int
	Cursor string
}

// Cursor is the opaque pagination cursor returned by list queries.
type Cursor string

// SpecimenFilter holds the v1 list filters (per design §4.4).
type SpecimenFilter struct {
	Type        *SpecimenType
	Visibility  *Visibility
	CollectorID *uuid.UUID
	Query       string
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
	TypeData      []byte // raw JSON; service unmarshals into MineralData/RockData/MeteoriteData
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Photo mirrors design §2.
type Photo struct {
	ID         uuid.UUID
	SpecimenID uuid.UUID
	FileID     uuid.UUID
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
type MineralData struct {
	ChemicalFormula *string  `json:"chemical_formula,omitempty"`
	MineralSpecies  []string `json:"mineral_species,omitempty"`
	CrystalSystem   *string  `json:"crystal_system,omitempty"`
	MohsHardness    *float64 `json:"mohs_hardness,omitempty"`
	Color           *string  `json:"color,omitempty"`
	Luster          *string  `json:"luster,omitempty"`
	Fluorescence    *string  `json:"fluorescence,omitempty"`
	Radioactive     *bool    `json:"radioactive,omitempty"`
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

// SpecimenRepo is the consumer-side interface for specimens persistence.
type SpecimenRepo interface {
	Create(ctx context.Context, tx Tx, s Specimen) error
	GetByID(ctx context.Context, id uuid.UUID) (Specimen, error)
	Update(ctx context.Context, tx Tx, s Specimen) error
	Delete(ctx context.Context, tx Tx, id uuid.UUID) error
	List(ctx context.Context, filter SpecimenFilter, page Page) ([]Specimen, Cursor, error)
}

// PhotoRepo is the consumer-side interface for photos persistence.
type PhotoRepo interface {
	Create(ctx context.Context, tx Tx, p Photo) error
	GetByID(ctx context.Context, id uuid.UUID) (Photo, error)
	Update(ctx context.Context, tx Tx, p Photo) error
	Delete(ctx context.Context, tx Tx, id uuid.UUID) error
	ListBySpecimen(ctx context.Context, specimenID uuid.UUID) ([]Photo, error)
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
	List(ctx context.Context, page Page) ([]Collector, Cursor, error)
}
