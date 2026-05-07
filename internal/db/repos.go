package db

import "github.com/jackc/pgx/v5/pgxpool"

// Placeholder repository structs. Subsequent feature beads add real
// CRUD methods that satisfy the interfaces in internal/domain.

// JournalEntryPostgres is the Postgres-backed domain.JournalEntryRepo.
type JournalEntryPostgres struct{ pool *pgxpool.Pool }

// NewJournalEntryPostgres constructs a JournalEntryPostgres bound to pool.
func NewJournalEntryPostgres(pool *pgxpool.Pool) *JournalEntryPostgres {
	return &JournalEntryPostgres{pool: pool}
}

// CollectorPostgres lives in collector_postgres.go (mi-yvt / B-1).
// FilePostgres lives in file_postgres.go (mi-jpu / B-3).
// PhotoPostgres lives in photo_postgres.go (mi-jpu / B-3).
// SpecimenPostgres lives in specimen_postgres.go (mi-quf / B-2).
