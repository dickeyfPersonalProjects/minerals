package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// MineralSpeciesPostgres is the Postgres-backed
// domain.MineralSpeciesRepo (mi-dtg / F-1).
type MineralSpeciesPostgres struct{ pool *pgxpool.Pool }

// NewMineralSpeciesPostgres constructs a MineralSpeciesPostgres
// bound to pool.
func NewMineralSpeciesPostgres(pool *pgxpool.Pool) *MineralSpeciesPostgres {
	return &MineralSpeciesPostgres{pool: pool}
}

// Create inserts a new mineral_species row. The caller's UUIDv7
// must already be in s.ID and CreatedAt/UpdatedAt must be set;
// author_id is taken from auth context (per CONTRACT.md §11 / §13).
func (r *MineralSpeciesPostgres) Create(ctx context.Context, tx domain.Tx, s domain.MineralSpecies) error {
	exec := r.execer(tx)
	user := auth.FromContext(ctx)
	const q = `
		INSERT INTO mineral_species
			(id, name, source, mindat_id, data, attribution, author_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	data := s.Data
	if len(data) == 0 {
		data = []byte("{}")
	}
	_, err := exec.Exec(ctx, q,
		s.ID, s.Name, string(s.Source), s.MindatID, data, s.Attribution,
		user.ID, s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrMineralSpeciesConflict
		}
		return fmt.Errorf("mineral_species repo: create: %w", err)
	}
	return nil
}

// GetByID returns the row identified by id, or
// domain.ErrMineralSpeciesNotFound.
func (r *MineralSpeciesPostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.MineralSpecies, error) {
	const q = `
		SELECT id, name, source, mindat_id, data, attribution, author_id, created_at, updated_at
		FROM mineral_species WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	s, err := scanMineralSpecies(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MineralSpecies{}, domain.ErrMineralSpeciesNotFound
	}
	if err != nil {
		return domain.MineralSpecies{}, fmt.Errorf("mineral_species repo: get by id: %w", err)
	}
	return s, nil
}

// FindByName performs a case-insensitive substring match on `name`.
// Empty queries return all rows up to MaxListLimit. Results are
// ordered by (lower(name) ASC, id ASC).
func (r *MineralSpeciesPostgres) FindByName(ctx context.Context, q string) ([]domain.MineralSpecies, error) {
	args := []any{MaxListLimit}
	sql := `
		SELECT id, name, source, mindat_id, data, attribution, author_id, created_at, updated_at
		FROM mineral_species`
	if trimmed := strings.TrimSpace(q); trimmed != "" {
		sql += " WHERE name ILIKE $2"
		args = append(args, "%"+escapeLike(trimmed)+"%")
	}
	sql += " ORDER BY lower(name) ASC, id ASC LIMIT $1"

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("mineral_species repo: find by name: %w", err)
	}
	defer rows.Close()

	out := make([]domain.MineralSpecies, 0)
	for rows.Next() {
		s, err := scanMineralSpecies(rows)
		if err != nil {
			return nil, fmt.Errorf("mineral_species repo: find by name: scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mineral_species repo: find by name: rows: %w", err)
	}
	return out, nil
}

// FindByMindatID returns the row whose mindat_id matches, or
// domain.ErrMineralSpeciesNotFound.
func (r *MineralSpeciesPostgres) FindByMindatID(ctx context.Context, mindatID string) (domain.MineralSpecies, error) {
	const q = `
		SELECT id, name, source, mindat_id, data, attribution, author_id, created_at, updated_at
		FROM mineral_species WHERE mindat_id = $1`
	row := r.pool.QueryRow(ctx, q, mindatID)
	s, err := scanMineralSpecies(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MineralSpecies{}, domain.ErrMineralSpeciesNotFound
	}
	if err != nil {
		return domain.MineralSpecies{}, fmt.Errorf("mineral_species repo: find by mindat_id: %w", err)
	}
	return s, nil
}

func scanMineralSpecies(s rowScanner) (domain.MineralSpecies, error) {
	var out domain.MineralSpecies
	var mindatID, attribution *string
	var sourceStr string
	var data []byte
	var createdAt, updatedAt time.Time
	if err := s.Scan(
		&out.ID, &out.Name, &sourceStr, &mindatID, &data, &attribution,
		&out.AuthorID, &createdAt, &updatedAt,
	); err != nil {
		return domain.MineralSpecies{}, err
	}
	out.Source = domain.MineralSpeciesSource(sourceStr)
	out.MindatID = mindatID
	out.Attribution = attribution
	out.Data = data
	out.CreatedAt = createdAt
	out.UpdatedAt = updatedAt
	return out, nil
}

func (r *MineralSpeciesPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}
