// Package visibility implements the per-field visibility resolution
// chain described in the V2 per-field visibility EPIC (mi-fo8). Given
// an already-loaded specimen, its owning user, and (for images) the
// photo, it picks the applicable [domain.Visibility] value plus the
// chain layer the value came from.
//
// The package is pure: no DB calls, no HTTP, no request context. The
// "is the viewer allowed to see this Visibility" check is a separate
// concern (see internal/authz and internal/api). Handlers call into
// this package to discover which value applies, then run the existing
// viewer-allows-visibility predicate against the result before deciding
// whether to redact a field from the response.
//
// Both [ResolveScalar] and [ResolveImage] stop at the first non-nil
// layer of their chain. No merging, no most-restrictive-of, no
// intersection — see mi-fo8 for the canonical description.
package visibility

import (
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// Field discriminates the redactable specimen fields covered by the
// V2 launch (mi-fo8): two scalars and the images collection. The
// String form matches the keys used in users.field_defaults JSON.
type Field int

const (
	// FieldPrice selects the resolution chain for specimens.price_cents.
	FieldPrice Field = iota
	// FieldAcquiredFrom selects the chain for specimens.acquired_from.
	FieldAcquiredFrom
	// FieldImages selects the chain for the photos collection. Pass
	// this to [ResolveImage], not [ResolveScalar] — see ResolveScalar.
	FieldImages
)

// String returns the canonical lowercase name of the Field, matching
// the keys used in the users.field_defaults JSON column.
func (f Field) String() string {
	switch f {
	case FieldPrice:
		return "price"
	case FieldAcquiredFrom:
		return "acquired_from"
	case FieldImages:
		return "images"
	}
	return ""
}

// Source labels the layer of the resolution chain that produced a
// [Resolution]. Handlers may surface this in debug responses or
// log fields to explain why a field was redacted; nothing in the
// authorization path branches on it.
type Source string

const (
	// SourceImage means the photo's own Visibility override applied.
	// Only reachable from [ResolveImage].
	SourceImage Source = "image"
	// SourceSpecimenField means a specimen-level per-field override
	// applied — VisibilityPrice / VisibilityAcquiredFrom for the
	// scalar chains, VisibilityImages for the image chain.
	SourceSpecimenField Source = "specimen-field"
	// SourceSpecimenOverall means the specimen's overall Visibility
	// column applied. Only reachable from [ResolveImage]; the scalar
	// chain does not consult specimens.visibility (per mi-fo8).
	SourceSpecimenOverall Source = "specimen-overall"
	// SourceUserDefault means the owner's per-field default applied
	// (users.field_defaults).
	SourceUserDefault Source = "user-default"
	// SourceSystemDefault means no layer set a value; the conservative
	// system default applied. See [SystemDefault].
	SourceSystemDefault Source = "system-default"
)

// SystemDefault is the conservative fallback applied when no layer of
// the resolution chain set a value. Per mi-fo8, owner-only is the
// most-restrictive value and the V1→V2 default that keeps migrated
// rows from leaking on the wire post-V2; that maps onto the existing
// [domain.VisibilityPrivate] enum value.
const SystemDefault = domain.VisibilityPrivate

// Resolution is the output of the visibility chain: the value that
// applies and the layer it came from.
type Resolution struct {
	Visibility domain.Visibility
	Source     Source
}

// ResolveScalar resolves the visibility of a scalar redactable field
// — [FieldPrice] or [FieldAcquiredFrom] — by walking the chain
// documented in mi-fo8:
//
//  1. the specimen's per-field override (VisibilityPrice / VisibilityAcquiredFrom)
//  2. the owner's per-field default (users.field_defaults)
//  3. [SystemDefault]
//
// owner is the specimen's author; per mi-fo8 the per-user defaults map
// lives on the owning user, not the viewer.
//
// Passing [FieldImages] is a programmer error — image resolution
// requires the photo-level override and the specimen overall column
// that ResolveScalar does not touch. The call is handled defensively
// by skipping the specimen-field layer (there is no scalar override
// for images) and falling through to the owner default and system
// default, but callers SHOULD use [ResolveImage] for image fields.
func ResolveScalar(field Field, spec domain.Specimen, owner domain.User) Resolution {
	if v := specimenScalarOverride(field, spec); v != nil {
		return Resolution{Visibility: *v, Source: SourceSpecimenField}
	}
	if v := ownerDefault(field, owner); v != nil {
		return Resolution{Visibility: *v, Source: SourceUserDefault}
	}
	return Resolution{Visibility: SystemDefault, Source: SourceSystemDefault}
}

// ResolveImage resolves the visibility of a single photo by walking
// the image-specific chain documented in mi-fo8:
//
//  1. the photo's own Visibility override
//  2. the specimen's VisibilityImages override
//  3. the specimen's overall Visibility column
//  4. the owner's "images" default
//  5. [SystemDefault]
//
// The chain stops at the first non-nil layer. Per the mi-fo8 open
// question called out in the bead, an image override that LOOSENS
// against the specimen overall (e.g. public photo on a private
// specimen) is allowed by design — the helper implements the chain
// as written. If that policy reverses later, change happens here.
func ResolveImage(spec domain.Specimen, owner domain.User, img domain.Photo) Resolution {
	if img.Visibility != nil {
		return Resolution{Visibility: *img.Visibility, Source: SourceImage}
	}
	if spec.VisibilityImages != nil {
		return Resolution{Visibility: *spec.VisibilityImages, Source: SourceSpecimenField}
	}
	if spec.Visibility != "" {
		return Resolution{Visibility: spec.Visibility, Source: SourceSpecimenOverall}
	}
	if v := ownerDefault(FieldImages, owner); v != nil {
		return Resolution{Visibility: *v, Source: SourceUserDefault}
	}
	return Resolution{Visibility: SystemDefault, Source: SourceSystemDefault}
}

func specimenScalarOverride(field Field, spec domain.Specimen) *domain.Visibility {
	switch field {
	case FieldPrice:
		return spec.VisibilityPrice
	case FieldAcquiredFrom:
		return spec.VisibilityAcquiredFrom
	}
	return nil
}

func ownerDefault(field Field, owner domain.User) *domain.Visibility {
	fd := owner.FieldDefaults
	if fd == nil {
		return nil
	}
	switch field {
	case FieldPrice:
		return fd.Price
	case FieldAcquiredFrom:
		return fd.AcquiredFrom
	case FieldImages:
		return fd.Images
	}
	return nil
}
