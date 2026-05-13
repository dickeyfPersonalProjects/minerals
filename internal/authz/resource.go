package authz

// Resource is the authorization-side view of a domain object. It
// carries exactly the fields the enforcer's matcher functions
// inspect — nothing else from the domain type leaks in.
//
// For non-resource action targets (e.g. devops endpoints) the
// Visibility and AuthorID fields are unused; pass a Resource with
// just Type set.
type Resource struct {
	// Type is the resource family. Must be one of the families listed
	// in CONTRACT §13 v2: specimens, photos, journal, collectors,
	// qr-sheets, devops, users.
	Type string

	// ID is the resource's UUID as a string. Empty for collection-
	// level checks (e.g. "can this user create a specimen?").
	ID string

	// Visibility is "public", "unlisted", or "private". Empty for
	// resource types that don't carry visibility (e.g. devops, users).
	Visibility string

	// AuthorID is the owner's user UUID as a string. Empty when the
	// resource has no owner (devops, users) or is unsaved.
	AuthorID string
}

// User is the authorization-side view of the caller. Roles is
// authoritative; the enforcer evaluates each role independently and
// permits if any role grants the action.
//
// Distinct from auth.User: that type is the v1 stub identity carried
// in the request context. Wiring (mi-aw3) will populate the
// authz.User from the validated JWT.
type User struct {
	// ID is the caller's UUID as a string. Used by the "own"
	// instance qualifier.
	ID string

	// Roles is the caller's set of role names. Per CONTRACT §13 v2
	// every authenticated user has at least "user"; unauthenticated
	// callers have "anonymous".
	Roles []string
}
