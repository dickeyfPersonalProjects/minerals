package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// JournalEntryPostgres is the Postgres-backed domain.JournalEntryRepo
// (mi-y6b / C-1).
type JournalEntryPostgres struct{ pool *pgxpool.Pool }

// NewJournalEntryPostgres constructs a JournalEntryPostgres bound to pool.
func NewJournalEntryPostgres(pool *pgxpool.Pool) *JournalEntryPostgres {
	return &JournalEntryPostgres{pool: pool}
}

// Create inserts a new journal entry. The caller has already populated
// e.ID (UUIDv7), e.SpecimenID, e.BodyMD, e.CreatedAt and e.UpdatedAt;
// author_id is taken from auth context (per §13). A foreign-key
// violation on specimen_id surfaces as ErrJournalEntryNotFound — the
// caller hits that path when the specimen vanished mid-request.
func (r *JournalEntryPostgres) Create(ctx context.Context, tx domain.Tx, e domain.JournalEntry) error {
	exec := r.execer(tx)
	user := auth.FromContext(ctx)
	const q = `
		INSERT INTO journal_entries (id, specimen_id, author_id, body_md, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := exec.Exec(ctx, q, e.ID, e.SpecimenID, user.ID, e.BodyMD, e.CreatedAt, e.UpdatedAt)
	if err != nil {
		if isFKViolation(err) {
			return domain.ErrJournalEntryNotFound
		}
		return fmt.Errorf("journal entry repo: create: %w", err)
	}
	return nil
}

// GetByID returns the entry with the given id, or
// domain.ErrJournalEntryNotFound.
func (r *JournalEntryPostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.JournalEntry, error) {
	const q = `
		SELECT id, specimen_id, author_id, body_md, created_at, updated_at
		FROM journal_entries WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	e, err := scanJournalEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.JournalEntry{}, domain.ErrJournalEntryNotFound
	}
	if err != nil {
		return domain.JournalEntry{}, fmt.Errorf("journal entry repo: get by id: %w", err)
	}
	return e, nil
}

// Update writes body_md and updated_at to the row identified by e.ID.
// created_at is immutable per §2 — the column is not in the SET list,
// so even if the caller mutated e.CreatedAt the stored value is
// preserved. Returns ErrJournalEntryNotFound when no row matched.
func (r *JournalEntryPostgres) Update(ctx context.Context, tx domain.Tx, e domain.JournalEntry) error {
	exec := r.execer(tx)
	const q = `
		UPDATE journal_entries
		   SET body_md = $2, updated_at = $3
		 WHERE id = $1`
	tag, err := exec.Exec(ctx, q, e.ID, e.BodyMD, e.UpdatedAt)
	if err != nil {
		return fmt.Errorf("journal entry repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrJournalEntryNotFound
	}
	return nil
}

// Delete removes the entry. Returns ErrJournalEntryConflict when the
// entry still has rows in journal_entry_files (attachments land in
// C-2; the bead acceptance criteria require 409 in that case).
// Returns ErrJournalEntryNotFound when no row matched.
func (r *JournalEntryPostgres) Delete(ctx context.Context, tx domain.Tx, id uuid.UUID) error {
	exec := r.execer(tx)

	// Probe for attachments before deleting. Same TOCTOU caveat as
	// SpecimenPostgres.Delete — the v1 single-overseer threat model
	// makes this acceptable; multi-user lands SERIALIZABLE.
	var hasFiles bool
	const probeQ = `SELECT EXISTS (SELECT 1 FROM journal_entry_files WHERE entry_id = $1)`
	if err := exec.QueryRow(ctx, probeQ, id).Scan(&hasFiles); err != nil {
		return fmt.Errorf("journal entry repo: delete: probe attachments: %w", err)
	}
	if hasFiles {
		return domain.ErrJournalEntryConflict
	}

	tag, err := exec.Exec(ctx, `DELETE FROM journal_entries WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("journal entry repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrJournalEntryNotFound
	}
	return nil
}

// ListBySpecimen returns entries for a specimen ordered by
// (created_at DESC, id DESC) — the §10 default. The cursor encodes
// the (created_at, id) of the LAST returned row when more rows may
// exist.
func (r *JournalEntryPostgres) ListBySpecimen(
	ctx context.Context, specimenID uuid.UUID, page domain.Page,
) ([]domain.JournalEntry, domain.Cursor, error) {
	limit := clampLimit(page.Limit)

	curTS, curID, err := DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("journal entry repo: list: %w", err)
	}

	args := []any{specimenID, limit + 1}
	sql := `
		SELECT id, specimen_id, author_id, body_md, created_at, updated_at
		FROM journal_entries
		WHERE specimen_id = $1`
	if page.Cursor != "" {
		sql += fmt.Sprintf(
			" AND (created_at, id) < ($%d, $%d)",
			len(args)+1, len(args)+2)
		args = append(args, curTS, curID)
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT $2"

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("journal entry repo: list: %w", err)
	}
	defer rows.Close()

	out := make([]domain.JournalEntry, 0, limit)
	for rows.Next() {
		e, err := scanJournalEntry(rows)
		if err != nil {
			return nil, "", fmt.Errorf("journal entry repo: list: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("journal entry repo: list: rows: %w", err)
	}

	var cursor domain.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		cursor = domain.Cursor(EncodeCursor(last.CreatedAt, last.ID))
	}
	return out, cursor, nil
}

func scanJournalEntry(s rowScanner) (domain.JournalEntry, error) {
	var e domain.JournalEntry
	var createdAt, updatedAt time.Time
	if err := s.Scan(&e.ID, &e.SpecimenID, &e.AuthorID, &e.BodyMD, &createdAt, &updatedAt); err != nil {
		return domain.JournalEntry{}, err
	}
	e.CreatedAt = createdAt.UTC()
	e.UpdatedAt = updatedAt.UTC()
	return e, nil
}

func (r *JournalEntryPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}
