package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// AccountErasePostgres is the Postgres-backed domain.AccountEraser
// (mi-nwg5). It runs the GDPR right-to-erasure cascade for one user in
// a single transaction.
type AccountErasePostgres struct{ pool *pgxpool.Pool }

// NewAccountErasePostgres constructs an AccountErasePostgres bound to
// pool.
func NewAccountErasePostgres(pool *pgxpool.Pool) *AccountErasePostgres {
	return &AccountErasePostgres{pool: pool}
}

// Erase cascade-deletes the user's personal data and the user row in
// one transaction, returning the audit summary. See domain.AccountEraser
// for the policy contract. The deletion ORDER is dictated by the
// RESTRICT foreign keys (migration 0001/0011):
//
//	files.id is referenced ON DELETE RESTRICT by photos.file_id and
//	journal_entry_files.file_id, so the files rows can only be deleted
//	after the specimens (which CASCADE-delete those photo and
//	journal-entry-file rows) are gone. collectors and the users row
//	follow the same "children first" discipline.
func (r *AccountErasePostgres) Erase(ctx context.Context, id uuid.UUID) (domain.AccountErasure, error) {
	if id == auth.StubUser.ID {
		return domain.AccountErasure{}, domain.ErrStubUserUndeletable
	}

	var out domain.AccountErasure
	err := RunInTx(ctx, r.pool, func(tx pgx.Tx) error {
		// Guard: the row must exist. Doing this inside the tx keeps the
		// existence check and the cascade atomic.
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id,
		).Scan(&exists); err != nil {
			return fmt.Errorf("account erase: existence check: %w", err)
		}
		if !exists {
			return domain.ErrUserNotFound
		}

		// 1. Capture the object-store keys to purge post-commit. Done
		//    first so the rows are still present to read from.
		keys, err := collectFileKeys(ctx, tx, id)
		if err != nil {
			return err
		}
		out.FreedObjectKeys = keys

		// 2. Count photos + journal entries before the specimen cascade
		//    removes them (they have no direct author column to delete
		//    by, so the audit count must be read up front).
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM photos p
			   JOIN specimens s ON s.id = p.specimen_id
			  WHERE s.author_id = $1`, id,
		).Scan(&out.Photos); err != nil {
			return fmt.Errorf("account erase: count photos: %w", err)
		}
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM journal_entries WHERE author_id = $1`, id,
		).Scan(&out.JournalEntries); err != nil {
			return fmt.Errorf("account erase: count journal entries: %w", err)
		}

		// 3. qr_sheets (cascades qr_sheet_specimens). Independent of the
		//    specimen cascade, so order here is not load-bearing.
		if out.QRSheets, err = execCount(ctx, tx,
			`DELETE FROM qr_sheets WHERE user_id = $1`, id); err != nil {
			return fmt.Errorf("account erase: delete qr_sheets: %w", err)
		}

		// 4. specimens — CASCADE removes photos, journal_entries (->
		//    journal_entry_files), specimen_collectors, and any
		//    qr_sheet_specimens rows that referenced them.
		if out.Specimens, err = execCount(ctx, tx,
			`DELETE FROM specimens WHERE author_id = $1`, id); err != nil {
			return fmt.Errorf("account erase: delete specimens: %w", err)
		}

		// 5. files — now unreferenced (the photos and journal-entry-file
		//    links that RESTRICT-referenced them are gone with step 4).
		if out.Files, err = execCount(ctx, tx,
			`DELETE FROM files WHERE uploaded_by = $1`, id); err != nil {
			return fmt.Errorf("account erase: delete files: %w", err)
		}

		// 6. collectors — specimen_collectors links from the user's own
		//    specimens cascaded in step 4. A collector still referenced
		//    by another user's specimen would trip the RESTRICT FK and
		//    fail the whole transaction (the safe outcome — see the
		//    shared-collector note in mi-nwg5).
		if out.Collectors, err = execCount(ctx, tx,
			`DELETE FROM collectors WHERE author_id = $1`, id); err != nil {
			return fmt.Errorf("account erase: delete collectors: %w", err)
		}

		// 7. mineral_species — REASSIGN to stub-overseer rather than
		//    delete: the catalog is canonical reference data (migration
		//    0002), and author_id there is provenance, not personal
		//    content. Reassigning satisfies the RESTRICT FK on the
		//    upcoming users delete without losing shared lookups.
		if out.ReassignedSpecies, err = execCount(ctx, tx,
			`UPDATE mineral_species SET author_id = $1 WHERE author_id = $2`,
			auth.StubUser.ID, id); err != nil {
			return fmt.Errorf("account erase: reassign mineral_species: %w", err)
		}

		// 8. the users row — shares cascade with it (migration 0010).
		tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("account erase: delete user: %w", err)
		}
		if tag.RowsAffected() == 0 {
			// Lost a race with a concurrent delete after the existence
			// check; treat as not-found rather than reporting success.
			return domain.ErrUserNotFound
		}
		return nil
	})
	if err != nil {
		return domain.AccountErasure{}, err
	}
	return out, nil
}

// collectFileKeys returns every files.s3_key the user owns, read inside
// the erase transaction before the rows are deleted.
func collectFileKeys(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT s3_key FROM files WHERE uploaded_by = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("account erase: collect file keys: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("account erase: scan file key: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account erase: iterate file keys: %w", err)
	}
	return keys, nil
}

// execCount runs an INSERT/UPDATE/DELETE and returns RowsAffected.
func execCount(ctx context.Context, tx pgx.Tx, sql string, args ...any) (int64, error) {
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
