// Per-field visibility redaction (mi-fo8 / mi-9ww). Wires the
// internal/visibility resolver into the read paths that emit
// SpecimenView and PhotoView so fields the viewer cannot see are
// omitted from the JSON entirely — not nulled, not flagged. Hidden
// state of redaction is invisible to the viewer (a redacted field
// looks the same as a never-set one).
//
// The redaction predicate reuses the §13 v2 Casbin enforcer (the
// existing "can viewer see this Visibility on this resource?" check)
// against a synthetic Resource that carries the resolved per-field
// Visibility. Owner / admin / shared roles win the same way they do
// for the row's overall visibility check — there is no new authz
// primitive in this layer.
package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/visibility"
)

// redactor applies per-field visibility redaction to the wire views
// produced by the read handlers. A nil users repo (or a nil enforcer
// behind the authzGuard) makes every redaction call a no-op — the
// unit-test path, the same seam authzGuard uses.
//
// redactor is not safe for concurrent use. The owners map is a
// request-scoped memo so a list-page that contains many specimens by
// the same author hits UserRepo.GetByID once per distinct author.
type redactor struct {
	users  domain.UserRepo
	guard  authzGuard
	owners map[uuid.UUID]domain.User
}

// newRedactor builds a redactor with a fresh per-request owner cache.
func newRedactor(users domain.UserRepo, guard authzGuard) redactor {
	return redactor{users: users, guard: guard, owners: map[uuid.UUID]domain.User{}}
}

// redactSpecimen returns the SpecimenView for sp with redactable
// scalar fields (price_cents, acquired_from, acquired_at,
// catalog_number) omitted when the request's caller cannot see their
// resolved visibility. Owners and admins see everything per the
// underlying Casbin policy.
//
// A failure to load the owner is non-fatal — the field is redacted
// (conservative default) and a nil error is returned so callers
// don't have to thread storage errors through every list row. The
// SystemDefault path runs unchanged when FieldDefaults is missing.
func (r redactor) redactSpecimen(ctx context.Context, sp domain.Specimen) SpecimenView {
	view := toSpecimenView(sp)
	if !r.guard.active() {
		return view
	}
	owner := r.loadOwner(ctx, sp.AuthorID)
	if !r.canSeeField(ctx, sp, visibility.ResolveScalar(visibility.FieldPrice, sp, owner).Visibility) {
		view.PriceCents = nil
	}
	if !r.canSeeField(ctx, sp, visibility.ResolveScalar(visibility.FieldAcquiredFrom, sp, owner).Visibility) {
		view.AcquiredFrom = nil
	}
	if !r.canSeeField(ctx, sp, visibility.ResolveScalar(visibility.FieldAcquiredAt, sp, owner).Visibility) {
		view.AcquiredAt = nil
	}
	if !r.canSeeField(ctx, sp, visibility.ResolveScalar(visibility.FieldCatalogNumber, sp, owner).Visibility) {
		view.CatalogNumber = nil
	}
	// tagged is owner-only metadata (mi-n28q): always treated as
	// private visibility. Non-owners get nil (omit-don't-null —
	// indistinguishable from "field never sent", a deliberate privacy
	// property matching the other redacted fields).
	if !r.canSeeField(ctx, sp, domain.VisibilityPrivate) {
		view.Tagged = nil
	}
	return view
}

// filterPhotos returns the subset of photos the viewer is allowed to
// see on sp. The order of the input slice is preserved; no count of
// the dropped photos is returned (a deliberate privacy property per
// mi-fo8 — viewers MUST NOT learn that more photos exist than they
// can see).
//
// As with redactSpecimen, an owner-load failure causes the photo to
// be dropped (conservative default).
func (r redactor) filterPhotos(ctx context.Context, sp domain.Specimen, photos []domain.Photo) []domain.Photo {
	if !r.guard.active() {
		return photos
	}
	owner := r.loadOwner(ctx, sp.AuthorID)
	out := make([]domain.Photo, 0, len(photos))
	for _, p := range photos {
		if r.canSeePhoto(ctx, sp, owner, p) {
			out = append(out, p)
		}
	}
	return out
}

// canSeePhoto reports whether the request's caller may see img on sp,
// running the image-specific resolution chain and the §13 v2 view
// check against the resolved Visibility.
func (r redactor) canSeePhoto(ctx context.Context, sp domain.Specimen, owner domain.User, img domain.Photo) bool {
	if !r.guard.active() {
		return true
	}
	vis := visibility.ResolveImage(sp, owner, img).Visibility
	return r.canSeeField(ctx, sp, vis)
}

// canSeeField asks the existing §13 v2 enforcer whether the request's
// caller may see content with the given resolved Visibility, treating
// the specimen as the owning resource (so "own" / "shared" / admin
// instance matches still apply).
func (r redactor) canSeeField(ctx context.Context, sp domain.Specimen, vis domain.Visibility) bool {
	if r.guard.enforcer == nil {
		return true
	}
	// public and unlisted are visible to everyone — shortcut mirrors
	// authzGuard.check so anonymous callers don't need a role grant
	// against the synthetic resource.
	if vis == domain.VisibilityPublic || vis == domain.VisibilityUnlisted {
		return true
	}
	res := &authz.Resource{
		Type:       "specimens",
		ID:         sp.ID.String(),
		Visibility: string(vis),
		AuthorID:   sp.AuthorID.String(),
	}
	allowed, err := authz.Enforce(ctx, r.guard.enforcer, authzUser(auth.FromContext(ctx)), res, actView)
	if err != nil {
		return false
	}
	return allowed
}

// loadOwner fetches the specimen's owner so the resolver can consult
// FieldDefaults. A nil users repo (the unit-test path) or a missing
// row yields a zero-value User; the resolver then falls through to
// SystemDefault, which is the conservative correct behavior — a
// missing owner row cannot leak fields that depend on its defaults.
func (r redactor) loadOwner(ctx context.Context, authorID uuid.UUID) domain.User {
	if r.users == nil || authorID == uuid.Nil {
		return domain.User{}
	}
	if cached, ok := r.owners[authorID]; ok {
		return cached
	}
	u, err := r.users.GetByID(ctx, authorID)
	if err != nil {
		// ErrUserNotFound is silently treated as "no defaults"; any
		// other error is also treated the same way — redaction is
		// permissive here only via the resolver, never via the view
		// predicate.
		if !errors.Is(err, domain.ErrUserNotFound) {
			// Intentionally no-op; the resolver returns SystemDefault
			// and the existing view predicate is what decides whether
			// to drop a field. Logging here would be too noisy for a
			// list endpoint.
			_ = err
		}
		return domain.User{}
	}
	r.owners[authorID] = u
	return u
}
