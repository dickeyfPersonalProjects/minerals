package bff

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// Session is the persisted server-side state behind a browser session
// cookie (canonical: docs/design/auth-bff.md §sessions-table). Every
// field except RevokedAt is set at creation; UpdateTokens / Touch /
// Revoke mutate a narrow subset by id.
//
// The struct is the in-memory shape only — the BYTEA columns
// (`id`, `csrf_token`) are exchanged as fixed [32]byte arrays so
// callers cannot accidentally fan out a wrong-length slice.
type Session struct {
	ID                    [32]byte
	UserSub               string
	UserID                uuid.UUID
	AccessToken           string // server-side only; never logged in full
	RefreshToken          string
	IDToken               string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
	CreatedAt             time.Time
	LastUsedAt            time.Time
	AbsoluteExpiresAt     time.Time
	CSRFToken             [32]byte
	IP                    netip.Addr
	UserAgent             string
	RevokedAt             *time.Time
}

// CreateParams carries the inputs to SessionResolver.Create. The
// resolver fills in ID, CSRFToken, CreatedAt and LastUsedAt — those
// derive from the storage layer (random bytes + DB clock) and are
// not the caller's concern.
type CreateParams struct {
	UserSub               string
	UserID                uuid.UUID
	AccessToken           string
	RefreshToken          string
	IDToken               string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
	AbsoluteExpiresAt     time.Time
	IP                    netip.Addr
	UserAgent             string
}

// TokenSet is the OAuth token bundle returned by the OAuth client
// after Exchange or Refresh. SessionResolver.UpdateTokens persists
// it atomically — Keycloak rotates the refresh token on every use,
// so all four fields update together.
type TokenSet struct {
	AccessToken           string
	RefreshToken          string
	IDToken               string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
}

// SessionResolver is the storage boundary for auth.sessions. The
// session middleware (mi-ken4) depends ONLY on this interface — the
// Postgres impl, a future cache decorator, and a future
// auth-microservice RPC client all conform to it without changing
// the middleware contract (see docs/design/auth-bff.md §microservice-extraction).
//
// Liveness semantics: GetByID returns the row whenever it exists,
// even when RevokedAt is non-nil. The caller (middleware) is
// responsible for evaluating revocation, idle timeout, and absolute
// expiry. Keeping that policy out of the resolver lets the same
// storage serve admin / audit code paths that legitimately need to
// read revoked rows.
type SessionResolver interface {
	GetByID(ctx context.Context, id [32]byte) (Session, error)
	Create(ctx context.Context, params CreateParams) (Session, error)
	UpdateTokens(ctx context.Context, id [32]byte, t TokenSet) (Session, error)
	Touch(ctx context.Context, id [32]byte, at time.Time) error
	Revoke(ctx context.Context, id [32]byte) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// ErrSessionNotFound is returned by GetByID when no row matches the
// given id. The middleware treats this as anonymous (clear cookie,
// proceed) rather than 500 — see docs/design/auth-bff.md
// §session-middleware. Implementations MUST return this exact
// sentinel (wrapped via fmt.Errorf("%w", ErrSessionNotFound) is fine)
// so callers can errors.Is against it.
var ErrSessionNotFound = errors.New("bff: session not found")
