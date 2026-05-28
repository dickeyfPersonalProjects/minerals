package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// SettingsPostgres is the Postgres-backed domain.SettingsRepo (mi-pkn2),
// storing runtime-mutable settings as rows in the app_settings
// key-value table (migration 0018). Today the only key is the
// registration toggle.
type SettingsPostgres struct{ pool *pgxpool.Pool }

// NewSettingsPostgres constructs a SettingsPostgres bound to pool.
func NewSettingsPostgres(pool *pgxpool.Pool) *SettingsPostgres {
	return &SettingsPostgres{pool: pool}
}

// settingRegistrationEnabled is the app_settings key for the runtime
// registration toggle. Values are the literal strings 'true' / 'false'.
const settingRegistrationEnabled = "registration_enabled"

// RegistrationEnabled reads the registration toggle. A missing row
// (never flipped) returns found=false and is not an error — the caller
// falls back to the deploy-time default.
func (r *SettingsPostgres) RegistrationEnabled(ctx context.Context) (bool, bool, error) {
	var value string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM app_settings WHERE key = $1`, settingRegistrationEnabled).
		Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("settings repo: get registration_enabled: %w", err)
	}
	return value == "true", true, nil
}

// SetRegistrationEnabled upserts the registration toggle, stamping the
// operator id and update time. actor may be uuid.Nil (stored NULL) when
// the writer is unknown.
func (r *SettingsPostgres) SetRegistrationEnabled(ctx context.Context, enabled bool, actor uuid.UUID) error {
	value := "false"
	if enabled {
		value = "true"
	}
	var updatedBy *uuid.UUID
	if actor != uuid.Nil {
		updatedBy = &actor
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at, updated_by)
		VALUES ($1, $2, now(), $3)
		ON CONFLICT (key) DO UPDATE
		   SET value = EXCLUDED.value,
		       updated_at = EXCLUDED.updated_at,
		       updated_by = EXCLUDED.updated_by`,
		settingRegistrationEnabled, value, updatedBy)
	if err != nil {
		return fmt.Errorf("settings repo: set registration_enabled: %w", err)
	}
	return nil
}

// compile-time assertion that SettingsPostgres satisfies the interface.
var _ domain.SettingsRepo = (*SettingsPostgres)(nil)
