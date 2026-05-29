// Package portability defines the versioned, self-describing archive
// format for user-data export/import (mi-dkuu) and the import engine
// that consumes it. The format is the shared contract between the
// export endpoint (GET /api/v1/export) and the import endpoint
// (POST /api/v1/import): both sides serialize/deserialize the same
// records here so a round-trip preserves every persisted, user-owned
// field. See docs/export-import-design.md.
//
// An archive is a ZIP laid out as:
//
//	manifest.json                 archive metadata + schema version + counts
//	data/collectors.jsonl         one CollectorRecord per line
//	data/files.jsonl              one FileRecord per line (integrity index)
//	data/specimens.jsonl          one SpecimenRecord per line (embeds collector membership)
//	data/photos.jsonl             one PhotoRecord per line (references specimen_id + file_id)
//	data/journal_entries.jsonl    one JournalEntryRecord per line (embeds attachment membership)
//	data/qrsheets.jsonl           one QRSheetRecord per line (embeds specimen membership)
//	files/<fileId>                original file binaries (path mirrored in FileRecord.Path)
//
// JSONL (newline-delimited JSON) lets both sides stream row-by-row
// without buffering the whole collection. IDs are preserved in the
// archive purely for intra-archive cross-references; on import every
// ID is regenerated and the references are rewritten (see import.go).
package portability

import (
	"encoding/json"
	"time"
)

// SchemaVersion is the current archive schema version. Import refuses
// archives whose schemaVersion is newer than this (a forward-incompatible
// archive produced by a later release) with a clear error, and refuses
// the zero value (a manifest that omitted the field). Bump on any
// breaking change to the record shapes or layout below.
const SchemaVersion = 1

// Application is the fixed application tag stamped into every manifest.
// Import rejects archives that carry a different application string so
// an unrelated ZIP can't be mistaken for a minerals export.
const Application = "minerals"

// Archive layout paths. Entity collections live under data/, binaries
// under files/. FilePathPrefix is joined with a file's UUID to form the
// in-archive object path (FileRecord.Path).
const (
	ManifestPath       = "manifest.json"
	CollectorsPath     = "data/collectors.jsonl"
	FilesPath          = "data/files.jsonl"
	SpecimensPath      = "data/specimens.jsonl"
	PhotosPath         = "data/photos.jsonl"
	JournalEntriesPath = "data/journal_entries.jsonl"
	QRSheetsPath       = "data/qrsheets.jsonl"
	FileBinaryPrefix   = "files/"
)

// Counts is the per-entity tally recorded in the manifest and echoed in
// the import report. It is advisory metadata — import validates the
// actual JSONL/binary contents rather than trusting these numbers.
type Counts struct {
	Collectors     int `json:"collectors"`
	Files          int `json:"files"`
	Specimens      int `json:"specimens"`
	Photos         int `json:"photos"`
	JournalEntries int `json:"journal_entries"`
	QRSheets       int `json:"qrsheets"`
}

// Manifest is the archive-level metadata at manifest.json. SchemaVersion
// gates import compatibility and is validated before any data is read.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	Application   string    `json:"application"`
	ExportedAt    time.Time `json:"exported_at"`
	// ExportedBy is the Keycloak sub of the exporting user. Recorded for
	// provenance only — import always re-homes to the importer and never
	// trusts this for ownership.
	ExportedBy string `json:"exported_by,omitempty"`
	Counts     Counts `json:"counts"`
}

// CollectorRecord is the archive serialization of a collector. author_id
// is intentionally absent: export is always the caller's own data and
// import re-homes every row to the importer.
type CollectorRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Notes     *string   `json:"notes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FileRecord is the archive serialization of a stored file's metadata
// and integrity record. Path is the in-archive location of the binary
// (FileBinaryPrefix + id); SHA256 is the hex digest import verifies the
// binary against. The S3 key is NOT carried: import derives a fresh key
// under the importer's namespace from the regenerated id.
type FileRecord struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	ContentType string    `json:"content_type"`
	ByteSize    int64     `json:"byte_size"`
	SHA256      string    `json:"sha256"`
	UploadedAt  time.Time `json:"uploaded_at"`
}

// SpecimenRecord is the archive serialization of a specimen including
// every persisted field and the per-field visibility overrides. TypeData
// is the raw type-specific JSON blob (mineral/rock/meteorite/fossil),
// carried verbatim. CollectorIDs is the ordered collector membership
// (the specimen_collectors chain); MainImageFileID references a file in
// files.jsonl. All ID-shaped fields are archive-local and remapped on
// import.
type SpecimenRecord struct {
	ID                     string          `json:"id"`
	Type                   string          `json:"type"`
	CatalogNumber          *string         `json:"catalog_number,omitempty"`
	Name                   string          `json:"name"`
	Description            string          `json:"description"`
	Visibility             string          `json:"visibility"`
	AcquiredAt             *time.Time      `json:"acquired_at,omitempty"`
	AcquiredFrom           *string         `json:"acquired_from,omitempty"`
	PriceCents             *int64          `json:"price_cents,omitempty"`
	SourceNotes            *string         `json:"source_notes,omitempty"`
	LocalityText           *string         `json:"locality_text,omitempty"`
	Locality               json.RawMessage `json:"locality,omitempty"`
	MassG                  *float64        `json:"mass_g,omitempty"`
	Dimensions             json.RawMessage `json:"dimensions,omitempty"`
	TypeData               json.RawMessage `json:"type_data,omitempty"`
	MainImageFileID        *string         `json:"main_image_file_id,omitempty"`
	VisibilityPrice        *string         `json:"visibility_price,omitempty"`
	VisibilityAcquiredFrom *string         `json:"visibility_acquired_from,omitempty"`
	VisibilityImages       *string         `json:"visibility_images,omitempty"`
	Tagged                 bool            `json:"tagged"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	// CollectorIDs is the ordered specimen_collectors membership; index
	// position becomes the 1-indexed chain position on import.
	CollectorIDs []string `json:"collector_ids,omitempty"`
}

// PhotoRecord is the archive serialization of a photo (the specimen↔file
// link plus per-photo metadata). SpecimenID and FileID are archive-local
// references remapped on import; the binary lives under the referenced
// file's FileRecord.Path.
type PhotoRecord struct {
	ID         string     `json:"id"`
	SpecimenID string     `json:"specimen_id"`
	FileID     string     `json:"file_id"`
	Kind       string     `json:"kind"`
	TakenAt    *time.Time `json:"taken_at,omitempty"`
	Position   int        `json:"position"`
	Visibility *string    `json:"visibility,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// JournalAttachment is one entry↔file link inside a JournalEntryRecord.
type JournalAttachment struct {
	FileID    string    `json:"file_id"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

// JournalEntryRecord is the archive serialization of a journal entry and
// its ordered file attachments (the journal_entry_files membership).
type JournalEntryRecord struct {
	ID          string              `json:"id"`
	SpecimenID  string              `json:"specimen_id"`
	BodyMD      string              `json:"body_md"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Attachments []JournalAttachment `json:"attachments,omitempty"`
}

// QRSheetSpecimen is one sheet↔specimen membership row inside a
// QRSheetRecord, carrying the print-order position and add time.
type QRSheetSpecimen struct {
	SpecimenID string    `json:"specimen_id"`
	Position   int       `json:"position"`
	AddedAt    time.Time `json:"added_at"`
}

// QRSheetRecord is the archive serialization of a QR sheet and its
// ordered specimen membership. v1 enforces one sheet per user, so an
// archive carries at most one.
type QRSheetRecord struct {
	ID        string            `json:"id"`
	Template  string            `json:"template"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Specimens []QRSheetSpecimen `json:"specimens,omitempty"`
}
