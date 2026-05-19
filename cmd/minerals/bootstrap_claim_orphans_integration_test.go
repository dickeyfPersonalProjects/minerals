//go:build integration

package main

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
)

// bootstrapTestEnv stands up an isolated Postgres schema, runs the
// embedded migrations against it, returns a scoped pgxpool, and
// registers cleanup. Mirrors the per-test-schema pattern in
// internal/migrations/migrations_integration_test.go so concurrent
// integration suites don't collide.
func bootstrapTestEnv(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping bootstrap-claim-orphans integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	schema := "bootstrap_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	admin, err := pgxpool.New(ctx, rawDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(admin.Close)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("cleanup drop schema %s: %v", schema, err)
		}
	})

	scopedDSN := dsnWithSearchPath(t, rawDSN, schema)
	m, err := migrate.New("file://"+repoMigrationsDir(t), scopedDSN)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

func repoMigrationsDir(t *testing.T) string {
	t.Helper()
	_, this, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(this), "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("abs migrations dir: %v", err)
	}
	return abs
}

func dsnWithSearchPath(t *testing.T, raw, schema string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// seedV1Data inserts a real user (status='active', a non-stub sub)
// and a sampling of V1-era rows owned by the stub-overseer across
// every orphanColumns table. Returns the real user's UUID.
//
// Counts per table are intentionally small but distinct so the
// per-table summary line in the CLI output is easy to assert.
func seedV1Data(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (realUser uuid.UUID, counts map[string]int) {
	t.Helper()
	stub := auth.StubUser.ID
	realUser = uuid.New()

	// Real user — status='active', new keycloak_sub.
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, keycloak_sub, email, display_name, status)
		VALUES ($1, 'real-user-sub', 'me@example.com', 'Real User', 'active')
	`, realUser); err != nil {
		t.Fatalf("seed real user: %v", err)
	}

	counts = map[string]int{
		"specimens":       2,
		"collectors":      3,
		"journal_entries": 1,
		"files":           2,
		"mineral_species": 4,
		"qr_sheets":       1,
	}

	// specimens (2)
	specIDs := []uuid.UUID{uuid.New(), uuid.New()}
	for i, id := range specIDs {
		if _, err := pool.Exec(ctx, `
			INSERT INTO specimens (id, type, name, author_id, type_data, created_at, updated_at)
			VALUES ($1, 'mineral', $2, $3, '{}'::jsonb, now(), now())
		`, id, "S"+strings.Repeat("x", i+1), stub); err != nil {
			t.Fatalf("seed specimen %d: %v", i, err)
		}
	}

	// collectors (3)
	for i := 0; i < counts["collectors"]; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO collectors (id, name, author_id, created_at, updated_at)
			VALUES ($1, $2, $3, now(), now())
		`, uuid.New(), "Collector-"+uuid.NewString()[:8], stub); err != nil {
			t.Fatalf("seed collector %d: %v", i, err)
		}
	}

	// journal_entries (1) — must reference a specimen
	if _, err := pool.Exec(ctx, `
		INSERT INTO journal_entries (id, specimen_id, author_id, body_md, created_at, updated_at)
		VALUES ($1, $2, $3, 'hello', now(), now())
	`, uuid.New(), specIDs[0], stub); err != nil {
		t.Fatalf("seed journal entry: %v", err)
	}

	// files (2)
	for i := 0; i < counts["files"]; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
			VALUES ($1, $2, 'image/png', 1, $3, $4, now())
		`, uuid.New(), "key-"+uuid.NewString(), uuid.NewString(), stub); err != nil {
			t.Fatalf("seed file %d: %v", i, err)
		}
	}

	// mineral_species (4)
	for i := 0; i < counts["mineral_species"]; i++ {
		if _, err := pool.Exec(ctx, `
			INSERT INTO mineral_species (id, name, source, data, author_id, created_at, updated_at)
			VALUES ($1, $2, 'user', '{}'::jsonb, $3, now(), now())
		`, uuid.New(), "Mineral-"+uuid.NewString()[:8], stub); err != nil {
			t.Fatalf("seed mineral_species %d: %v", i, err)
		}
	}

	// qr_sheets (1) — UNIQUE(user_id) caps this at one per owner.
	if _, err := pool.Exec(ctx, `
		INSERT INTO qr_sheets (id, user_id, template, created_at, updated_at)
		VALUES ($1, $2, 'default', now(), now())
	`, uuid.New(), stub); err != nil {
		t.Fatalf("seed qr_sheet: %v", err)
	}

	return realUser, counts
}

// TestBootstrapClaimOrphans_HappyPath_Yes runs the full --yes path
// end-to-end: seed orphans, run the command, assert every row moved
// from stub-overseer to the real user, then re-run for idempotency.
func TestBootstrapClaimOrphans_HappyPath_Yes(t *testing.T) {
	ctx, pool := bootstrapTestEnv(t)
	realUser, counts := seedV1Data(t, ctx, pool)

	var out bytes.Buffer
	code, err := bootstrapClaimOrphansWithPool(ctx, &out, pool, bootstrapArgs{
		email:   "me@example.com",
		confirm: true,
	})
	if err != nil {
		t.Fatalf("first run: code=%d err=%v\noutput:\n%s", code, err, out.String())
	}
	if code != bootstrapExitOK {
		t.Fatalf("first run code=%d, want %d", code, bootstrapExitOK)
	}

	// Spot-check the summary mentions every table + the total.
	body := out.String()
	for table, n := range counts {
		needle := "Claimed " + strconv.Itoa(n) + " " + table
		if !strings.Contains(body, needle) {
			t.Errorf("summary missing %q\noutput:\n%s", needle, body)
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if !strings.Contains(body, "Total: "+strconv.Itoa(total)+" rows") {
		t.Errorf("summary missing total %d rows\noutput:\n%s", total, body)
	}

	// Verify every orphan column moved.
	for _, oc := range orphanColumns {
		var nStub, nReal int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+oc.table+" WHERE "+oc.column+" = $1",
			auth.StubUser.ID,
		).Scan(&nStub); err != nil {
			t.Fatalf("count stub %s.%s: %v", oc.table, oc.column, err)
		}
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+oc.table+" WHERE "+oc.column+" = $1",
			realUser,
		).Scan(&nReal); err != nil {
			t.Fatalf("count real %s.%s: %v", oc.table, oc.column, err)
		}
		if nStub != 0 {
			t.Errorf("%s.%s: %d row(s) still owned by stub-overseer", oc.table, oc.column, nStub)
		}
		if nReal != counts[oc.table] {
			t.Errorf("%s.%s: real-user rows = %d, want %d", oc.table, oc.column, nReal, counts[oc.table])
		}
	}

	// Idempotent: second run claims zero, exits 0.
	var out2 bytes.Buffer
	code2, err := bootstrapClaimOrphansWithPool(ctx, &out2, pool, bootstrapArgs{
		email:   "me@example.com",
		confirm: true,
	})
	if err != nil {
		t.Fatalf("second run err: %v\noutput:\n%s", err, out2.String())
	}
	if code2 != bootstrapExitOK {
		t.Fatalf("second run code=%d, want %d", code2, bootstrapExitOK)
	}
	if !strings.Contains(out2.String(), "Total: 0 rows") {
		t.Errorf("second run total != 0\noutput:\n%s", out2.String())
	}
}

// TestBootstrapClaimOrphans_DryRun_NoWrites confirms that --dry-run
// emits a plan and leaves the database untouched.
func TestBootstrapClaimOrphans_DryRun_NoWrites(t *testing.T) {
	ctx, pool := bootstrapTestEnv(t)
	_, counts := seedV1Data(t, ctx, pool)

	var out bytes.Buffer
	code, err := bootstrapClaimOrphansWithPool(ctx, &out, pool, bootstrapArgs{
		email:  "me@example.com",
		dryRun: true,
	})
	if err != nil {
		t.Fatalf("dry-run err: %v\noutput:\n%s", err, out.String())
	}
	if code != bootstrapExitOK {
		t.Fatalf("dry-run code=%d, want %d", code, bootstrapExitOK)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("dry-run output missing the 'dry-run' header tag\noutput:\n%s", out.String())
	}

	// Nothing moved.
	for _, oc := range orphanColumns {
		var n int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM "+oc.table+" WHERE "+oc.column+" = $1",
			auth.StubUser.ID,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", oc.table, err)
		}
		if n != counts[oc.table] {
			t.Errorf("%s.%s: stub rows after dry-run = %d, want %d (untouched)", oc.table, oc.column, n, counts[oc.table])
		}
	}
}

// TestBootstrapClaimOrphans_NoYesRefuses confirms the bead's
// no-write-without-yes safety: missing --yes prints the plan and
// exits 2 with the database unchanged.
func TestBootstrapClaimOrphans_NoYesRefuses(t *testing.T) {
	ctx, pool := bootstrapTestEnv(t)
	_, counts := seedV1Data(t, ctx, pool)

	var out bytes.Buffer
	code, err := bootstrapClaimOrphansWithPool(ctx, &out, pool, bootstrapArgs{
		email: "me@example.com",
	})
	if err == nil {
		t.Fatalf("expected refusal error, got nil\noutput:\n%s", out.String())
	}
	if code != bootstrapExitGuardTripped {
		t.Errorf("refusal code=%d, want %d", code, bootstrapExitGuardTripped)
	}
	// Database untouched.
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM specimens WHERE author_id = $1",
		auth.StubUser.ID,
	).Scan(&n); err != nil {
		t.Fatalf("count specimens: %v", err)
	}
	if n != counts["specimens"] {
		t.Errorf("specimens stub rows after refusal = %d, want %d (untouched)", n, counts["specimens"])
	}
}

// TestBootstrapClaimOrphans_UserNotFound asserts exit code 1.
func TestBootstrapClaimOrphans_UserNotFound(t *testing.T) {
	ctx, pool := bootstrapTestEnv(t)
	// Note: do NOT seed a real user — the lookup must fail.

	var out bytes.Buffer
	code, err := bootstrapClaimOrphansWithPool(ctx, &out, pool, bootstrapArgs{
		email:   "missing@example.com",
		confirm: true,
	})
	if err == nil {
		t.Fatal("expected user-not-found error, got nil")
	}
	if code != bootstrapExitUserNotFound {
		t.Errorf("code=%d, want %d", code, bootstrapExitUserNotFound)
	}
}

// TestBootstrapClaimOrphans_PendingUserRefused asserts the
// status='pending' guard exits 2.
func TestBootstrapClaimOrphans_PendingUserRefused(t *testing.T) {
	ctx, pool := bootstrapTestEnv(t)
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, keycloak_sub, email, display_name, status)
		VALUES ($1, 'pending-sub', 'pending@example.com', NULL, 'pending')
	`, id); err != nil {
		t.Fatalf("seed pending user: %v", err)
	}

	var out bytes.Buffer
	code, err := bootstrapClaimOrphansWithPool(ctx, &out, pool, bootstrapArgs{
		email:   "pending@example.com",
		confirm: true,
	})
	if err == nil {
		t.Fatal("expected pending-status guard, got nil")
	}
	if code != bootstrapExitGuardTripped {
		t.Errorf("code=%d, want %d", code, bootstrapExitGuardTripped)
	}
	if !strings.Contains(err.Error(), "pending") {
		t.Errorf("error %q does not mention 'pending'", err.Error())
	}
}

// TestBootstrapClaimOrphans_StubTargetRefused asserts that selecting
// the stub-overseer as the target user trips exit 2 — claiming
// stub-to-stub is a misconfiguration the bead refuses.
func TestBootstrapClaimOrphans_StubTargetRefused(t *testing.T) {
	ctx, pool := bootstrapTestEnv(t)
	// Add a real second user so the "exactly 1 active non-stub user"
	// guard wouldn't fire here — we want to isolate the stub-target
	// rejection cleanly.
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, keycloak_sub, email, status)
		VALUES ($1, 'real-extra', 'extra@example.com', 'active')
	`, uuid.New()); err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	var out bytes.Buffer
	code, err := bootstrapClaimOrphansWithPool(ctx, &out, pool, bootstrapArgs{
		sub:     auth.StubUserSub,
		confirm: true,
	})
	if err == nil {
		t.Fatal("expected stub-target rejection, got nil")
	}
	if code != bootstrapExitGuardTripped {
		t.Errorf("code=%d, want %d", code, bootstrapExitGuardTripped)
	}
}

// TestBootstrapClaimOrphans_MultipleActiveUsersRefused asserts the
// bead's "exactly one active non-stub user" guard fires when two
// real users coexist.
func TestBootstrapClaimOrphans_MultipleActiveUsersRefused(t *testing.T) {
	ctx, pool := bootstrapTestEnv(t)
	seedV1Data(t, ctx, pool) // adds the first real user
	// Second real user.
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, keycloak_sub, email, status)
		VALUES ($1, 'second-real', 'second@example.com', 'active')
	`, uuid.New()); err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	var out bytes.Buffer
	code, err := bootstrapClaimOrphansWithPool(ctx, &out, pool, bootstrapArgs{
		email:   "me@example.com",
		confirm: true,
	})
	if err == nil {
		t.Fatal("expected multi-user guard, got nil")
	}
	if code != bootstrapExitGuardTripped {
		t.Errorf("code=%d, want %d", code, bootstrapExitGuardTripped)
	}
	if !strings.Contains(err.Error(), "got 2") {
		t.Errorf("error %q does not name the user count", err.Error())
	}
}
