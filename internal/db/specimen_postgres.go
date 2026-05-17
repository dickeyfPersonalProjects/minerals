package db

import (
	"context"
	"encoding/json"
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

// SpecimenPostgres is the Postgres-backed domain.SpecimenRepo (mi-quf).
type SpecimenPostgres struct{ pool *pgxpool.Pool }

// NewSpecimenPostgres constructs a SpecimenPostgres bound to pool.
func NewSpecimenPostgres(pool *pgxpool.Pool) *SpecimenPostgres {
	return &SpecimenPostgres{pool: pool}
}

// specimenColumns is the canonical column list shared by every read
// query. mass_g is cast to double precision so it scans cleanly into
// *float64 — Postgres' native numeric type doesn't have a default
// pgx codec into float64, and v1 doesn't need fractional precision
// beyond what double affords (locked in design §2).
//
// visibility_price / visibility_acquired_from / visibility_images
// are the per-field overrides added by migration 0013 (mi-fo8); all
// three scan as *domain.Visibility (nullable).
const specimenColumns = `id, type, catalog_number, name, description, visibility,
		author_id, acquired_at, acquired_from, price_cents, source_notes,
		locality_text, locality, mass_g::double precision, dimensions, type_data,
		main_image_id, visibility_price, visibility_acquired_from, visibility_images,
		created_at, updated_at`

// Create inserts a new specimen. Caller has already populated s.ID
// (UUIDv7), CreatedAt, UpdatedAt; author_id is taken from auth ctx
// (per CONTRACT.md §11/§13). Empty type_data is stored as `{}`.
func (r *SpecimenPostgres) Create(ctx context.Context, tx domain.Tx, s domain.Specimen) error {
	exec := r.execer(tx)
	user := auth.FromContext(ctx)

	locality, err := marshalNullable(s.Locality)
	if err != nil {
		return fmt.Errorf("specimen repo: create: locality: %w", err)
	}
	dimensions, err := marshalNullable(s.Dimensions)
	if err != nil {
		return fmt.Errorf("specimen repo: create: dimensions: %w", err)
	}
	typeData := s.TypeData
	if len(typeData) == 0 {
		typeData = []byte(`{}`)
	}

	const q = `
		INSERT INTO specimens (
			id, type, catalog_number, name, description, visibility,
			author_id, acquired_at, acquired_from, price_cents, source_notes,
			locality_text, locality, mass_g, dimensions, type_data,
			main_image_id, visibility_price, visibility_acquired_from, visibility_images,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16,
			$17, $18, $19, $20,
			$21, $22
		)`
	_, err = exec.Exec(ctx, q,
		s.ID, string(s.Type), s.CatalogNumber, s.Name, s.Description, string(s.Visibility),
		user.ID, s.AcquiredAt, s.AcquiredFrom, s.PriceCents, s.SourceNotes,
		s.LocalityText, locality, s.MassG, dimensions, typeData,
		s.MainImageID, visibilityArg(s.VisibilityPrice), visibilityArg(s.VisibilityAcquiredFrom), visibilityArg(s.VisibilityImages),
		s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrSpecimenConflict
		}
		return fmt.Errorf("specimen repo: create: %w", err)
	}
	return nil
}

// GetByID returns the specimen or domain.ErrSpecimenNotFound.
func (r *SpecimenPostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.Specimen, error) {
	q := `SELECT ` + specimenColumns + ` FROM specimens WHERE id = $1`
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

// Update writes the row identified by s.ID. The `type` column is
// immutable per design §2: rows whose stored type differs from
// s.Type are rejected with ErrSpecimenTypeImmutable. Returns
// ErrSpecimenNotFound when no row matches and ErrSpecimenConflict
// on uniqueness violations (e.g. catalog_number clash).
func (r *SpecimenPostgres) Update(ctx context.Context, tx domain.Tx, s domain.Specimen) error {
	exec := r.execer(tx)

	locality, err := marshalNullable(s.Locality)
	if err != nil {
		return fmt.Errorf("specimen repo: update: locality: %w", err)
	}
	dimensions, err := marshalNullable(s.Dimensions)
	if err != nil {
		return fmt.Errorf("specimen repo: update: dimensions: %w", err)
	}
	typeData := s.TypeData
	if len(typeData) == 0 {
		typeData = []byte(`{}`)
	}

	const q = `
		UPDATE specimens SET
			catalog_number             = $2,
			name                       = $3,
			description                = $4,
			visibility                 = $5,
			acquired_at                = $6,
			acquired_from              = $7,
			price_cents                = $8,
			source_notes               = $9,
			locality_text              = $10,
			locality                   = $11,
			mass_g                     = $12,
			dimensions                 = $13,
			type_data                  = $14,
			main_image_id              = $15,
			visibility_price           = $16,
			visibility_acquired_from   = $17,
			visibility_images          = $18,
			updated_at                 = $19
		 WHERE id = $1 AND type = $20`
	tag, err := exec.Exec(ctx, q,
		s.ID, s.CatalogNumber, s.Name, s.Description, string(s.Visibility),
		s.AcquiredAt, s.AcquiredFrom, s.PriceCents, s.SourceNotes,
		s.LocalityText, locality, s.MassG, dimensions, typeData,
		s.MainImageID, visibilityArg(s.VisibilityPrice), visibilityArg(s.VisibilityAcquiredFrom), visibilityArg(s.VisibilityImages),
		s.UpdatedAt, string(s.Type),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrSpecimenConflict
		}
		return fmt.Errorf("specimen repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// 0 rows means either id doesn't exist, or it exists but the
		// stored type differs from s.Type. Disambiguate so the API
		// can return 404 vs 409.
		var existing string
		err := exec.QueryRow(ctx, `SELECT type::text FROM specimens WHERE id = $1`, s.ID).Scan(&existing)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrSpecimenNotFound
		}
		if err != nil {
			return fmt.Errorf("specimen repo: update: probe type: %w", err)
		}
		// Row exists with a different type — caller tried to mutate
		// `type`, which is forbidden per design §2.
		return domain.ErrSpecimenTypeImmutable
	}
	return nil
}

// Delete removes a specimen. The schema cascades specimen_collectors
// rows automatically; photos and journal_entries also cascade at the
// FK level, but for v1 we refuse to delete a specimen that still has
// either of those — per the mi-quf acceptance criteria. Once the
// photos bead (B-3) lands, that polecat may revisit cascade
// semantics.
func (r *SpecimenPostgres) Delete(ctx context.Context, tx domain.Tx, id uuid.UUID) error {
	exec := r.execer(tx)

	// Check for child rows BEFORE deleting. There's a small TOCTOU
	// window if another writer inserts a photo concurrently — v1
	// has a single overseer, so this is acceptable. When real
	// multi-user lands, wrap in a SERIALIZABLE transaction.
	var hasPhotos, hasEntries bool
	const probeQ = `
		SELECT EXISTS (SELECT 1 FROM photos WHERE specimen_id = $1),
		       EXISTS (SELECT 1 FROM journal_entries WHERE specimen_id = $1)`
	if err := exec.QueryRow(ctx, probeQ, id).Scan(&hasPhotos, &hasEntries); err != nil {
		return fmt.Errorf("specimen repo: delete: probe children: %w", err)
	}
	if hasPhotos || hasEntries {
		return domain.ErrSpecimenReferenced
	}

	tag, err := exec.Exec(ctx, `DELETE FROM specimens WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("specimen repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSpecimenNotFound
	}
	return nil
}

// List returns up to page.Limit specimens, applying every v1 filter
// (per design §4.4). When filter.Query is non-empty, ordering shifts
// to `ts_rank DESC, created_at DESC, id DESC` and the returned cursor
// encodes (rank, created_at, id). Cursors issued under one ordering
// are NOT valid under the other (per CONTRACT.md §10).
//
// When filter.CollectorID is set the query joins against
// specimen_collectors and returns specimens that have the given
// collector anywhere in their chain (mi-zv3 / C-3 — replaces the
// stub mi-quf left while the linkage table was unwired).
func (r *SpecimenPostgres) List(
	ctx context.Context, filter domain.SpecimenFilter, page domain.Page,
) ([]domain.Specimen, domain.Cursor, error) {
	limit := clampLimit(page.Limit)

	if strings.TrimSpace(filter.Query) != "" {
		return r.listRanked(ctx, filter, page, limit)
	}
	return r.listDefault(ctx, filter, page, limit)
}

// listDefault handles ordering by (created_at DESC, id DESC).
func (r *SpecimenPostgres) listDefault(
	ctx context.Context, filter domain.SpecimenFilter, page domain.Page, limit int,
) ([]domain.Specimen, domain.Cursor, error) {
	curTS, curID, err := DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("specimen repo: list: %w", err)
	}

	args := []any{limit + 1}
	var where []string

	if page.Cursor != "" {
		where = append(where, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)+1, len(args)+2))
		args = append(args, curTS, curID)
	}
	where, args = applySharedFilters(where, args, filter)
	// CONTRACT.md §13 v2 layer-1: scope the list to rows the caller
	// may see (own + public + shared; admin sees all).
	if clause, scoped := specimenListScope(auth.FromContext(ctx), "specimens", args); clause != "" {
		where = append(where, clause)
		args = scoped
	}

	sql := `SELECT ` + specimenColumns + ` FROM specimens`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT $1"

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("specimen repo: list: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Specimen, 0, limit)
	for rows.Next() {
		s, err := scanSpecimen(rows)
		if err != nil {
			return nil, "", fmt.Errorf("specimen repo: list: scan: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("specimen repo: list: rows: %w", err)
	}

	var cursor domain.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		cursor = domain.Cursor(EncodeCursor(last.CreatedAt, last.ID))
	}
	return out, cursor, nil
}

// listRanked handles ordering by ts_rank when filter.Query is set.
// Cursor includes the rank so a follow-up page resumes at the right
// ranked position even when many rows share rank values.
func (r *SpecimenPostgres) listRanked(
	ctx context.Context, filter domain.SpecimenFilter, page domain.Page, limit int,
) ([]domain.Specimen, domain.Cursor, error) {
	curRank, curTS, curID, err := DecodeRankCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("specimen repo: list: %w", err)
	}

	q := strings.TrimSpace(filter.Query)
	args := []any{limit + 1, q}
	// $1 = limit, $2 = q
	const tsRankExpr = `ts_rank(search_tsv, websearch_to_tsquery('english', $2))`
	const tsMatchExpr = `search_tsv @@ websearch_to_tsquery('english', $2)`

	where := []string{tsMatchExpr}
	if page.Cursor != "" {
		// Keyset condition for `(rank, created_at, id) DESC` ordering.
		where = append(where, fmt.Sprintf(
			"(%s, created_at, id) < ($%d, $%d, $%d)",
			tsRankExpr, len(args)+1, len(args)+2, len(args)+3,
		))
		args = append(args, curRank, curTS, curID)
	}
	where, args = applySharedFilters(where, args, filter)
	// CONTRACT.md §13 v2 layer-1: scope the list to rows the caller
	// may see (own + public + shared; admin sees all).
	if clause, scoped := specimenListScope(auth.FromContext(ctx), "specimens", args); clause != "" {
		where = append(where, clause)
		args = scoped
	}

	sql := `
		SELECT ` + specimenColumns + `, ` + tsRankExpr + ` AS rank
		FROM specimens
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY rank DESC, created_at DESC, id DESC
		LIMIT $1`

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("specimen repo: list: %w", err)
	}
	defer rows.Close()

	type rankedRow struct {
		s    domain.Specimen
		rank float32
	}
	rowsOut := make([]rankedRow, 0, limit)
	for rows.Next() {
		s, rank, err := scanSpecimenRanked(rows)
		if err != nil {
			return nil, "", fmt.Errorf("specimen repo: list: scan: %w", err)
		}
		rowsOut = append(rowsOut, rankedRow{s: s, rank: rank})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("specimen repo: list: rows: %w", err)
	}

	out := make([]domain.Specimen, 0, len(rowsOut))
	for _, rr := range rowsOut {
		out = append(out, rr.s)
	}

	var cursor domain.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := rowsOut[limit-1]
		cursor = domain.Cursor(EncodeRankCursor(last.rank, last.s.CreatedAt, last.s.ID))
	}
	return out, cursor, nil
}

// applySharedFilters appends WHERE clauses common to both default
// and ranked list queries. Returns the augmented where slice and the
// args slice with the new parameters appended.
//
// The collector_id filter uses EXISTS rather than a JOIN so a
// specimen with multiple collectors in its chain doesn't appear
// duplicated in the result, which would corrupt cursor pagination
// (mi-zv3).
func applySharedFilters(
	where []string, args []any, filter domain.SpecimenFilter,
) ([]string, []any) {
	if filter.Type != nil {
		where = append(where, fmt.Sprintf("type = $%d", len(args)+1))
		args = append(args, string(*filter.Type))
	}
	if filter.Visibility != nil {
		where = append(where, fmt.Sprintf("visibility = $%d", len(args)+1))
		args = append(args, string(*filter.Visibility))
	}
	if filter.HasCatalogNumber != nil {
		if *filter.HasCatalogNumber {
			where = append(where, "catalog_number IS NOT NULL")
		} else {
			where = append(where, "catalog_number IS NULL")
		}
	}
	if filter.AcquiredAfter != nil {
		where = append(where, fmt.Sprintf("acquired_at >= $%d", len(args)+1))
		args = append(args, *filter.AcquiredAfter)
	}
	if filter.AcquiredBefore != nil {
		where = append(where, fmt.Sprintf("acquired_at <= $%d", len(args)+1))
		args = append(args, *filter.AcquiredBefore)
	}
	if filter.CollectorID != nil {
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM specimen_collectors sc "+
				"WHERE sc.specimen_id = specimens.id AND sc.collector_id = $%d)",
			len(args)+1,
		))
		args = append(args, *filter.CollectorID)
	}
	return where, args
}

// scanSpecimen reads one row in the canonical column order shared
// by every specimen query.
func scanSpecimen(s rowScanner) (domain.Specimen, error) {
	var sp domain.Specimen
	var typeStr, visStr string
	var visPrice, visAcq, visImg *string
	var locality, dimensions, typeData []byte
	if err := s.Scan(
		&sp.ID, &typeStr, &sp.CatalogNumber, &sp.Name, &sp.Description, &visStr,
		&sp.AuthorID, &sp.AcquiredAt, &sp.AcquiredFrom, &sp.PriceCents, &sp.SourceNotes,
		&sp.LocalityText, &locality, &sp.MassG, &dimensions, &typeData,
		&sp.MainImageID, &visPrice, &visAcq, &visImg,
		&sp.CreatedAt, &sp.UpdatedAt,
	); err != nil {
		return domain.Specimen{}, err
	}
	sp.Type = domain.SpecimenType(typeStr)
	sp.Visibility = domain.Visibility(visStr)
	sp.VisibilityPrice = visibilityPtr(visPrice)
	sp.VisibilityAcquiredFrom = visibilityPtr(visAcq)
	sp.VisibilityImages = visibilityPtr(visImg)
	if len(locality) > 0 && string(locality) != "null" {
		var loc domain.Locality
		if err := json.Unmarshal(locality, &loc); err != nil {
			return domain.Specimen{}, fmt.Errorf("locality unmarshal: %w", err)
		}
		sp.Locality = &loc
	}
	if len(dimensions) > 0 && string(dimensions) != "null" {
		var dim domain.Dimensions
		if err := json.Unmarshal(dimensions, &dim); err != nil {
			return domain.Specimen{}, fmt.Errorf("dimensions unmarshal: %w", err)
		}
		sp.Dimensions = &dim
	}
	if len(typeData) > 0 {
		sp.TypeData = append([]byte(nil), typeData...)
	}
	sp.CreatedAt = sp.CreatedAt.UTC()
	sp.UpdatedAt = sp.UpdatedAt.UTC()
	return sp, nil
}

// scanSpecimenRanked reads one row plus a trailing ts_rank.
func scanSpecimenRanked(rs pgx.Rows) (domain.Specimen, float32, error) {
	var sp domain.Specimen
	var typeStr, visStr string
	var visPrice, visAcq, visImg *string
	var locality, dimensions, typeData []byte
	var rank float32
	if err := rs.Scan(
		&sp.ID, &typeStr, &sp.CatalogNumber, &sp.Name, &sp.Description, &visStr,
		&sp.AuthorID, &sp.AcquiredAt, &sp.AcquiredFrom, &sp.PriceCents, &sp.SourceNotes,
		&sp.LocalityText, &locality, &sp.MassG, &dimensions, &typeData,
		&sp.MainImageID, &visPrice, &visAcq, &visImg,
		&sp.CreatedAt, &sp.UpdatedAt, &rank,
	); err != nil {
		return domain.Specimen{}, 0, err
	}
	sp.Type = domain.SpecimenType(typeStr)
	sp.Visibility = domain.Visibility(visStr)
	sp.VisibilityPrice = visibilityPtr(visPrice)
	sp.VisibilityAcquiredFrom = visibilityPtr(visAcq)
	sp.VisibilityImages = visibilityPtr(visImg)
	if len(locality) > 0 && string(locality) != "null" {
		var loc domain.Locality
		if err := json.Unmarshal(locality, &loc); err != nil {
			return domain.Specimen{}, 0, fmt.Errorf("locality unmarshal: %w", err)
		}
		sp.Locality = &loc
	}
	if len(dimensions) > 0 && string(dimensions) != "null" {
		var dim domain.Dimensions
		if err := json.Unmarshal(dimensions, &dim); err != nil {
			return domain.Specimen{}, 0, fmt.Errorf("dimensions unmarshal: %w", err)
		}
		sp.Dimensions = &dim
	}
	if len(typeData) > 0 {
		sp.TypeData = append([]byte(nil), typeData...)
	}
	sp.CreatedAt = sp.CreatedAt.UTC()
	sp.UpdatedAt = sp.UpdatedAt.UTC()
	return sp, rank, nil
}

// marshalNullable returns nil bytes for a nil pointer (so pgx writes
// SQL NULL into the JSONB column), otherwise the JSON encoding.
func marshalNullable[T any](v *T) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// visibilityArg adapts a *domain.Visibility to the value pgx wants
// for a nullable specimen_visibility column: a *string (NULL when
// the source pointer is nil). Postgres' text-codec path for the
// enum accepts the underlying string.
func visibilityArg(v *domain.Visibility) any {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}

// visibilityPtr is the read-side mirror of visibilityArg: a NULL
// column (scanned as *string == nil) returns nil; otherwise the
// enum text becomes a *domain.Visibility.
func visibilityPtr(s *string) *domain.Visibility {
	if s == nil {
		return nil
	}
	v := domain.Visibility(*s)
	return &v
}

// execer returns the Tx caller-supplied, falling back to the bound
// pool when nil — keeps callers from juggling nil checks.
func (r *SpecimenPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}

// HasPhotoWithFile reports whether the specimen has a photo row
// whose file_id matches fileID. Used by the API to validate
// main_image_id before writing it on the specimen (mi-m8q).
func (r *SpecimenPostgres) HasPhotoWithFile(
	ctx context.Context, specimenID, fileID uuid.UUID,
) (bool, error) {
	const q = `SELECT EXISTS (
		SELECT 1 FROM photos WHERE specimen_id = $1 AND file_id = $2
	)`
	var ok bool
	if err := r.pool.QueryRow(ctx, q, specimenID, fileID).Scan(&ok); err != nil {
		return false, fmt.Errorf("specimen repo: has photo with file: %w", err)
	}
	return ok, nil
}

// IsRecentUUIDv7 reports whether id is a UUIDv7 whose embedded
// timestamp lies within drift seconds of t. v1 acceptance tests
// assert that repo-generated ids are time-prefixed (per §11).
func IsRecentUUIDv7(id uuid.UUID, t time.Time, drift time.Duration) bool {
	if id.Version() != 7 {
		return false
	}
	// UUIDv7 layout: first 48 bits are unix milliseconds, big-endian.
	// ms is constructed from 48 bits, so it always fits in int64 (max 2^48-1).
	b := id[:]
	ms := uint64(b[0])<<40 | uint64(b[1])<<32 | uint64(b[2])<<24 |
		uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
	embedded := time.UnixMilli(int64(ms)) //nolint:gosec // G115: ms is a 48-bit value (see comment above), safe in int64
	delta := embedded.Sub(t)
	if delta < 0 {
		delta = -delta
	}
	return delta <= drift
}
