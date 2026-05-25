// Package db — AdminStatsPostgres implements domain.AdminStatsProvider
// for the admin stats endpoint (mi-ilvt). All queries are read-only
// aggregate COUNTs; no PII columns are touched.
package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminStatsPostgres implements domain.AdminStatsProvider backed by
// a pgxpool.Pool. All methods issue a single SELECT COUNT(*) against
// the named table and return the result.
type AdminStatsPostgres struct {
	pool *pgxpool.Pool
}

// NewAdminStatsPostgres constructs an AdminStatsPostgres.
func NewAdminStatsPostgres(pool *pgxpool.Pool) *AdminStatsPostgres {
	return &AdminStatsPostgres{pool: pool}
}

// CountUsers returns the total number of rows in the users table.
func (r *AdminStatsPostgres) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountSpecimens returns the total number of rows in the specimens table.
func (r *AdminStatsPostgres) CountSpecimens(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM specimens`).Scan(&n)
	return n, err
}

// CountPhotos returns the total number of rows in the photos table.
func (r *AdminStatsPostgres) CountPhotos(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM photos`).Scan(&n)
	return n, err
}

// CountJournalEntries returns the total number of rows in the
// journal_entries table.
func (r *AdminStatsPostgres) CountJournalEntries(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM journal_entries`).Scan(&n)
	return n, err
}
