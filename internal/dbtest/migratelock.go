// Package dbtest holds test-only Postgres helpers shared across
// integration-test packages (which cannot import each other's _test.go
// helpers). It is intentionally narrow — only utilities that must be
// shared belong here.
package dbtest

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// MigrationLockKey is the bigint passed to pg_advisory_lock to serialize
// migration apply across the integration-test process. The `auth` schema in
// migration 0015 is database-global (schema names ignore search_path), so
// concurrent migrate.Up/Down/Migrate calls from different per-test
// search_path schemas race on CREATE/DROP SCHEMA against the same
// pg_namespace row (mi-omqp). golang-migrate's own advisory lock keys off
// the per-test schema name, so each test holds a different key and the
// built-in serialization does not help here. Tests acquire this single
// shared key around their migrate call instead.
//
// Value is an arbitrary stable int64 (ASCII bytes of "mi=omqp\0"). It just
// needs to be the same across every caller.
const MigrationLockKey int64 = 0x6d693d6f6d717000

// AcquireMigrateLock opens a dedicated Postgres connection against rawDSN
// and takes a session-scoped advisory lock on [MigrationLockKey]. The
// returned function releases the lock and closes the connection; it is
// safe to call multiple times. A t.Cleanup is also registered as a
// fallback so a lock can never leak past the test, regardless of how the
// caller exits.
//
// Callers should release the lock as soon as the migrate.Up/Down/Migrate
// call returns — the lock window covers only the migration apply, not
// the rest of the test.
func AcquireMigrateLock(ctx context.Context, t *testing.T, rawDSN string) func() {
	t.Helper()
	conn, err := pgx.Connect(ctx, rawDSN)
	if err != nil {
		t.Fatalf("dbtest: lock conn: %v", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", MigrationLockKey); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("dbtest: pg_advisory_lock: %v", err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			// Use Background ctx — the caller's ctx may have been
			// cancelled by a t.Cleanup running before us, but the
			// connection is still live and the unlock must complete.
			_, _ = conn.Exec(context.Background(),
				"SELECT pg_advisory_unlock($1)", MigrationLockKey)
			_ = conn.Close(context.Background())
		})
	}
	t.Cleanup(release)
	return release
}
