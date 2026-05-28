package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// AdminPostgres is the Postgres-backed domain.AdminRepo (mi-n5av /
// mi-gtkp) — the admin/devops console's see-all data source. Unlike the
// per-user repos it applies NO owner/visibility scoping: that is exactly
// the point of the admin view, and access is gated entirely at the API
// layer behind the §13 v2 `devops` Casbin resource.
type AdminPostgres struct{ pool *pgxpool.Pool }

// NewAdminPostgres constructs an AdminPostgres bound to pool.
func NewAdminPostgres(pool *pgxpool.Pool) *AdminPostgres {
	return &AdminPostgres{pool: pool}
}

// ListUsers returns every user as the non-personal admin view
// (mi-n5av), ordered (created_at DESC, id DESC) and cursor-paginated.
//
// PII boundary (mayor 2026-05-24): the SELECT lists ONLY id,
// display_name, status, created_at and three derived content counts.
// It deliberately does NOT select email — there is no path for that
// column to reach the response. The counts come from correlated
// subqueries keyed on author_id (photos count joins through the
// specimen, since photos carry no author of their own).
func (r *AdminPostgres) ListUsers(
	ctx context.Context, page domain.Page,
) ([]domain.AdminUser, domain.Cursor, error) {
	limit := clampLimit(page.Limit)

	curTS, curID, err := DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("admin repo: list users: %w", err)
	}

	args := []any{limit + 1} // fetch one extra to detect end-of-results
	where := ""
	if page.Cursor != "" {
		where = fmt.Sprintf(" WHERE (u.created_at, u.id) < ($%d, $%d)", len(args)+1, len(args)+2)
		args = append(args, curTS, curID)
	}

	sql := `
		SELECT
			u.id,
			u.display_name,
			u.status,
			u.created_at,
			(SELECT count(*) FROM specimens s WHERE s.author_id = u.id) AS specimen_count,
			(SELECT count(*) FROM photos p
			   JOIN specimens ps ON ps.id = p.specimen_id
			  WHERE ps.author_id = u.id) AS photo_count,
			(SELECT count(*) FROM journal_entries j WHERE j.author_id = u.id) AS journal_count
		FROM users u` + where + `
		ORDER BY u.created_at DESC, u.id DESC
		LIMIT $1`

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("admin repo: list users: %w", err)
	}
	defer rows.Close()

	out := make([]domain.AdminUser, 0, limit)
	for rows.Next() {
		var (
			au          domain.AdminUser
			displayName *string
			status      string
			createdAt   time.Time
			specCount   int64
			photoCount  int64
			jrnlCount   int64
		)
		if err := rows.Scan(&au.ID, &displayName, &status, &createdAt,
			&specCount, &photoCount, &jrnlCount); err != nil {
			return nil, "", fmt.Errorf("admin repo: list users: scan: %w", err)
		}
		au.DisplayName = displayName
		au.Status = domain.UserStatus(status)
		au.CreatedAt = createdAt
		au.SpecimenCount = int(specCount)
		au.PhotoCount = int(photoCount)
		au.JournalCount = int(jrnlCount)
		out = append(out, au)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("admin repo: list users: rows: %w", err)
	}

	var cursor domain.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		cursor = domain.Cursor(EncodeCursor(last.CreatedAt, last.ID))
	}
	return out, cursor, nil
}

// ListPublishedContent returns the unified published-content review
// feed (mi-gtkp): every public/unlisted specimen, the photos on those
// specimens whose own visibility override is not 'private', and the
// journal entries under them — all attributed to the specimen owner
// (display_name + opaque id, NO email) and ordered (created_at DESC,
// id DESC) across the union.
//
// The keyset condition is applied on the OUTER feed so pagination is
// consistent across the three sources. The literals public/unlisted/
// private are constants in the SQL, not bound parameters.
func (r *AdminPostgres) ListPublishedContent(
	ctx context.Context, page domain.Page,
) ([]domain.AdminContent, domain.Cursor, error) {
	limit := clampLimit(page.Limit)

	curTS, curID, err := DecodeCursor(page.Cursor)
	if err != nil {
		return nil, "", fmt.Errorf("admin repo: list published content: %w", err)
	}

	args := []any{limit + 1}
	keyset := ""
	if page.Cursor != "" {
		keyset = fmt.Sprintf(" WHERE (feed.created_at, feed.id) < ($%d, $%d)", len(args)+1, len(args)+2)
		args = append(args, curTS, curID)
	}

	sql := `
		SELECT feed.kind, feed.id, feed.specimen_id, feed.title, feed.preview,
		       feed.visibility, feed.owner_id, feed.owner_display_name, feed.created_at
		FROM (
			SELECT 'specimen' AS kind, s.id AS id, s.id AS specimen_id,
			       s.name AS title, '' AS preview,
			       s.visibility::text AS visibility,
			       s.author_id AS owner_id, u.display_name AS owner_display_name,
			       s.created_at AS created_at
			FROM specimens s
			JOIN users u ON u.id = s.author_id
			WHERE s.visibility IN ('public', 'unlisted')

			UNION ALL

			SELECT 'photo', p.id, p.specimen_id,
			       s.name, '',
			       COALESCE(p.visibility, s.visibility)::text,
			       s.author_id, u.display_name,
			       p.created_at
			FROM photos p
			JOIN specimens s ON s.id = p.specimen_id
			JOIN users u ON u.id = s.author_id
			WHERE s.visibility IN ('public', 'unlisted')
			  AND p.visibility IS DISTINCT FROM 'private'

			UNION ALL

			SELECT 'journal', j.id, j.specimen_id,
			       s.name, left(j.body_md, 200),
			       s.visibility::text,
			       s.author_id, u.display_name,
			       j.created_at
			FROM journal_entries j
			JOIN specimens s ON s.id = j.specimen_id
			JOIN users u ON u.id = s.author_id
			WHERE s.visibility IN ('public', 'unlisted')
		) feed` + keyset + `
		ORDER BY feed.created_at DESC, feed.id DESC
		LIMIT $1`

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("admin repo: list published content: %w", err)
	}
	defer rows.Close()

	out := make([]domain.AdminContent, 0, limit)
	for rows.Next() {
		var (
			ac          domain.AdminContent
			kind        string
			visibility  string
			displayName *string
			createdAt   time.Time
		)
		if err := rows.Scan(&kind, &ac.ID, &ac.SpecimenID, &ac.Title, &ac.Preview,
			&visibility, &ac.OwnerID, &displayName, &createdAt); err != nil {
			return nil, "", fmt.Errorf("admin repo: list published content: scan: %w", err)
		}
		ac.Kind = domain.AdminContentKind(kind)
		ac.Visibility = domain.Visibility(visibility)
		ac.OwnerDisplayName = displayName
		ac.CreatedAt = createdAt
		out = append(out, ac)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("admin repo: list published content: rows: %w", err)
	}

	var cursor domain.Cursor
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		cursor = domain.Cursor(EncodeCursor(last.CreatedAt, last.ID))
	}
	return out, cursor, nil
}

// compile-time assertion that AdminPostgres satisfies the interface.
var _ domain.AdminRepo = (*AdminPostgres)(nil)
