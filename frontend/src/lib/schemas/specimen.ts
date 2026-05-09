// Zod schemas for the specimen create/edit form (CONTRACT.md §7b).
//
// One base schema (fields shared by every type) plus three
// type-specific extensions that pin the `type` discriminator and
// the matching `type_data` shape. The form component validates
// against `specimenFormSchema` (a discriminated union) so error
// reporting respects the currently-selected type.
//
// The form binds raw HTML input values (every value enters as a
// string), so numeric / boolean / array fields go through
// `z.preprocess` to coerce empty strings to undefined and parse
// numbers. The output is then mapped to the API payload shape via
// `toCreateBody` / `toPatchBody`.
import { z } from 'zod';
import type { components } from '../api/schema';

type MineralData = components['schemas']['MineralData'];
type RockData = components['schemas']['RockData'];
type MeteoriteData = components['schemas']['MeteoriteData'];
type CreateSpecimenBody = components['schemas']['CreateSpecimenBody'];
type PatchSpecimenBody = components['schemas']['PatchSpecimenBody'];
type SpecimenView = components['schemas']['SpecimenView'];

// `''` / whitespace-only strings come from empty inputs — treat
// them as "field not provided" so optional zod schemas don't fire
// type errors against the empty string.
const emptyToUndef = (v: unknown): unknown =>
  typeof v === 'string' && v.trim() === '' ? undefined : v;

const optStr = z.preprocess(emptyToUndef, z.string().trim().optional());

// Numeric inputs come back as strings; convert and reject NaN.
const optNum = z.preprocess((v) => {
  if (v === undefined || v === null) return undefined;
  if (typeof v === 'number') return Number.isFinite(v) ? v : undefined;
  if (typeof v === 'string') {
    const trimmed = v.trim();
    if (trimmed === '') return undefined;
    const n = Number(trimmed);
    return Number.isFinite(n) ? n : trimmed; // pass-through string keeps zod's "expected number" error
  }
  return v;
}, z.number().optional());

// Comma-or-newline separated tag entry, e.g. "fluorite, calcite".
const optStrList = z.preprocess((v) => {
  if (v === undefined || v === null) return undefined;
  if (Array.isArray(v)) {
    const cleaned = v.map((x) => String(x).trim()).filter((x) => x.length > 0);
    return cleaned.length > 0 ? cleaned : undefined;
  }
  if (typeof v === 'string') {
    const cleaned = v
      .split(/[,\n]/)
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
    return cleaned.length > 0 ? cleaned : undefined;
  }
  return v;
}, z.array(z.string()).optional());

// Date inputs (`<input type="date">`) emit `YYYY-MM-DD`. We keep
// that on the wire for `acquired_at` (the server's spec marks it
// as date-time but the design treats it as date-only) — the API
// accepts either by ignoring the time component.
const optDate = optStr;

// Checkboxes return `true | false`; `undefined` only happens via
// preset state. Felte sometimes emits the literal string "true"
// for hidden inputs, so coerce defensively.
const optBool = z.preprocess((v) => {
  if (v === undefined || v === null) return undefined;
  if (typeof v === 'boolean') return v;
  if (v === 'true') return true;
  if (v === 'false' || v === '') return false;
  return v;
}, z.boolean().optional());

const visibilityEnum = z.enum(['private', 'unlisted', 'public']);

const localitySchema = z.object({
  country: optStr,
  region: optStr,
  site: optStr,
  lat: optNum.refine((n) => n === undefined || (n >= -90 && n <= 90), {
    message: 'latitude must be between -90 and 90',
  }),
  lon: optNum.refine((n) => n === undefined || (n >= -180 && n <= 180), {
    message: 'longitude must be between -180 and 180',
  }),
  mindat_id: optStr,
});

const dimensionsSchema = z.object({
  length_mm: optNum,
  width_mm: optNum,
  height_mm: optNum,
});

const baseFields = {
  name: z.preprocess(
    emptyToUndef,
    z
      .string({ required_error: 'name is required' })
      .trim()
      .min(1, 'name is required')
      .max(200, 'name is too long'),
  ),
  catalog_number: optStr,
  description: z.preprocess(emptyToUndef, z.string().optional()),
  visibility: visibilityEnum,
  acquired_at: optDate,
  acquired_from: optStr,
  price_dollars: optNum.refine((n) => n === undefined || n >= 0, {
    message: 'price must be ≥ 0',
  }),
  source_notes: optStr,
  locality_text: optStr,
  locality: localitySchema,
  mass_g: optNum.refine((n) => n === undefined || n >= 0, {
    message: 'mass must be ≥ 0',
  }),
  dimensions: dimensionsSchema,
} as const;

const mineralDataSchema = z.object({
  chemical_formula: optStr,
  mineral_species: optStrList,
  crystal_system: optStr,
  mohs_hardness: optNum.refine((n) => n === undefined || (n >= 0 && n <= 10), {
    message: 'Mohs hardness is on a 0–10 scale',
  }),
  color: optStr,
  luster: optStr,
  fluorescence: optStr,
  radioactive: optBool,
  mindat_id: optStr,
});

const rockDataSchema = z.object({
  rock_type: z.preprocess(
    emptyToUndef,
    z.enum(['igneous', 'sedimentary', 'metamorphic']).optional(),
  ),
  composition: optStr,
  formation_context: optStr,
});

const meteoriteDataSchema = z.object({
  classification: optStr,
  fall_or_find: z.preprocess(emptyToUndef, z.enum(['fall', 'find']).optional()),
  fall_or_find_date: optDate,
  official_name: optStr,
  total_known_weight_g: optNum.refine((n) => n === undefined || n >= 0, {
    message: 'weight must be ≥ 0',
  }),
  metbull_ref: optStr,
});

export const specimenFormSchema = z.discriminatedUnion('type', [
  z.object({ ...baseFields, type: z.literal('mineral'), type_data: mineralDataSchema }),
  z.object({ ...baseFields, type: z.literal('rock'), type_data: rockDataSchema }),
  z.object({ ...baseFields, type: z.literal('meteorite'), type_data: meteoriteDataSchema }),
]);

export type SpecimenFormValues = z.infer<typeof specimenFormSchema>;

// The form binds these raw shapes; zod parses to the typed values
// above on submit. Two distinct types because TypeScript widens
// inference too aggressively for the form initial values
// otherwise.
export type SpecimenFormInput = {
  type: 'mineral' | 'rock' | 'meteorite';
  name: string;
  catalog_number: string;
  description: string;
  visibility: 'private' | 'unlisted' | 'public';
  acquired_at: string;
  acquired_from: string;
  price_dollars: string;
  source_notes: string;
  locality_text: string;
  locality: {
    country: string;
    region: string;
    site: string;
    lat: string;
    lon: string;
    mindat_id: string;
  };
  mass_g: string;
  dimensions: {
    length_mm: string;
    width_mm: string;
    height_mm: string;
  };
  type_data: {
    chemical_formula: string;
    mineral_species: string;
    crystal_system: string;
    mohs_hardness: string;
    color: string;
    luster: string;
    fluorescence: string;
    radioactive: boolean;
    mindat_id: string;
    rock_type: '' | 'igneous' | 'sedimentary' | 'metamorphic';
    composition: string;
    formation_context: string;
    classification: string;
    fall_or_find: '' | 'fall' | 'find';
    fall_or_find_date: string;
    official_name: string;
    total_known_weight_g: string;
    metbull_ref: string;
  };
};

export function emptyTypeData(): SpecimenFormInput['type_data'] {
  return {
    chemical_formula: '',
    mineral_species: '',
    crystal_system: '',
    mohs_hardness: '',
    color: '',
    luster: '',
    fluorescence: '',
    radioactive: false,
    mindat_id: '',
    rock_type: '',
    composition: '',
    formation_context: '',
    classification: '',
    fall_or_find: '',
    fall_or_find_date: '',
    official_name: '',
    total_known_weight_g: '',
    metbull_ref: '',
  };
}

export function emptyFormInput(): SpecimenFormInput {
  return {
    type: 'mineral',
    name: '',
    catalog_number: '',
    description: '',
    visibility: 'private',
    acquired_at: '',
    acquired_from: '',
    price_dollars: '',
    source_notes: '',
    locality_text: '',
    locality: { country: '', region: '', site: '', lat: '', lon: '', mindat_id: '' },
    mass_g: '',
    dimensions: { length_mm: '', width_mm: '', height_mm: '' },
    type_data: emptyTypeData(),
  };
}

// Map a server SpecimenView into the flat form input shape used by
// the edit form. Missing fields render as empty strings; the
// PATCH-mapper later collapses them back into "omitted" keys.
export function fromSpecimenView(s: SpecimenView): SpecimenFormInput {
  const td = (s.type_data ?? {}) as Partial<MineralData & RockData & MeteoriteData>;
  const out = emptyFormInput();
  out.type = s.type;
  out.name = s.name;
  out.catalog_number = s.catalog_number ?? '';
  out.description = s.description;
  out.visibility = s.visibility;
  // SpecimenView ships acquired_at as a full RFC 3339 timestamp;
  // <input type="date"> wants `YYYY-MM-DD`.
  out.acquired_at = s.acquired_at ? s.acquired_at.slice(0, 10) : '';
  out.acquired_from = s.acquired_from ?? '';
  out.price_dollars = typeof s.price_cents === 'number' ? (s.price_cents / 100).toString() : '';
  out.source_notes = s.source_notes ?? '';
  out.locality_text = s.locality_text ?? '';
  const loc = s.locality ?? {};
  out.locality = {
    country: loc.country ?? '',
    region: loc.region ?? '',
    site: loc.site ?? '',
    lat: typeof loc.lat === 'number' ? String(loc.lat) : '',
    lon: typeof loc.lon === 'number' ? String(loc.lon) : '',
    mindat_id: loc.mindat_id ?? '',
  };
  out.mass_g = typeof s.mass_g === 'number' ? String(s.mass_g) : '';
  const d = s.dimensions ?? {};
  out.dimensions = {
    length_mm: typeof d.length_mm === 'number' ? String(d.length_mm) : '',
    width_mm: typeof d.width_mm === 'number' ? String(d.width_mm) : '',
    height_mm: typeof d.height_mm === 'number' ? String(d.height_mm) : '',
  };
  out.type_data = {
    ...emptyTypeData(),
    chemical_formula: td.chemical_formula ?? '',
    mineral_species: Array.isArray(td.mineral_species) ? td.mineral_species.join(', ') : '',
    crystal_system: td.crystal_system ?? '',
    mohs_hardness: typeof td.mohs_hardness === 'number' ? String(td.mohs_hardness) : '',
    color: td.color ?? '',
    luster: td.luster ?? '',
    fluorescence: td.fluorescence ?? '',
    radioactive: td.radioactive === true,
    mindat_id: td.mindat_id ?? '',
    rock_type: (td.rock_type as SpecimenFormInput['type_data']['rock_type']) ?? '',
    composition: td.composition ?? '',
    formation_context: td.formation_context ?? '',
    classification: td.classification ?? '',
    fall_or_find: (td.fall_or_find as SpecimenFormInput['type_data']['fall_or_find']) ?? '',
    fall_or_find_date: td.fall_or_find_date ? td.fall_or_find_date.slice(0, 10) : '',
    official_name: td.official_name ?? '',
    total_known_weight_g:
      typeof td.total_known_weight_g === 'number' ? String(td.total_known_weight_g) : '',
    metbull_ref: td.metbull_ref ?? '',
  };
  return out;
}

// Strip undefined / empty fields; the API treats omitted keys as
// "leave unchanged" on PATCH and "default" on POST.
function pruneObject<T extends Record<string, unknown>>(o: T): Partial<T> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(o)) {
    if (v === undefined) continue;
    if (typeof v === 'string' && v.length === 0) continue;
    if (typeof v === 'object' && v !== null && !Array.isArray(v)) {
      const nested = pruneObject(v as Record<string, unknown>);
      if (Object.keys(nested).length > 0) out[k] = nested;
      continue;
    }
    if (Array.isArray(v) && v.length === 0) continue;
    out[k] = v;
  }
  return out as Partial<T>;
}

function buildTypeData(values: SpecimenFormValues): Record<string, unknown> {
  const td = values.type_data;
  if (values.type === 'mineral') {
    return pruneObject({ ...td });
  }
  if (values.type === 'rock') {
    return pruneObject({ ...td });
  }
  // meteorite — `fall_or_find_date` accepts YYYY-MM-DD; the server
  // parses both date and date-time strings into the underlying
  // date column.
  return pruneObject({ ...td });
}

function buildBaseFields(values: SpecimenFormValues): Record<string, unknown> {
  const out: Record<string, unknown> = {
    name: values.name,
    visibility: values.visibility,
  };
  if (values.catalog_number !== undefined) out.catalog_number = values.catalog_number;
  if (values.description !== undefined) out.description = values.description;
  if (values.acquired_at !== undefined) out.acquired_at = values.acquired_at;
  if (values.acquired_from !== undefined) out.acquired_from = values.acquired_from;
  if (values.price_dollars !== undefined) {
    // Convert to cents on submit; round to nearest int to avoid
    // floating-point noise from 19.99 * 100.
    out.price_cents = Math.round(values.price_dollars * 100);
  }
  if (values.source_notes !== undefined) out.source_notes = values.source_notes;
  if (values.locality_text !== undefined) out.locality_text = values.locality_text;
  if (values.mass_g !== undefined) out.mass_g = values.mass_g;
  const loc = pruneObject(values.locality as Record<string, unknown>);
  if (Object.keys(loc).length > 0) out.locality = loc;
  const dims = pruneObject(values.dimensions as Record<string, unknown>);
  if (Object.keys(dims).length > 0) out.dimensions = dims;
  return out;
}

export function toCreateBody(values: SpecimenFormValues): CreateSpecimenBody {
  const td = buildTypeData(values);
  return {
    type: values.type,
    ...buildBaseFields(values),
    ...(Object.keys(td).length > 0 ? { type_data: td as CreateSpecimenBody['type_data'] } : {}),
  } as CreateSpecimenBody;
}

export function toPatchBody(values: SpecimenFormValues): PatchSpecimenBody {
  // PATCH leaves out `type` (immutable per CONTRACT.md §10) and
  // sends only the fields the form rendered. Empty strings have
  // already been collapsed to undefined upstream by the schema's
  // optStr preprocess, so they appear as "omitted" here.
  const td = buildTypeData(values);
  return {
    ...buildBaseFields(values),
    ...(Object.keys(td).length > 0 ? { type_data: td as PatchSpecimenBody['type_data'] } : {}),
  } as PatchSpecimenBody;
}
