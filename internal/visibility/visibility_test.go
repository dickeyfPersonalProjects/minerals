package visibility_test

import (
	"testing"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/visibility"
)

// vis returns a pointer to v, the layout used by every domain field
// in the per-field visibility chain (nil = fall through).
func vis(v domain.Visibility) *domain.Visibility { return &v }

// allVisibilities is the closed enum from CONTRACT §13 the resolution
// chain reuses. Every test that walks "every value" iterates this.
var allVisibilities = []domain.Visibility{
	domain.VisibilityPrivate,
	domain.VisibilityUnlisted,
	domain.VisibilityPublic,
}

// allScalarFields is the set of Field values that ResolveScalar
// canonically handles. FieldImages is exercised separately against
// ResolveImage. FieldAcquiredAt and FieldCatalogNumber have no
// specimen-level override column — the scalar chain just walks
// owner-default → system-default for them; specimen-field cases
// are excluded by the helpers that drive that layer.
var allScalarFields = []visibility.Field{
	visibility.FieldPrice,
	visibility.FieldAcquiredFrom,
	visibility.FieldAcquiredAt,
	visibility.FieldCatalogNumber,
}

// scalarFieldsWithSpecimenOverride is the subset of scalar fields that
// have a per-specimen override column (visibility_price /
// visibility_acquired_from). FieldAcquiredAt and FieldCatalogNumber are
// excluded — they only exist on the user-default layer.
var scalarFieldsWithSpecimenOverride = []visibility.Field{
	visibility.FieldPrice,
	visibility.FieldAcquiredFrom,
}

func hasSpecimenOverride(f visibility.Field) bool {
	for _, c := range scalarFieldsWithSpecimenOverride {
		if c == f {
			return true
		}
	}
	return false
}

// fullyDefaultedUser returns a domain.User with every field_defaults
// key set to v. Used by the "fully-defaulted user" cases the bead
// calls out.
func fullyDefaultedUser(v domain.Visibility) domain.User {
	return domain.User{
		FieldDefaults: &domain.FieldDefaults{
			Price:         vis(v),
			AcquiredFrom:  vis(v),
			AcquiredAt:    vis(v),
			CatalogNumber: vis(v),
			Images:        vis(v),
		},
	}
}

// noDefaultsUser returns a domain.User with FieldDefaults == nil, the
// SQL NULL case described in domain.User.FieldDefaults' doc.
func noDefaultsUser() domain.User { return domain.User{} }

// setScalarOverride writes v onto the matching specimen column for
// field. The bead documents the chain in terms of these columns;
// keeping the helper next to the test table makes the table read as
// pure data. FieldAcquiredAt and FieldCatalogNumber are no-ops — no
// per-specimen override column exists for those fields.
func setScalarOverride(spec *domain.Specimen, field visibility.Field, v *domain.Visibility) {
	switch field {
	case visibility.FieldPrice:
		spec.VisibilityPrice = v
	case visibility.FieldAcquiredFrom:
		spec.VisibilityAcquiredFrom = v
	case visibility.FieldImages:
		spec.VisibilityImages = v
	}
}

// setOwnerDefault writes v onto the field_defaults slot named by field.
// Pairs with setScalarOverride for the user-default layer of the chain.
func setOwnerDefault(fd *domain.FieldDefaults, field visibility.Field, v *domain.Visibility) {
	switch field {
	case visibility.FieldPrice:
		fd.Price = v
	case visibility.FieldAcquiredFrom:
		fd.AcquiredFrom = v
	case visibility.FieldAcquiredAt:
		fd.AcquiredAt = v
	case visibility.FieldCatalogNumber:
		fd.CatalogNumber = v
	case visibility.FieldImages:
		fd.Images = v
	}
}

func TestField_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field visibility.Field
		want  string
	}{
		{visibility.FieldPrice, "price"},
		{visibility.FieldAcquiredFrom, "acquired_from"},
		{visibility.FieldAcquiredAt, "acquired_at"},
		{visibility.FieldCatalogNumber, "catalog_number"},
		{visibility.FieldImages, "images"},
		{visibility.Field(99), ""},
	}
	for _, c := range cases {
		if got := c.field.String(); got != c.want {
			t.Errorf("Field(%d).String() = %q, want %q", c.field, got, c.want)
		}
	}
}

// TestResolveScalar_SpecimenFieldLayer covers chain step 1: every
// scalar field × every visibility value, the per-specimen override
// wins regardless of owner defaults.
func TestResolveScalar_SpecimenFieldLayer(t *testing.T) {
	t.Parallel()
	for _, field := range scalarFieldsWithSpecimenOverride {
		for _, v := range allVisibilities {
			name := field.String() + "=" + string(v)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var spec domain.Specimen
				setScalarOverride(&spec, field, vis(v))
				// Owner has a CONFLICTING default to prove the
				// chain stops at the first non-nil layer.
				owner := fullyDefaultedUser(domain.VisibilityPublic)
				if v == domain.VisibilityPublic {
					owner = fullyDefaultedUser(domain.VisibilityPrivate)
				}
				got := visibility.ResolveScalar(field, spec, owner)
				want := visibility.Resolution{
					Visibility: v,
					Source:     visibility.SourceSpecimenField,
				}
				if got != want {
					t.Errorf("ResolveScalar(%s) = %+v, want %+v", field, got, want)
				}
			})
		}
	}
}

// TestResolveScalar_UserDefaultLayer covers chain step 2 — the
// specimen has no override, so the owner's field_defaults value
// applies. Every scalar field × every visibility value.
func TestResolveScalar_UserDefaultLayer(t *testing.T) {
	t.Parallel()
	for _, field := range allScalarFields {
		for _, v := range allVisibilities {
			name := field.String() + "=" + string(v)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				spec := domain.Specimen{Visibility: domain.VisibilityPublic}
				// Sparse FieldDefaults: only the field under test
				// has a value. Confirms the chain reads the right key.
				owner := domain.User{FieldDefaults: &domain.FieldDefaults{}}
				setOwnerDefault(owner.FieldDefaults, field, vis(v))
				got := visibility.ResolveScalar(field, spec, owner)
				want := visibility.Resolution{
					Visibility: v,
					Source:     visibility.SourceUserDefault,
				}
				if got != want {
					t.Errorf("ResolveScalar(%s) = %+v, want %+v", field, got, want)
				}
			})
		}
	}
}

// TestResolveScalar_SystemDefaultLayer covers chain step 3 — no
// specimen override, no user default → SystemDefault.
func TestResolveScalar_SystemDefaultLayer(t *testing.T) {
	t.Parallel()
	for _, field := range allScalarFields {
		t.Run(field.String(), func(t *testing.T) {
			t.Parallel()
			// Specimen.Visibility is populated (it's a non-pointer
			// enum on every row) but the scalar chain MUST NOT read
			// it. Set it to a value that would be wrong if it did.
			spec := domain.Specimen{Visibility: domain.VisibilityPublic}
			got := visibility.ResolveScalar(field, spec, noDefaultsUser())
			want := visibility.Resolution{
				Visibility: visibility.SystemDefault,
				Source:     visibility.SourceSystemDefault,
			}
			if got != want {
				t.Errorf("ResolveScalar(%s) = %+v, want %+v", field, got, want)
			}
		})
	}
}

// TestResolveScalar_OtherFieldDefaultIgnored confirms the chain reads
// only the requested field's entry from field_defaults — a Price
// default does not leak into FieldAcquiredFrom resolution and vice
// versa. Regression guard against the obvious switch-statement
// fallthrough bug.
func TestResolveScalar_OtherFieldDefaultIgnored(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		field   visibility.Field
		owner   domain.User
		wantVis domain.Visibility
		wantSrc visibility.Source
	}{
		{
			name:  "price asked, only acquired_from default set",
			field: visibility.FieldPrice,
			owner: domain.User{FieldDefaults: &domain.FieldDefaults{
				AcquiredFrom: vis(domain.VisibilityPublic),
			}},
			wantVis: visibility.SystemDefault,
			wantSrc: visibility.SourceSystemDefault,
		},
		{
			name:  "acquired_from asked, only price default set",
			field: visibility.FieldAcquiredFrom,
			owner: domain.User{FieldDefaults: &domain.FieldDefaults{
				Price: vis(domain.VisibilityPublic),
			}},
			wantVis: visibility.SystemDefault,
			wantSrc: visibility.SourceSystemDefault,
		},
		{
			name:  "price asked, images default set (must not leak)",
			field: visibility.FieldPrice,
			owner: domain.User{FieldDefaults: &domain.FieldDefaults{
				Images: vis(domain.VisibilityPublic),
			}},
			wantVis: visibility.SystemDefault,
			wantSrc: visibility.SourceSystemDefault,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := visibility.ResolveScalar(tc.field, domain.Specimen{}, tc.owner)
			want := visibility.Resolution{Visibility: tc.wantVis, Source: tc.wantSrc}
			if got != want {
				t.Errorf("ResolveScalar = %+v, want %+v", got, want)
			}
		})
	}
}

// TestResolveScalar_DoesNotConsultSpecimenOverall confirms the scalar
// chain ignores specimens.visibility (it only appears in the IMAGE
// chain per mi-fo8). Without this, a "public" specimen would
// accidentally make the price public too.
func TestResolveScalar_DoesNotConsultSpecimenOverall(t *testing.T) {
	t.Parallel()
	for _, v := range allVisibilities {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			spec := domain.Specimen{Visibility: v}
			got := visibility.ResolveScalar(visibility.FieldPrice, spec, noDefaultsUser())
			want := visibility.Resolution{
				Visibility: visibility.SystemDefault,
				Source:     visibility.SourceSystemDefault,
			}
			if got != want {
				t.Errorf("ResolveScalar with specimen.visibility=%s = %+v, want %+v", v, got, want)
			}
		})
	}
}

// TestResolveImage_ImageLayer covers image-chain step 1: the photo's
// own Visibility wins over every higher layer, for every value.
func TestResolveImage_ImageLayer(t *testing.T) {
	t.Parallel()
	for _, v := range allVisibilities {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			// Higher layers all set to a CONFLICTING value to prove
			// the image-level override wins and the chain stops.
			conflict := domain.VisibilityPrivate
			if v == domain.VisibilityPrivate {
				conflict = domain.VisibilityPublic
			}
			spec := domain.Specimen{
				Visibility:       conflict,
				VisibilityImages: vis(conflict),
			}
			img := domain.Photo{Visibility: vis(v)}
			owner := fullyDefaultedUser(conflict)
			got := visibility.ResolveImage(spec, owner, img)
			want := visibility.Resolution{Visibility: v, Source: visibility.SourceImage}
			if got != want {
				t.Errorf("ResolveImage = %+v, want %+v", got, want)
			}
		})
	}
}

// TestResolveImage_SpecimenFieldLayer covers image-chain step 2 —
// the photo has no override, so specimens.visibility_images wins.
func TestResolveImage_SpecimenFieldLayer(t *testing.T) {
	t.Parallel()
	for _, v := range allVisibilities {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			conflict := domain.VisibilityPrivate
			if v == domain.VisibilityPrivate {
				conflict = domain.VisibilityPublic
			}
			spec := domain.Specimen{
				Visibility:       conflict,
				VisibilityImages: vis(v),
			}
			owner := fullyDefaultedUser(conflict)
			got := visibility.ResolveImage(spec, owner, domain.Photo{})
			want := visibility.Resolution{Visibility: v, Source: visibility.SourceSpecimenField}
			if got != want {
				t.Errorf("ResolveImage = %+v, want %+v", got, want)
			}
		})
	}
}

// TestResolveImage_SpecimenOverallLayer covers image-chain step 3 —
// neither the photo nor specimens.visibility_images is set, so the
// specimen's overall Visibility column applies. The mi-fo8 bead
// flagged "public image on owner-only specimen" as an open question:
// this test pins the chain-as-written behavior so the answer changes
// here when the EPIC resolves it.
func TestResolveImage_SpecimenOverallLayer(t *testing.T) {
	t.Parallel()
	for _, v := range allVisibilities {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			conflict := domain.VisibilityPrivate
			if v == domain.VisibilityPrivate {
				conflict = domain.VisibilityPublic
			}
			spec := domain.Specimen{Visibility: v}
			owner := fullyDefaultedUser(conflict)
			got := visibility.ResolveImage(spec, owner, domain.Photo{})
			want := visibility.Resolution{Visibility: v, Source: visibility.SourceSpecimenOverall}
			if got != want {
				t.Errorf("ResolveImage = %+v, want %+v", got, want)
			}
		})
	}
}

// TestResolveImage_UserDefaultLayer covers image-chain step 4 — image
// and specimen layers are unset; the owner's "images" default applies.
// specimens.visibility is the zero value ("") which the chain treats
// as absent (covers the new-specimen / not-yet-persisted construction
// path).
func TestResolveImage_UserDefaultLayer(t *testing.T) {
	t.Parallel()
	for _, v := range allVisibilities {
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			// Sparse defaults: ONLY the images key is set, to prove
			// the right field_defaults entry is consulted.
			owner := domain.User{FieldDefaults: &domain.FieldDefaults{Images: vis(v)}}
			got := visibility.ResolveImage(domain.Specimen{}, owner, domain.Photo{})
			want := visibility.Resolution{Visibility: v, Source: visibility.SourceUserDefault}
			if got != want {
				t.Errorf("ResolveImage = %+v, want %+v", got, want)
			}
		})
	}
}

// TestResolveImage_SystemDefaultLayer covers image-chain step 5 — no
// layer set a value, fall through to SystemDefault.
func TestResolveImage_SystemDefaultLayer(t *testing.T) {
	t.Parallel()
	got := visibility.ResolveImage(domain.Specimen{}, noDefaultsUser(), domain.Photo{})
	want := visibility.Resolution{
		Visibility: visibility.SystemDefault,
		Source:     visibility.SourceSystemDefault,
	}
	if got != want {
		t.Errorf("ResolveImage = %+v, want %+v", got, want)
	}
}

// TestResolveImage_OtherFieldDefaultsIgnored confirms the image chain
// reads only field_defaults.images — a Price or AcquiredFrom default
// must not leak.
func TestResolveImage_OtherFieldDefaultsIgnored(t *testing.T) {
	t.Parallel()
	owner := domain.User{FieldDefaults: &domain.FieldDefaults{
		Price:        vis(domain.VisibilityPublic),
		AcquiredFrom: vis(domain.VisibilityPublic),
	}}
	got := visibility.ResolveImage(domain.Specimen{}, owner, domain.Photo{})
	want := visibility.Resolution{
		Visibility: visibility.SystemDefault,
		Source:     visibility.SourceSystemDefault,
	}
	if got != want {
		t.Errorf("ResolveImage = %+v, want %+v", got, want)
	}
}

// TestResolveImage_NoDefaultsUser is the "no-defaults user" case the
// bead asks for, called out as a deliberate fixture.
func TestResolveImage_NoDefaultsUser(t *testing.T) {
	t.Parallel()
	spec := domain.Specimen{Visibility: domain.VisibilityPublic}
	got := visibility.ResolveImage(spec, noDefaultsUser(), domain.Photo{})
	want := visibility.Resolution{
		Visibility: domain.VisibilityPublic,
		Source:     visibility.SourceSpecimenOverall,
	}
	if got != want {
		t.Errorf("ResolveImage = %+v, want %+v", got, want)
	}
}

// TestResolveImage_FullyDefaultedUser is the "fully-defaulted user"
// case the bead asks for. Specimen and photo expose no overrides;
// every field_defaults key is set, so the images key applies.
func TestResolveImage_FullyDefaultedUser(t *testing.T) {
	t.Parallel()
	owner := fullyDefaultedUser(domain.VisibilityUnlisted)
	got := visibility.ResolveImage(domain.Specimen{}, owner, domain.Photo{})
	want := visibility.Resolution{
		Visibility: domain.VisibilityUnlisted,
		Source:     visibility.SourceUserDefault,
	}
	if got != want {
		t.Errorf("ResolveImage = %+v, want %+v", got, want)
	}
}

// TestResolveImage_PublicImageOnPrivateSpecimen is the open-question
// case mi-fo8 flagged — a "public" photo override on an owner-only
// specimen MUST resolve to public per the chain-as-written. This
// pinning test exists so a future reversal of the policy fails this
// test loudly, surfacing the design decision rather than silently
// shipping a leak.
func TestResolveImage_PublicImageOnPrivateSpecimen(t *testing.T) {
	t.Parallel()
	spec := domain.Specimen{Visibility: domain.VisibilityPrivate}
	img := domain.Photo{Visibility: vis(domain.VisibilityPublic)}
	got := visibility.ResolveImage(spec, noDefaultsUser(), img)
	want := visibility.Resolution{
		Visibility: domain.VisibilityPublic,
		Source:     visibility.SourceImage,
	}
	if got != want {
		t.Errorf("ResolveImage = %+v, want %+v", got, want)
	}
}

// TestResolveScalar_FieldImages documents the defensive fall-through
// for the programmer-error case: passing FieldImages skips the
// (non-existent) scalar specimen override and lands on whichever of
// the user-images default or the system default applies. This is
// also where the unknown-Field path is exercised so the package's
// defensive switch returns are pinned.
func TestResolveScalar_FieldImages(t *testing.T) {
	t.Parallel()
	t.Run("no defaults → system default", func(t *testing.T) {
		t.Parallel()
		got := visibility.ResolveScalar(visibility.FieldImages, domain.Specimen{}, noDefaultsUser())
		want := visibility.Resolution{
			Visibility: visibility.SystemDefault,
			Source:     visibility.SourceSystemDefault,
		}
		if got != want {
			t.Errorf("ResolveScalar(FieldImages) = %+v, want %+v", got, want)
		}
	})
	t.Run("images default applied", func(t *testing.T) {
		t.Parallel()
		owner := domain.User{FieldDefaults: &domain.FieldDefaults{
			Images: vis(domain.VisibilityPublic),
		}}
		got := visibility.ResolveScalar(visibility.FieldImages, domain.Specimen{}, owner)
		want := visibility.Resolution{
			Visibility: domain.VisibilityPublic,
			Source:     visibility.SourceUserDefault,
		}
		if got != want {
			t.Errorf("ResolveScalar(FieldImages) = %+v, want %+v", got, want)
		}
	})
	t.Run("unknown Field value → system default", func(t *testing.T) {
		t.Parallel()
		// Sparse owner with every key set so the switch in ownerDefault
		// has somewhere to NOT match — proves an unrecognized Field
		// constant doesn't accidentally pick a populated key.
		owner := fullyDefaultedUser(domain.VisibilityPublic)
		got := visibility.ResolveScalar(visibility.Field(99), domain.Specimen{}, owner)
		want := visibility.Resolution{
			Visibility: visibility.SystemDefault,
			Source:     visibility.SourceSystemDefault,
		}
		if got != want {
			t.Errorf("ResolveScalar(unknown) = %+v, want %+v", got, want)
		}
	})
}

// TestResolveScalar_FullChainTransitions walks every fall-through edge
// for both scalar fields. Reads as documentation of the chain: each
// row shows which layers are populated and which Source the helper
// must pick.
func TestResolveScalar_FullChainTransitions(t *testing.T) {
	t.Parallel()
	type setup struct {
		name     string
		specOver *domain.Visibility
		ownerDef *domain.Visibility
		wantVis  domain.Visibility
		wantSrc  visibility.Source
	}
	setups := []setup{
		{
			name:    "no layers → system default",
			wantVis: visibility.SystemDefault, wantSrc: visibility.SourceSystemDefault,
		},
		{
			name:     "owner default only → user-default",
			ownerDef: vis(domain.VisibilityUnlisted),
			wantVis:  domain.VisibilityUnlisted, wantSrc: visibility.SourceUserDefault,
		},
		{
			name:     "specimen override only → specimen-field",
			specOver: vis(domain.VisibilityPublic),
			wantVis:  domain.VisibilityPublic, wantSrc: visibility.SourceSpecimenField,
		},
		{
			name:     "both set → specimen-field wins",
			specOver: vis(domain.VisibilityPublic), ownerDef: vis(domain.VisibilityPrivate),
			wantVis: domain.VisibilityPublic, wantSrc: visibility.SourceSpecimenField,
		},
	}
	for _, field := range allScalarFields {
		for _, s := range setups {
			// FieldAcquiredAt and FieldCatalogNumber have no
			// specimen-level override column; skip the rows that
			// drive that layer for those fields.
			if s.specOver != nil && !hasSpecimenOverride(field) {
				continue
			}
			name := field.String() + "/" + s.name
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var spec domain.Specimen
				setScalarOverride(&spec, field, s.specOver)
				owner := domain.User{}
				if s.ownerDef != nil {
					fd := &domain.FieldDefaults{}
					setOwnerDefault(fd, field, s.ownerDef)
					owner.FieldDefaults = fd
				}
				got := visibility.ResolveScalar(field, spec, owner)
				want := visibility.Resolution{Visibility: s.wantVis, Source: s.wantSrc}
				if got != want {
					t.Errorf("ResolveScalar = %+v, want %+v", got, want)
				}
			})
		}
	}
}

// TestResolveImage_FullChainTransitions walks every fall-through edge
// of the image chain. Each row populates exactly the layers named in
// "set"; the helper must pick the highest-priority layer that's set.
func TestResolveImage_FullChainTransitions(t *testing.T) {
	t.Parallel()
	type setup struct {
		name        string
		imgVis      *domain.Visibility
		specImg     *domain.Visibility
		specOverall domain.Visibility // "" means "not set" (new specimen path)
		ownerImages *domain.Visibility
		wantVis     domain.Visibility
		wantSrc     visibility.Source
	}
	setups := []setup{
		{
			name:    "no layers → system default",
			wantVis: visibility.SystemDefault, wantSrc: visibility.SourceSystemDefault,
		},
		{
			name:        "only user default",
			ownerImages: vis(domain.VisibilityUnlisted),
			wantVis:     domain.VisibilityUnlisted, wantSrc: visibility.SourceUserDefault,
		},
		{
			name:        "only specimen overall",
			specOverall: domain.VisibilityPublic,
			wantVis:     domain.VisibilityPublic, wantSrc: visibility.SourceSpecimenOverall,
		},
		{
			name:        "specimen overall + user default → overall wins",
			specOverall: domain.VisibilityPublic, ownerImages: vis(domain.VisibilityPrivate),
			wantVis: domain.VisibilityPublic, wantSrc: visibility.SourceSpecimenOverall,
		},
		{
			name:    "only specimen-images field",
			specImg: vis(domain.VisibilityUnlisted),
			wantVis: domain.VisibilityUnlisted, wantSrc: visibility.SourceSpecimenField,
		},
		{
			name:        "specimen-images + overall → specimen-images wins",
			specImg:     vis(domain.VisibilityUnlisted),
			specOverall: domain.VisibilityPublic,
			wantVis:     domain.VisibilityUnlisted, wantSrc: visibility.SourceSpecimenField,
		},
		{
			name:    "image override only",
			imgVis:  vis(domain.VisibilityPrivate),
			wantVis: domain.VisibilityPrivate, wantSrc: visibility.SourceImage,
		},
		{
			name:    "image override + every higher layer → image wins",
			imgVis:  vis(domain.VisibilityPrivate),
			specImg: vis(domain.VisibilityPublic), specOverall: domain.VisibilityPublic,
			ownerImages: vis(domain.VisibilityPublic),
			wantVis:     domain.VisibilityPrivate, wantSrc: visibility.SourceImage,
		},
	}
	for _, s := range setups {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			spec := domain.Specimen{
				Visibility:       s.specOverall,
				VisibilityImages: s.specImg,
			}
			owner := domain.User{}
			if s.ownerImages != nil {
				owner.FieldDefaults = &domain.FieldDefaults{Images: s.ownerImages}
			}
			img := domain.Photo{Visibility: s.imgVis}
			got := visibility.ResolveImage(spec, owner, img)
			want := visibility.Resolution{Visibility: s.wantVis, Source: s.wantSrc}
			if got != want {
				t.Errorf("ResolveImage = %+v, want %+v", got, want)
			}
		})
	}
}
