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

// QRSheetPostgres is the Postgres-backed domain.QRSheetRepo (mi-c78.1).
// The tables are documented in migrations/0003_qr_sheets.up.sql.
type QRSheetPostgres struct{ pool *pgxpool.Pool }

// NewQRSheetPostgres constructs a QRSheetPostgres bound to pool.
func NewQRSheetPostgres(pool *pgxpool.Pool) *QRSheetPostgres {
	return &QRSheetPostgres{pool: pool}
}

// GetByUser returns the user's active sheet or domain.ErrQRSheetNotFound.
func (r *QRSheetPostgres) GetByUser(
	ctx context.Context, userID uuid.UUID,
) (domain.QRSheet, error) {
	const q = `
		SELECT id, user_id, template, created_at, updated_at
		  FROM qr_sheets
		 WHERE user_id = $1`
	row := r.pool.QueryRow(ctx, q, userID)
	var s domain.QRSheet
	var template string
	var createdAt, updatedAt time.Time
	err := row.Scan(&s.ID, &s.UserID, &template, &createdAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.QRSheet{}, domain.ErrQRSheetNotFound
	}
	if err != nil {
		return domain.QRSheet{}, fmt.Errorf("qr_sheet repo: get by user: %w", err)
	}
	s.Template = domain.QRSheetTemplate(template)
	s.CreatedAt = createdAt
	s.UpdatedAt = updatedAt
	return s, nil
}

// Create inserts a new qr_sheets row. The unique constraint on
// user_id surfaces as domain.ErrQRSheetConflict.
func (r *QRSheetPostgres) Create(ctx context.Context, tx domain.Tx, s domain.QRSheet) error {
	exec := r.execer(tx)
	const q = `
		INSERT INTO qr_sheets (id, user_id, template, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`
	_, err := exec.Exec(ctx, q, s.ID, s.UserID, string(s.Template), s.CreatedAt, s.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrQRSheetConflict
		}
		return fmt.Errorf("qr_sheet repo: create: %w", err)
	}
	return nil
}

// UpdateTemplate sets the template on the user's sheet and bumps
// updated_at. Returns domain.ErrQRSheetNotFound when the user has no sheet.
func (r *QRSheetPostgres) UpdateTemplate(
	ctx context.Context, tx domain.Tx, userID uuid.UUID,
	template domain.QRSheetTemplate, updatedAt time.Time,
) error {
	exec := r.execer(tx)
	const q = `
		UPDATE qr_sheets
		   SET template = $2, updated_at = $3
		 WHERE user_id = $1`
	tag, err := exec.Exec(ctx, q, userID, string(template), updatedAt)
	if err != nil {
		return fmt.Errorf("qr_sheet repo: update template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrQRSheetNotFound
	}
	return nil
}

// Delete removes the user's sheet (cascade drops qr_sheet_specimens).
func (r *QRSheetPostgres) Delete(
	ctx context.Context, tx domain.Tx, userID uuid.UUID,
) error {
	exec := r.execer(tx)
	const q = `DELETE FROM qr_sheets WHERE user_id = $1`
	tag, err := exec.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("qr_sheet repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrQRSheetNotFound
	}
	return nil
}

// AddSpecimen appends specimenID to the end of the user's sheet.
// Idempotent — already-present specimens succeed without changing
// position. Runs in its own transaction when tx is nil so the
// sheet-existence probe, MAX(position) read, and INSERT land
// atomically (preventing two concurrent adds from racing onto the
// same position).
func (r *QRSheetPostgres) AddSpecimen(
	ctx context.Context, tx domain.Tx, userID, specimenID uuid.UUID, addedAt time.Time,
) error {
	if tx != nil {
		return r.addSpecimenTx(ctx, tx, userID, specimenID, addedAt)
	}
	return RunInTx(ctx, r.pool, func(pgxTx pgx.Tx) error {
		return r.addSpecimenTx(ctx, pgxTx, userID, specimenID, addedAt)
	})
}

func (r *QRSheetPostgres) addSpecimenTx(
	ctx context.Context, tx domain.Tx, userID, specimenID uuid.UUID, addedAt time.Time,
) error {
	// Look up the sheet id for this user (and lock the row so a
	// concurrent DELETE on the sheet can't tear the INSERT below
	// in half).
	var sheetID uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM qr_sheets WHERE user_id = $1 FOR UPDATE`,
		userID,
	).Scan(&sheetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrQRSheetNotFound
	}
	if err != nil {
		return fmt.Errorf("qr_sheet repo: add specimen: load sheet: %w", err)
	}

	// Idempotency: already-present specimen → success, no change.
	var existing bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM qr_sheet_specimens
			 WHERE sheet_id = $1 AND specimen_id = $2)`,
		sheetID, specimenID,
	).Scan(&existing); err != nil {
		return fmt.Errorf("qr_sheet repo: add specimen: probe: %w", err)
	}
	if existing {
		return nil
	}

	// Next position = max(position) + 1, or 1 when the sheet is empty.
	var nextPos int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(position), 0) + 1
		   FROM qr_sheet_specimens WHERE sheet_id = $1`,
		sheetID,
	).Scan(&nextPos); err != nil {
		return fmt.Errorf("qr_sheet repo: add specimen: max position: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO qr_sheet_specimens
			(id, sheet_id, specimen_id, position, added_at)
		VALUES ($1, $2, $3, $4, $5)`,
		domain.NewID(), sheetID, specimenID, nextPos, addedAt,
	); err != nil {
		if isFKViolation(err) {
			// Either sheet_id or specimen_id missed. The sheet was
			// just probed under FOR UPDATE, so this is the specimen.
			return domain.ErrSpecimenNotFound
		}
		if isUniqueViolation(err) {
			// Two concurrent adds raced — the second one should
			// have been caught by the idempotency probe but the
			// row was inserted between the probe and the INSERT.
			// Treat as success: the specimen is now on the sheet.
			return nil
		}
		return fmt.Errorf("qr_sheet repo: add specimen: insert: %w", err)
	}
	return nil
}

// RemoveSpecimen drops a specimen from the user's sheet and repacks
// the remaining positions so they stay contiguous (1..N). The
// (sheet_id, position) UNIQUE constraint is DEFERRABLE so the bulk
// shift doesn't trip mid-statement.
func (r *QRSheetPostgres) RemoveSpecimen(
	ctx context.Context, tx domain.Tx, userID, specimenID uuid.UUID,
) error {
	if tx != nil {
		return r.removeSpecimenTx(ctx, tx, userID, specimenID)
	}
	return RunInTx(ctx, r.pool, func(pgxTx pgx.Tx) error {
		return r.removeSpecimenTx(ctx, pgxTx, userID, specimenID)
	})
}

func (r *QRSheetPostgres) removeSpecimenTx(
	ctx context.Context, tx domain.Tx, userID, specimenID uuid.UUID,
) error {
	var sheetID uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM qr_sheets WHERE user_id = $1 FOR UPDATE`,
		userID,
	).Scan(&sheetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrQRSheetNotFound
	}
	if err != nil {
		return fmt.Errorf("qr_sheet repo: remove specimen: load sheet: %w", err)
	}

	// Defer the (sheet_id, position) uniqueness check to commit so
	// the repack UPDATE below can move rows N→N-1 without colliding
	// with the rows it hasn't moved yet.
	if _, err := tx.Exec(ctx,
		`SET CONSTRAINTS qr_sheet_specimens_sheet_id_position_key DEFERRED`,
	); err != nil {
		return fmt.Errorf("qr_sheet repo: remove specimen: set constraints: %w", err)
	}

	var removedPos int
	err = tx.QueryRow(ctx,
		`DELETE FROM qr_sheet_specimens
		  WHERE sheet_id = $1 AND specimen_id = $2
		RETURNING position`,
		sheetID, specimenID,
	).Scan(&removedPos)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrQRSheetSpecimenNotFound
	}
	if err != nil {
		return fmt.Errorf("qr_sheet repo: remove specimen: delete: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE qr_sheet_specimens
		   SET position = position - 1
		 WHERE sheet_id = $1 AND position > $2`,
		sheetID, removedPos,
	); err != nil {
		return fmt.Errorf("qr_sheet repo: remove specimen: repack: %w", err)
	}
	return nil
}

// ListSpecimens returns the sheet's specimens with their display
// name and lowest-positioned photo id (when any), in position order.
// A LEFT JOIN LATERAL keeps the photo lookup to one row per
// specimen — the v1 "first photo" rule (per design §2).
func (r *QRSheetPostgres) ListSpecimens(
	ctx context.Context, sheetID uuid.UUID,
) ([]domain.QRSheetEntry, error) {
	const q = `
		SELECT qss.specimen_id, qss.position, qss.added_at,
		       sp.name, p.id
		  FROM qr_sheet_specimens qss
		  JOIN specimens sp ON sp.id = qss.specimen_id
		  LEFT JOIN LATERAL (
		      SELECT id FROM photos
		       WHERE specimen_id = qss.specimen_id
		       ORDER BY position ASC, created_at ASC, id ASC
		       LIMIT 1
		  ) p ON true
		 WHERE qss.sheet_id = $1
		 ORDER BY qss.position ASC`
	rows, err := r.pool.Query(ctx, q, sheetID)
	if err != nil {
		return nil, fmt.Errorf("qr_sheet repo: list specimens: %w", err)
	}
	defer rows.Close()

	out := []domain.QRSheetEntry{}
	for rows.Next() {
		var e domain.QRSheetEntry
		var addedAt time.Time
		var photoID *uuid.UUID
		if err := rows.Scan(
			&e.SpecimenID, &e.Position, &addedAt, &e.SpecimenName, &photoID,
		); err != nil {
			return nil, fmt.Errorf("qr_sheet repo: list specimens: scan: %w", err)
		}
		e.AddedAt = addedAt
		e.FirstPhotoID = photoID
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qr_sheet repo: list specimens: rows: %w", err)
	}
	return out, nil
}

func (r *QRSheetPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}

var _ domain.QRSheetRepo = (*QRSheetPostgres)(nil)
