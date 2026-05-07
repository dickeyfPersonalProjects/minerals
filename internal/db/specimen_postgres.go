package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// SpecimenPostgres is the Postgres-backed domain.SpecimenRepo. The
// row-shape mirrors migrations/0001_init.up.sql; type_data goes in/out
// as bytes (the service layer is responsible for the typed
// marshal/unmarshal per CONTRACT.md §11).
type SpecimenPostgres struct{ pool *pgxpool.Pool }

// NewSpecimenPostgres constructs a SpecimenPostgres bound to pool.
func NewSpecimenPostgres(pool *pgxpool.Pool) *SpecimenPostgres {
	return &SpecimenPostgres{pool: pool}
}

// Pool exposes the underlying connection pool so handlers can wrap
// multi-step operations in RunInTx without reaching for a global.
func (r *SpecimenPostgres) Pool() *pgxpool.Pool { return r.pool }

// specimenInsertColumns is the bare column list used in INSERT.
const specimenInsertColumns = `id, type, catalog_number, name, description, visibility,
	author_id, acquired_at, acquired_from, price_cents, source_notes,
	locality_text, locality, mass_g, dimensions, type_data,
	created_at, updated_at`

// specimenSelectColumns is the SELECT list. mass_g (numeric) is cast
// to float8 so pgx scans straight into *float64 — pgx v5 returns
// numeric as pgtype.Numeric otherwise. Order MUST match scanSpecimen.
const specimenSelectColumns = `id, type, catalog_number, name, description, visibility,
	author_id, acquired_at, acquired_from, price_cents, source_notes,
	locality_text, locality, mass_g::float8, dimensions, type_data,
	created_at, updated_at`

// Create inserts s. type_data must be valid JSON bytes (an empty slice
// is normalized to "{}").
func (r *SpecimenPostgres) Create(ctx context.Context, tx domain.Tx, s domain.Specimen) error {
	loc, err := jsonbValue(s.Locality)
	if err != nil {
		return err
	}
	dim, err := jsonbValue(s.Dimensions)
	if err != nil {
		return err
	}
	const q = `INSERT INTO specimens (` + specimenInsertColumns + `) VALUES
		($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`
	_, err = tx.Exec(ctx, q,
		s.ID, s.Type, s.CatalogNumber, s.Name, s.Description, s.Visibility,
		s.AuthorID, s.AcquiredAt, s.AcquiredFrom, s.PriceCents, s.SourceNotes,
		s.LocalityText, loc, s.MassG, dim,
		typeDataValue(s.TypeData), s.CreatedAt, s.UpdatedAt,
	)
	return mapSpecimenErr(err)
}

// GetByID fetches by id.
func (r *SpecimenPostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.Specimen, error) {
	const q = `SELECT ` + specimenSelectColumns + ` FROM specimens WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	s, err := scanSpecimen(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Specimen{}, domain.ErrSpecimenNotFound
	}
	if err != nil {
		return domain.Specimen{}, fmt.Errorf("specimen repo: get by id: %w", err)
	}
	return s, nil
}

// Update writes every column of s to the matching row. The repo
// rejects any change to specimens.type (per CONTRACT acceptance:
// reclassification is delete + recreate, not an in-place update).
// Caller MUST set s.UpdatedAt to the current time.
func (r *SpecimenPostgres) Update(ctx context.Context, tx domain.Tx, s domain.Specimen) error {
	var existingType domain.SpecimenType
	if err := tx.QueryRow(ctx, `SELECT type FROM specimens WHERE id = $1`, s.ID).
		Scan(&existingType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrSpecimenNotFound
		}
		return fmt.Errorf("specimen repo: load existing type: %w", err)
	}
	if existingType != s.Type {
		return domain.ErrSpecimenTypeImmutable
	}

	loc, err := jsonbValue(s.Locality)
	if err != nil {
		return err
	}
	dim, err := jsonbValue(s.Dimensions)
	if err != nil {
		return err
	}
	const q = `UPDATE specimens SET
		catalog_number=$2, name=$3, description=$4, visibility=$5,
		acquired_at=$6, acquired_from=$7, price_cents=$8, source_notes=$9,
		locality_text=$10, locality=$11, mass_g=$12, dimensions=$13,
		type_data=$14, updated_at=$15
		WHERE id=$1`
	tag, err := tx.Exec(ctx, q,
		s.ID, s.CatalogNumber, s.Name, s.Description, s.Visibility,
		s.AcquiredAt, s.AcquiredFrom, s.PriceCents, s.SourceNotes,
		s.LocalityText, loc, s.MassG, dim,
		typeDataValue(s.TypeData), s.UpdatedAt,
	)
	if err := mapSpecimenErr(err); err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSpecimenNotFound
	}
	return nil
}

// Delete removes the row by id. Caller MUST run this inside a
// transaction so the existence check and the delete cannot race
// against a concurrent insert of children. Returns
// ErrSpecimenReferenced if photos or journal_entries reference id;
// ErrSpecimenNotFound when no row matches. specimen_collectors is
// pruned by the schema's ON DELETE CASCADE.
func (r *SpecimenPostgres) Delete(ctx context.Context, tx domain.Tx, id uuid.UUID) error {
	var hasChildren bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM photos WHERE specimen_id = $1)
		    OR EXISTS (SELECT 1 FROM journal_entries WHERE specimen_id = $1)`,
		id,
	).Scan(&hasChildren); err != nil {
		return fmt.Errorf("specimen repo: check children: %w", err)
	}
	if hasChildren {
		return domain.ErrSpecimenReferenced
	}
	tag, err := tx.Exec(ctx, `DELETE FROM specimens WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("specimen repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSpecimenNotFound
	}
	return nil
}

// List returns up to page.Limit specimens matching filter, plus a
// cursor for the next page (empty string when no more rows).
//
// When filter.Query is non-empty, results are ordered by ts_rank
// (descending) and the cursor encodes that rank. Otherwise rows are
// ordered by (created_at, id) descending. The collector_id filter is
// a v1 stub (per the bead): when set, the method returns no rows
// without consulting specimen_collectors (which is populated by B-4).
func (r *SpecimenPostgres) List(
	ctx context.Context,
	filter domain.SpecimenFilter,
	page domain.Page,
) ([]domain.Specimen, domain.Cursor, error) {
	if filter.CollectorID != nil {
		return []domain.Specimen{}, "", nil
	}

	limit := page.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	cursor, err := decodeCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("specimen repo: list: %w", err)
	}

	var (
		args         []any
		conds        []string
		ordSQL       string
		selSQL       = `SELECT ` + specimenSelectColumns + ` FROM specimens`
		qPlaceholder string
	)

	addArg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	// Search clause first so the q-placeholder is allocated before
	// any filter that wants to reuse it (the rank-cursor branch).
	if filter.Query != "" {
		qPlaceholder = addArg(filter.Query)
		selSQL = `SELECT ` + specimenSelectColumns +
			`, ts_rank(search_tsv, websearch_to_tsquery('english', ` + qPlaceholder + `)) AS rank
			 FROM specimens`
		conds = append(conds,
			"search_tsv @@ websearch_to_tsquery('english', "+qPlaceholder+")")
		ordSQL = " ORDER BY rank DESC, id DESC"
	} else {
		ordSQL = " ORDER BY created_at DESC, id DESC"
	}

	if filter.Type != nil {
		conds = append(conds, "type = "+addArg(*filter.Type))
	}
	if filter.Visibility != nil {
		conds = append(conds, "visibility = "+addArg(*filter.Visibility))
	}
	if filter.HasCatalogNumber != nil {
		if *filter.HasCatalogNumber {
			conds = append(conds, "catalog_number IS NOT NULL")
		} else {
			conds = append(conds, "catalog_number IS NULL")
		}
	}
	if filter.AcquiredAfter != nil {
		conds = append(conds, "acquired_at >= "+addArg(*filter.AcquiredAfter))
	}
	if filter.AcquiredBefore != nil {
		conds = append(conds, "acquired_at <= "+addArg(*filter.AcquiredBefore))
	}

	// Cursor predicate.
	if cursor.ID != uuid.Nil {
		switch {
		case cursor.isRank() && filter.Query != "":
			rp := addArg(*cursor.Rank)
			ip := addArg(cursor.ID)
			conds = append(conds, fmt.Sprintf(
				"(ts_rank(search_tsv, websearch_to_tsquery('english', %s)) < %s "+
					"OR (ts_rank(search_tsv, websearch_to_tsquery('english', %s)) = %s AND id < %s))",
				qPlaceholder, rp, qPlaceholder, rp, ip,
			))
		case !cursor.isRank() && filter.Query == "" && cursor.CreatedAt != nil:
			cp := addArg(*cursor.CreatedAt)
			ip := addArg(cursor.ID)
			conds = append(conds, "(created_at, id) < ("+cp+", "+ip+")")
		default:
			// Cursor issued under a different ordering (q= changed
			// since issuance). Treat as start-of-results, per
			// CONTRACT §10.3 (the SPA discards cursors when filters
			// change).
		}
	}

	sql := selSQL
	if len(conds) > 0 {
		sql += " WHERE " + strings.Join(conds, " AND ")
	}
	sql += ordSQL + " LIMIT " + addArg(limit+1)

	// When filter.Query is set the SELECT carries an extra `rank`
	// column; use a separate scan path so we can read it for the
	// next-cursor.
	if filter.Query != "" {
		return r.listWithRank(ctx, sql, args, limit)
	}

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("specimen repo: list query: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Specimen, 0, limit+1)
	for rows.Next() {
		s, err := scanSpecimen(rows)
		if err != nil {
			return nil, "", fmt.Errorf("specimen repo: list scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("specimen repo: list iter: %w", err)
	}

	var nextCursor string
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		ca := last.CreatedAt
		nc, err := encodeCursor(specimenCursor{CreatedAt: &ca, ID: last.ID})
		if err != nil {
			return nil, "", err
		}
		nextCursor = nc
	}
	return out, domain.Cursor(nextCursor), nil
}

func (r *SpecimenPostgres) listWithRank(
	ctx context.Context, sql string, args []any, limit int,
) ([]domain.Specimen, domain.Cursor, error) {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("specimen repo: list query: %w", err)
	}
	defer rows.Close()

	type withRank struct {
		s    domain.Specimen
		rank float64
	}
	collected := make([]withRank, 0, limit+1)
	for rows.Next() {
		var s domain.Specimen
		var rank float64
		if err := scanSpecimenWithRank(rows, &s, &rank); err != nil {
			return nil, "", fmt.Errorf("specimen repo: list scan: %w", err)
		}
		collected = append(collected, withRank{s: s, rank: rank})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("specimen repo: list iter: %w", err)
	}

	out := make([]domain.Specimen, 0, len(collected))
	for _, w := range collected {
		out = append(out, w.s)
	}

	var nextCursor string
	if len(out) > limit {
		last := collected[limit-1]
		out = out[:limit]
		rk := last.rank
		nc, err := encodeCursor(specimenCursor{Rank: &rk, ID: last.s.ID})
		if err != nil {
			return nil, "", err
		}
		nextCursor = nc
	}
	return out, domain.Cursor(nextCursor), nil
}

// jsonbValue marshals a *struct (or nil) to []byte for a JSONB
// column. A nil pointer produces a SQL NULL.
func jsonbValue(v any) (any, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() == reflect.Pointer && rv.IsNil()) {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("specimen repo: marshal jsonb: %w", err)
	}
	return b, nil
}

// typeDataValue returns the bytes to write into specimens.type_data,
// defaulting to "{}" when the input is empty (the column is NOT NULL
// with a default of '{}').
func typeDataValue(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

// rowScanner abstracts pgx.Row and pgx.Rows for shared scanSpecimen.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSpecimen(row rowScanner) (domain.Specimen, error) {
	var s domain.Specimen
	var locRaw, dimRaw, tdRaw []byte
	var massG *float64
	err := row.Scan(
		&s.ID, &s.Type, &s.CatalogNumber, &s.Name, &s.Description, &s.Visibility,
		&s.AuthorID, &s.AcquiredAt, &s.AcquiredFrom, &s.PriceCents, &s.SourceNotes,
		&s.LocalityText, &locRaw, &massG, &dimRaw, &tdRaw,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return s, err
	}
	if err := decodeSpecimenJSONB(&s, locRaw, dimRaw, tdRaw); err != nil {
		return s, err
	}
	s.MassG = massG
	return s, nil
}

func scanSpecimenWithRank(row rowScanner, s *domain.Specimen, rank *float64) error {
	var locRaw, dimRaw, tdRaw []byte
	var massG *float64
	if err := row.Scan(
		&s.ID, &s.Type, &s.CatalogNumber, &s.Name, &s.Description, &s.Visibility,
		&s.AuthorID, &s.AcquiredAt, &s.AcquiredFrom, &s.PriceCents, &s.SourceNotes,
		&s.LocalityText, &locRaw, &massG, &dimRaw, &tdRaw,
		&s.CreatedAt, &s.UpdatedAt, rank,
	); err != nil {
		return err
	}
	if err := decodeSpecimenJSONB(s, locRaw, dimRaw, tdRaw); err != nil {
		return err
	}
	s.MassG = massG
	return nil
}

func decodeSpecimenJSONB(s *domain.Specimen, locRaw, dimRaw, tdRaw []byte) error {
	if len(locRaw) > 0 {
		var loc domain.Locality
		if err := json.Unmarshal(locRaw, &loc); err != nil {
			return fmt.Errorf("specimen repo: decode locality: %w", err)
		}
		s.Locality = &loc
	}
	if len(dimRaw) > 0 {
		var dim domain.Dimensions
		if err := json.Unmarshal(dimRaw, &dim); err != nil {
			return fmt.Errorf("specimen repo: decode dimensions: %w", err)
		}
		s.Dimensions = &dim
	}
	if len(tdRaw) > 0 {
		s.TypeData = append([]byte(nil), tdRaw...)
	}
	return nil
}

// mapSpecimenErr converts driver/connection errors into domain
// sentinels at the repo boundary (CONTRACT §11). Unique-violation on
// catalog_number → ErrSpecimenConflict.
func mapSpecimenErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.ErrSpecimenConflict
	}
	return fmt.Errorf("specimen repo: %w", err)
}
