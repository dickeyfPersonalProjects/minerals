// Property-based tests for the specimen form schema and its marshalling
// helpers (Q-2 R8). Three properties: round-trip idempotency, no spurious
// patch diffs, and rejection of whitespace-only names. fast-check defaults
// to 100 runs per property; no seed pinning required so far.

import { describe, expect, it } from 'vitest';
import * as fc from 'fast-check';
import { ZodFastCheck } from 'zod-fast-check';
import type { components } from '../api/schema';
import {
  emptyFormValues,
  formToCreateBody,
  formToPatchBody,
  rockDataSchema,
  specimenFormSchema,
  specimenToFormValues,
  type SpecimenFormValues,
  type SpecimenType,
} from './specimen';

type SpecimenView = components['schemas']['SpecimenView'];
type CreateBody = components['schemas']['CreateSpecimenBody'];

// Inflate a CreateSpecimenBody into a SpecimenView-shaped object so the
// round-trip (form → body → view → form) can reuse `specimenToFormValues`.
// The marshalling helpers do not read id/author_id/created_at/updated_at,
// so placeholder values are inert.
function bodyAsView(body: CreateBody): SpecimenView {
  return {
    id: '01HZX0000000000000000PROPS',
    author_id: '01HZX0000000000000000USER1',
    name: body.name,
    type: body.type,
    visibility: body.visibility ?? 'private',
    description: body.description ?? '',
    catalog_number: body.catalog_number ?? null,
    acquired_at: body.acquired_at ?? null,
    // acquired_from / price_cents are optional in the response shape
    // (mi-fo8 / mi-9ww — per-field visibility redaction). Round-trip
    // them as undefined when the body didn't supply a value.
    acquired_from: body.acquired_from ?? undefined,
    price_cents: body.price_cents ?? undefined,
    source_notes: body.source_notes ?? null,
    locality_text: body.locality_text ?? null,
    mass_g: body.mass_g ?? null,
    dimensions: body.dimensions ?? {},
    locality: body.locality ?? {},
    type_data: body.type_data ?? ({} as SpecimenView['type_data']),
    main_image_id: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  } satisfies SpecimenView;
}

// Form-values arbitrary built atop `emptyFormValues` so the defaulted
// fields are stable; only fields with non-trivial marshalling behavior
// are randomized.
const trimmedStr = (max = 30) => fc.string({ maxLength: max }).map((s) => s.trim());
const optNumStr = fc.oneof(fc.constant(''), fc.integer({ min: 0, max: 1_000_000 }).map(String));
const partialArb = fc.record({
  type: fc.constantFrom<SpecimenType>('mineral', 'rock', 'meteorite'),
  name: fc
    .string({ minLength: 1, maxLength: 50 })
    .map((s) => s.trim())
    .filter((s) => s.length > 0),
  catalog_number: trimmedStr(),
  description: trimmedStr(80),
  visibility: fc.constantFrom<'private' | 'unlisted' | 'public'>('private', 'unlisted', 'public'),
  acquired_at: fc.constantFrom('', '2024-06-15', '1969-09-28'),
  price_dollars: optNumStr,
  mass_g: optNumStr,
  length_mm: optNumStr,
  m_chemical_formula: trimmedStr(),
  m_mineral_species: fc
    .array(
      trimmedStr(10).filter((s) => s.length > 0 && !s.includes(',')),
      { maxLength: 3 },
    )
    .map((xs) => xs.join(', ')),
  m_radioactive: fc.boolean(),
  r_rock_type: fc.constantFrom<'' | 'igneous' | 'sedimentary' | 'metamorphic'>(
    '',
    'igneous',
    'sedimentary',
    'metamorphic',
  ),
  r_composition: trimmedStr(40),
  me_classification: trimmedStr(),
  me_fall_or_find: fc.constantFrom<'' | 'fall' | 'find'>('', 'fall', 'find'),
});
const formValuesArb: fc.Arbitrary<SpecimenFormValues> = partialArb.map((p) => ({
  ...emptyFormValues(p.type),
  ...p,
}));

describe('specimenFormSchema property tests', () => {
  it('form → body → view → form reaches a stable fixpoint on a second pass', () => {
    fc.assert(
      fc.property(formValuesArb, (values) => {
        // First pass normalizes (trims, drops empty fields, joins species);
        // a second pass must produce identical output.
        const v1 = specimenToFormValues(bodyAsView(formToCreateBody(values)));
        const v2 = specimenToFormValues(bodyAsView(formToCreateBody(v1)));
        expect(v2).toEqual(v1);
      }),
    );
  });

  it('formToPatchBody(view, specimenToFormValues(view)) emits no spurious diffs', () => {
    fc.assert(
      fc.property(
        formValuesArb.map((v) => bodyAsView(formToCreateBody(v))),
        (view) => {
          expect(formToPatchBody(view, specimenToFormValues(view))).toEqual({});
        },
      ),
    );
  });

  it('whitespace-only name does not survive validation', () => {
    fc.assert(
      fc.property(
        fc.string({ minLength: 1, maxLength: 50 }).filter((s) => s.length > 0 && s.trim() === ''),
        (ws) => {
          const v = { ...emptyFormValues('mineral'), name: ws };
          expect(specimenFormSchema.safeParse(v).success).toBe(false);
        },
      ),
    );
  });

  it('zod-fast-check inputs for rockDataSchema validate cleanly', () => {
    // Smoke test of the zod-fast-check integration: derived arbitraries
    // must satisfy the source schema they were derived from.
    const rockArb = ZodFastCheck().inputOf(rockDataSchema);
    fc.assert(
      fc.property(rockArb, (v) => {
        expect(rockDataSchema.safeParse(v).success).toBe(true);
      }),
    );
  });
});
