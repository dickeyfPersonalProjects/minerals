// Authorization wiring (mi-aw3b / CONTRACT.md §13 v2). authzGuard is
// the per-resource enforcement seam (layer 2 of the §13 hybrid
// strategy — point lookups and writes); the DB-level list scoping
// (layer 1) lives in internal/db. The two layers are intentionally
// separate and MUST NOT be collapsed.
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/casbin/casbin/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// authz action verbs — the operations CONTRACT.md §13 v2 policies grant.
const (
	actView   = "view"
	actCreate = "create"
	actEdit   = "edit"
	actDelete = "delete"
)

// authzGuard wraps the optional Casbin enforcer that backs CONTRACT.md
// §13 v2 per-resource authorization. A nil enforcer makes every check
// pass — this mirrors the nil-Verifier stub-auth path and is the seam
// unit tests rely on. The production wiring in cmd/minerals always
// builds a real enforcer, so authorization is always live in serve.
type authzGuard struct {
	enforcer *casbin.Enforcer
}

// active reports whether a real enforcer is wired. Handlers that must
// fetch a parent resource purely to build the authz.Resource use this
// to skip that work when authorization is disabled (the unit-test
// path).
func (g authzGuard) active() bool { return g.enforcer != nil }

// check evaluates whether the request's caller may perform act on res
// and returns a §10 403 envelope when denied (nil when permitted, or
// when no enforcer is wired).
//
// The §13 v2 hybrid shortcut is applied first: a `view` on a public or
// unlisted resource is always permitted. Writes — and any access to a
// private resource — go through the Casbin enforcer, which resolves
// `own` against author_id and `shared` against the shares table.
func (g authzGuard) check(ctx context.Context, res *authz.Resource, act string) error {
	if g.enforcer == nil {
		return nil
	}
	if act == actView && (res.Visibility == "public" || res.Visibility == "unlisted") {
		return nil
	}
	allowed, err := authz.Enforce(ctx, g.enforcer, authzUser(auth.FromContext(ctx)), res, act)
	if err != nil {
		return newAPIError(http.StatusInternalServerError, "internal_error",
			"authorization check failed", nil)
	}
	if !allowed {
		return newAPIError(http.StatusForbidden, "forbidden",
			"you do not have permission to perform this action", nil)
	}
	return nil
}

// checkHTTP is the net/http analogue of check for the raw download
// routes (photos, journal files) that stream bytes outside huma. It
// writes a §10 403 envelope and returns false when access is denied.
func (g authzGuard) checkHTTP(
	w http.ResponseWriter, r *http.Request, res *authz.Resource, act string,
) bool {
	err := g.check(r.Context(), res, act)
	if err == nil {
		return true
	}
	var ae *apiError
	if !errors.As(err, &ae) {
		writeError(w, http.StatusInternalServerError, "internal_error",
			"internal server error", nil)
		return false
	}
	writeError(w, ae.Status, ae.Envelope.Code, ae.Envelope.Message, ae.Envelope.Details)
	return false
}

// authzUser projects the request-scoped auth.User onto the
// authorization-side authz.User. A caller with no roles is treated as
// `anonymous` when it has no application identity, otherwise as the
// base `user` role — defensive only; production auth always populates
// Roles from the JWT realm-roles claim.
func authzUser(u auth.User) authz.User {
	roles := u.Roles
	if len(roles) == 0 {
		if u.ID == uuid.Nil {
			roles = []string{"anonymous"}
		} else {
			roles = []string{"user"}
		}
	}
	id := ""
	if u.ID != uuid.Nil {
		id = u.ID.String()
	}
	return authz.User{ID: id, Roles: roles}
}

// specimenResource is the §13 v2 authorization view of a specimen.
func specimenResource(s domain.Specimen) *authz.Resource {
	return &authz.Resource{
		Type:       "specimens",
		ID:         s.ID.String(),
		Visibility: string(s.Visibility),
		AuthorID:   s.AuthorID.String(),
	}
}

// newSpecimenResource is the resource for a not-yet-persisted specimen
// (the create path): author is the caller, visibility is the requested
// value, id is empty.
func newSpecimenResource(authorID uuid.UUID, visibility domain.Visibility) *authz.Resource {
	return &authz.Resource{
		Type:       "specimens",
		Visibility: string(visibility),
		AuthorID:   authorID.String(),
	}
}

// collectorResource is the §13 v2 authorization view of a collector.
// Collectors carry no visibility — the `user` role only ever has
// `collectors:*:own`.
func collectorResource(c domain.Collector) *authz.Resource {
	return &authz.Resource{
		Type:     "collectors",
		ID:       c.ID.String(),
		AuthorID: c.AuthorID.String(),
	}
}

// ownedResource builds a resource of the given type owned by authorID,
// for collection-level checks (create) and owner-keyed resources
// (qr-sheets — one per user).
func ownedResource(resourceType string, authorID uuid.UUID) *authz.Resource {
	return &authz.Resource{Type: resourceType, AuthorID: authorID.String()}
}

// journalResource is the §13 v2 authorization view of a journal entry.
// Journal entries carry no visibility — the `user` role only ever has
// `journal:*:own`.
func journalResource(e domain.JournalEntry) *authz.Resource {
	return &authz.Resource{
		Type:     "journal",
		ID:       e.ID.String(),
		AuthorID: e.AuthorID.String(),
	}
}

// photoResource is the §13 v2 authorization view of a photo. Photos
// carry no owner or visibility of their own — both are inherited from
// the parent specimen.
func photoResource(photoID uuid.UUID, parent domain.Specimen) *authz.Resource {
	return &authz.Resource{
		Type:       "photos",
		ID:         photoID.String(),
		Visibility: string(parent.Visibility),
		AuthorID:   parent.AuthorID.String(),
	}
}
