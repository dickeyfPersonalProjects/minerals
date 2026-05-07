package db

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// PhotoPostgres is the Postgres-backed domain.PhotoRepo (mi-jpu / B-3).
type PhotoPostgres struct{ pool *pgxpool.Pool }

// NewPhotoPostgres constructs a PhotoPostgres bound to pool.
func NewPhotoPostgres(pool *pgxpool.Pool) *PhotoPostgres {
	return &PhotoPostgres{pool: pool}
}

// Create inserts a new photo row. The caller has already populated
// p.ID (UUIDv7), p.SpecimenID, p.FileID, p.Position, and p.CreatedAt.
// Foreign-key violations on specimen_id or file_id surface as
// ErrPhotoNotFound (more useful than leaking the constraint name).
func (r *PhotoPostgres) Create(ctx context.Context, tx domain.Tx, p domain.Photo) error {
	exec := r.execer(tx)
	const q = `
		INSERT INTO photos (id, specimen_id, file_id, taken_at, position, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := exec.Exec(ctx, q, p.ID, p.SpecimenID, p.FileID, p.TakenAt, p.Position, p.CreatedAt)
	if err != nil {
		if isFKViolation(err) {
			// Either specimen_id or file_id didn't resolve. Return a
			// not-found sentinel; the handler maps to 404.
			return domain.ErrPhotoNotFound
		}
		return fmt.Errorf("photo repo: create: %w", err)
	}
	return nil
}

// GetByID returns the photo with the given id, or domain.ErrPhotoNotFound.
func (r *PhotoPostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.Photo, error) {
	const q = `
		SELECT id, specimen_id, file_id, taken_at, position, created_at
		FROM photos WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	p, err := scanPhoto(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Photo{}, domain.ErrPhotoNotFound
	}
	if err != nil {
		return domain.Photo{}, fmt.Errorf("photo repo: get by id: %w", err)
	}
	return p, nil
}

// Update writes taken_at + position to the row identified by p.ID. The
// caller has already merged patch fields onto a fresh Photo. Returns
// ErrPhotoNotFound when no row matched.
func (r *PhotoPostgres) Update(ctx context.Context, tx domain.Tx, p domain.Photo) error {
	exec := r.execer(tx)
	const q = `
		UPDATE photos
		   SET taken_at = $2, position = $3
		 WHERE id = $1`
	tag, err := exec.Exec(ctx, q, p.ID, p.TakenAt, p.Position)
	if err != nil {
		return fmt.Errorf("photo repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPhotoNotFound
	}
	return nil
}

// Delete removes a photo row. Returns ErrPhotoNotFound when no row
// matched. The associated files row is removed by the service layer
// in the same transaction (per §12 transactional cleanup).
func (r *PhotoPostgres) Delete(ctx context.Context, tx domain.Tx, id uuid.UUID) error {
	exec := r.execer(tx)
	const q = `DELETE FROM photos WHERE id = $1`
	tag, err := exec.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("photo repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPhotoNotFound
	}
	return nil
}

// ListBySpecimen returns photos for a specimen ordered by
// (position ASC, created_at ASC, id ASC) — the manual ordering the
// user controls, with stable tiebreaks. The cursor encodes the
// position+created_at+id of the LAST returned row when more rows may
// exist.
func (r *PhotoPostgres) ListBySpecimen(
	ctx context.Context, specimenID uuid.UUID, page domain.Page,
) ([]domain.Photo, domain.Cursor, error) {
	limit := clampLimit(page.Limit)

	curPos, curTS, curID, err := decodePhotoCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("photo repo: list: %w", err)
	}

	args := []any{specimenID, limit + 1}
	sql := `
		SELECT id, specimen_id, file_id, taken_at, position, created_at
		FROM photos
		WHERE specimen_id = $1`
	if page.Cursor != "" {
		// Keyset condition for ascending (position, created_at, id) — fetch rows
		// strictly AFTER the cursor row.
		sql += fmt.Sprintf(
			" AND (position, created_at, id) > ($%d, $%d, $%d)",
			len(args)+1, len(args)+2, len(args)+3)
		args = append(args, curPos, curTS, curID)
	}
	sql += " ORDER BY position ASC, created_at ASC, id ASC LIMIT $2"

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("photo repo: list: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Photo, 0, limit)
	for rows.Next() {
		p, err := scanPhoto(rows)
		if err != nil {
			return nil, "", fmt.Errorf("photo repo: list: scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("photo repo: list: rows: %w", err)
	}

	var cursor domain.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		cursor = domain.Cursor(encodePhotoCursor(last.Position, last.CreatedAt, last.ID))
	}
	return out, cursor, nil
}

// MaxPosition returns the largest `position` currently in use on the
// specimen's photos, or 0 if the specimen has none.
func (r *PhotoPostgres) MaxPosition(ctx context.Context, tx domain.Tx, specimenID uuid.UUID) (int, error) {
	exec := r.execer(tx)
	const q = `SELECT COALESCE(MAX(position), 0) FROM photos WHERE specimen_id = $1`
	var max int
	if err := exec.QueryRow(ctx, q, specimenID).Scan(&max); err != nil {
		return 0, fmt.Errorf("photo repo: max position: %w", err)
	}
	return max, nil
}

func scanPhoto(s rowScanner) (domain.Photo, error) {
	var p domain.Photo
	var takenAt *time.Time
	var createdAt time.Time
	if err := s.Scan(&p.ID, &p.SpecimenID, &p.FileID, &takenAt, &p.Position, &createdAt); err != nil {
		return domain.Photo{}, err
	}
	p.TakenAt = takenAt
	p.CreatedAt = createdAt
	return p, nil
}

func (r *PhotoPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}

// photoCursor is the encoded shape for photo list cursors. Photos
// order ascending by (position, created_at, id), so the cursor needs
// all three.
type photoCursor struct {
	Position  int       `json:"p"`
	CreatedAt time.Time `json:"t"`
	ID        uuid.UUID `json:"i"`
}

func encodePhotoCursor(pos int, createdAt time.Time, id uuid.UUID) string {
	c := photoCursor{Position: pos, CreatedAt: createdAt.UTC(), ID: id}
	b, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Errorf("photo cursor marshal: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePhotoCursor(s string) (int, time.Time, uuid.UUID, error) {
	if s == "" {
		return 0, time.Time{}, uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, time.Time{}, uuid.Nil, fmt.Errorf("cursor: invalid base64: %w", err)
	}
	var c photoCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, time.Time{}, uuid.Nil, fmt.Errorf("cursor: invalid payload: %w", err)
	}
	if c.ID == uuid.Nil {
		return 0, time.Time{}, uuid.Nil, fmt.Errorf("cursor: missing id")
	}
	return c.Position, c.CreatedAt, c.ID, nil
}
