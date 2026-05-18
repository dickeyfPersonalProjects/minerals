package bff

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresResolver implements SessionResolver against the
// auth.sessions table created by migration 0015 (mi-twql). It is the
// only persistence path that actually touches the database — any
// caching or microservice-extraction work wraps it as a decorator
// (see docs/design/auth-bff.md §session-resolver-interface).
type PostgresResolver struct {
	pool *pgxpool.Pool
}

// NewPostgresResolver builds a PostgresResolver backed by pool. The
// pool's lifetime is managed by the caller (server bootstrap) so the
// resolver can be wrapped or composed without owning shutdown.
func NewPostgresResolver(pool *pgxpool.Pool) *PostgresResolver {
	return &PostgresResolver{pool: pool}
}

// selectColumns is the column list used by every SELECT/RETURNING in
// this file. Centralising it guarantees scanSession's positional
// scan stays in sync — drift here is a runtime decoder panic, so the
// single source of truth is worth the small indirection.
const selectColumns = `id, user_sub, user_id,
		access_token, refresh_token, id_token,
		access_token_expires_at, refresh_token_expires_at,
		created_at, last_used_at, absolute_expires_at,
		csrf_token, ip, user_agent, revoked_at`

// GetByID returns the row for id, including rows where revoked_at is
// non-nil. Liveness is the middleware's responsibility, per the
// SessionResolver contract — keeping that policy out of the resolver
// lets audit code legitimately read revoked rows.
func (r *PostgresResolver) GetByID(ctx context.Context, id [32]byte) (Session, error) {
	const q = `SELECT ` + selectColumns + ` FROM auth.sessions WHERE id = $1`
	row := r.pool.QueryRow(ctx, q, id[:])
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, fmt.Errorf("bff: GetByID: %w", err)
	}
	return sess, nil
}

// Create generates the session ID and CSRF token from crypto/rand and
// inserts the row. The DB clock owns created_at and last_used_at
// (DEFAULT now() on the column), so a session timestamp is never
// drifted from the storage layer's view of wall-clock time. We
// RETURNING * the inserted row to get those DB-assigned values
// without a second SELECT.
func (r *PostgresResolver) Create(ctx context.Context, p CreateParams) (Session, error) {
	id, err := randomBytes32()
	if err != nil {
		return Session{}, fmt.Errorf("bff: Create: rand session id: %w", err)
	}
	csrf, err := randomBytes32()
	if err != nil {
		return Session{}, fmt.Errorf("bff: Create: rand csrf token: %w", err)
	}

	const q = `
		INSERT INTO auth.sessions (
			id, user_sub, user_id,
			access_token, refresh_token, id_token,
			access_token_expires_at, refresh_token_expires_at,
			absolute_expires_at, csrf_token, ip, user_agent
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING ` + selectColumns

	row := r.pool.QueryRow(ctx, q,
		id[:], p.UserSub, p.UserID,
		p.AccessToken, p.RefreshToken, p.IDToken,
		p.AccessTokenExpiresAt, p.RefreshTokenExpiresAt,
		p.AbsoluteExpiresAt, csrf[:], ipForDB(p.IP), nullableString(p.UserAgent),
	)
	sess, err := scanSession(row)
	if err != nil {
		return Session{}, fmt.Errorf("bff: Create: %w", err)
	}
	return sess, nil
}

// UpdateTokens replaces all four token fields atomically with the
// rotated set returned by Keycloak. UPDATE ... RETURNING * gives the
// caller the post-update row without a second SELECT — the
// middleware needs the new AccessTokenExpiresAt to decide whether
// the very next request still needs a refresh.
func (r *PostgresResolver) UpdateTokens(ctx context.Context, id [32]byte, t TokenSet) (Session, error) {
	const q = `
		UPDATE auth.sessions
		   SET access_token             = $2,
		       refresh_token            = $3,
		       id_token                 = $4,
		       access_token_expires_at  = $5,
		       refresh_token_expires_at = $6
		 WHERE id = $1
		RETURNING ` + selectColumns
	row := r.pool.QueryRow(ctx, q,
		id[:], t.AccessToken, t.RefreshToken, t.IDToken,
		t.AccessTokenExpiresAt, t.RefreshTokenExpiresAt,
	)
	sess, err := scanSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, fmt.Errorf("bff: UpdateTokens: %w", err)
	}
	return sess, nil
}

// Touch advances last_used_at without re-fetching the row. The
// middleware debounces calls (30s, per the design doc) so this is a
// thin UPDATE — no RETURNING, no roundtrip cost for the row image
// the caller already holds.
func (r *PostgresResolver) Touch(ctx context.Context, id [32]byte, at time.Time) error {
	const q = `UPDATE auth.sessions SET last_used_at = $2 WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id[:], at)
	if err != nil {
		return fmt.Errorf("bff: Touch: %w", err)
	}
	return nil
}

// Revoke soft-deletes the row by setting revoked_at = now(). The
// cleanup loop (Cleaner.Cleanup) physically removes rows 30 days
// after revocation; the middleware filters revoked rows from
// liveness checks immediately.
//
// Setting revoked_at to the DB clock (rather than a passed-in time)
// ensures the value is monotonic with cleanup's own `now` — a
// caller-supplied past time would make the cleanup window think the
// row is older than it is.
func (r *PostgresResolver) Revoke(ctx context.Context, id [32]byte) error {
	const q = `UPDATE auth.sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	_, err := r.pool.Exec(ctx, q, id[:])
	if err != nil {
		return fmt.Errorf("bff: Revoke: %w", err)
	}
	return nil
}

// RevokeAllForUser is the admin force-logout path: revoke every
// alive session for a user in one statement. Filtering
// `revoked_at IS NULL` is important — otherwise re-revoking would
// reset the cleanup-window anchor and keep rows around longer than
// the 30-day audit retention intends.
func (r *PostgresResolver) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	const q = `UPDATE auth.sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := r.pool.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("bff: RevokeAllForUser: %w", err)
	}
	return nil
}

// rowScanner is the narrow interface common to *pgx.Row and
// pgx.Rows.Scan — lets scanSession serve both QueryRow and a future
// list-scan caller without duplication.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSession decodes one row in the canonical `selectColumns`
// order. BYTEA columns scan into []byte then copy to fixed arrays;
// the migration enforces the 32-byte length, so a wrong length here
// would be DB corruption and is fatal for the request.
func scanSession(row rowScanner) (Session, error) {
	var (
		s         Session
		idBytes   []byte
		csrfBytes []byte
		ipNetIP   *netip.Addr
		userAgent *string
	)
	err := row.Scan(
		&idBytes, &s.UserSub, &s.UserID,
		&s.AccessToken, &s.RefreshToken, &s.IDToken,
		&s.AccessTokenExpiresAt, &s.RefreshTokenExpiresAt,
		&s.CreatedAt, &s.LastUsedAt, &s.AbsoluteExpiresAt,
		&csrfBytes, &ipNetIP, &userAgent, &s.RevokedAt,
	)
	if err != nil {
		return Session{}, err
	}
	if len(idBytes) != 32 {
		return Session{}, fmt.Errorf("bff: session id has length %d, want 32", len(idBytes))
	}
	if len(csrfBytes) != 32 {
		return Session{}, fmt.Errorf("bff: csrf token has length %d, want 32", len(csrfBytes))
	}
	copy(s.ID[:], idBytes)
	copy(s.CSRFToken[:], csrfBytes)
	if ipNetIP != nil {
		s.IP = *ipNetIP
	}
	if userAgent != nil {
		s.UserAgent = *userAgent
	}
	return s, nil
}

// randomBytes32 produces 32 cryptographically random bytes for use
// as a session id or CSRF token. Failure is exceptional —
// crypto/rand.Read only fails on hard-down /dev/urandom — but we
// surface it rather than panic so callers can return a graceful 500.
func randomBytes32() ([32]byte, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return [32]byte{}, err
	}
	return b, nil
}

// ipForDB converts the netip.Addr that handlers carry around into
// the form pgx serialises into Postgres INET. A zero Addr marshals
// to NULL so the column reflects "unknown" rather than 0.0.0.0.
func ipForDB(a netip.Addr) any {
	if !a.IsValid() {
		return nil
	}
	return &a
}

// nullableString returns nil for the empty string so the column
// stores NULL — distinguishes "no user agent header" from "empty
// string body".
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
