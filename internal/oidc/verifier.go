package oidc

import (
	"context"
	"fmt"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

// Config is the static configuration a Verifier needs.
type Config struct {
	// Issuer is the Keycloak realm URL — e.g.
	// https://keycloak.example.com/realms/minerals. The verifier
	// checks that the JWT's `iss` claim matches this exactly.
	Issuer string

	// ClientID is the expected audience. The verifier checks that
	// `aud` contains this value. For Keycloak access tokens this is
	// the OIDC client_id (cross-ref terraform/keycloak/).
	ClientID string

	// JWKSURL is the public-keys endpoint. When empty, the verifier
	// falls back to OIDC discovery against Issuer to learn it.
	// Tests that don't run a full discovery document set this
	// directly.
	JWKSURL string

	// SkipClientIDCheck disables the audience check. Use only in
	// development with explicit justification — production code MUST
	// leave this false so a stolen token for a different client
	// cannot reach this service.
	SkipClientIDCheck bool
}

// Verifier validates JWTs and extracts the subset of claims this
// app cares about. It is safe for concurrent use.
//
// Construction is cheap and does no network I/O: NewVerifier only
// validates the config. The first Verify call lazily performs OIDC
// discovery (or builds the remote key set) and memoizes the result.
// This keeps server startup independent of Keycloak availability —
// the process boots even when the realm is briefly unreachable, and
// a transient discovery failure is retried on the next request
// rather than being fatal.
type Verifier struct {
	cfg     Config
	initCtx context.Context //nolint:containedctx // long-lived ctx for lazy keyset/discovery init, not a per-request value

	mu    sync.Mutex
	inner *gooidc.IDTokenVerifier
}

// NewVerifier validates cfg and returns a Verifier. It performs no
// network I/O — discovery and key-set fetching are deferred to the
// first Verify call. The ctx governs that deferred initialization
// (the OIDC discovery request and the remote key set's HTTP client),
// so callers should pass a context that lives as long as the server.
func NewVerifier(ctx context.Context, cfg Config) (*Verifier, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("oidc: Issuer is required")
	}
	if cfg.ClientID == "" && !cfg.SkipClientIDCheck {
		return nil, fmt.Errorf("oidc: ClientID is required (or set SkipClientIDCheck for dev)")
	}
	return &Verifier{cfg: cfg, initCtx: ctx}, nil
}

// ensureInner lazily builds the underlying go-oidc verifier on first
// use and memoizes it. A build failure is NOT memoized: the next
// call retries, so a Keycloak blip during cold-start self-heals once
// the realm is reachable. The mutex is held across the (one-time)
// network call — concurrent first callers serialize briefly, then
// every subsequent call takes the fast lock-and-return path.
func (v *Verifier) ensureInner() (*gooidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inner != nil {
		return v.inner, nil
	}

	gooidcCfg := &gooidc.Config{
		ClientID:          v.cfg.ClientID,
		SkipClientIDCheck: v.cfg.SkipClientIDCheck,
	}

	if v.cfg.JWKSURL != "" {
		ks := gooidc.NewRemoteKeySet(v.initCtx, v.cfg.JWKSURL)
		v.inner = gooidc.NewVerifier(v.cfg.Issuer, ks, gooidcCfg)
		return v.inner, nil
	}

	provider, err := gooidc.NewProvider(v.initCtx, v.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover provider: %w", err)
	}
	v.inner = provider.Verifier(gooidcCfg)
	return v.inner, nil
}

// Verify validates rawToken's signature against the JWKS, then
// checks the iss, aud, and exp claims. On success it returns the
// parsed Claims; on failure it returns a wrapped error suitable for
// logging (the error never contains the raw token).
//
// The first call also triggers lazy OIDC initialization; an
// initialization failure (e.g. Keycloak unreachable) surfaces here
// as a verification error rather than crashing the process.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	if rawToken == "" {
		return nil, fmt.Errorf("oidc: empty token")
	}
	inner, err := v.ensureInner()
	if err != nil {
		return nil, err
	}
	tok, err := inner.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify token: %w", err)
	}

	var raw struct {
		Subject     string `json:"sub"`
		Email       string `json:"email"`
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := tok.Claims(&raw); err != nil {
		return nil, fmt.Errorf("oidc: parse claims: %w", err)
	}

	return &Claims{
		Subject: raw.Subject,
		Email:   raw.Email,
		Roles:   raw.RealmAccess.Roles,
	}, nil
}
