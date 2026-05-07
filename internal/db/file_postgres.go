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

// FilePostgres is the Postgres-backed domain.FileRepo (mi-jpu / B-3).
type FilePostgres struct{ pool *pgxpool.Pool }

// NewFilePostgres constructs a FilePostgres bound to pool.
func NewFilePostgres(pool *pgxpool.Pool) *FilePostgres {
	return &FilePostgres{pool: pool}
}

// Create inserts a new files row. uploaded_by is taken from
// auth.FromContext (per CONTRACT.md §11/§13). The caller has already
// populated f.ID (UUIDv7 per §11).
func (r *FilePostgres) Create(ctx context.Context, tx domain.Tx, f domain.File) error {
	exec := r.execer(tx)
	user := auth.FromContext(ctx)
	const q = `
		INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := exec.Exec(ctx, q, f.ID, f.S3Key, f.ContentType, f.ByteSize, f.SHA256, user.ID, f.UploadedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrFileConflict
		}
		return fmt.Errorf("file repo: create: %w", err)
	}
	return nil
}

// GetByID returns the file with the given id, or domain.ErrFileNotFound.
func (r *FilePostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.File, error) {
	const q = `
		SELECT id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at
		FROM files WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	f, err := scanFile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.File{}, domain.ErrFileNotFound
	}
	if err != nil {
		return domain.File{}, fmt.Errorf("file repo: get by id: %w", err)
	}
	return f, nil
}

// Delete removes a files row. Returns ErrFileNotFound if no row matched.
// FK references from photos/journal_entry_files use ON DELETE RESTRICT
// — deleting a file that's still referenced surfaces as a wrapped
// 23503 fk_violation; callers MUST delete the parent row first.
func (r *FilePostgres) Delete(ctx context.Context, tx domain.Tx, id uuid.UUID) error {
	exec := r.execer(tx)
	const q = `DELETE FROM files WHERE id = $1`
	tag, err := exec.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("file repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrFileNotFound
	}
	return nil
}

func scanFile(s rowScanner) (domain.File, error) {
	var f domain.File
	var uploadedAt time.Time
	if err := s.Scan(&f.ID, &f.S3Key, &f.ContentType, &f.ByteSize, &f.SHA256, &f.UploadedBy, &uploadedAt); err != nil {
		return domain.File{}, err
	}
	f.UploadedAt = uploadedAt
	return f, nil
}

func (r *FilePostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}
