package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// CollectorPostgres is the Postgres-backed domain.CollectorRepo (mi-yvt).
type CollectorPostgres struct{ pool *pgxpool.Pool }

// NewCollectorPostgres constructs a CollectorPostgres bound to pool.
func NewCollectorPostgres(pool *pgxpool.Pool) *CollectorPostgres {
	return &CollectorPostgres{pool: pool}
}

// MaxListLimit caps the per-page row count regardless of caller input
// (per CONTRACT.md §10).
const MaxListLimit = 200

const defaultListLimit = 50

// Create inserts a new collector. The caller's UUIDv7 must already be
// in c.ID and CreatedAt/UpdatedAt must be set; author_id is taken from
// auth context (per CONTRACT.md §11 / §13).
func (r *CollectorPostgres) Create(ctx context.Context, tx domain.Tx, c domain.Collector) error {
	exec := r.execer(tx)
	user := auth.FromContext(ctx)
	const q = `
		INSERT INTO collectors (id, name, notes, author_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := exec.Exec(ctx, q, c.ID, c.Name, c.Notes, user.ID, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrCollectorConflict
		}
		return fmt.Errorf("collector repo: create: %w", err)
	}
	return nil
}

// GetByID returns the collector with the given id, or
// domain.ErrCollectorNotFound.
func (r *CollectorPostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.Collector, error) {
	const q = `
		SELECT id, name, notes, author_id, created_at, updated_at
		FROM collectors WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	c, err := scanCollector(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Collector{}, domain.ErrCollectorNotFound
	}
	if err != nil {
		return domain.Collector{}, fmt.Errorf("collector repo: get by id: %w", err)
	}
	return c, nil
}

// Update writes name and/or notes to the row identified by c.ID. The
// caller has already merged patch fields onto a fresh Collector and
// bumped UpdatedAt. Returns ErrCollectorNotFound if no row matched
// and ErrCollectorConflict on unique-name violation.
func (r *CollectorPostgres) Update(ctx context.Context, tx domain.Tx, c domain.Collector) error {
	exec := r.execer(tx)
	const q = `
		UPDATE collectors
		   SET name = $2, notes = $3, updated_at = $4
		 WHERE id = $1`
	tag, err := exec.Exec(ctx, q, c.ID, c.Name, c.Notes, c.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrCollectorConflict
		}
		return fmt.Errorf("collector repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCollectorNotFound
	}
	return nil
}

// Delete removes a collector. If the row is referenced by
// specimen_collectors, the FK ON DELETE RESTRICT (per migration
// 0001_init) raises a foreign_key_violation, which this method maps
// to domain.ErrCollectorReferenced (translated to 409 by the handler).
func (r *CollectorPostgres) Delete(ctx context.Context, tx domain.Tx, id uuid.UUID) error {
	exec := r.execer(tx)
	const q = `DELETE FROM collectors WHERE id = $1`
	tag, err := exec.Exec(ctx, q, id)
	if err != nil {
		if isFKViolation(err) {
			return domain.ErrCollectorReferenced
		}
		return fmt.Errorf("collector repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrCollectorNotFound
	}
	return nil
}

// List returns up to filter+page.Limit collectors ordered by
// (created_at DESC, id DESC). The returned cursor encodes the
// (created_at, id) of the LAST row when more rows may exist;
// otherwise it is empty.
func (r *CollectorPostgres) List(
	ctx context.Context, filter domain.CollectorFilter, page domain.Page,
) ([]domain.Collector, domain.Cursor, error) {
	limit := clampLimit(page.Limit)

	curTS, curID, err := DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("collector repo: list: %w", err)
	}

	args := []any{limit + 1} // fetch one extra to detect end-of-results
	var where []string
	if page.Cursor != "" {
		// Keyset condition for `(created_at, id) DESC` ordering: we want rows
		// strictly older than the cursor row, with id as the tiebreaker.
		where = append(where, fmt.Sprintf(
			"(created_at, id) < ($%d, $%d)", len(args)+1, len(args)+2))
		args = append(args, curTS, curID)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		where = append(where, fmt.Sprintf("name ILIKE $%d", len(args)+1))
		// Escape LIKE metacharacters in user input then wrap with %.
		args = append(args, "%"+escapeLike(q)+"%")
	}
	// CONTRACT.md §13 v2 layer-1: the `user` role only ever has
	// `collectors:*:own` — list returns the caller's own rows (admin
	// sees all; anonymous sees none).
	if clause, scoped := ownerScope(auth.FromContext(ctx), "author_id", args); clause != "" {
		where = append(where, clause)
		args = scoped
	}

	sql := `
		SELECT id, name, notes, author_id, created_at, updated_at
		FROM collectors`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT $1"

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("collector repo: list: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Collector, 0, limit)
	for rows.Next() {
		c, err := scanCollector(rows)
		if err != nil {
			return nil, "", fmt.Errorf("collector repo: list: scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("collector repo: list: rows: %w", err)
	}

	var cursor domain.Cursor
	if len(out) > limit {
		// We fetched limit+1 — drop the extra and emit a cursor pointing
		// at the LAST item the client will see.
		out = out[:limit]
		last := out[limit-1]
		cursor = domain.Cursor(EncodeCursor(last.CreatedAt, last.ID))
	}
	return out, cursor, nil
}

// scanCollector reads one row in the canonical column order shared by
// every collector query. Accepts pgx.Row (single-row QueryRow) and
// pgx.Rows.
func scanCollector(s rowScanner) (domain.Collector, error) {
	var c domain.Collector
	var notes *string
	var createdAt, updatedAt time.Time
	if err := s.Scan(&c.ID, &c.Name, &notes, &c.AuthorID, &createdAt, &updatedAt); err != nil {
		return domain.Collector{}, err
	}
	c.Notes = notes
	c.CreatedAt = createdAt
	c.UpdatedAt = updatedAt
	return c, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// execer returns the Tx caller-supplied, falling back to the bound
// pool when nil — keeps callers from juggling nil checks.
func (r *CollectorPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}

func clampLimit(in int) int {
	if in <= 0 {
		return defaultListLimit
	}
	if in > MaxListLimit {
		return MaxListLimit
	}
	return in
}

// escapeLike escapes the pattern metacharacters consumed by ILIKE so
// user-controlled query strings can't accidentally turn into wildcard
// matches.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
