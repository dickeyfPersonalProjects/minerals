package oidc

import (
	"context"
	"fmt"

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

	// JWKSURL is the public-keys endpoint. When empty, NewVerifier
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
type Verifier struct {
	inner *gooidc.IDTokenVerifier
}

// NewVerifier constructs a Verifier. When cfg.JWKSURL is set, the
// verifier uses that JWKS endpoint directly; otherwise it performs
// OIDC discovery against cfg.Issuer to learn the JWKS URL. Both
// paths fetch keys lazily on first Verify call.
func NewVerifier(ctx context.Context, cfg Config) (*Verifier, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("oidc: Issuer is required")
	}
	if cfg.ClientID == "" && !cfg.SkipClientIDCheck {
		return nil, fmt.Errorf("oidc: ClientID is required (or set SkipClientIDCheck for dev)")
	}

	gooidcCfg := &gooidc.Config{
		ClientID:          cfg.ClientID,
		SkipClientIDCheck: cfg.SkipClientIDCheck,
	}

	var verifier *gooidc.IDTokenVerifier
	if cfg.JWKSURL != "" {
		ks := gooidc.NewRemoteKeySet(ctx, cfg.JWKSURL)
		verifier = gooidc.NewVerifier(cfg.Issuer, ks, gooidcCfg)
	} else {
		provider, err := gooidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("oidc: discover provider: %w", err)
		}
		verifier = provider.Verifier(gooidcCfg)
	}

	return &Verifier{inner: verifier}, nil
}

// Verify validates rawToken's signature against the JWKS, then
// checks the iss, aud, and exp claims. On success it returns the
// parsed Claims; on failure it returns a wrapped error suitable for
// logging (the error never contains the raw token).
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Claims, error) {
	if rawToken == "" {
		return nil, fmt.Errorf("oidc: empty token")
	}
	tok, err := v.inner.Verify(ctx, rawToken)
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
