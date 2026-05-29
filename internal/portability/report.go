package portability

import "strings"

// Validation error codes. They map to a stable, machine-checkable
// `code` in the API error envelope so the frontend can distinguish a
// malformed upload from an incompatible version.
const (
	// CodeMalformedArchive: the container or a record could not be
	// parsed (bad ZIP, missing/invalid manifest, unparseable JSONL).
	CodeMalformedArchive = "malformed_archive"
	// CodeIncompatibleSchema: the manifest's schema_version is missing,
	// zero, or newer than this build can read.
	CodeIncompatibleSchema = "incompatible_schema_version"
	// CodeIntegrity: a referenced file binary is missing, the wrong
	// size, or fails its SHA-256 check.
	CodeIntegrity = "integrity_check_failed"
	// CodeReference: a cross-reference inside the archive does not
	// resolve (e.g. a photo points at an unknown specimen or file).
	CodeReference = "broken_reference"
	// CodeInvalidRecord: a record is structurally valid JSON but
	// violates a field invariant (bad UUID, unknown enum value, etc.).
	CodeInvalidRecord = "invalid_record"
)

// ValidationError is the typed failure returned when an archive cannot
// be imported. It carries a stable code, a human message, and an
// optional list of specific problems (capped by the caller). Validation
// errors are terminal: nothing is written. Distinct from a Conflict,
// which is resolved and merely reported.
type ValidationError struct {
	Code    string
	Message string
	Details []string
}

func (e *ValidationError) Error() string {
	if len(e.Details) == 0 {
		return e.Message
	}
	return e.Message + ": " + strings.Join(e.Details, "; ")
}

// ConflictKind discriminates the kinds of conflict the import engine
// resolves rather than rejects.
type ConflictKind string

const (
	// ConflictCatalogNumber is recorded when an imported specimen's
	// catalog_number collides with one the importer already uses (or
	// another imported specimen). The row is still imported as a new
	// specimen with a suffixed catalog_number; the original is never
	// clobbered.
	ConflictCatalogNumber ConflictKind = "catalog_number"
)

// Conflict is one resolved collision recorded in the import report so
// the user can see what the dry-run/commit changed and why.
type Conflict struct {
	Kind       ConflictKind `json:"kind"`
	SpecimenID string       `json:"specimen_id"`
	Detail     string       `json:"detail"`
}

// Report is the dry-run/commit result. It is the dry-run deliverable and
// the commit response body. Counts reflect what was (or would be)
// created; Conflicts and Warnings describe deviations; ImageFailures
// lists binaries that failed their best-effort post-commit upload.
type Report struct {
	SchemaVersion int        `json:"schema_version"`
	DryRun        bool       `json:"dry_run"`
	Committed     bool       `json:"committed"`
	Counts        Counts     `json:"counts"`
	Conflicts     []Conflict `json:"conflicts"`
	Warnings      []string   `json:"warnings"`
	// ImageFailures names file binaries whose post-commit upload failed.
	// The DB rows exist; the objects can be re-uploaded by re-importing.
	ImageFailures []string `json:"image_failures"`
}
