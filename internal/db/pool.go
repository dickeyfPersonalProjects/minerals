// Package db hosts the Postgres connection pool, transaction helper,
// and concrete *Postgres repository implementations of the interfaces
// defined in internal/domain (per CONTRACT.md §11).
package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool sizing defaults per CONTRACT.md §11. URL query-string overrides
// (e.g. ?pool_max_conns=...) win when present.
//
// defaultMaxConns was raised from 10 to 20 after the mi-hkh6 prod
// incident: under the SpecimenCard fan-out (a single list page fires
// N authenticated /photos requests, each holding a pool connection for
// its handler's duration) 10 conns saturated across 2 replicas, and
// because /readyz pings the SAME pool a saturated pool flapped the
// readiness probe to 503 → NotReady. 20 gives headroom while keeping
// 2×replicas (40) under a conservative Postgres max_connections (100).
const (
	defaultMaxConns        = int32(20)
	defaultMinConns        = int32(2)
	defaultMaxConnLifetime = time.Hour
	defaultMaxConnIdleTime = 30 * time.Minute
)

// maxConnsEnvVar is an explicit operator override for the pool's
// MaxConns, honored ahead of the compiled default but below an
// explicit ?pool_max_conns= in the DSN. It exists so the immediate
// mitigation for a saturation incident (raise the ceiling, roll the
// pods) is a single env change with no rebuild — the DSN is often
// templated/sealed in prod, the env var is not.
const maxConnsEnvVar = "DB_MAX_CONNS"

// NewPool parses url, applies the v1 default sizing knobs (when not
// overridden), and connects.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse pool config: %w", err)
	}
	// Precedence: an explicit ?pool_max_conns= in the DSN (pgx parsed a
	// non-default value) wins; else the DB_MAX_CONNS env override; else
	// the compiled default. pgx reports an unset MaxConns as its own
	// default of 4, so we treat 0/4 as "not set in the DSN".
	if cfg.MaxConns == 0 || cfg.MaxConns == 4 /* pgx default */ {
		if env, ok := maxConnsFromEnv(); ok {
			cfg.MaxConns = env
		} else {
			cfg.MaxConns = defaultMaxConns
		}
	}
	if cfg.MinConns == 0 {
		cfg.MinConns = defaultMinConns
	}
	if cfg.MaxConnLifetime == 0 {
		cfg.MaxConnLifetime = defaultMaxConnLifetime
	}
	if cfg.MaxConnIdleTime == 0 {
		cfg.MaxConnIdleTime = defaultMaxConnIdleTime
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}
	return pool, nil
}

// maxConnsFromEnv reads DB_MAX_CONNS as a positive int32. A missing,
// empty, unparseable, or non-positive value reports ok=false so the
// caller falls through to the compiled default — a fat-fingered env
// var must never silently shrink the pool to 0 and wedge the process.
func maxConnsFromEnv() (int32, bool) {
	raw := os.Getenv(maxConnsEnvVar)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n <= 0 {
		return 0, false
	}
	return int32(n), true
}

// RunInTx executes fn inside a single Postgres transaction at the
// default isolation (READ COMMITTED). Per §11 transaction boundaries
// are owned by the service layer; this helper is the sanctioned way
// to draw one.
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}
	defer func() {
		// Rollback after Commit is a no-op in pgx; rollback after a
		// successful path is harmless. After an error path it's the
		// reason the helper exists.
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tx: %w", err)
	}
	return nil
}
