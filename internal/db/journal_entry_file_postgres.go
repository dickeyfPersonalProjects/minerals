package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// JournalEntryFilePostgres is the Postgres-backed
// domain.JournalEntryFileRepo (mi-720 / C-2). The journal_entry_files
// table is the join between a journal entry and a stored file —
// rows have (entry_id, file_id) as their composite primary key, and
// the layer above this repo is responsible for the §12 transactional
// MinIO+files+journal_entry_files write.
type JournalEntryFilePostgres struct{ pool *pgxpool.Pool }

// NewJournalEntryFilePostgres constructs a JournalEntryFilePostgres
// bound to pool.
func NewJournalEntryFilePostgres(pool *pgxpool.Pool) *JournalEntryFilePostgres {
	return &JournalEntryFilePostgres{pool: pool}
}

// Create inserts a journal_entry_files row. Foreign-key violations on
// entry_id surface as ErrJournalEntryNotFound (the entry vanished
// mid-request); the file_id FK is satisfied by the caller's prior
// files-row insert in the same transaction.
func (r *JournalEntryFilePostgres) Create(ctx context.Context, tx domain.Tx, j domain.JournalEntryFile) error {
	exec := r.execer(tx)
	const q = `
		INSERT INTO journal_entry_files (entry_id, file_id, position, created_at)
		VALUES ($1, $2, $3, $4)`
	_, err := exec.Exec(ctx, q, j.EntryID, j.FileID, j.Position, j.CreatedAt)
	if err != nil {
		if isFKViolation(err) {
			return domain.ErrJournalEntryNotFound
		}
		return fmt.Errorf("journal entry file repo: create: %w", err)
	}
	return nil
}

// GetByFileID returns the join row for a given file_id, or
// ErrJournalAttachmentNotFound when no attachment row exists. file_id
// is unique within journal_entry_files (a file is attached to at
// most one entry — the v1 design doesn't share files between entries).
func (r *JournalEntryFilePostgres) GetByFileID(ctx context.Context, fileID uuid.UUID) (domain.JournalEntryFile, error) {
	const q = `
		SELECT entry_id, file_id, position, created_at
		FROM journal_entry_files WHERE file_id = $1`
	row := r.pool.QueryRow(ctx, q, fileID)
	j, err := scanJournalEntryFile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.JournalEntryFile{}, domain.ErrJournalAttachmentNotFound
	}
	if err != nil {
		return domain.JournalEntryFile{}, fmt.Errorf("journal entry file repo: get by file id: %w", err)
	}
	return j, nil
}

// ListByEntry returns all attachments for an entry, ordered by
// (position ASC, created_at ASC, file_id ASC). Attachments are
// expected to be small in count (a journal entry has at most a
// handful of files in practice) so v1 does not paginate this list —
// the caller hydrates each row's File metadata in the same response.
func (r *JournalEntryFilePostgres) ListByEntry(ctx context.Context, entryID uuid.UUID) ([]domain.JournalEntryFile, error) {
	const q = `
		SELECT entry_id, file_id, position, created_at
		FROM journal_entry_files
		WHERE entry_id = $1
		ORDER BY position ASC, created_at ASC, file_id ASC`
	rows, err := r.pool.Query(ctx, q, entryID)
	if err != nil {
		return nil, fmt.Errorf("journal entry file repo: list: %w", err)
	}
	defer rows.Close()

	var out []domain.JournalEntryFile
	for rows.Next() {
		j, err := scanJournalEntryFile(rows)
		if err != nil {
			return nil, fmt.Errorf("journal entry file repo: list: scan: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal entry file repo: list: rows: %w", err)
	}
	return out, nil
}

// Delete removes the journal_entry_files row for a given file_id.
// Returns ErrJournalAttachmentNotFound when no row matched. The
// caller is responsible for removing the underlying files row in
// the same transaction (the service layer wraps both).
func (r *JournalEntryFilePostgres) Delete(ctx context.Context, tx domain.Tx, fileID uuid.UUID) error {
	exec := r.execer(tx)
	const q = `DELETE FROM journal_entry_files WHERE file_id = $1`
	tag, err := exec.Exec(ctx, q, fileID)
	if err != nil {
		return fmt.Errorf("journal entry file repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrJournalAttachmentNotFound
	}
	return nil
}

// MaxPosition returns the largest `position` currently in use on the
// entry's attachments, or 0 if there are none.
func (r *JournalEntryFilePostgres) MaxPosition(ctx context.Context, tx domain.Tx, entryID uuid.UUID) (int, error) {
	exec := r.execer(tx)
	const q = `SELECT COALESCE(MAX(position), 0) FROM journal_entry_files WHERE entry_id = $1`
	var max int
	if err := exec.QueryRow(ctx, q, entryID).Scan(&max); err != nil {
		return 0, fmt.Errorf("journal entry file repo: max position: %w", err)
	}
	return max, nil
}

func scanJournalEntryFile(s rowScanner) (domain.JournalEntryFile, error) {
	var j domain.JournalEntryFile
	var createdAt time.Time
	if err := s.Scan(&j.EntryID, &j.FileID, &j.Position, &createdAt); err != nil {
		return domain.JournalEntryFile{}, err
	}
	j.CreatedAt = createdAt.UTC()
	return j, nil
}

func (r *JournalEntryFilePostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}
