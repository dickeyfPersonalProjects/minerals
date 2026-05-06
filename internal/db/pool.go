// Package db hosts the Postgres connection pool, transaction helper,
// and concrete *Postgres repository implementations of the interfaces
// defined in internal/domain (per CONTRACT.md §11).
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool sizing defaults per CONTRACT.md §11. URL query-string overrides
// (e.g. ?pool_max_conns=...) win when present.
const (
	defaultMaxConns        = int32(10)
	defaultMinConns        = int32(2)
	defaultMaxConnLifetime = time.Hour
	defaultMaxConnIdleTime = 30 * time.Minute
)

// NewPool parses url, applies the v1 default sizing knobs (when not
// overridden), and connects.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse pool config: %w", err)
	}
	if cfg.MaxConns == 0 || cfg.MaxConns == 4 /* pgx default */ {
		cfg.MaxConns = defaultMaxConns
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
