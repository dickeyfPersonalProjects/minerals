package db

import "github.com/jackc/pgx/v5/pgxpool"

// Placeholder repository structs. Subsequent feature beads add real
// CRUD methods that satisfy the interfaces in internal/domain.
// SpecimenPostgres has its real implementation in specimen_postgres.go.

// PhotoPostgres is the Postgres-backed domain.PhotoRepo.
type PhotoPostgres struct{ pool *pgxpool.Pool }

// NewPhotoPostgres constructs a PhotoPostgres bound to pool.
func NewPhotoPostgres(pool *pgxpool.Pool) *PhotoPostgres {
	return &PhotoPostgres{pool: pool}
}

// JournalEntryPostgres is the Postgres-backed domain.JournalEntryRepo.
type JournalEntryPostgres struct{ pool *pgxpool.Pool }

// NewJournalEntryPostgres constructs a JournalEntryPostgres bound to pool.
func NewJournalEntryPostgres(pool *pgxpool.Pool) *JournalEntryPostgres {
	return &JournalEntryPostgres{pool: pool}
}

// FilePostgres is the Postgres-backed domain.FileRepo.
type FilePostgres struct{ pool *pgxpool.Pool }

// NewFilePostgres constructs a FilePostgres bound to pool.
func NewFilePostgres(pool *pgxpool.Pool) *FilePostgres {
	return &FilePostgres{pool: pool}
}

// CollectorPostgres is the Postgres-backed domain.CollectorRepo.
type CollectorPostgres struct{ pool *pgxpool.Pool }

// NewCollectorPostgres constructs a CollectorPostgres bound to pool.
func NewCollectorPostgres(pool *pgxpool.Pool) *CollectorPostgres {
	return &CollectorPostgres{pool: pool}
}
