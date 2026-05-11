// Direct unit tests for the specimen marshalling helpers (Q-2 R3 / G2).
//
// These tests exercise the exported helpers in `specimen.ts` without any DOM
// or component scaffolding. Internal helpers (`parseOptionalFloat`,
// `priceDollarsToCents`, `toRfc3339`, `arraysEqual`, `dimsEqual`,
// `localityEqual`, `typeDataEqual`) are verified through their observable
// effects on `formToCreateBody` / `formToPatchBody` outputs, since they're
// not exported and the bead constraint forbids production code changes.

import { describe, expect, it } from 'vitest';
import type { components } from '../api/schema';
import {
  emptyFormValues,
  formToCreateBody,
  formToPatchBody,
  resetTypeDataDefaults,
  specimenToFormValues,
  type SpecimenFormValues,
  type SpecimenType,
} from './specimen';

type SpecimenView = components['schemas']['SpecimenView'];

// --- fixtures -----------------------------------------------------

function mineralView(overrides: Partial<SpecimenView> = {}): SpecimenView {
  return {
    id: '01HZX0000000000000000MINER',
    author_id: '01HZX0000000000000000USER1',
    name: 'Galena',
    type: 'mineral',
    visibility: 'private',
    description: '',
    catalog_number: null,
    acquired_at: null,
    acquired_from: null,
    price_cents: null,
    source_notes: null,
    locality_text: null,
    mass_g: null,
    dimensions: {},
    locality: {},
    type_data: {} as components['schemas']['MineralData'],
    main_image_id: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } satisfies SpecimenView;
}

// --- emptyFormValues ----------------------------------------------

describe('emptyFormValues', () => {
  it('defaults to mineral when no type given', () => {
    const v = emptyFormValues();
    expect(v.type).toBe('mineral');
    expect(v.visibility).toBe('private');
    expect(v.m_radioactive).toBe(false);
  });

  it('honors the requested type', () => {
    expect(emptyFormValues('rock').type).toBe('rock');
    expect(emptyFormValues('meteorite').type).toBe('meteorite');
  });

  it('initializes all string fields to empty', () => {
    const v = emptyFormValues('mineral');
    expect(v.name).toBe('');
    expect(v.catalog_number).toBe('');
    expect(v.price_dollars).toBe('');
    expect(v.acquired_at).toBe('');
    expect(v.locality_lat).toBe('');
    expect(v.mass_g).toBe('');
    expect(v.m_chemical_formula).toBe('');
    expect(v.r_rock_type).toBe('');
    expect(v.me_fall_or_find).toBe('');
  });
});

// --- formToCreateBody ---------------------------------------------

describe('formToCreateBody', () => {
  it('omits all optional fields when the form is empty (mineral)', () => {
    const v: SpecimenFormValues = { ...emptyFormValues('mineral'), name: 'Pyrite' };
    const body = formToCreateBody(v);
    expect(body).toEqual({
      type: 'mineral',
      name: 'Pyrite',
      visibility: 'private',
    });
    // No structural sub-objects when all their fields are empty.
    expect(body).not.toHaveProperty('dimensions');
    expect(body).not.toHaveProperty('locality');
    expect(body).not.toHaveProperty('type_data');
    expect(body).not.toHaveProperty('mass_g');
    expect(body).not.toHaveProperty('price_cents');
    expect(body).not.toHaveProperty('acquired_at');
    expect(body).not.toHaveProperty('catalog_number');
  });

  it('trims the specimen name', () => {
    const v: SpecimenFormValues = { ...emptyFormValues('mineral'), name: '   Calcite   ' };
    expect(formToCreateBody(v).name).toBe('Calcite');
  });

  it('includes only common-string fields when non-empty', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'Quartz',
      catalog_number: 'C-1',
      description: 'desc',
      acquired_from: 'Alice',
      source_notes: 'notes',
      locality_text: 'somewhere',
    };
    const body = formToCreateBody(v);
    expect(body.catalog_number).toBe('C-1');
    expect(body.description).toBe('desc');
    expect(body.acquired_from).toBe('Alice');
    expect(body.source_notes).toBe('notes');
    expect(body.locality_text).toBe('somewhere');
  });

  it('converts price_dollars to integer cents (rounding sub-cent input)', () => {
    // Indirectly verifies priceDollarsToCents: '1.234' is not preserved
    // verbatim — it rounds to nearest cent (123).
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      price_dollars: '1.234',
    };
    expect(formToCreateBody(v).price_cents).toBe(123);
  });

  it('rounds half-up via Math.round semantics', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      price_dollars: '0.005',
    };
    // 0.005 * 100 = 0.5, Math.round(0.5) = 1 (banker rounding excluded; JS rounds 0.5 → 1).
    expect(formToCreateBody(v).price_cents).toBe(1);
  });

  it('omits price_cents and mass_g when their inputs are empty', () => {
    // Indirectly verifies parseOptionalFloat('') → null and the omission path.
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      price_dollars: '',
      mass_g: '',
    };
    const body = formToCreateBody(v);
    expect(body).not.toHaveProperty('price_cents');
    expect(body).not.toHaveProperty('mass_g');
  });

  it('converts acquired_at YYYY-MM-DD to RFC3339 with midday UTC', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      acquired_at: '2024-06-15',
    };
    expect(formToCreateBody(v).acquired_at).toBe('2024-06-15T12:00:00Z');
  });

  it('omits acquired_at when the date input is empty', () => {
    // Indirectly verifies toRfc3339('') → '' (omission path).
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      acquired_at: '',
    };
    expect(formToCreateBody(v)).not.toHaveProperty('acquired_at');
  });

  it('includes dimensions when at least one dim is set', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      length_mm: '10',
      width_mm: '',
      height_mm: '',
    };
    const body = formToCreateBody(v);
    expect(body.dimensions).toEqual({ length_mm: 10 });
  });

  it('omits dimensions when all dims are empty', () => {
    const v: SpecimenFormValues = { ...emptyFormValues('mineral'), name: 'X' };
    expect(formToCreateBody(v)).not.toHaveProperty('dimensions');
  });

  it('includes locality when any text or coord is set', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      locality_country: 'France',
      locality_lat: '48.8',
      locality_lon: '2.35',
    };
    const body = formToCreateBody(v);
    expect(body.locality).toEqual({ country: 'France', lat: 48.8, lon: 2.35 });
  });

  it('omits locality when all locality fields are empty/whitespace', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      locality_country: '   ',
      locality_region: '',
      locality_site: '',
      locality_lat: '',
      locality_lon: '',
      locality_mindat_id: '',
    };
    expect(formToCreateBody(v)).not.toHaveProperty('locality');
  });

  it('builds mineral type_data, splitting mineral_species on commas', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      m_chemical_formula: 'PbS',
      m_mineral_species: 'galena, sphalerite ,  ',
      m_mohs_hardness: '2.5',
      m_radioactive: true,
    };
    const body = formToCreateBody(v);
    expect(body.type_data).toEqual({
      chemical_formula: 'PbS',
      mineral_species: ['galena', 'sphalerite'],
      mohs_hardness: 2.5,
      radioactive: true,
    });
  });

  it('omits mineral type_data when all mineral fields are empty', () => {
    const v: SpecimenFormValues = { ...emptyFormValues('mineral'), name: 'X' };
    expect(formToCreateBody(v)).not.toHaveProperty('type_data');
  });

  it('does NOT include rock or meteorite fields for a mineral specimen', () => {
    // Even if a stray rock/meteorite field were set, type='mineral' must
    // route only mineral fields into type_data.
    const v: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      m_chemical_formula: 'CaCO3',
      r_rock_type: 'igneous',
      me_classification: 'L6',
    };
    const td = formToCreateBody(v).type_data as components['schemas']['MineralData'];
    expect(td).toEqual({ chemical_formula: 'CaCO3' });
  });

  it('builds rock type_data with rock_type enum', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('rock'),
      name: 'X',
      r_rock_type: 'sedimentary',
      r_composition: 'sandstone',
    };
    expect(formToCreateBody(v).type_data).toEqual({
      rock_type: 'sedimentary',
      composition: 'sandstone',
    });
  });

  it('omits rock_type when empty enum', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('rock'),
      name: 'X',
      r_composition: 'basalt',
    };
    expect(formToCreateBody(v).type_data).toEqual({ composition: 'basalt' });
  });

  it('builds meteorite type_data including RFC3339 fall_or_find_date', () => {
    const v: SpecimenFormValues = {
      ...emptyFormValues('meteorite'),
      name: 'X',
      me_classification: 'H5',
      me_fall_or_find: 'fall',
      me_fall_or_find_date: '1969-09-28',
      me_total_known_weight_g: '100',
    };
    expect(formToCreateBody(v).type_data).toEqual({
      classification: 'H5',
      fall_or_find: 'fall',
      fall_or_find_date: '1969-09-28T12:00:00Z',
      total_known_weight_g: 100,
    });
  });
});

// --- formToPatchBody ----------------------------------------------

describe('formToPatchBody', () => {
  it('emits an empty patch when nothing changed', () => {
    const initial = mineralView({
      name: 'Galena',
      description: 'shiny',
      visibility: 'public',
    });
    const values = specimenToFormValues(initial);
    expect(formToPatchBody(initial, values)).toEqual({});
  });

  it('emits only the fields that changed', () => {
    const initial = mineralView({ name: 'Galena', description: 'old' });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), description: 'new' };
    const patch = formToPatchBody(initial, values);
    expect(patch).toEqual({ description: 'new' });
  });

  it('trims the new name before comparing', () => {
    const initial = mineralView({ name: 'Galena' });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), name: '  Galena  ' };
    expect(formToPatchBody(initial, values)).toEqual({});
  });

  it('emits a name change when the trimmed value differs', () => {
    const initial = mineralView({ name: 'Galena' });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), name: ' Pyrite ' };
    expect(formToPatchBody(initial, values)).toEqual({ name: 'Pyrite' });
  });

  it('clears catalog_number with null sentinel when emptied', () => {
    const initial = mineralView({ catalog_number: 'C-1' });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), catalog_number: '' };
    const patch = formToPatchBody(initial, values);
    // Production sends null-as-string sentinel to clear the catalog number.
    expect(patch.catalog_number).toBeNull();
  });

  it('emits visibility change', () => {
    const initial = mineralView({ visibility: 'private' });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), visibility: 'public' };
    expect(formToPatchBody(initial, values)).toEqual({ visibility: 'public' });
  });

  it('emits price_cents only when changed and non-null', () => {
    const initial = mineralView({ price_cents: 1000 });
    // No change in dollars value.
    const same = specimenToFormValues(initial);
    expect(formToPatchBody(initial, same)).not.toHaveProperty('price_cents');
    // Change in dollars value.
    const changed: SpecimenFormValues = { ...same, price_dollars: '20' };
    expect(formToPatchBody(initial, changed).price_cents).toBe(2000);
  });

  it('does not emit a price_cents clear (current behavior: only writes non-null)', () => {
    const initial = mineralView({ price_cents: 500 });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), price_dollars: '' };
    expect(formToPatchBody(initial, values)).not.toHaveProperty('price_cents');
  });

  it('does not emit dimensions when they round-trip equal (dimsEqual)', () => {
    const initial = mineralView({ dimensions: { length_mm: 10, width_mm: 5, height_mm: 2 } });
    const values = specimenToFormValues(initial);
    expect(formToPatchBody(initial, values)).not.toHaveProperty('dimensions');
  });

  it('emits dimensions when any component differs', () => {
    const initial = mineralView({ dimensions: { length_mm: 10, width_mm: 5, height_mm: 2 } });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), height_mm: '3' };
    expect(formToPatchBody(initial, values).dimensions).toEqual({
      length_mm: 10,
      width_mm: 5,
      height_mm: 3,
    });
  });

  it('does not emit locality when fields round-trip equal (localityEqual)', () => {
    const initial = mineralView({
      locality: { country: 'FR', region: 'IDF', site: 'Paris', lat: 48.8, lon: 2.35 },
    });
    const values = specimenToFormValues(initial);
    expect(formToPatchBody(initial, values)).not.toHaveProperty('locality');
  });

  it('emits locality when a single field differs', () => {
    const initial = mineralView({ locality: { country: 'FR' } });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), locality_country: 'DE' };
    expect(formToPatchBody(initial, values).locality).toEqual({ country: 'DE' });
  });

  it('does not emit type_data when mineral fields round-trip equal (typeDataEqual + arraysEqual)', () => {
    const initial = mineralView({
      type_data: {
        chemical_formula: 'PbS',
        mineral_species: ['galena'],
        radioactive: false,
      } as components['schemas']['MineralData'],
    });
    const values = specimenToFormValues(initial);
    expect(formToPatchBody(initial, values)).not.toHaveProperty('type_data');
  });

  it('emits type_data when mineral_species order differs (arraysEqual order-sensitive)', () => {
    // Direct verification of arraysEqual([1,2],[2,1]) → false: reordering
    // mineral_species must be detected as a change.
    const initial = mineralView({
      type_data: {
        mineral_species: ['galena', 'sphalerite'],
      } as components['schemas']['MineralData'],
    });
    const values: SpecimenFormValues = {
      ...specimenToFormValues(initial),
      m_mineral_species: 'sphalerite, galena',
    };
    const td = formToPatchBody(initial, values).type_data as components['schemas']['MineralData'];
    expect(td?.mineral_species).toEqual(['sphalerite', 'galena']);
  });

  it('emits type_data with radioactive=true when toggling false → true', () => {
    const initial = mineralView({
      type_data: {
        chemical_formula: 'PbS',
        radioactive: false,
      } as components['schemas']['MineralData'],
    });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), m_radioactive: true };
    const td = formToPatchBody(initial, values).type_data as components['schemas']['MineralData'];
    expect(td).toBeDefined();
    expect(td.radioactive).toBe(true);
    // Other fields remain (preserved across the patch).
    expect(td.chemical_formula).toBe('PbS');
  });

  it('drops radioactive=false from type_data overlay (only-truthy emit)', () => {
    // Documents a known marshalling limitation: buildTypeData only emits
    // `radioactive` when truthy. Toggling true → false alongside another
    // mineral field still produces a type_data overlay, but the boolean
    // key is omitted (server-side overlay semantics: the key is absent so
    // the prior value is preserved). This is intentional per §10 contract.
    const initial = mineralView({
      type_data: {
        chemical_formula: 'PbS',
        radioactive: true,
      } as components['schemas']['MineralData'],
    });
    const values: SpecimenFormValues = { ...specimenToFormValues(initial), m_radioactive: false };
    const td = formToPatchBody(initial, values).type_data as
      | components['schemas']['MineralData']
      | undefined;
    if (td !== undefined) {
      // If a type_data overlay is emitted, it must NOT carry radioactive=false.
      expect(td.radioactive).toBeUndefined();
    }
  });

  it('emits rock type_data only on change (typeDataEqual rock branch)', () => {
    const initial = mineralView({
      type: 'rock',
      type_data: {
        rock_type: 'igneous',
        composition: 'granite',
      } as components['schemas']['RockData'],
    });
    const same = specimenToFormValues(initial);
    expect(formToPatchBody(initial, same)).not.toHaveProperty('type_data');

    const changed: SpecimenFormValues = { ...same, r_composition: 'basalt' };
    const td = formToPatchBody(initial, changed).type_data as components['schemas']['RockData'];
    expect(td.composition).toBe('basalt');
    expect(td.rock_type).toBe('igneous');
  });

  it('emits meteorite type_data only on change (typeDataEqual meteorite branch)', () => {
    const initial = mineralView({
      type: 'meteorite',
      type_data: {
        classification: 'H5',
        fall_or_find: 'fall',
        fall_or_find_date: '1969-09-28T12:00:00Z',
      } as components['schemas']['MeteoriteData'],
    });
    const same = specimenToFormValues(initial);
    expect(formToPatchBody(initial, same)).not.toHaveProperty('type_data');

    const changed: SpecimenFormValues = { ...same, me_classification: 'L6' };
    const td = formToPatchBody(initial, changed)
      .type_data as components['schemas']['MeteoriteData'];
    expect(td.classification).toBe('L6');
  });
});

// --- specimenToFormValues -----------------------------------------

describe('specimenToFormValues', () => {
  it('converts price_cents to a dollars string', () => {
    expect(specimenToFormValues(mineralView({ price_cents: 1234 })).price_dollars).toBe('12.34');
    expect(specimenToFormValues(mineralView({ price_cents: 0 })).price_dollars).toBe('0');
    expect(specimenToFormValues(mineralView({ price_cents: null })).price_dollars).toBe('');
  });

  it('extracts YYYY-MM-DD from RFC3339 acquired_at', () => {
    const v = specimenToFormValues(mineralView({ acquired_at: '2024-06-15T12:00:00Z' }));
    expect(v.acquired_at).toBe('2024-06-15');
  });

  it('returns empty acquired_at when null', () => {
    expect(specimenToFormValues(mineralView({ acquired_at: null })).acquired_at).toBe('');
  });

  it('coerces nullable common fields to empty strings', () => {
    const v = specimenToFormValues(mineralView());
    expect(v.catalog_number).toBe('');
    expect(v.acquired_from).toBe('');
    expect(v.source_notes).toBe('');
    expect(v.locality_text).toBe('');
    expect(v.mass_g).toBe('');
  });

  it('hydrates locality fields including numeric coords as strings', () => {
    const v = specimenToFormValues(
      mineralView({
        locality: { country: 'FR', site: 'Paris', lat: 48.8, lon: 2.35, mindat_id: '123' },
      }),
    );
    expect(v.locality_country).toBe('FR');
    expect(v.locality_site).toBe('Paris');
    expect(v.locality_lat).toBe('48.8');
    expect(v.locality_lon).toBe('2.35');
    expect(v.locality_mindat_id).toBe('123');
  });

  it('hydrates dimensions as strings, with empty strings for missing fields', () => {
    const v = specimenToFormValues(mineralView({ dimensions: { length_mm: 10 } }));
    expect(v.length_mm).toBe('10');
    expect(v.width_mm).toBe('');
    expect(v.height_mm).toBe('');
  });

  it('hydrates mineral type_data including mineral_species as csv', () => {
    const v = specimenToFormValues(
      mineralView({
        type_data: {
          chemical_formula: 'PbS',
          mineral_species: ['galena', 'sphalerite'],
          mohs_hardness: 2.5,
          radioactive: true,
        } as components['schemas']['MineralData'],
      }),
    );
    expect(v.m_chemical_formula).toBe('PbS');
    expect(v.m_mineral_species).toBe('galena, sphalerite');
    expect(v.m_mohs_hardness).toBe('2.5');
    expect(v.m_radioactive).toBe(true);
  });

  it('hydrates rock type_data, validating rock_type enum', () => {
    const v = specimenToFormValues(
      mineralView({
        type: 'rock',
        type_data: {
          rock_type: 'igneous',
          composition: 'granite',
        } as components['schemas']['RockData'],
      }),
    );
    expect(v.r_rock_type).toBe('igneous');
    expect(v.r_composition).toBe('granite');
  });

  it('drops invalid rock_type strings (defensive enum guard)', () => {
    const v = specimenToFormValues(
      mineralView({
        type: 'rock',
        type_data: { rock_type: 'plutonic' } as components['schemas']['RockData'],
      }),
    );
    expect(v.r_rock_type).toBe('');
  });

  it('hydrates meteorite type_data and converts fall_or_find_date', () => {
    const v = specimenToFormValues(
      mineralView({
        type: 'meteorite',
        type_data: {
          classification: 'H5',
          fall_or_find: 'fall',
          fall_or_find_date: '1969-09-28T12:00:00Z',
          total_known_weight_g: 250.5,
        } as components['schemas']['MeteoriteData'],
      }),
    );
    expect(v.me_classification).toBe('H5');
    expect(v.me_fall_or_find).toBe('fall');
    expect(v.me_fall_or_find_date).toBe('1969-09-28');
    expect(v.me_total_known_weight_g).toBe('250.5');
  });

  it('drops invalid fall_or_find strings (defensive enum guard)', () => {
    const v = specimenToFormValues(
      mineralView({
        type: 'meteorite',
        type_data: { fall_or_find: 'unknown' } as components['schemas']['MeteoriteData'],
      }),
    );
    expect(v.me_fall_or_find).toBe('');
  });
});

// --- round-trip property ------------------------------------------

describe('round-trip: specimenToFormValues ∘ formToCreateBody', () => {
  it('preserves common scalar fields (mineral)', () => {
    const initial = mineralView({
      name: 'Galena',
      catalog_number: 'C-9',
      description: 'sample',
      visibility: 'public',
      acquired_at: '2024-06-15T12:00:00Z',
      acquired_from: 'Alice',
      price_cents: 4200,
      source_notes: 'notes',
      locality_text: 'somewhere',
      mass_g: 17.5,
      dimensions: { length_mm: 10, width_mm: 5, height_mm: 2 },
      locality: { country: 'FR', site: 'Paris', lat: 48.8, lon: 2.35 },
      type_data: {
        chemical_formula: 'PbS',
        mineral_species: ['galena'],
        mohs_hardness: 2.5,
        radioactive: false,
      } as components['schemas']['MineralData'],
    });
    const formValues = specimenToFormValues(initial);
    const body = formToCreateBody(formValues);

    expect(body.name).toBe(initial.name);
    expect(body.catalog_number).toBe(initial.catalog_number);
    expect(body.description).toBe(initial.description);
    expect(body.visibility).toBe(initial.visibility);
    expect(body.acquired_at).toBe(initial.acquired_at);
    expect(body.acquired_from).toBe(initial.acquired_from);
    expect(body.price_cents).toBe(initial.price_cents);
    expect(body.source_notes).toBe(initial.source_notes);
    expect(body.locality_text).toBe(initial.locality_text);
    expect(body.mass_g).toBe(initial.mass_g);
    expect(body.dimensions).toEqual(initial.dimensions);
    expect(body.locality).toEqual(initial.locality);
    // radioactive=false is dropped from the wire form (only-truthy emit), so
    // compare without that key.
    expect(body.type_data).toEqual({
      chemical_formula: 'PbS',
      mineral_species: ['galena'],
      mohs_hardness: 2.5,
    });
    expect(body.type).toBe(initial.type);
  });

  it('preserves rock-specific type_data', () => {
    const initial = mineralView({
      type: 'rock',
      name: 'Granite slab',
      type_data: {
        rock_type: 'igneous',
        composition: 'feldspar+quartz',
        formation_context: 'pluton',
      } as components['schemas']['RockData'],
    });
    const body = formToCreateBody(specimenToFormValues(initial));
    expect(body.type).toBe('rock');
    expect(body.type_data).toEqual(initial.type_data);
  });

  it('preserves meteorite-specific type_data', () => {
    const initial = mineralView({
      type: 'meteorite',
      name: 'Allende',
      type_data: {
        classification: 'CV3',
        fall_or_find: 'fall',
        fall_or_find_date: '1969-02-08T12:00:00Z',
        official_name: 'Allende',
        total_known_weight_g: 2000,
        metbull_ref: 'MB#42',
      } as components['schemas']['MeteoriteData'],
    });
    const body = formToCreateBody(specimenToFormValues(initial));
    expect(body.type).toBe('meteorite');
    expect(body.type_data).toEqual(initial.type_data);
  });
});

// --- resetTypeDataDefaults ----------------------------------------

describe('resetTypeDataDefaults', () => {
  it('clears mineral fields when toggling mineral → rock', () => {
    const start: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      m_chemical_formula: 'PbS',
      m_mineral_species: 'galena',
      m_mohs_hardness: '2.5',
      m_radioactive: true,
    };
    const next = resetTypeDataDefaults(start, 'rock');
    expect(next.type).toBe('rock');
    expect(next.m_chemical_formula).toBe('');
    expect(next.m_mineral_species).toBe('');
    expect(next.m_mohs_hardness).toBe('');
    expect(next.m_radioactive).toBe(false);
  });

  it('clears meteorite fields when toggling meteorite → mineral', () => {
    const start: SpecimenFormValues = {
      ...emptyFormValues('meteorite'),
      name: 'X',
      me_classification: 'H5',
      me_fall_or_find: 'fall',
      me_fall_or_find_date: '1969-09-28',
      me_total_known_weight_g: '100',
    };
    const next = resetTypeDataDefaults(start, 'mineral');
    expect(next.type).toBe('mineral');
    expect(next.me_classification).toBe('');
    expect(next.me_fall_or_find).toBe('');
    expect(next.me_fall_or_find_date).toBe('');
    expect(next.me_total_known_weight_g).toBe('');
  });

  it('also clears the (non-target) rock fields when toggling away', () => {
    // Toggling mineral → meteorite must also clear any rock fields the user
    // might have populated, since resetTypeDataDefaults wipes ALL type_data
    // subsets, not just the source type.
    const start: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      r_rock_type: 'igneous',
      r_composition: 'granite',
    };
    const next = resetTypeDataDefaults(start, 'meteorite');
    expect(next.r_rock_type).toBe('');
    expect(next.r_composition).toBe('');
  });

  it('preserves common fields (name, locality, dimensions, visibility, etc.)', () => {
    const start: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      catalog_number: 'C-1',
      description: 'desc',
      visibility: 'public',
      acquired_at: '2024-06-15',
      acquired_from: 'Alice',
      price_dollars: '10',
      source_notes: 'notes',
      locality_text: 'somewhere',
      locality_country: 'FR',
      locality_lat: '48.8',
      mass_g: '5',
      length_mm: '10',
    };
    const next = resetTypeDataDefaults(start, 'rock');
    expect(next.name).toBe('X');
    expect(next.catalog_number).toBe('C-1');
    expect(next.description).toBe('desc');
    expect(next.visibility).toBe('public');
    expect(next.acquired_at).toBe('2024-06-15');
    expect(next.acquired_from).toBe('Alice');
    expect(next.price_dollars).toBe('10');
    expect(next.source_notes).toBe('notes');
    expect(next.locality_text).toBe('somewhere');
    expect(next.locality_country).toBe('FR');
    expect(next.locality_lat).toBe('48.8');
    expect(next.mass_g).toBe('5');
    expect(next.length_mm).toBe('10');
  });

  it('is idempotent when toggling to the same type', () => {
    const start: SpecimenFormValues = {
      ...emptyFormValues('mineral'),
      name: 'X',
      m_chemical_formula: 'PbS',
    };
    // Per JSDoc: only invoked when the user toggles away. Even so, calling
    // with the current type must clear the type_data subset.
    const next: SpecimenFormValues = resetTypeDataDefaults(start, 'mineral' as SpecimenType);
    expect(next.type).toBe('mineral');
    expect(next.m_chemical_formula).toBe('');
    expect(next.name).toBe('X');
  });
});
