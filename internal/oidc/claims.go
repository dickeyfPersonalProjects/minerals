package oidc

// Claims is the subset of JWT claims this app extracts from a
// verified Keycloak access token. Per CONTRACT §13 v2:
//
//   - Subject  → auth.User.ID (mapped from JWT `sub`)
//   - Email    → auth.User.Email (mapped from JWT `email`)
//   - Roles    → auth.User.Roles (mapped from `realm_access.roles`)
//
// Other Keycloak claims (resource_access, scope, preferred_username,
// etc.) are intentionally not surfaced here. Add them only when a
// concrete handler requirement appears — speculatively-added fields
// turn into fields callers come to depend on.
type Claims struct {
	Subject string
	Email   string
	Roles   []string
}
