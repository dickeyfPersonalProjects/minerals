//go:build integration

package migrations_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/dbtest"
)

// migrationsDir resolves the absolute path to the repo's migrations/
// directory from this test file's location.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve caller path")
	}
	// internal/migrations/<this file> → repo root is two levels up.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(filepath.Join(repoRoot, "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return abs
}

// dsnWithSearchPath returns the DSN with `search_path=<schema>` appended
// so golang-migrate (and pgx connections) operate inside an isolated
// per-test schema.
func dsnWithSearchPath(t *testing.T, raw, schema string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// dsnWithDatabase returns the DSN with its path component replaced by
// dbname. Used by TestMigrations_UpDownUp to point migrate at an
// isolated temporary database.
func dsnWithDatabase(t *testing.T, raw, dbname string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	u.Path = "/" + dbname
	return u.String()
}

// TestMigrations_UpDownUp exercises the full round-trip required by the
// bead: up → introspect → down → introspect → up.
//
// Runs against a dedicated temporary DATABASE (not just a search_path
// schema) because migration 0015 manipulates the database-global `auth`
// schema (schema names ignore search_path). m.Down would otherwise
// DROP that schema while other concurrent integration tests are mid-
// use of auth.sessions, surfacing as "schema auth does not exist" or
// "relation auth.sessions does not exist" failures on main (mi-omqp).
// A dedicated database isolates this test's auth-schema lifecycle.
func TestMigrations_UpDownUp(t *testing.T) {
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping migrations integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dbname := "mig_test_db_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	// Admin connection on the source database used only to CREATE/DROP
	// the temporary database. CREATE DATABASE cannot run inside a
	// transaction, so we use a single Conn (not a pool — pool can wrap
	// statements in implicit transactions for some operations).
	adminConn, err := pgx.Connect(ctx, rawDSN)
	if err != nil {
		t.Fatalf("admin conn: %v", err)
	}
	if _, err := adminConn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, dbname)); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create database %s: %v", dbname, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		// Force-close any lingering connections to the temp DB so the
		// drop doesn't error with "is being accessed by other users".
		_, _ = adminConn.Exec(bg,
			`SELECT pg_terminate_backend(pid)
			 FROM pg_stat_activity
			 WHERE datname = $1 AND pid != pg_backend_pid()`,
			dbname,
		)
		if _, err := adminConn.Exec(bg,
			fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, dbname),
		); err != nil {
			t.Logf("cleanup: drop database %s: %v", dbname, err)
		}
		_ = adminConn.Close(bg)
	})

	testDSN := dsnWithDatabase(t, rawDSN, dbname)

	// Per-test schema inside the temporary database — keeps the
	// assertion helpers (parameterized by schema name) working as-is.
	schema := "mig_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("temp db pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	scopedDSN := dsnWithSearchPath(t, testDSN, schema)
	migrationsURL := "file://" + migrationsDir(t)

	m, err := migrate.New(migrationsURL, scopedDSN)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			t.Logf("migrate.Close: src=%v db=%v", srcErr, dbErr)
		}
	}()

	// No advisory lock needed — the dedicated database means we're the
	// only migrator that can touch this auth schema.

	// Up.
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	assertSchemaPresent(ctx, t, pool, schema)

	// Down.
	if err := m.Down(); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	assertSchemaAbsent(ctx, t, pool, schema)

	// Up again — idempotent reapply must succeed cleanly.
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up (second): %v", err)
	}
	assertSchemaPresent(ctx, t, pool, schema)
}

// expectedTables is the list of tables 0001_init.up.sql creates.
var expectedTables = []string{
	"specimens",
	"collectors",
	"files",
	"specimen_collectors",
	"photos",
	"journal_entries",
	"journal_entry_files",
}

// expectedIndexes covers the GIN tsvector index plus every FK index
// the bead's acceptance criteria require.
var expectedIndexes = []string{
	"specimens_search_tsv_idx",
	"specimen_collectors_collector_id_idx",
	"photos_specimen_id_idx",
	"photos_file_id_idx",
	"journal_entries_specimen_id_idx",
	"journal_entry_files_file_id_idx",
}

// expectedEnums lists the enum types created by the migration.
var expectedEnums = []string{"specimen_type", "specimen_visibility"}

func assertSchemaPresent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	for _, table := range expectedTables {
		if !tableExists(ctx, t, pool, schema, table) {
			t.Errorf("expected table %s.%s to exist after up", schema, table)
		}
	}
	for _, idx := range expectedIndexes {
		if !indexExists(ctx, t, pool, schema, idx) {
			t.Errorf("expected index %s.%s to exist after up", schema, idx)
		}
	}
	for _, enum := range expectedEnums {
		if !enumExists(ctx, t, pool, schema, enum) {
			t.Errorf("expected enum type %s.%s to exist after up", schema, enum)
		}
	}
	// Sanity-check key constraints/columns to prove the schema matches.
	assertColumn(ctx, t, pool, schema, "specimens", "author_id", isNotNull(true), hasDefault(false))
	assertColumn(ctx, t, pool, schema, "specimens", "type_data", isNotNull(true), hasDefault(true))
	assertColumn(ctx, t, pool, schema, "specimens", "search_tsv", isGenerated(true))
	assertColumn(ctx, t, pool, schema, "files", "uploaded_by", isNotNull(true), hasDefault(false))
	assertColumn(ctx, t, pool, schema, "journal_entries", "author_id", isNotNull(true), hasDefault(false))
	assertColumn(ctx, t, pool, schema, "collectors", "author_id", isNotNull(true), hasDefault(false))
	assertColumn(ctx, t, pool, schema, "specimens", "id", dataType("uuid"))
	assertColumn(ctx, t, pool, schema, "files", "s3_key", isNotNull(true))

	// Foreign keys exist where expected.
	for _, fk := range []struct{ table, column, refTable string }{
		{"photos", "specimen_id", "specimens"},
		{"photos", "file_id", "files"},
		{"journal_entries", "specimen_id", "specimens"},
		{"journal_entry_files", "entry_id", "journal_entries"},
		{"journal_entry_files", "file_id", "files"},
		{"specimen_collectors", "specimen_id", "specimens"},
		{"specimen_collectors", "collector_id", "collectors"},
	} {
		if !fkExists(ctx, t, pool, schema, fk.table, fk.column, fk.refTable) {
			t.Errorf("expected FK %s.%s → %s.id", fk.table, fk.column, fk.refTable)
		}
	}
}

func assertSchemaAbsent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	for _, table := range expectedTables {
		if tableExists(ctx, t, pool, schema, table) {
			t.Errorf("expected table %s.%s to be gone after down", schema, table)
		}
	}
	for _, enum := range expectedEnums {
		if enumExists(ctx, t, pool, schema, enum) {
			t.Errorf("expected enum %s.%s to be gone after down", schema, enum)
		}
	}
}

// TestMigration0009_NormalizesChemicalFormula seeds HTML-flavored
// chemical_formula values into specimens.type_data and
// mineral_species.data BEFORE applying 0009, then verifies the
// migration rewrote them into clean Unicode (mi-c8v). Also asserts the
// post-migration acceptance criterion: no row in either column
// contains '<' or a recognized HTML entity prefix.
func TestMigration0009_NormalizesChemicalFormula(t *testing.T) {
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping migrations integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schema := "mig0009_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	adminPool, err := pgxpool.New(ctx, rawDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(adminPool.Close)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(clean, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("cleanup: drop schema %s: %v", schema, err)
		}
	})

	scopedDSN := dsnWithSearchPath(t, rawDSN, schema)
	m, err := migrate.New("file://"+migrationsDir(t), scopedDSN)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })

	// Migrate up to 0008 (everything BEFORE the backfill). This ensures
	// specimens + mineral_species exist and we can seed dirty rows.
	// Lock-guarded because earlier migrations don't touch `auth`, but
	// later steps in this test do; serializing every migrate call keeps
	// the helper's contract uniform (mi-omqp).
	{
		unlock := dbtest.AcquireMigrateLock(ctx, t, rawDSN)
		err := m.Migrate(8)
		unlock()
		if err != nil {
			t.Fatalf("migrate up to 8: %v", err)
		}
	}

	// Seed dirty data through a scoped pool.
	pool, err := pgxpool.New(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	defer pool.Close()

	const dirtyFormula = "Pb(UO<sub>2</sub>)<sub>3</sub>O<sub>3</sub>(OH)<sub>2</sub> &middot; 3H<sub>2</sub>O"
	const cleanFormula = "Pb(UO₂)₃O₃(OH)₂ · 3H₂O"
	const alreadyClean = "SiO₂"

	specimenDirty := uuid.New()
	specimenClean := uuid.New()
	speciesDirty := uuid.New()
	speciesAlreadyClean := uuid.New()
	authorID := "00000000-0000-0000-0000-000000000001" // overseer stub from 0008

	if _, err := pool.Exec(ctx, `
		INSERT INTO specimens (id, type, name, author_id, type_data, created_at, updated_at)
		VALUES
			($1, 'mineral', 'Curite-dirty', $2,
				jsonb_build_object('chemical_formula', $3::text), now(), now()),
			($4, 'mineral', 'Quartz-clean', $2,
				jsonb_build_object('chemical_formula', $5::text), now(), now())
	`, specimenDirty, authorID, dirtyFormula, specimenClean, alreadyClean); err != nil {
		t.Fatalf("seed specimens: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO mineral_species (id, name, source, data, author_id, created_at, updated_at)
		VALUES
			($1, 'Curite-dirty', 'mindat',
				jsonb_build_object('chemical_formula', $2::text), $3, now(), now()),
			($4, 'Quartz-clean', 'user',
				jsonb_build_object('chemical_formula', $5::text), $3, now(), now())
	`, speciesDirty, dirtyFormula, authorID, speciesAlreadyClean, alreadyClean); err != nil {
		t.Fatalf("seed mineral_species: %v", err)
	}

	// Apply 0009.
	{
		unlock := dbtest.AcquireMigrateLock(ctx, t, rawDSN)
		err := m.Migrate(9)
		unlock()
		if err != nil {
			t.Fatalf("migrate up to 9: %v", err)
		}
	}

	// Dirty rows are now clean.
	for _, c := range []struct {
		table, idCol, dataCol string
		id                    uuid.UUID
		want                  string
	}{
		{"specimens", "id", "type_data", specimenDirty, cleanFormula},
		{"specimens", "id", "type_data", specimenClean, alreadyClean},
		{"mineral_species", "id", "data", speciesDirty, cleanFormula},
		{"mineral_species", "id", "data", speciesAlreadyClean, alreadyClean},
	} {
		var got string
		q := "SELECT " + c.dataCol + "->>'chemical_formula' FROM " + c.table + " WHERE " + c.idCol + " = $1"
		if err := pool.QueryRow(ctx, q, c.id).Scan(&got); err != nil {
			t.Errorf("readback %s/%s: %v", c.table, c.id, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s/%s chemical_formula = %q, want %q", c.table, c.id, got, c.want)
		}
		if strings.ContainsAny(got, "<&") {
			t.Errorf("%s/%s still contains markup: %q", c.table, c.id, got)
		}
	}

	// Down is a documented no-op for the data; should run cleanly.
	{
		unlock := dbtest.AcquireMigrateLock(ctx, t, rawDSN)
		err := m.Migrate(8)
		unlock()
		if err != nil {
			t.Fatalf("migrate down to 8: %v", err)
		}
	}
}

// --- pg_catalog introspection helpers ---

func tableExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema, table string) bool {
	t.Helper()
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'r'
		)`
	var ok bool
	if err := pool.QueryRow(ctx, q, schema, table).Scan(&ok); err != nil {
		t.Fatalf("tableExists query: %v", err)
	}
	return ok
}

func indexExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema, index string) bool {
	t.Helper()
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'i'
		)`
	var ok bool
	if err := pool.QueryRow(ctx, q, schema, index).Scan(&ok); err != nil {
		t.Fatalf("indexExists query: %v", err)
	}
	return ok
}

func enumExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema, name string) bool {
	t.Helper()
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM pg_type t
			JOIN pg_namespace n ON n.oid = t.typnamespace
			WHERE n.nspname = $1 AND t.typname = $2 AND t.typtype = 'e'
		)`
	var ok bool
	if err := pool.QueryRow(ctx, q, schema, name).Scan(&ok); err != nil {
		t.Fatalf("enumExists query: %v", err)
	}
	return ok
}

func fkExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema, table, column, refTable string) bool {
	t.Helper()
	const q = `
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint c
			JOIN pg_class src ON src.oid = c.conrelid
			JOIN pg_namespace srcns ON srcns.oid = src.relnamespace
			JOIN pg_class dst ON dst.oid = c.confrelid
			JOIN pg_attribute a ON a.attrelid = src.oid AND a.attnum = ANY(c.conkey)
			WHERE c.contype = 'f'
			  AND srcns.nspname = $1
			  AND src.relname = $2
			  AND a.attname = $3
			  AND dst.relname = $4
			  AND array_length(c.conkey, 1) = 1
		)`
	var ok bool
	if err := pool.QueryRow(ctx, q, schema, table, column, refTable).Scan(&ok); err != nil {
		t.Fatalf("fkExists query: %v", err)
	}
	return ok
}

// columnAssertion is a fluent option set for assertColumn.
type columnAssertion struct {
	notNull   *bool
	hasDef    *bool
	generated *bool
	udtName   string
}

type colOpt func(*columnAssertion)

func isNotNull(v bool) colOpt   { return func(a *columnAssertion) { a.notNull = &v } }
func hasDefault(v bool) colOpt  { return func(a *columnAssertion) { a.hasDef = &v } }
func isGenerated(v bool) colOpt { return func(a *columnAssertion) { a.generated = &v } }
func dataType(name string) colOpt {
	return func(a *columnAssertion) { a.udtName = name }
}

func assertColumn(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema, table, column string, opts ...colOpt) {
	t.Helper()
	a := columnAssertion{}
	for _, o := range opts {
		o(&a)
	}

	const q = `
		SELECT
			is_nullable,
			column_default,
			COALESCE(is_generated, 'NEVER'),
			udt_name
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`

	var (
		isNullable    string
		columnDefault *string
		isGen         string
		udt           string
	)
	if err := pool.QueryRow(ctx, q, schema, table, column).Scan(&isNullable, &columnDefault, &isGen, &udt); err != nil {
		t.Errorf("introspect %s.%s.%s: %v", schema, table, column, err)
		return
	}

	if a.notNull != nil {
		got := isNullable == "NO"
		if got != *a.notNull {
			t.Errorf("%s.%s.%s NOT NULL: want=%v got=%v", schema, table, column, *a.notNull, got)
		}
	}
	if a.hasDef != nil {
		got := columnDefault != nil
		if got != *a.hasDef {
			t.Errorf("%s.%s.%s has default: want=%v got=%v (default=%v)", schema, table, column, *a.hasDef, got, derefStr(columnDefault))
		}
	}
	if a.generated != nil {
		got := isGen == "ALWAYS"
		if got != *a.generated {
			t.Errorf("%s.%s.%s generated: want=%v got=%q", schema, table, column, *a.generated, isGen)
		}
	}
	if a.udtName != "" && udt != a.udtName {
		t.Errorf("%s.%s.%s data type: want=%s got=%s", schema, table, column, a.udtName, udt)
	}
}

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
