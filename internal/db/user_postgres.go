package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// UserPostgres is the Postgres-backed domain.UserRepo (mi-2hf).
// It backs the auth resolver: GetBySub is hit on every authenticated
// request, and Create runs once per first-login.
type UserPostgres struct{ pool *pgxpool.Pool }

// NewUserPostgres constructs a UserPostgres bound to pool.
func NewUserPostgres(pool *pgxpool.Pool) *UserPostgres {
	return &UserPostgres{pool: pool}
}

// userColumns is the canonical read column list shared by every
// user query. Kept in sync with scanUser.
const userColumns = `id, keycloak_sub, email, display_name, status,
		field_defaults, default_specimen_visibility, created_at, updated_at`

// GetBySub returns the row whose keycloak_sub matches, or
// domain.ErrUserNotFound.
func (r *UserPostgres) GetBySub(ctx context.Context, sub string) (domain.User, error) {
	q := `SELECT ` + userColumns + ` FROM users WHERE keycloak_sub = $1`
	row := r.pool.QueryRow(ctx, q, sub)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("user repo: get by sub: %w", err)
	}
	return u, nil
}

// GetByID returns the row whose id matches, or domain.ErrUserNotFound.
// Used by the per-field visibility resolver (mi-fo8) to load a
// specimen's owner so its FieldDefaults feed the resolution chain.
func (r *UserPostgres) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	q := `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("user repo: get by id: %w", err)
	}
	return u, nil
}

// Create inserts a new user. Returns domain.ErrUserConflict when
// keycloak_sub collides — the resolver maps that to "another request
// won the race" and retries GetBySub.
func (r *UserPostgres) Create(ctx context.Context, tx domain.Tx, u domain.User) error {
	exec := r.execer(tx)
	fieldDefaults, err := marshalNullable(u.FieldDefaults)
	if err != nil {
		return fmt.Errorf("user repo: create: field_defaults: %w", err)
	}
	const q = `
		INSERT INTO users (id, keycloak_sub, email, display_name, status,
			field_defaults, default_specimen_visibility, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err = exec.Exec(ctx, q,
		u.ID, u.KeycloakSub, u.Email, u.DisplayName, string(u.Status),
		fieldDefaults, visibilityToText(u.DefaultSpecimenVisibility), u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrUserConflict
		}
		return fmt.Errorf("user repo: create: %w", err)
	}
	return nil
}

// MarkActive sets display_name and flips status to 'active'. Used by
// the /api/v1/profile setup handler to lift the first-login gate.
func (r *UserPostgres) MarkActive(
	ctx context.Context, tx domain.Tx, id uuid.UUID, displayName string, updatedAt time.Time,
) error {
	exec := r.execer(tx)
	const q = `
		UPDATE users
		   SET display_name = $2, status = 'active', updated_at = $3
		 WHERE id = $1`
	tag, err := exec.Exec(ctx, q, id, displayName, updatedAt)
	if err != nil {
		return fmt.Errorf("user repo: mark active: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// UpdateDisplayName overwrites display_name on the row identified by
// id. Used by PATCH /api/v1/profile (mi-j3kn) for post-setup name
// edits — MarkActive is reserved for the pending→active flip.
func (r *UserPostgres) UpdateDisplayName(
	ctx context.Context, tx domain.Tx, id uuid.UUID, displayName string, updatedAt time.Time,
) error {
	exec := r.execer(tx)
	const q = `
		UPDATE users
		   SET display_name = $2, updated_at = $3
		 WHERE id = $1`
	tag, err := exec.Exec(ctx, q, id, displayName, updatedAt)
	if err != nil {
		return fmt.Errorf("user repo: update display name: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// UpdateDefaultSpecimenVisibility writes the per-user default
// whole-specimen visibility (mi-q2d8 / migration 0016). Passing nil
// clears the column to SQL NULL — the create form then falls back to
// the system default. Returns ErrUserNotFound when no row matched.
func (r *UserPostgres) UpdateDefaultSpecimenVisibility(
	ctx context.Context, tx domain.Tx, id uuid.UUID, visibility *domain.Visibility, updatedAt time.Time,
) error {
	exec := r.execer(tx)
	const q = `
		UPDATE users
		   SET default_specimen_visibility = $2, updated_at = $3
		 WHERE id = $1`
	tag, err := exec.Exec(ctx, q, id, visibilityToText(visibility), updatedAt)
	if err != nil {
		return fmt.Errorf("user repo: update default specimen visibility: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

// UpdateFieldDefaults writes the per-user visibility defaults map
// (mi-fo8 / migration 0012). Passing nil clears the column to SQL
// NULL — the all-fields-fall-through state. Returns
// ErrUserNotFound when no row matched.
func (r *UserPostgres) UpdateFieldDefaults(
	ctx context.Context, tx domain.Tx, id uuid.UUID, defaults *domain.FieldDefaults, updatedAt time.Time,
) error {
	exec := r.execer(tx)
	fieldDefaults, err := marshalNullable(defaults)
	if err != nil {
		return fmt.Errorf("user repo: update field defaults: marshal: %w", err)
	}
	const q = `
		UPDATE users
		   SET field_defaults = $2, updated_at = $3
		 WHERE id = $1`
	tag, err := exec.Exec(ctx, q, id, fieldDefaults, updatedAt)
	if err != nil {
		return fmt.Errorf("user repo: update field defaults: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

func (r *UserPostgres) execer(tx domain.Tx) domain.Tx {
	if tx != nil {
		return tx
	}
	return r.pool
}

// scanUser reads one row in the canonical column order shared by
// every user query. Accepts pgx.Row and pgx.Rows.
func scanUser(s rowScanner) (domain.User, error) {
	var u domain.User
	var displayName *string
	var status string
	var fieldDefaults []byte
	var defaultSpecimenVisibility *string
	var createdAt, updatedAt time.Time
	if err := s.Scan(
		&u.ID, &u.KeycloakSub, &u.Email, &displayName, &status,
		&fieldDefaults, &defaultSpecimenVisibility, &createdAt, &updatedAt,
	); err != nil {
		return domain.User{}, err
	}
	u.DisplayName = displayName
	u.Status = domain.UserStatus(status)
	if len(fieldDefaults) > 0 && string(fieldDefaults) != "null" {
		var fd domain.FieldDefaults
		if err := json.Unmarshal(fieldDefaults, &fd); err != nil {
			return domain.User{}, fmt.Errorf("field_defaults unmarshal: %w", err)
		}
		u.FieldDefaults = &fd
	}
	if defaultSpecimenVisibility != nil {
		v := domain.Visibility(*defaultSpecimenVisibility)
		u.DefaultSpecimenVisibility = &v
	}
	u.CreatedAt = createdAt
	u.UpdatedAt = updatedAt
	return u, nil
}

// visibilityToText maps a nullable domain.Visibility to the *string
// pgx writes as a nullable text column: nil → SQL NULL, otherwise the
// enum string. Used by the create insert and the default-specimen-
// visibility update.
func visibilityToText(v *domain.Visibility) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}
