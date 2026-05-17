// Mirror of internal/visibility/visibility_test.go. Each test
// here pairs with a backend test of the same name so that any
// drift between the two helpers fails CI loudly. When the Go
// matrix changes, mirror the change here in the same commit.

import { describe, expect, it } from 'vitest';
import {
  resolveImage,
  resolveScalar,
  SystemDefault,
  type Field,
  type OwnerLike,
  type Resolution,
  type SpecimenLike,
  type Visibility,
} from './visibility';

const ALL_VISIBILITIES: Visibility[] = ['private', 'unlisted', 'public'];
const ALL_SCALAR_FIELDS: Field[] = ['price', 'acquired_from'];

function fullyDefaultedUser(v: Visibility): OwnerLike {
  return { field_defaults: { price: v, acquired_from: v, images: v } };
}

function noDefaultsUser(): OwnerLike {
  return {};
}

function setScalarOverride(spec: SpecimenLike, field: Field, v: Visibility | null): void {
  switch (field) {
    case 'price':
      spec.visibility_price = v;
      break;
    case 'acquired_from':
      spec.visibility_acquired_from = v;
      break;
    case 'images':
      spec.visibility_images = v;
      break;
  }
}

// Mirrors TestResolveScalar_SpecimenFieldLayer.
describe('resolveScalar: specimen-field layer', () => {
  for (const field of ALL_SCALAR_FIELDS) {
    for (const v of ALL_VISIBILITIES) {
      it(`${field}=${v}`, () => {
        const spec: SpecimenLike = {};
        setScalarOverride(spec, field, v);
        // Owner has a CONFLICTING default to prove the chain
        // stops at the first non-nil layer.
        const owner = v === 'public' ? fullyDefaultedUser('private') : fullyDefaultedUser('public');
        const want: Resolution = { visibility: v, source: 'specimen-field' };
        expect(resolveScalar(field, spec, owner)).toEqual(want);
      });
    }
  }
});

// Mirrors TestResolveScalar_UserDefaultLayer.
describe('resolveScalar: user-default layer', () => {
  for (const field of ALL_SCALAR_FIELDS) {
    for (const v of ALL_VISIBILITIES) {
      it(`${field}=${v}`, () => {
        const spec: SpecimenLike = { visibility: 'public' };
        // Sparse field_defaults: only the field under test has
        // a value. Confirms the chain reads the right key.
        const owner: OwnerLike = { field_defaults: {} };
        if (field === 'price') {
          owner.field_defaults!.price = v;
        } else {
          owner.field_defaults!.acquired_from = v;
        }
        const want: Resolution = { visibility: v, source: 'user-default' };
        expect(resolveScalar(field, spec, owner)).toEqual(want);
      });
    }
  }
});

// Mirrors TestResolveScalar_SystemDefaultLayer.
describe('resolveScalar: system-default layer', () => {
  for (const field of ALL_SCALAR_FIELDS) {
    it(field, () => {
      // specimen.visibility is set to a value that would be
      // wrong if the scalar chain accidentally consulted it
      // (it MUST NOT — that's the IMAGE chain).
      const spec: SpecimenLike = { visibility: 'public' };
      const want: Resolution = {
        visibility: SystemDefault,
        source: 'system-default',
      };
      expect(resolveScalar(field, spec, noDefaultsUser())).toEqual(want);
    });
  }
});

// Mirrors TestResolveScalar_OtherFieldDefaultIgnored.
describe('resolveScalar: other-field default ignored', () => {
  const cases: {
    name: string;
    field: Field;
    owner: OwnerLike;
  }[] = [
    {
      name: 'price asked, only acquired_from default set',
      field: 'price',
      owner: { field_defaults: { acquired_from: 'public' } },
    },
    {
      name: 'acquired_from asked, only price default set',
      field: 'acquired_from',
      owner: { field_defaults: { price: 'public' } },
    },
    {
      name: 'price asked, images default set (must not leak)',
      field: 'price',
      owner: { field_defaults: { images: 'public' } },
    },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const want: Resolution = {
        visibility: SystemDefault,
        source: 'system-default',
      };
      expect(resolveScalar(c.field, {}, c.owner)).toEqual(want);
    });
  }
});

// Mirrors TestResolveScalar_DoesNotConsultSpecimenOverall.
describe('resolveScalar: does not consult specimen.visibility', () => {
  for (const v of ALL_VISIBILITIES) {
    it(v, () => {
      const spec: SpecimenLike = { visibility: v };
      const want: Resolution = {
        visibility: SystemDefault,
        source: 'system-default',
      };
      expect(resolveScalar('price', spec, noDefaultsUser())).toEqual(want);
    });
  }
});

// Mirrors TestResolveImage_ImageLayer.
describe('resolveImage: image layer', () => {
  for (const v of ALL_VISIBILITIES) {
    it(v, () => {
      const conflict: Visibility = v === 'private' ? 'public' : 'private';
      const spec: SpecimenLike = {
        visibility: conflict,
        visibility_images: conflict,
      };
      const img = { visibility: v };
      const owner = fullyDefaultedUser(conflict);
      const want: Resolution = { visibility: v, source: 'image' };
      expect(resolveImage(spec, owner, img)).toEqual(want);
    });
  }
});

// Mirrors TestResolveImage_SpecimenFieldLayer.
describe('resolveImage: specimen-field layer', () => {
  for (const v of ALL_VISIBILITIES) {
    it(v, () => {
      const conflict: Visibility = v === 'private' ? 'public' : 'private';
      const spec: SpecimenLike = {
        visibility: conflict,
        visibility_images: v,
      };
      const owner = fullyDefaultedUser(conflict);
      const want: Resolution = { visibility: v, source: 'specimen-field' };
      expect(resolveImage(spec, owner, {})).toEqual(want);
    });
  }
});

// Mirrors TestResolveImage_SpecimenOverallLayer. Pins the
// mi-fo8 open-question behavior for "public image on owner-only
// specimen" — see the Go test comment.
describe('resolveImage: specimen-overall layer', () => {
  for (const v of ALL_VISIBILITIES) {
    it(v, () => {
      const conflict: Visibility = v === 'private' ? 'public' : 'private';
      const spec: SpecimenLike = { visibility: v };
      const owner = fullyDefaultedUser(conflict);
      const want: Resolution = { visibility: v, source: 'specimen-overall' };
      expect(resolveImage(spec, owner, {})).toEqual(want);
    });
  }
});

// Mirrors TestResolveImage_UserDefaultLayer.
describe('resolveImage: user-default layer', () => {
  for (const v of ALL_VISIBILITIES) {
    it(v, () => {
      // Sparse defaults: only the images key is set, to prove
      // the right field_defaults entry is consulted.
      const owner: OwnerLike = { field_defaults: { images: v } };
      const want: Resolution = { visibility: v, source: 'user-default' };
      expect(resolveImage({}, owner, {})).toEqual(want);
    });
  }
});

// Mirrors TestResolveImage_SystemDefaultLayer.
it('resolveImage: system-default layer (no layers set)', () => {
  const want: Resolution = {
    visibility: SystemDefault,
    source: 'system-default',
  };
  expect(resolveImage({}, noDefaultsUser(), {})).toEqual(want);
});

// Mirrors TestResolveImage_OtherFieldDefaultsIgnored.
it('resolveImage: other-field defaults ignored', () => {
  const owner: OwnerLike = {
    field_defaults: { price: 'public', acquired_from: 'public' },
  };
  const want: Resolution = {
    visibility: SystemDefault,
    source: 'system-default',
  };
  expect(resolveImage({}, owner, {})).toEqual(want);
});

// Mirrors TestResolveImage_NoDefaultsUser.
it('resolveImage: no-defaults user, specimen overall applies', () => {
  const spec: SpecimenLike = { visibility: 'public' };
  const want: Resolution = { visibility: 'public', source: 'specimen-overall' };
  expect(resolveImage(spec, noDefaultsUser(), {})).toEqual(want);
});

// Mirrors TestResolveImage_FullyDefaultedUser.
it('resolveImage: fully-defaulted user, images key applies', () => {
  const owner = fullyDefaultedUser('unlisted');
  const want: Resolution = { visibility: 'unlisted', source: 'user-default' };
  expect(resolveImage({}, owner, {})).toEqual(want);
});

// Mirrors TestResolveImage_PublicImageOnPrivateSpecimen — the
// pinning test for the mi-fo8 open question. If the policy is
// ever reversed, this test (and the Go twin) must change in the
// same commit so the design decision is surfaced loudly.
it('resolveImage: public image override on private specimen → public', () => {
  const spec: SpecimenLike = { visibility: 'private' };
  const img = { visibility: 'public' as Visibility };
  const want: Resolution = { visibility: 'public', source: 'image' };
  expect(resolveImage(spec, noDefaultsUser(), img)).toEqual(want);
});

// Mirrors TestResolveScalar_FieldImages — the defensive
// fall-through for the programmer-error case of passing
// 'images' to resolveScalar.
describe("resolveScalar('images') defensive fall-through", () => {
  it('no defaults → system default', () => {
    const want: Resolution = {
      visibility: SystemDefault,
      source: 'system-default',
    };
    expect(resolveScalar('images', {}, noDefaultsUser())).toEqual(want);
  });
  it('images default applied', () => {
    const owner: OwnerLike = { field_defaults: { images: 'public' } };
    const want: Resolution = { visibility: 'public', source: 'user-default' };
    expect(resolveScalar('images', {}, owner)).toEqual(want);
  });
});

// Mirrors TestResolveScalar_FullChainTransitions. Reads as
// documentation: each row shows which layers are populated and
// the Source the helper must pick.
describe('resolveScalar: full chain transitions', () => {
  type Setup = {
    name: string;
    specOver?: Visibility;
    ownerDef?: Visibility;
    want: Resolution;
  };
  const setups: Setup[] = [
    {
      name: 'no layers → system default',
      want: { visibility: SystemDefault, source: 'system-default' },
    },
    {
      name: 'owner default only → user-default',
      ownerDef: 'unlisted',
      want: { visibility: 'unlisted', source: 'user-default' },
    },
    {
      name: 'specimen override only → specimen-field',
      specOver: 'public',
      want: { visibility: 'public', source: 'specimen-field' },
    },
    {
      name: 'both set → specimen-field wins',
      specOver: 'public',
      ownerDef: 'private',
      want: { visibility: 'public', source: 'specimen-field' },
    },
  ];
  for (const field of ALL_SCALAR_FIELDS) {
    for (const s of setups) {
      it(`${field}/${s.name}`, () => {
        const spec: SpecimenLike = {};
        if (s.specOver !== undefined) {
          setScalarOverride(spec, field, s.specOver);
        }
        const owner: OwnerLike = {};
        if (s.ownerDef !== undefined) {
          owner.field_defaults = {};
          if (field === 'price') {
            owner.field_defaults.price = s.ownerDef;
          } else {
            owner.field_defaults.acquired_from = s.ownerDef;
          }
        }
        expect(resolveScalar(field, spec, owner)).toEqual(s.want);
      });
    }
  }
});

// Mirrors TestResolveImage_FullChainTransitions. Each row
// populates exactly the layers named; the helper must pick the
// highest-priority layer that is set.
describe('resolveImage: full chain transitions', () => {
  type Setup = {
    name: string;
    imgVis?: Visibility;
    specImg?: Visibility;
    specOverall?: Visibility;
    ownerImages?: Visibility;
    want: Resolution;
  };
  const setups: Setup[] = [
    {
      name: 'no layers → system default',
      want: { visibility: SystemDefault, source: 'system-default' },
    },
    {
      name: 'only user default',
      ownerImages: 'unlisted',
      want: { visibility: 'unlisted', source: 'user-default' },
    },
    {
      name: 'only specimen overall',
      specOverall: 'public',
      want: { visibility: 'public', source: 'specimen-overall' },
    },
    {
      name: 'specimen overall + user default → overall wins',
      specOverall: 'public',
      ownerImages: 'private',
      want: { visibility: 'public', source: 'specimen-overall' },
    },
    {
      name: 'only specimen-images field',
      specImg: 'unlisted',
      want: { visibility: 'unlisted', source: 'specimen-field' },
    },
    {
      name: 'specimen-images + overall → specimen-images wins',
      specImg: 'unlisted',
      specOverall: 'public',
      want: { visibility: 'unlisted', source: 'specimen-field' },
    },
    {
      name: 'image override only',
      imgVis: 'private',
      want: { visibility: 'private', source: 'image' },
    },
    {
      name: 'image override + every higher layer → image wins',
      imgVis: 'private',
      specImg: 'public',
      specOverall: 'public',
      ownerImages: 'public',
      want: { visibility: 'private', source: 'image' },
    },
  ];
  for (const s of setups) {
    it(s.name, () => {
      const spec: SpecimenLike = {};
      if (s.specOverall !== undefined) spec.visibility = s.specOverall;
      if (s.specImg !== undefined) spec.visibility_images = s.specImg;
      const owner: OwnerLike = {};
      if (s.ownerImages !== undefined) {
        owner.field_defaults = { images: s.ownerImages };
      }
      const img = s.imgVis !== undefined ? { visibility: s.imgVis } : {};
      expect(resolveImage(spec, owner, img)).toEqual(s.want);
    });
  }
});
