// Client-side mirror of the backend's per-field visibility chain
// (internal/visibility/visibility.go, mi-37w / mi-fo8). Pure
// function, no DOM or Svelte deps — exists so edit screens can
// surface "Inherited: owner-only (from your account default)"
// style affordances by introspecting which layer of the chain
// supplied the effective value.
//
// This is UI-affordance only. Security enforcement happens on
// the server; by the time a response reaches the SPA, redacted
// fields have already been omitted. If this file ever drifts
// from the Go helper, the table-driven tests in visibility.test.ts
// (mirrored from internal/visibility/visibility_test.go) fail and
// surface the divergence loudly. Keep the two in lock-step.

import type { components } from './schema';

export type Visibility = 'private' | 'unlisted' | 'public';

// Field names match the keys used in users.field_defaults JSON
// (see CONTRACT.md §13 v2 / mi-fo8). They are the canonical
// identifiers the chain reads from FieldDefaultsView.
export type Field = 'price' | 'acquired_from' | 'images';

// Source labels the chain layer that supplied the effective
// value. Mirrors visibility.Source in the Go helper; edit
// screens use it to phrase the affordance ('from your account
// default', 'from this specimen', etc.).
export type Source =
  | 'image'
  | 'specimen-field'
  | 'specimen-overall'
  | 'user-default'
  | 'system-default';

export interface Resolution {
  visibility: Visibility;
  source: Source;
}

// SystemDefault is the conservative fallback when no chain layer
// supplied a value. Matches visibility.SystemDefault on the Go
// side (domain.VisibilityPrivate); this is the V1→V2 default that
// keeps migrated rows from leaking on the wire post-V2.
export const SystemDefault: Visibility = 'private';

// SpecimenLike is the subset of SpecimenView the chain consumes.
// The per-field override columns (visibility_price,
// visibility_acquired_from, visibility_images) are not yet
// surfaced on SpecimenView in schema.d.ts — the edit-screen
// beads (#6/#7/#8) will add them. Declaring the input as a
// minimal interface here lets this helper compile today and
// stay stable when those fields land.
export interface SpecimenLike {
  visibility?: Visibility | null;
  visibility_price?: Visibility | null;
  visibility_acquired_from?: Visibility | null;
  visibility_images?: Visibility | null;
}

// PhotoLike is the subset of PhotoView the chain consumes.
// PhotoView.visibility is not yet surfaced in schema.d.ts for
// the same reason as SpecimenLike's override columns.
export interface PhotoLike {
  visibility?: Visibility | null;
}

// OwnerLike is the subset of ProfileBody the chain consumes —
// only the field_defaults map. Backend uses the owning user
// (the specimen's author), not the viewer; the SPA must pass
// the same.
export interface OwnerLike {
  field_defaults?: components['schemas']['FieldDefaultsView'] | null;
}

// resolveScalar resolves the visibility of a scalar redactable
// field — 'price' or 'acquired_from' — by walking the chain:
//
//   1. the specimen's per-field override
//   2. the owner's per-field default (field_defaults)
//   3. SystemDefault
//
// Passing 'images' here is a programmer error — image
// resolution requires the photo-level override and the
// specimen overall column that resolveScalar does not touch.
// The call is handled defensively (no scalar override exists
// for 'images', so the chain falls through to the owner
// default and system default) but callers SHOULD use
// resolveImage for image fields, matching the Go helper.
export function resolveScalar(field: Field, specimen: SpecimenLike, owner: OwnerLike): Resolution {
  const override = specimenScalarOverride(field, specimen);
  if (override != null) {
    return { visibility: override, source: 'specimen-field' };
  }
  const def = ownerDefault(field, owner);
  if (def != null) {
    return { visibility: def, source: 'user-default' };
  }
  return { visibility: SystemDefault, source: 'system-default' };
}

// resolveImage resolves the visibility of a single photo by
// walking the image-specific chain:
//
//   1. the photo's own visibility override
//   2. the specimen's visibility_images override
//   3. the specimen's overall visibility column
//   4. the owner's 'images' default
//   5. SystemDefault
//
// Per the mi-fo8 open question, an image override that LOOSENS
// against the specimen overall (e.g. public photo on a private
// specimen) is allowed by design. If that policy reverses,
// change happens here AND in the Go helper.
export function resolveImage(
  specimen: SpecimenLike,
  owner: OwnerLike,
  image: PhotoLike,
): Resolution {
  if (image.visibility != null) {
    return { visibility: image.visibility, source: 'image' };
  }
  if (specimen.visibility_images != null) {
    return { visibility: specimen.visibility_images, source: 'specimen-field' };
  }
  // The Go helper treats the empty string as 'not set' to cover
  // the new-specimen / not-yet-persisted construction path; the
  // SPA equivalent treats null/undefined the same way. A blank
  // specimen.visibility (zero value on the wire) falls through.
  if (specimen.visibility != null && specimen.visibility !== ('' as Visibility)) {
    return { visibility: specimen.visibility, source: 'specimen-overall' };
  }
  const def = ownerDefault('images', owner);
  if (def != null) {
    return { visibility: def, source: 'user-default' };
  }
  return { visibility: SystemDefault, source: 'system-default' };
}

function specimenScalarOverride(
  field: Field,
  specimen: SpecimenLike,
): Visibility | null | undefined {
  switch (field) {
    case 'price':
      return specimen.visibility_price;
    case 'acquired_from':
      return specimen.visibility_acquired_from;
    case 'images':
      // Programmer error guard — matches the Go helper's
      // defensive fall-through.
      return null;
  }
}

function ownerDefault(field: Field, owner: OwnerLike): Visibility | null | undefined {
  const fd = owner.field_defaults;
  if (fd == null) {
    return null;
  }
  switch (field) {
    case 'price':
      return fd.price;
    case 'acquired_from':
      return fd.acquired_from;
    case 'images':
      return fd.images;
  }
}
