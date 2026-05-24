package domain

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Account-erasure sentinels (mi-nwg5). Handlers branch on these via
// errors.Is, never on pgx internals.
var (
	// ErrStubUserUndeletable is returned by AccountEraser.Erase when
	// the target id is the v1 stub-overseer sentinel
	// (00000000-0000-0000-0000-000000000001 / sub=stub-overseer).
	// Deleting it would strip the row every legacy author_id FK points
	// at and break the bootstrap/claim-orphans path, so erasure refuses
	// it outright.
	ErrStubUserUndeletable = errors.New("stub-overseer account cannot be erased")
)

// AccountErasure is the audit-grade summary AccountEraser.Erase
// returns: the per-table delete counts plus the object-store keys the
// transaction freed. The service layer logs the counts (no PII) and
// purges FreedObjectKeys from MinIO after the DB transaction commits.
//
// Counts are advisory (operator-facing audit), not a contract the
// caller branches on — a fully-empty account erases cleanly with every
// count zero.
type AccountErasure struct {
	// FreedObjectKeys is every files.s3_key the erased user owned
	// (uploaded_by = target), captured inside the transaction BEFORE
	// the rows were deleted. The service purges these from the object
	// store post-commit (best-effort, idempotent) so no orphaned
	// objects remain. CONTRACT.md §12 / mi-nwg5 acceptance.
	FreedObjectKeys []string
	// Specimens, Photos, JournalEntries, Collectors, Files, QRSheets are
	// per-table row counts the erasure deleted. ReassignedSpecies counts
	// mineral_species rows reassigned to stub-overseer (reference catalog
	// data is preserved, not deleted — see migration 0002).
	Specimens         int64
	Photos            int64
	JournalEntries    int64
	Collectors        int64
	Files             int64
	QRSheets          int64
	ReassignedSpecies int64
}

// AccountEraser cascade-deletes one user's personal data and the user
// row itself in a single transaction, returning an AccountErasure
// summary (mi-nwg5 / GDPR right-to-erasure). Implementations live in
// internal/db.
//
// Scope and policy (see migration comments in 0001/0002/0008/0011):
//   - Personal collection data is hard-deleted: specimens (cascading to
//     photos, journal entries, journal-entry-file links, specimen↔
//     collector links, and qr-sheet membership), collectors, files, and
//     qr-sheets the user owns.
//   - mineral_species rows the user authored are REASSIGNED to the
//     stub-overseer, not deleted: that table is the canonical reference
//     catalog ("we never delete records", migration 0002) and author_id
//     there is provenance, not personal content.
//   - The users row is hard-deleted last; `shares` rows cascade with it
//     (migration 0010 ON DELETE CASCADE). With every owned row gone,
//     nothing else references users.id, so the RESTRICT author FKs
//     (migration 0011) no longer block the delete.
//
// Erase does NOT touch auth.sessions (no FK; revoked separately by the
// service) or the Keycloak identity (removed separately via
// IdentityDeleter). Returns ErrStubUserUndeletable for the stub user
// and ErrUserNotFound when no row matches id.
type AccountEraser interface {
	Erase(ctx context.Context, id uuid.UUID) (AccountErasure, error)
}

// IdentityDeleter removes the external identity-provider record for a
// deleted account (mi-nwg5). The single implementation talks to the
// Keycloak admin REST API; a no-op variant is wired when admin
// credentials are absent so account deletion still succeeds (the app
// row and sessions are gone; the orphaned IdP user can log in only to
// have a fresh pending row created — equivalent to re-registration).
//
// sub is the Keycloak `sub` claim, which in Keycloak equals the user's
// admin-API id, so the implementation can DELETE the user directly
// without a lookup. Errors are surfaced to the caller, which treats
// them as best-effort (logs + continues) — the DB erasure has already
// committed by the time DeleteIdentity is called.
type IdentityDeleter interface {
	DeleteIdentity(ctx context.Context, sub string) error
}
