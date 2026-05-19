//go:build integration

package bff_test

import (
	"context"
	"crypto/rand"
	"errors"
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
	"github.com/dickeyfPersonalProjects/minerals/internal/dbtest"
)

// TestCleanup_DeletesExpiredAndRevokedRows is the acceptance test
// called out by mi-twql: insert rows that straddle both retention
// windows, run Cleanup with a fixed `now`, and assert only the
// expected rows were deleted.
//
// All four rows share a freshly-generated user_id so the assertions
// scope cleanly to this test even though auth.sessions is a global
// schema (the migration creates it in `auth.*` regardless of
// search_path, so it is shared across concurrent tests).
func TestCleanup_DeletesExpiredAndRevokedRows(t *testing.T) {
	pool := setupPool(t)
	cleaner := bff.NewCleaner(pool)
	ctx := context.Background()

	// Fixed clock so the retention windows ('< now - 30 days' and
	// '< now - 7 days') are deterministic regardless of when the
	// test runs.
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	userID := uuid.New() // isolates this test's rows from any others
	t.Cleanup(func() {
		drop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(drop,
			`DELETE FROM auth.sessions WHERE user_id = $1`, userID,
		); err != nil {
			t.Logf("post-test cleanup of auth.sessions for %s: %v", userID, err)
		}
	})

	keepFresh := randID(t)             // alive: not revoked, not absolute-expired
	keepRevokedRecent := randID(t)     // revoked 29d ago — inside 30d window
	deleteAbsoluteExpired := randID(t) // absolute_expires_at 8d ago — past 7d grace
	deleteRevokedStale := randID(t)    // revoked 31d ago — past 30d window

	insertSession(t, ctx, pool, sessionRow{
		id:                    keepFresh,
		userID:                userID,
		accessTokenExpiresAt:  now.Add(5 * time.Minute),
		refreshTokenExpiresAt: now.Add(30 * 24 * time.Hour),
		absoluteExpiresAt:     now.Add(7 * 24 * time.Hour),
	})
	insertSession(t, ctx, pool, sessionRow{
		id:                    keepRevokedRecent,
		userID:                userID,
		accessTokenExpiresAt:  now.Add(5 * time.Minute),
		refreshTokenExpiresAt: now.Add(30 * 24 * time.Hour),
		absoluteExpiresAt:     now.Add(7 * 24 * time.Hour),
		revokedAt:             timePtr(now.Add(-29 * 24 * time.Hour)),
	})
	insertSession(t, ctx, pool, sessionRow{
		id:                    deleteAbsoluteExpired,
		userID:                userID,
		accessTokenExpiresAt:  now.Add(-time.Hour),
		refreshTokenExpiresAt: now.Add(-time.Hour),
		absoluteExpiresAt:     now.Add(-8 * 24 * time.Hour),
	})
	insertSession(t, ctx, pool, sessionRow{
		id:                    deleteRevokedStale,
		userID:                userID,
		accessTokenExpiresAt:  now.Add(5 * time.Minute),
		refreshTokenExpiresAt: now.Add(30 * 24 * time.Hour),
		absoluteExpiresAt:     now.Add(7 * 24 * time.Hour),
		revokedAt:             timePtr(now.Add(-31 * 24 * time.Hour)),
	})

	if _, err := cleaner.Cleanup(ctx, now); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	got := surviveSessionIDs(t, ctx, pool, userID)
	want := map[string]bool{
		string(keepFresh):         true,
		string(keepRevokedRecent): true,
	}
	if len(got) != len(want) {
		t.Errorf("survivors: got %d rows, want %d (got=%v)", len(got), len(want), summarise(got))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected row to survive (id prefix %x) but it was deleted", id[:4])
		}
	}
	for id := range got {
		if !want[id] {
			t.Errorf("unexpected survivor (id prefix %x) — should have been deleted", id[:4])
		}
	}
}

// TestCleanup_BoundaryRowsAreInclusive verifies the strict-less-than
// semantics in the WHERE clause: a row exactly at the boundary is
// NOT deleted. This matches the SQL's `<` operator and gives
// operators a predictable retention floor.
func TestCleanup_BoundaryRowsAreInclusive(t *testing.T) {
	pool := setupPool(t)
	cleaner := bff.NewCleaner(pool)
	ctx := context.Background()

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	t.Cleanup(func() {
		drop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(drop,
			`DELETE FROM auth.sessions WHERE user_id = $1`, userID,
		)
	})

	// absolute_expires_at = now - 7d exactly → should NOT be deleted
	// (the WHERE uses < not <=).
	boundaryAbs := randID(t)
	// revoked_at = now - 30d exactly → should NOT be deleted.
	boundaryRev := randID(t)

	insertSession(t, ctx, pool, sessionRow{
		id:                    boundaryAbs,
		userID:                userID,
		accessTokenExpiresAt:  now,
		refreshTokenExpiresAt: now,
		absoluteExpiresAt:     now.Add(-7 * 24 * time.Hour),
	})
	insertSession(t, ctx, pool, sessionRow{
		id:                    boundaryRev,
		userID:                userID,
		accessTokenExpiresAt:  now,
		refreshTokenExpiresAt: now,
		absoluteExpiresAt:     now.Add(7 * 24 * time.Hour),
		revokedAt:             timePtr(now.Add(-30 * 24 * time.Hour)),
	})

	if _, err := cleaner.Cleanup(ctx, now); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	got := surviveSessionIDs(t, ctx, pool, userID)
	if !got[string(boundaryAbs)] {
		t.Errorf("absolute boundary row (now - 7d exactly) was deleted; should survive")
	}
	if !got[string(boundaryRev)] {
		t.Errorf("revoked boundary row (now - 30d exactly) was deleted; should survive")
	}
}

// sessionRow is the minimum-field shape used by tests to drive
// inserts. Fields the cleanup logic ignores get plausible
// placeholder values.
type sessionRow struct {
	id                    []byte
	userID                uuid.UUID
	accessTokenExpiresAt  time.Time
	refreshTokenExpiresAt time.Time
	absoluteExpiresAt     time.Time
	revokedAt             *time.Time
}

func insertSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, r sessionRow) {
	t.Helper()
	csrf := randID(t) // 32 bytes, fine as placeholder
	_, err := pool.Exec(ctx, `
		INSERT INTO auth.sessions (
			id, user_sub, user_id,
			access_token, refresh_token, id_token,
			access_token_expires_at, refresh_token_expires_at,
			absolute_expires_at, csrf_token, revoked_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.id, "test-"+r.userID.String(), r.userID,
		"access", "refresh", "id",
		r.accessTokenExpiresAt, r.refreshTokenExpiresAt,
		r.absoluteExpiresAt, csrf, r.revokedAt,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// surviveSessionIDs returns the set of session ids in auth.sessions
// owned by userID (encoded as a map[string]bool because []byte is not
// directly map-keyable in Go).
func surviveSessionIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) map[string]bool {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT id FROM auth.sessions WHERE user_id = $1`, userID,
	)
	if err != nil {
		t.Fatalf("query survivors: %v", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[string(id)] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	return out
}

// summarise formats a survivor set with short hex prefixes for
// readable failure output.
func summarise(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		if len(k) >= 4 {
			out = append(out, string([]byte{k[0], k[1], k[2], k[3]}))
		} else {
			out = append(out, k)
		}
	}
	return out
}

// timePtr is a tiny helper for the *time.Time fields on sessionRow.
func timePtr(t time.Time) *time.Time { return &t }

// randID returns 32 random bytes (matches the design doc's session
// id shape). Fatal-fails the test on any rand.Read error so the
// individual test methods stay free of error plumbing for inert
// preconditions.
func randID(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return b
}

// setupPool stands up a real pool against DATABASE_URL after applying
// the full migration chain into an isolated per-test schema. The
// migration chain creates the global `auth` schema (idempotent) so
// auth.sessions is available to the returned pool regardless of
// search_path.
func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping bff integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	schema := "bff_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

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
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	scopedDSN := dsnWithSearchPath(t, rawDSN, schema)

	m, err := migrate.New("file://"+migrationsDir(t), scopedDSN)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })

	// Serialize the migrate window: `auth` is a database-global schema
	// (mi-omqp), so parallel migrate.Up/Down across per-test schemas
	// race on its create/drop. Released immediately after Up returns.
	unlock := dbtest.AcquireMigrateLock(ctx, t, rawDSN)
	upErr := m.Up()
	unlock()
	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", upErr)
	}

	pool, err := pgxpool.New(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
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

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller path")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("abs migrations dir: %v", err)
	}
	return abs
}
