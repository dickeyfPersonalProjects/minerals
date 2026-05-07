package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// SpecimenCollectorPostgres is the Postgres-backed
// domain.SpecimenCollectorRepo (mi-zv3 / C-3).
type SpecimenCollectorPostgres struct{ pool *pgxpool.Pool }

// NewSpecimenCollectorPostgres constructs a SpecimenCollectorPostgres
// bound to pool.
func NewSpecimenCollectorPostgres(pool *pgxpool.Pool) *SpecimenCollectorPostgres {
	return &SpecimenCollectorPostgres{pool: pool}
}

// GetChain returns every collector linked to specimen_id, ordered by
// position ascending. The collector rows are joined in so the API
// can return the full {collector, position} shape in one round-trip.
func (r *SpecimenCollectorPostgres) GetChain(
	ctx context.Context, tx domain.Tx, specimenID uuid.UUID,
) ([]domain.SpecimenCollectorLink, error) {
	exec := r.execer(tx)
	const q = `
		SELECT c.id, c.name, c.notes, c.author_id, c.created_at, c.updated_at,
		       sc.position
		  FROM specimen_collectors sc
		  JOIN collectors c ON c.id = sc.collector_id
		 WHERE sc.specimen_id = $1
		 ORDER BY sc.position ASC`
	rows, err := exec.Query(ctx, q, specimenID)
	if err != nil {
		return nil, fmt.Errorf("specimen_collector repo: get chain: %w", err)
	}
	defer rows.Close()

	out := []domain.SpecimenCollectorLink{}
	for rows.Next() {
		var c domain.Collector
		var notes *string
		var createdAt, updatedAt time.Time
		var position int
		if err := rows.Scan(
			&c.ID, &c.Name, &notes, &c.AuthorID, &createdAt, &updatedAt, &position,
		); err != nil {
			return nil, fmt.Errorf("specimen_collector repo: get chain: scan: %w", err)
		}
		c.Notes = notes
		c.CreatedAt = createdAt
		c.UpdatedAt = updatedAt
		out = append(out, domain.SpecimenCollectorLink{Collector: c, Position: position})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("specimen_collector repo: get chain: rows: %w", err)
	}
	return out, nil
}

// ReplaceChain atomically replaces every row in specimen_collectors
// for specimen_id with the supplied collector_ids in order. Position
// is 1-indexed (array index + 1). When tx is nil the operation runs
// in its own short-lived transaction so the DELETE+INSERTs land
// together; when tx is non-nil the caller's transaction owns the
// atomicity guarantee.
//
// Returns domain.ErrSpecimenNotFound if specimen_id doesn't exist
// (probed up-front so the API layer can return 404), and
// domain.ErrCollectorNotFound if any collector_id is missing
// (the FK violation is the source of truth, but we probe first to
// preserve the "no partial replace" invariant — a failed insert
// after a successful delete would leave the chain empty when the
// caller asked for a replace).
func (r *SpecimenCollectorPostgres) ReplaceChain(
	ctx context.Context, tx domain.Tx, specimenID uuid.UUID, collectorIDs []uuid.UUID,
) error {
	if tx != nil {
		return r.replaceChainTx(ctx, tx, specimenID, collectorIDs)
	}
	return RunInTx(ctx, r.pool, func(pgxTx pgx.Tx) error {
		return r.replaceChainTx(ctx, pgxTx, specimenID, collectorIDs)
	})
}

func (r *SpecimenCollectorPostgres) replaceChainTx(
	ctx context.Context, tx domain.Tx, specimenID uuid.UUID, collectorIDs []uuid.UUID,
) error {
	// Probe specimen existence so a "no such specimen" replace returns
	// ErrSpecimenNotFound even when the chain was empty (a bare DELETE
	// on the join table would return 0 rows for both cases).
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM specimens WHERE id = $1)`, specimenID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("specimen_collector repo: replace chain: probe specimen: %w", err)
	}
	if !exists {
		return domain.ErrSpecimenNotFound
	}

	// Probe every collector_id up-front. The FK on insert would catch
	// a missing one, but a partial INSERT before the failure could
	// leave the chain in a half-replaced state — atomic-replace is
	// the contract.
	if len(collectorIDs) > 0 {
		var presentCount int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM collectors WHERE id = ANY($1)`, collectorIDs,
		).Scan(&presentCount); err != nil {
			return fmt.Errorf("specimen_collector repo: replace chain: probe collectors: %w", err)
		}
		// Distinct count handles the duplicate-id case at a different
		// layer (the API rejects duplicates with 400 before we get
		// here). When called from the API path collectorIDs is already
		// dedup-validated; the mismatch here means at least one id
		// doesn't exist.
		if presentCount < distinctCount(collectorIDs) {
			return domain.ErrCollectorNotFound
		}
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM specimen_collectors WHERE specimen_id = $1`, specimenID,
	); err != nil {
		return fmt.Errorf("specimen_collector repo: replace chain: delete: %w", err)
	}

	if len(collectorIDs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	const insert = `
		INSERT INTO specimen_collectors (specimen_id, collector_id, position, created_at)
		VALUES ($1, $2, $3, $4)`
	for i, cid := range collectorIDs {
		if _, err := tx.Exec(ctx, insert, specimenID, cid, i+1, now); err != nil {
			// FK violation here would mean a collector was deleted
			// between the probe and the insert (TOCTOU). v1 is
			// single-overseer; mapping it to ErrCollectorNotFound
			// keeps the semantics consistent.
			if isFKViolation(err) {
				return domain.ErrCollectorNotFound
			}
			if isUniqueViolation(err) {
				// Caller passed a duplicate that survived API-layer
				// validation. Surface as bad input via Conflict so
				// the API maps it to a 400-ish response.
				return domain.ErrCollectorConflict
			}
			return fmt.Errorf("specimen_collector repo: replace chain: insert: %w", err)
		}
	}
	return nil
}

// distinctCount returns the number of unique uuids in ids.
func distinctCount(ids []uuid.UUID) int {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	return len(seen)
}

// execer returns the Tx caller-supplied, falling back to the bound
// pool when nil — keeps callers from juggling nil checks.
func (r *SpecimenCollectorPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}

// Compile-time guard so the impl keeps satisfying the interface.
var _ domain.SpecimenCollectorRepo = (*SpecimenCollectorPostgres)(nil)
