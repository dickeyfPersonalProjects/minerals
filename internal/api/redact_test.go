package api

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// Redaction matrix tests (mi-fo8 / mi-9ww).
//
// These are the unit-level guards for the per-field visibility
// redaction wired into the specimens / photos read paths. They drive
// the redactor directly with a real Casbin enforcer (the same
// DefaultPolicies the production enforcer loads) so the table is
// exhaustive without an HTTP round-trip or a DB.
//
// The table cross-products: viewer × field × resolved-visibility ×
// override-layer. Per the bead, "scalar redacted → key absent from
// JSON; image redacted → image absent from the array; the hidden
// state of redaction is invisible to the viewer." The asserts here
// match those invariants exactly: a redacted scalar comes back as a
// nil pointer (which JSON-marshals to an omitted key thanks to
// `omitempty` on SpecimenView), and a redacted photo is missing from
// filterPhotos' output entirely.

// redactTestRig builds the {redactor, owner, three viewers} fixture
// that every matrix row uses. A real Casbin enforcer is seeded with
// the §13 v2 default policies so the view predicate behaves exactly
// like production. A nil shares lookup is fine — the matrix doesn't
// exercise the `shared` instance qualifier (covered by mi-37w's own
// chain tests and the enforcer suite).
type redactTestRig struct {
	red      redactor
	owner    domain.User
	ownerCtx context.Context
	otherCtx context.Context
	anonCtx  context.Context
}

func newRedactTestRig(t *testing.T) redactTestRig {
	t.Helper()
	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("authz.NewEnforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	owner := domain.User{ID: domain.NewID(), KeycloakSub: "owner-sub"}
	other := auth.User{ID: domain.NewID(), Roles: []string{"user"}}
	users := &redactFakeUserRepo{byID: map[uuid.UUID]domain.User{owner.ID: owner}}
	return redactTestRig{
		red:      newRedactor(users, authzGuard{enforcer: enf}),
		owner:    owner,
		ownerCtx: auth.WithUser(context.Background(), auth.User{ID: owner.ID, Roles: []string{"user"}}),
		otherCtx: auth.WithUser(context.Background(), other),
		anonCtx:  context.Background(),
	}
}

// redactFakeUserRepo is a minimal domain.UserRepo for the matrix —
// only GetByID is exercised by the redactor. The other methods are
// here to satisfy the interface.
type redactFakeUserRepo struct {
	byID map[uuid.UUID]domain.User
}

func (r *redactFakeUserRepo) GetByID(_ context.Context, id uuid.UUID) (domain.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return u, nil
}
func (r *redactFakeUserRepo) GetBySub(context.Context, string) (domain.User, error) {
	return domain.User{}, domain.ErrUserNotFound
}
func (r *redactFakeUserRepo) Create(context.Context, domain.Tx, domain.User) error {
	return nil
}
func (r *redactFakeUserRepo) MarkActive(context.Context, domain.Tx, uuid.UUID, string, time.Time) error {
	return nil
}
func (r *redactFakeUserRepo) UpdateDisplayName(_ context.Context, _ domain.Tx, id uuid.UUID, name string, _ time.Time) error {
	u := r.byID[id]
	u.DisplayName = &name
	r.byID[id] = u
	return nil
}
func (r *redactFakeUserRepo) UpdateFieldDefaults(_ context.Context, _ domain.Tx, id uuid.UUID, defaults *domain.FieldDefaults, _ time.Time) error {
	u := r.byID[id]
	u.FieldDefaults = defaults
	r.byID[id] = u
	return nil
}
func (r *redactFakeUserRepo) UpdateDefaultSpecimenVisibility(_ context.Context, _ domain.Tx, id uuid.UUID, visibility *domain.Visibility, _ time.Time) error {
	u := r.byID[id]
	u.DefaultSpecimenVisibility = visibility
	r.byID[id] = u
	return nil
}

func (r *redactFakeUserRepo) SetStatus(_ context.Context, _ domain.Tx, id uuid.UUID, status domain.UserStatus, _ time.Time) error {
	u := r.byID[id]
	u.Status = status
	r.byID[id] = u
	return nil
}

// visPtr is a one-shot Visibility pointer helper for the table; the
// matrix needs the explicit pointer-vs-nil distinction to drive the
// override-layer dimension.
func visPtr(v domain.Visibility) *domain.Visibility { return &v }

// TestRedactor_Scalars walks the cross-product of viewer × field ×
// override-layer × resolved-visibility for the price / acquired_from
// scalars. Each row asserts the redacted-or-not outcome on the
// SpecimenView the redactor returns.
func TestRedactor_Scalars(t *testing.T) {
	type viewer struct {
		name string
		who  string // "owner" | "other" | "anonymous"
	}
	viewers := []viewer{
		{"owner", "owner"},
		{"other", "other"},
		{"anonymous", "anonymous"},
	}
	type field struct {
		name string
		// configure the specimen + owner to drive the resolution
		// chain to the given visibility via the given layer. The
		// caller wires up sp.VisibilityPrice / sp.VisibilityAcquiredFrom
		// or owner.FieldDefaults; the visibility argument is the
		// terminal value the chain should produce.
		configure func(sp *domain.Specimen, owner *domain.User, vis domain.Visibility, layer string)
		// peek extracts the field-after-redaction so the assertion is
		// generic. Returns "present:<value>" or "absent".
		peek func(SpecimenView) string
		// set populates a non-default value on the specimen so the
		// "present" case is distinguishable from the "absent" case.
		set func(sp *domain.Specimen)
	}
	cents := int64(12345)
	from := "test-source"
	acquiredAt := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	catalog := "AB-1234"
	scalarFields := []field{
		{
			name: "price",
			configure: func(sp *domain.Specimen, owner *domain.User, vis domain.Visibility, layer string) {
				switch layer {
				case "specimen-field":
					sp.VisibilityPrice = visPtr(vis)
				case "user-default":
					if owner.FieldDefaults == nil {
						owner.FieldDefaults = &domain.FieldDefaults{}
					}
					owner.FieldDefaults.Price = visPtr(vis)
				case "system-default":
					// no-op: SystemDefault is the fallthrough value.
				}
			},
			peek: func(v SpecimenView) string {
				if v.PriceCents == nil {
					return "absent"
				}
				return "present"
			},
			set: func(sp *domain.Specimen) { sp.PriceCents = &cents },
		},
		{
			name: "acquired_from",
			configure: func(sp *domain.Specimen, owner *domain.User, vis domain.Visibility, layer string) {
				switch layer {
				case "specimen-field":
					sp.VisibilityAcquiredFrom = visPtr(vis)
				case "user-default":
					if owner.FieldDefaults == nil {
						owner.FieldDefaults = &domain.FieldDefaults{}
					}
					owner.FieldDefaults.AcquiredFrom = visPtr(vis)
				case "system-default":
					// no-op.
				}
			},
			peek: func(v SpecimenView) string {
				if v.AcquiredFrom == nil {
					return "absent"
				}
				return "present"
			},
			set: func(sp *domain.Specimen) { sp.AcquiredFrom = &from },
		},
		{
			// acquired_at has no per-specimen override column —
			// only the user-default and system-default layers exist
			// for it. The specimen-field rows for this field are
			// skipped below.
			name: "acquired_at",
			configure: func(_ *domain.Specimen, owner *domain.User, vis domain.Visibility, layer string) {
				switch layer {
				case "user-default":
					if owner.FieldDefaults == nil {
						owner.FieldDefaults = &domain.FieldDefaults{}
					}
					owner.FieldDefaults.AcquiredAt = visPtr(vis)
				case "system-default":
					// no-op.
				}
			},
			peek: func(v SpecimenView) string {
				if v.AcquiredAt == nil {
					return "absent"
				}
				return "present"
			},
			set: func(sp *domain.Specimen) { sp.AcquiredAt = &acquiredAt },
		},
		{
			// catalog_number has no per-specimen override column,
			// same shape as acquired_at.
			name: "catalog_number",
			configure: func(_ *domain.Specimen, owner *domain.User, vis domain.Visibility, layer string) {
				switch layer {
				case "user-default":
					if owner.FieldDefaults == nil {
						owner.FieldDefaults = &domain.FieldDefaults{}
					}
					owner.FieldDefaults.CatalogNumber = visPtr(vis)
				case "system-default":
					// no-op.
				}
			},
			peek: func(v SpecimenView) string {
				if v.CatalogNumber == nil {
					return "absent"
				}
				return "present"
			},
			set: func(sp *domain.Specimen) { sp.CatalogNumber = &catalog },
		},
	}

	// fieldHasSpecimenOverride reports whether the named field has a
	// per-specimen override column (visibility_price /
	// visibility_acquired_from). Used to skip "specimen-field" chain
	// rows for acquired_at and catalog_number, which only exist on
	// the user-default and system-default layers.
	fieldHasSpecimenOverride := func(name string) bool {
		return name == "price" || name == "acquired_from"
	}

	// Each (resolved-visibility, layer) pair describes one chain
	// outcome. system-default is hard-coded as SystemDefault
	// (private) per mi-37w; we don't iterate visibilities under it.
	type chainCase struct {
		layer string
		vis   domain.Visibility
	}
	chains := []chainCase{
		{"specimen-field", domain.VisibilityPublic},
		{"specimen-field", domain.VisibilityUnlisted},
		{"specimen-field", domain.VisibilityPrivate},
		{"user-default", domain.VisibilityPublic},
		{"user-default", domain.VisibilityUnlisted},
		{"user-default", domain.VisibilityPrivate},
		{"system-default", domain.VisibilityPrivate},
	}

	rig := newRedactTestRig(t)
	for _, v := range viewers {
		for _, f := range scalarFields {
			for _, c := range chains {
				if c.layer == "specimen-field" && !fieldHasSpecimenOverride(f.name) {
					continue
				}
				name := strings.Join([]string{f.name, v.name, c.layer, string(c.vis)}, "/")
				t.Run(name, func(t *testing.T) {
					// Fresh fixture per row — the redactor caches owners
					// per-instance, and the configure step mutates
					// owner.FieldDefaults.
					owner := rig.owner
					sp := domain.Specimen{
						ID:       domain.NewID(),
						Type:     domain.SpecimenMineral,
						Name:     "matrix",
						AuthorID: rig.owner.ID,
						// Overall visibility is public so the row-level
						// view shortcut admits every viewer; the redactor
						// is then the only thing deciding the field.
						Visibility: domain.VisibilityPublic,
					}
					f.set(&sp)
					f.configure(&sp, &owner, c.vis, c.layer)

					users := &redactFakeUserRepo{byID: map[uuid.UUID]domain.User{owner.ID: owner}}
					red := newRedactor(users, rig.red.guard)
					var ctx context.Context
					switch v.who {
					case "owner":
						ctx = rig.ownerCtx
					case "other":
						ctx = rig.otherCtx
					default:
						ctx = rig.anonCtx
					}
					view := red.redactSpecimen(ctx, sp)

					// Expected: owner always sees the field (the `own`
					// instance grants view on every visibility). For
					// non-owners, the resolved Visibility decides:
					// public / unlisted are visible to everyone, private
					// is not (anonymous and other-user are denied).
					wantPresent := false
					switch v.who {
					case "owner":
						wantPresent = true
					default:
						wantPresent = c.vis == domain.VisibilityPublic || c.vis == domain.VisibilityUnlisted
					}
					got := f.peek(view)
					if wantPresent && got != "present" {
						t.Errorf("%s: viewer=%s layer=%s vis=%s got %s, want present",
							f.name, v.name, c.layer, c.vis, got)
					}
					if !wantPresent && got != "absent" {
						t.Errorf("%s: viewer=%s layer=%s vis=%s got %s, want absent",
							f.name, v.name, c.layer, c.vis, got)
					}
				})
			}
		}
	}
}

// TestRedactor_Scalars_NoOpWithoutEnforcer pins the unit-test seam:
// a redactor with a nil enforcer (the seam authzGuard.active() ==
// false) returns the view unchanged regardless of viewer. The
// production wiring always provides an enforcer; this test exists so
// the seam stays a seam.
func TestRedactor_Scalars_NoOpWithoutEnforcer(t *testing.T) {
	owner := domain.User{ID: domain.NewID()}
	cents := int64(99)
	from := "anywhere"
	sp := domain.Specimen{
		ID:                     domain.NewID(),
		AuthorID:               owner.ID,
		Visibility:             domain.VisibilityPublic,
		PriceCents:             &cents,
		AcquiredFrom:           &from,
		VisibilityPrice:        visPtr(domain.VisibilityPrivate),
		VisibilityAcquiredFrom: visPtr(domain.VisibilityPrivate),
	}
	users := &redactFakeUserRepo{byID: map[uuid.UUID]domain.User{owner.ID: owner}}
	red := newRedactor(users, authzGuard{})
	v := red.redactSpecimen(context.Background(), sp)
	if v.PriceCents == nil || *v.PriceCents != cents {
		t.Errorf("nil enforcer: PriceCents=%v, want %d", v.PriceCents, cents)
	}
	if v.AcquiredFrom == nil || *v.AcquiredFrom != from {
		t.Errorf("nil enforcer: AcquiredFrom=%v, want %q", v.AcquiredFrom, from)
	}
}

// TestRedactor_FilterPhotos covers the image chain: per-photo
// visibility override, then specimen.VisibilityImages, then the
// specimen's overall Visibility, then owner default, then system
// default. The matrix walks each layer × each viewer; the assertion
// is presence-or-absence of the photo from filterPhotos' output.
func TestRedactor_FilterPhotos(t *testing.T) {
	rig := newRedactTestRig(t)

	type layerCase struct {
		name      string
		configure func(sp *domain.Specimen, owner *domain.User, photo *domain.Photo, vis domain.Visibility)
	}
	layers := []layerCase{
		{
			"image-override",
			func(_ *domain.Specimen, _ *domain.User, photo *domain.Photo, vis domain.Visibility) {
				photo.Visibility = visPtr(vis)
			},
		},
		{
			"specimen-field",
			func(sp *domain.Specimen, _ *domain.User, _ *domain.Photo, vis domain.Visibility) {
				sp.VisibilityImages = visPtr(vis)
			},
		},
		{
			"specimen-overall",
			func(sp *domain.Specimen, _ *domain.User, _ *domain.Photo, vis domain.Visibility) {
				sp.Visibility = vis
			},
		},
		{
			"user-default",
			func(_ *domain.Specimen, owner *domain.User, _ *domain.Photo, vis domain.Visibility) {
				if owner.FieldDefaults == nil {
					owner.FieldDefaults = &domain.FieldDefaults{}
				}
				owner.FieldDefaults.Images = visPtr(vis)
			},
		},
	}
	visValues := []domain.Visibility{
		domain.VisibilityPublic,
		domain.VisibilityUnlisted,
		domain.VisibilityPrivate,
	}
	viewers := []struct {
		name string
		ctx  context.Context
	}{
		{"owner", rig.ownerCtx},
		{"other", rig.otherCtx},
		{"anonymous", rig.anonCtx},
	}

	for _, layer := range layers {
		for _, vis := range visValues {
			for _, v := range viewers {
				name := strings.Join([]string{"image", v.name, layer.name, string(vis)}, "/")
				t.Run(name, func(t *testing.T) {
					owner := rig.owner
					// Overall starts unset so each layer's value is the
					// terminal one in the chain — anything earlier in
					// the chain is nil unless this row is testing it.
					sp := domain.Specimen{
						ID:       domain.NewID(),
						AuthorID: rig.owner.ID,
					}
					photo := domain.Photo{ID: domain.NewID(), SpecimenID: sp.ID}
					layer.configure(&sp, &owner, &photo, vis)

					users := &redactFakeUserRepo{byID: map[uuid.UUID]domain.User{owner.ID: owner}}
					red := newRedactor(users, rig.red.guard)
					out := red.filterPhotos(v.ctx, sp, []domain.Photo{photo})

					wantPresent := v.name == "owner" ||
						vis == domain.VisibilityPublic || vis == domain.VisibilityUnlisted
					if wantPresent && len(out) != 1 {
						t.Errorf("viewer=%s layer=%s vis=%s got %d photos, want 1",
							v.name, layer.name, vis, len(out))
					}
					if !wantPresent && len(out) != 0 {
						t.Errorf("viewer=%s layer=%s vis=%s got %d photos, want 0",
							v.name, layer.name, vis, len(out))
					}
				})
			}
		}
	}
}

// TestRedactor_FilterPhotos_SystemDefault covers the terminal
// fall-through: nothing in the chain set a value, so SystemDefault
// (private) applies. The owner sees the photo; everyone else does
// not. The case is split out because no `vis` argument is meaningful
// — SystemDefault is hard-coded.
func TestRedactor_FilterPhotos_SystemDefault(t *testing.T) {
	rig := newRedactTestRig(t)
	sp := domain.Specimen{ID: domain.NewID(), AuthorID: rig.owner.ID}
	photo := domain.Photo{ID: domain.NewID(), SpecimenID: sp.ID}
	cases := []struct {
		name        string
		ctx         context.Context
		wantPresent bool
	}{
		{"owner", rig.ownerCtx, true},
		{"other", rig.otherCtx, false},
		{"anonymous", rig.anonCtx, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := rig.red.filterPhotos(c.ctx, sp, []domain.Photo{photo})
			if c.wantPresent && len(out) != 1 {
				t.Errorf("got %d, want 1", len(out))
			}
			if !c.wantPresent && len(out) != 0 {
				t.Errorf("got %d, want 0", len(out))
			}
		})
	}
}

// TestRedactor_FilterPhotos_LeakyImagePolicy pins the mi-fo8 open
// question: a photo whose own Visibility is `public` on an otherwise
// `private` specimen IS visible to anonymous callers. The
// chain-as-written says yes; this test fails loudly if the policy is
// ever inverted to "most-restrictive-of."
func TestRedactor_FilterPhotos_LeakyImagePolicy(t *testing.T) {
	rig := newRedactTestRig(t)
	sp := domain.Specimen{
		ID:         domain.NewID(),
		AuthorID:   rig.owner.ID,
		Visibility: domain.VisibilityPrivate,
	}
	photo := domain.Photo{
		ID:         domain.NewID(),
		SpecimenID: sp.ID,
		Visibility: visPtr(domain.VisibilityPublic),
	}
	out := rig.red.filterPhotos(rig.anonCtx, sp, []domain.Photo{photo})
	if len(out) != 1 {
		t.Errorf("public photo on private specimen, anonymous viewer: got %d photos, want 1", len(out))
	}
}

// TestRedactor_FilterPhotos_OrderPreserved ensures the redactor does
// not reorder photos when filtering. Cursor pagination on
// /specimens/{id}/photos depends on stable underlying ordering — the
// filter step MUST preserve input order so the next-page cursor
// resolves correctly.
func TestRedactor_FilterPhotos_OrderPreserved(t *testing.T) {
	rig := newRedactTestRig(t)
	sp := domain.Specimen{
		ID:         domain.NewID(),
		AuthorID:   rig.owner.ID,
		Visibility: domain.VisibilityPublic,
	}
	// Three public photos in a known order; the filter step should
	// return them in the same order to anyone (public chain admits).
	photos := []domain.Photo{
		{ID: domain.NewID(), SpecimenID: sp.ID, Position: 1, Visibility: visPtr(domain.VisibilityPublic)},
		{ID: domain.NewID(), SpecimenID: sp.ID, Position: 2, Visibility: visPtr(domain.VisibilityPublic)},
		{ID: domain.NewID(), SpecimenID: sp.ID, Position: 3, Visibility: visPtr(domain.VisibilityPublic)},
	}
	out := rig.red.filterPhotos(rig.anonCtx, sp, photos)
	if len(out) != len(photos) {
		t.Fatalf("got %d photos, want %d", len(out), len(photos))
	}
	for i := range out {
		if out[i].ID != photos[i].ID {
			t.Errorf("position %d: id %s, want %s (order not preserved)", i, out[i].ID, photos[i].ID)
		}
	}
}

// TestRedactor_OwnerCacheReusesLookup verifies the per-redactor
// owner cache: a list call with N rows by the same author hits
// UserRepo.GetByID once, not N times. This is the cheap optimization
// that justifies running redaction on every list row.
func TestRedactor_OwnerCacheReusesLookup(t *testing.T) {
	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("authz.NewEnforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	owner := domain.User{ID: domain.NewID()}
	users := &countingUserRepo{inner: &redactFakeUserRepo{byID: map[uuid.UUID]domain.User{owner.ID: owner}}}
	red := newRedactor(users, authzGuard{enforcer: enf})
	ctx := auth.WithUser(context.Background(), auth.User{ID: owner.ID, Roles: []string{"user"}})
	for i := 0; i < 5; i++ {
		_ = red.redactSpecimen(ctx, domain.Specimen{ID: domain.NewID(), AuthorID: owner.ID, Visibility: domain.VisibilityPublic})
	}
	if users.getByIDCalls != 1 {
		t.Errorf("GetByID called %d times across 5 list rows, want 1 (cache miss?)", users.getByIDCalls)
	}
}

// TestRedactor_OwnerLookupErrorIsConservative pins the "missing
// owner row → SystemDefault → redact" behavior so a transient DB
// error or a deleted owner cannot leak a private field. The opposite
// behavior (permissive default) would be a data-leak bug.
func TestRedactor_OwnerLookupErrorIsConservative(t *testing.T) {
	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("authz.NewEnforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Empty repo: every GetByID returns ErrUserNotFound. The resolver
	// falls through to system-default (private). Owner-token couldn't
	// be set on the request anyway because the owner row is gone, so
	// the only viewer we exercise here is `other`.
	users := &redactFakeUserRepo{byID: map[uuid.UUID]domain.User{}}
	red := newRedactor(users, authzGuard{enforcer: enf})
	ctx := auth.WithUser(context.Background(), auth.User{
		ID: domain.NewID(), Roles: []string{"user"},
	})
	cents := int64(7)
	sp := domain.Specimen{
		ID:         domain.NewID(),
		AuthorID:   domain.NewID(), // unknown to the repo
		Visibility: domain.VisibilityPublic,
		PriceCents: &cents,
		// No specimen-level override either; resolver hits user-default
		// (unknown user → nil) then SystemDefault.
	}
	view := red.redactSpecimen(ctx, sp)
	if view.PriceCents != nil {
		t.Errorf("missing owner: PriceCents=%v, want nil (conservative redaction)", view.PriceCents)
	}
}

// countingUserRepo wraps a redactFakeUserRepo and counts GetByID
// calls so TestRedactor_OwnerCacheReusesLookup can assert the cache
// did its job.
type countingUserRepo struct {
	inner        *redactFakeUserRepo
	getByIDCalls int
}

func (r *countingUserRepo) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	r.getByIDCalls++
	return r.inner.GetByID(ctx, id)
}
func (r *countingUserRepo) GetBySub(ctx context.Context, sub string) (domain.User, error) {
	return r.inner.GetBySub(ctx, sub)
}
func (r *countingUserRepo) Create(ctx context.Context, tx domain.Tx, u domain.User) error {
	return r.inner.Create(ctx, tx, u)
}
func (r *countingUserRepo) MarkActive(ctx context.Context, tx domain.Tx, id uuid.UUID, n string, t time.Time) error {
	return r.inner.MarkActive(ctx, tx, id, n, t)
}
func (r *countingUserRepo) UpdateDisplayName(ctx context.Context, tx domain.Tx, id uuid.UUID, n string, t time.Time) error {
	return r.inner.UpdateDisplayName(ctx, tx, id, n, t)
}
func (r *countingUserRepo) UpdateFieldDefaults(ctx context.Context, tx domain.Tx, id uuid.UUID, defaults *domain.FieldDefaults, t time.Time) error {
	return r.inner.UpdateFieldDefaults(ctx, tx, id, defaults, t)
}
func (r *countingUserRepo) UpdateDefaultSpecimenVisibility(ctx context.Context, tx domain.Tx, id uuid.UUID, visibility *domain.Visibility, t time.Time) error {
	return r.inner.UpdateDefaultSpecimenVisibility(ctx, tx, id, visibility, t)
}
func (r *countingUserRepo) SetStatus(ctx context.Context, tx domain.Tx, id uuid.UUID, status domain.UserStatus, t time.Time) error {
	return r.inner.SetStatus(ctx, tx, id, status, t)
}

// reflectivePeek is a tiny utility used by the matrix's failure path
// to print stable, ordered field state when an assertion fails — the
// names are sorted so different Go map orderings don't make
// failures look different across runs.
//
//nolint:unused // referenced only by future debug expansions
func reflectivePeek(v SpecimenView) []string {
	out := []string{}
	if v.PriceCents != nil {
		out = append(out, "price")
	}
	if v.AcquiredFrom != nil {
		out = append(out, "acquired_from")
	}
	sort.Strings(out)
	return out
}
