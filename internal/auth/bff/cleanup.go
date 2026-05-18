// Package bff is the home of the V2 backend-for-frontend (BFF) auth
// implementation. Canonical design: docs/design/auth-bff.md (mi-bv66).
//
// As of mi-twql (mi-1d5i #1) the package only carries the
// auth.sessions cleanup loop — the SessionResolver interface, OAuth
// client, session middleware, CSRF middleware, and HTTP handlers land
// in mi-1d5i #2 (mi-ruyc). The cleanup lives here so the package
// boundary is established by the first bead in the chain rather than
// invented later.
package bff

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultCleanupInterval is how often Cleaner.Run wakes to prune
// stale auth.sessions rows. Hourly per the design doc: frequent
// enough to keep the table small, infrequent enough that the cost is
// negligible. The cleanup is a table-size control, not a liveness
// gate — the session middleware (mi-ruyc) evaluates per-request
// liveness independently.
const DefaultCleanupInterval = time.Hour

// Cleaner prunes stale rows from auth.sessions on a periodic
// schedule. Two retention windows apply (per docs/design/auth-bff.md
// §sessions/cleanup):
//
//   - Revoked rows linger 30 days for audit, then are deleted.
//   - Absolute-expired rows linger 7 days past their hard cap, then
//     are deleted.
//
// Both windows are evaluated by the SQL itself relative to the `now`
// passed to Cleanup; that explicit parameter keeps the deletion logic
// testable with a fixed clock without dragging a Clock abstraction
// into this minimal package.
type Cleaner struct {
	pool     *pgxpool.Pool
	interval time.Duration
}

// NewCleaner builds a Cleaner backed by pool, waking once per
// DefaultCleanupInterval.
func NewCleaner(pool *pgxpool.Pool) *Cleaner {
	return &Cleaner{pool: pool, interval: DefaultCleanupInterval}
}

// Run blocks until ctx is cancelled, calling Cleanup once per
// interval. The very first pass fires only after the first tick —
// startup is intentionally quiet so a fast deploy/restart loop does
// not generate cleanup storms.
//
// Errors from individual passes are logged and the loop continues; a
// transient pool failure must not silently kill the goroutine.
func (c *Cleaner) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	slog.Info("auth: session cleanup loop started", "interval", c.interval)
	for {
		select {
		case <-ctx.Done():
			slog.Info("auth: session cleanup loop stopped")
			return
		case t := <-ticker.C:
			n, err := c.Cleanup(ctx, t.UTC())
			if err != nil {
				slog.Warn("auth: session cleanup pass failed", "err", err)
				continue
			}
			slog.Info("auth: session cleanup pass complete", "deleted", n)
		}
	}
}

// Cleanup runs a single retention pass against auth.sessions,
// evaluating both retention windows relative to now. Returns the
// number of rows the pass deleted.
//
// Exported (rather than only invoked from Run) so the test suite can
// exercise it with a fixed clock, and so an operator-facing
// one-shot trigger can wire it directly if needed.
func (c *Cleaner) Cleanup(ctx context.Context, now time.Time) (int64, error) {
	// $1 is cast to timestamptz explicitly: when migrate sends the
	// parameter without an attached type OID (simple protocol or
	// some pgx codec paths), Postgres infers `$1 - INTERVAL` as
	// `interval - interval` and the subsequent `timestamptz <
	// interval` comparison errors out.
	const q = `
		DELETE FROM auth.sessions
		 WHERE (revoked_at IS NOT NULL AND revoked_at < $1::timestamptz - INTERVAL '30 days')
		    OR absolute_expires_at < $1::timestamptz - INTERVAL '7 days'`
	tag, err := c.pool.Exec(ctx, q, now)
	if err != nil {
		return 0, fmt.Errorf("auth: session cleanup: %w", err)
	}
	return tag.RowsAffected(), nil
}
