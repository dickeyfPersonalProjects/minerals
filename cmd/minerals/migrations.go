package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/dickeyfPersonalProjects/minerals/migrations"
)

// migrationsSubFS is the migrations directory rooted FS. golang-
// migrate's iofs source treats the root of this FS as the migrations
// directory.
func migrationsSubFS() fs.FS { return migrations.FS }

// migrationFiles lists *.up.sql / *.down.sql entries in the embedded
// migrations FS (sorted). Used to compute "is migrations/ empty?" and
// to discover the highest migration number.
func migrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationsSubFS(), ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if matched, _ := path.Match("*.up.sql", name); matched {
			out = append(out, name)
			continue
		}
		if matched, _ := path.Match("*.down.sql", name); matched {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// hasMigrations reports whether the embedded migrations FS contains
// at least one *.up.sql file. Used to skip the schema-version check
// before bd #1 lands.
func hasMigrations() (bool, error) {
	files, err := migrationFiles()
	if err != nil {
		return false, err
	}
	for _, f := range files {
		if matched, _ := path.Match("*.up.sql", f); matched {
			return true, nil
		}
	}
	return false, nil
}

// migrationVersionRegex parses the leading 4-digit number off a
// migration filename. Anchored so a filename like
// "9999_DESTRUCTIVE.up.sql" still matches.
var migrationVersionRegex = regexp.MustCompile(`^(\d{4})_`)

// highestMigration returns the largest NNNN sequence number among the
// embedded *.up.sql files, or 0 if there are none.
func highestMigration() (uint, error) {
	files, err := migrationFiles()
	if err != nil {
		return 0, err
	}
	var maxVer uint
	for _, f := range files {
		matched, _ := path.Match("*.up.sql", f)
		if !matched {
			continue
		}
		m := migrationVersionRegex.FindStringSubmatch(f)
		if len(m) < 2 {
			continue
		}
		n, err := strconv.ParseUint(m[1], 10, 32)
		if err != nil {
			continue
		}
		if uint(n) > maxVer {
			maxVer = uint(n)
		}
	}
	return maxVer, nil
}

// newMigrate builds a *migrate.Migrate over the embedded sources and
// the configured DATABASE_URL. We use the pgx5:// scheme (registered
// by the imported pgx/v5 migrate driver) so the migrate library
// opens its own short-lived sql.DB rather than sharing the app's
// pgxpool.Pool — keeps lifecycles cleanly separate.
func newMigrate(_ context.Context, dbURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrationsSubFS(), ".")
	if err != nil {
		return nil, fmt.Errorf("iofs source: %w", err)
	}
	migrateURL, err := toPgxMigrateURL(dbURL)
	if err != nil {
		return nil, err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL)
	if err != nil {
		return nil, fmt.Errorf("migrate.NewWithSourceInstance: %w", err)
	}
	return m, nil
}

// toPgxMigrateURL rewrites a postgres://... DSN to pgx5://... — the
// scheme expected by the pgx/v5 migrate driver. Other schemes pass
// through unchanged so callers can supply pgx5:// directly if they
// prefer.
func toPgxMigrateURL(dbURL string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		u.Scheme = "pgx5"
	}
	return u.String(), nil
}

// autoMigrateDev applies any pending migrations against dbURL. It is
// only called from the dev startup path (mi-8ky / serve.go) so that a
// fresh `docker compose up -d` lands a usable app on :8080 without
// requiring a separate `make migrate-up` first. In prod the schema is
// owned by the migrate Job per design §6.4 — this function is not
// invoked there. A no-op (nil) when migrations/ is empty or already
// at the highest version.
func autoMigrateDev(ctx context.Context, dbURL string) error {
	m, err := newMigrate(ctx, dbURL)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return nil
		}
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// newSchemaVersionProbe returns the SchemaVersion reader wired into
// /readyz, caching the first successful read. The applied schema
// version is immutable for the process lifetime — in prod the migrate
// Job runs before the deployment rolls (design §6.4) and serve startup
// already asserts the version via verifySchemaVersion — so re-reading
// it on every readiness probe only churns a fresh golang-migrate DB
// connection (schemaVersion → newMigrate opens its own short-lived
// sql.DB) against the same Postgres the app pool depends on. Under the
// mi-hkh6 saturation incident that per-probe connect competed for
// Postgres backend slots on the very path meant to report health;
// caching removes it from the hot path entirely.
//
// Errors are NOT cached: until the first success the probe keeps
// retrying, so a DB that is briefly unreachable still resolves to a
// real version once it comes up rather than latching a stale failure.
func newSchemaVersionProbe(dbURL string) func(context.Context) (uint, bool, error) {
	var (
		mu     sync.Mutex
		cached bool
		ver    uint
		dirty  bool
	)
	return func(ctx context.Context) (uint, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached {
			return ver, dirty, nil
		}
		v, d, err := schemaVersion(ctx, dbURL)
		if err != nil {
			return 0, false, err
		}
		ver, dirty, cached = v, d, true
		return ver, dirty, nil
	}
}

// schemaVersion reports the current applied migration version. If no
// migrations have been applied (or migrations/ is empty), returns
// (0, false, nil).
func schemaVersion(ctx context.Context, dbURL string) (uint, bool, error) {
	m, err := newMigrate(ctx, dbURL)
	if err != nil {
		return 0, false, err
	}
	defer func() { _, _ = m.Close() }()
	v, dirty, err := m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return v, dirty, nil
}
