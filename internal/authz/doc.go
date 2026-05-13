// Package authz is the v2 RBAC scaffold: a Casbin-based enforcer
// keyed off the role-and-instance permission model described in
// CONTRACT.md §13 v2.
//
// This package is intentionally not wired into the v1 stub auth
// middleware. The active auth.Auth implementation continues to
// populate the stub overseer user (per CONTRACT §13 v1). The Casbin
// enforcer here exists so handler code, list-query filters, and the
// shares-table lookup can be developed in parallel with the
// mi-aw3 work that flips the middleware over to Keycloak.
//
// Callers that wire this in MUST construct an enforcer with a
// Postgres adapter (policies live in the DB alongside application
// data) and a SharesLookup backed by the shares table. The
// shares table itself is added by mi-1mv.
package authz
