// Package oidc is the JWT verification library backing the
// Keycloak-based auth middleware (mi-aw3a). It validates access
// tokens against a Keycloak JWKS endpoint, checks issuer and
// audience claims, and extracts the realm roles claim used by the
// authz enforcer.
//
// The Verifier is wired into internal/auth's middleware via the
// auth.TokenVerifier interface. Construction does no network I/O —
// discovery and key-set fetching are lazy (first Verify call) — so
// the server boots independently of Keycloak availability.
package oidc
