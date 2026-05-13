// Package oidc is a standalone JWT verification library for the
// future Keycloak-based real-auth middleware (mi-aw3). It validates
// access tokens against a Keycloak JWKS endpoint, checks issuer and
// audience claims, and extracts the realm roles claim used by the
// authz enforcer.
//
// This package is not wired into the v1 stub auth.Auth middleware.
// It exists so the verifier can be developed, tested, and reviewed
// in isolation from the middleware swap.
package oidc
