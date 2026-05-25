// Zod schemas for the specimen create/edit form (CONTRACT.md §7b
// pre-approved libraries; mi-qvg / D-1).
//
// Form values are stored flat as strings/booleans because HTML
// inputs work in strings. Numeric/date fields are validated as
// strings that *would* parse cleanly; conversion to the API body
// happens in `formToCreateBody` / `formToPatchBody`.
//
// One base schema (shared common fields) + three type-specific
// extensions for `type_data`. The form runs the base schema for
// validation; the type-specific schemas live alongside so the
// type-data fields and their constraints are co-located with the
// form.

import { z } from 'zod';
import type { components } from '../api/schema';

// --- helpers -------------------------------------------------------

const trimmed = z.string().transform((s) => s.trim());

// Optional numeric: accepts the form's "stringly typed" empty
// state ('' / null) plus any string or number that parses as a
// finite number (predicate gates the accepted range). felte's DOM
// adapter coerces `<input type="number">` to a `number` (or `null`
// when empty), so the schema must accept both shapes.
function optionalNumber(message: string, predicate?: (n: number) => boolean) {
  return z.union([z.string(), z.number(), z.null()]).refine(
    (raw) => {
      if (raw === null) return true;
      if (typeof raw === 'number') {
        if (!Number.isFinite(raw)) return false;
        return predicate ? predicate(raw) : true;
      }
      if (raw === '' || raw.trim() === '') return true;
      const n = Number(raw);
      if (!Number.isFinite(n)) return false;
      return predicate ? predicate(n) : true;
    },
    { message },
  );
}

// Optional date: accepts empty and null (felte coerces empty
// `<input type="date">` to '' — but be defensive); non-empty
// strings must parse as a valid date.
const optionalDate = z.union([z.string(), z.null()]).refine(
  (s) => {
    if (s === null || s === '') return true;
    const t = Date.parse(s);
    return Number.isFinite(t);
  },
  { message: 'Enter a valid date (YYYY-MM-DD)' },
);

// --- type-specific schemas ----------------------------------------

// Closed UV-fluorescence color vocabulary (mi-qas). Kept in lockstep
// with internal/domain.ValidFluorescenceColors on the Go side; only
// colors that genuinely occur in mineral UV fluorescence are listed.
// "None" is represented by an empty selection (null on the wire), not
// a sentinel string.
export const FLUORESCENCE_COLORS = [
  'Red',
  'Orange',
  'Yellow',
  'Green',
  'Blue',
  'Violet',
  'Pink',
  'White',
  'Cream',
  'Blue-green',
  'Blue-violet',
  'Red-orange',
  'Orange-yellow',
  'Greenish-yellow',
  'Cherry red',
] as const;

export type FluorescenceColor = (typeof FLUORESCENCE_COLORS)[number];

const fluorescenceColorSet = new Set<string>(FLUORESCENCE_COLORS);

const fluorescenceColorList = z
  .array(z.string())
  .refine((xs) => xs.every((c) => fluorescenceColorSet.has(c)), {
    message: `Each color must be one of: ${FLUORESCENCE_COLORS.join(', ')}`,
  });

export const mineralDataSchema = z.object({
  m_chemical_formula: z.string().max(200, 'Too long'),
  m_mineral_species: z.string().max(500, 'Too long'),
  m_crystal_system: z.string().max(50, 'Too long'),
  m_mohs_hardness: optionalNumber('Mohs hardness must be 0–10', (n) => n >= 0 && n <= 10),
  m_color: z.string().max(100, 'Too long'),
  m_luster: z.string().max(100, 'Too long'),
  m_fluorescence_sw: fluorescenceColorList,
  m_fluorescence_mw: fluorescenceColorList,
  m_fluorescence_lw: fluorescenceColorList,
  m_radioactive: z.boolean(),
  m_magnetic: z.boolean(),
  m_reacts_to_acid: z.boolean(),
  m_mindat_id: z.string().max(50, 'Too long'),
});

export const rockDataSchema = z.object({
  r_rock_type: z.enum(['', 'igneous', 'sedimentary', 'metamorphic']),
  r_composition: z.string().max(500, 'Too long'),
  r_formation_context: z.string().max(500, 'Too long'),
});

export const meteoriteDataSchema = z.object({
  me_classification: z.string().max(50, 'Too long'),
  me_fall_or_find: z.enum(['', 'fall', 'find']),
  me_fall_or_find_date: optionalDate,
  me_official_name: z.string().max(200, 'Too long'),
  me_total_known_weight_g: optionalNumber('Must be a positive number', (n) => n >= 0),
  me_metbull_ref: z.string().max(50, 'Too long'),
});

export const fossilDataSchema = z.object({
  f_taxon: z.string().max(200, 'Too long'),
  f_taxonomic_group: z.string().max(200, 'Too long'),
  f_geologic_period: z.string().max(100, 'Too long'),
  f_formation: z.string().max(200, 'Too long'),
  f_locality: z.string().max(500, 'Too long'),
  f_preservation_type: z.string().max(100, 'Too long'),
  f_completeness: z.string().max(100, 'Too long'),
  f_prepared: z.boolean(),
  f_prep_notes: z.string().max(2000, 'Too long'),
});

// --- base schema --------------------------------------------------

// Sentinel for the per-field visibility selectors: the user has not
// set a specimen-level override, so the chain falls through to the
// owner's account default. On the wire this maps to JSON null; in the
// form it's stored as a literal string because <select value> can't
// represent null and an empty string risks colliding with a future
// enum value.
export const VISIBILITY_INHERIT = '__inherit__' as const;
export const visibilityFieldSchema = z.enum([VISIBILITY_INHERIT, 'private', 'unlisted', 'public']);
export type VisibilityFieldValue = z.infer<typeof visibilityFieldSchema>;

export const specimenBaseSchema = z.object({
  type: z.enum(['mineral', 'rock', 'meteorite', 'fossil']),
  name: trimmed.pipe(z.string().min(1, 'Name is required').max(200, 'Name is too long')),
  catalog_number: z.string().max(100, 'Catalog number is too long'),
  description: z.string().max(50_000, 'Description is too long'),
  visibility: z.enum(['private', 'unlisted', 'public']),
  acquired_at: optionalDate,
  acquired_from: z.string().max(200, 'Too long'),
  price_dollars: optionalNumber('Must be a positive number', (n) => n >= 0),
  source_notes: z.string().max(2000, 'Too long'),
  locality_text: z.string().max(500, 'Too long'),

  locality_country: z.string().max(100, 'Too long'),
  locality_region: z.string().max(200, 'Too long'),
  locality_site: z.string().max(200, 'Too long'),
  locality_lat: optionalNumber('Latitude must be -90..90', (n) => n >= -90 && n <= 90),
  locality_lon: optionalNumber('Longitude must be -180..180', (n) => n >= -180 && n <= 180),
  locality_mindat_id: z.string().max(50, 'Too long'),

  mass_g: optionalNumber('Must be a positive number', (n) => n >= 0),
  length_mm: optionalNumber('Must be a positive number', (n) => n >= 0),
  width_mm: optionalNumber('Must be a positive number', (n) => n >= 0),
  height_mm: optionalNumber('Must be a positive number', (n) => n >= 0),

  // Per-field visibility overrides (mi-fo8 #7). __inherit__ means
  // "no specimen-level override — fall through to the owner default".
  visibility_price: visibilityFieldSchema,
  visibility_acquired_from: visibilityFieldSchema,
  visibility_images: visibilityFieldSchema,

  // Owner-only physical-label tracking flag (mi-n28q). Not type-specific;
  // applies to all specimen kinds. Omitted from the API response for
  // non-owners, so on the edit form it starts false and is only sent
  // when the owner explicitly toggles it.
  tagged: z.boolean(),
});

// Full schema = base + all per-type fields. We always validate the
// full set; fields for the wrong type are simply ignored when we
// build the API body.
export const specimenFormSchema = specimenBaseSchema
  .merge(mineralDataSchema)
  .merge(rockDataSchema)
  .merge(meteoriteDataSchema)
  .merge(fossilDataSchema);

export type SpecimenFormValues = z.infer<typeof specimenFormSchema>;

export type SpecimenType = SpecimenFormValues['type'];

// --- defaults / serialization ------------------------------------

type SpecimenView = components['schemas']['SpecimenView'];
type CreateBody = components['schemas']['CreateSpecimenBody'];
type PatchBody = components['schemas']['PatchSpecimenBody'];
type MineralData = components['schemas']['MineralData'];
type RockData = components['schemas']['RockData'];
type MeteoriteData = components['schemas']['MeteoriteData'];
type FossilData = components['schemas']['FossilData'];

export function emptyFormValues(type: SpecimenType = 'mineral'): SpecimenFormValues {
  return {
    type,
    name: '',
    catalog_number: '',
    description: '',
    visibility: 'private',
    acquired_at: '',
    acquired_from: '',
    price_dollars: '',
    source_notes: '',
    locality_text: '',
    locality_country: '',
    locality_region: '',
    locality_site: '',
    locality_lat: '',
    locality_lon: '',
    locality_mindat_id: '',
    mass_g: '',
    length_mm: '',
    width_mm: '',
    height_mm: '',
    m_chemical_formula: '',
    m_mineral_species: '',
    m_crystal_system: '',
    m_mohs_hardness: '',
    m_color: '',
    m_luster: '',
    m_fluorescence_sw: [],
    m_fluorescence_mw: [],
    m_fluorescence_lw: [],
    m_radioactive: false,
    m_magnetic: false,
    m_reacts_to_acid: false,
    m_mindat_id: '',
    r_rock_type: '',
    r_composition: '',
    r_formation_context: '',
    me_classification: '',
    me_fall_or_find: '',
    me_fall_or_find_date: '',
    me_official_name: '',
    me_total_known_weight_g: '',
    me_metbull_ref: '',
    f_taxon: '',
    f_taxonomic_group: '',
    f_geologic_period: '',
    f_formation: '',
    f_locality: '',
    f_preservation_type: '',
    f_completeness: '',
    f_prepared: false,
    f_prep_notes: '',
    visibility_price: VISIBILITY_INHERIT,
    visibility_acquired_from: VISIBILITY_INHERIT,
    visibility_images: VISIBILITY_INHERIT,
    tagged: false,
  };
}

// Reset only the type_data subset of the form to the empty defaults
// for the new type (called when the user toggles `type` in create
// mode — edit mode disables `type` so this never fires there).
export function resetTypeDataDefaults(
  values: SpecimenFormValues,
  newType: SpecimenType,
): SpecimenFormValues {
  const empty = emptyFormValues(newType);
  return {
    ...values,
    type: newType,
    m_chemical_formula: empty.m_chemical_formula,
    m_mineral_species: empty.m_mineral_species,
    m_crystal_system: empty.m_crystal_system,
    m_mohs_hardness: empty.m_mohs_hardness,
    m_color: empty.m_color,
    m_luster: empty.m_luster,
    m_fluorescence_sw: empty.m_fluorescence_sw,
    m_fluorescence_mw: empty.m_fluorescence_mw,
    m_fluorescence_lw: empty.m_fluorescence_lw,
    m_radioactive: empty.m_radioactive,
    m_magnetic: empty.m_magnetic,
    m_reacts_to_acid: empty.m_reacts_to_acid,
    m_mindat_id: empty.m_mindat_id,
    r_rock_type: empty.r_rock_type,
    r_composition: empty.r_composition,
    r_formation_context: empty.r_formation_context,
    me_classification: empty.me_classification,
    me_fall_or_find: empty.me_fall_or_find,
    me_fall_or_find_date: empty.me_fall_or_find_date,
    me_official_name: empty.me_official_name,
    me_total_known_weight_g: empty.me_total_known_weight_g,
    me_metbull_ref: empty.me_metbull_ref,
    f_taxon: empty.f_taxon,
    f_taxonomic_group: empty.f_taxonomic_group,
    f_geologic_period: empty.f_geologic_period,
    f_formation: empty.f_formation,
    f_locality: empty.f_locality,
    f_preservation_type: empty.f_preservation_type,
    f_completeness: empty.f_completeness,
    f_prepared: empty.f_prepared,
    f_prep_notes: empty.f_prep_notes,
  };
}

export function specimenToFormValues(s: SpecimenView): SpecimenFormValues {
  const v = emptyFormValues(s.type);
  v.name = s.name;
  v.catalog_number = s.catalog_number ?? '';
  v.description = s.description;
  v.visibility = s.visibility;
  v.visibility_price = s.visibility_price ?? VISIBILITY_INHERIT;
  v.visibility_acquired_from = s.visibility_acquired_from ?? VISIBILITY_INHERIT;
  v.visibility_images = s.visibility_images ?? VISIBILITY_INHERIT;
  // tagged is owner-only: the API omits it for non-owners. The edit
  // form is only reachable by the owner, so s.tagged is always present
  // here; fall back to false defensively.
  v.tagged = s.tagged ?? false;
  v.acquired_at = s.acquired_at ? toDateInputValue(s.acquired_at) : '';
  v.acquired_from = s.acquired_from ?? '';
  v.price_dollars = s.price_cents == null ? '' : (s.price_cents / 100).toString();
  v.source_notes = s.source_notes ?? '';
  v.locality_text = s.locality_text ?? '';

  const loc = s.locality ?? {};
  v.locality_country = loc.country ?? '';
  v.locality_region = loc.region ?? '';
  v.locality_site = loc.site ?? '';
  v.locality_lat = loc.lat == null ? '' : String(loc.lat);
  v.locality_lon = loc.lon == null ? '' : String(loc.lon);
  v.locality_mindat_id = loc.mindat_id ?? '';

  v.mass_g = s.mass_g == null ? '' : String(s.mass_g);
  const dims = s.dimensions ?? {};
  v.length_mm = dims.length_mm == null ? '' : String(dims.length_mm);
  v.width_mm = dims.width_mm == null ? '' : String(dims.width_mm);
  v.height_mm = dims.height_mm == null ? '' : String(dims.height_mm);

  if (s.type === 'mineral') {
    const td = (s.type_data ?? {}) as MineralData;
    v.m_chemical_formula = td.chemical_formula ?? '';
    v.m_mineral_species = (td.mineral_species ?? []).join(', ');
    v.m_crystal_system = td.crystal_system ?? '';
    v.m_mohs_hardness = td.mohs_hardness == null ? '' : String(td.mohs_hardness);
    v.m_color = td.color ?? '';
    v.m_luster = td.luster ?? '';
    v.m_fluorescence_sw = sanitizeFluorescence(td.fluorescence_sw);
    v.m_fluorescence_mw = sanitizeFluorescence(td.fluorescence_mw);
    v.m_fluorescence_lw = sanitizeFluorescence(td.fluorescence_lw);
    v.m_radioactive = Boolean(td.radioactive);
    v.m_magnetic = Boolean(td.magnetic);
    v.m_reacts_to_acid = Boolean(td.reacts_to_acid);
    v.m_mindat_id = td.mindat_id ?? '';
  } else if (s.type === 'rock') {
    const td = (s.type_data ?? {}) as RockData;
    const rt = td.rock_type ?? '';
    if (rt === 'igneous' || rt === 'sedimentary' || rt === 'metamorphic') {
      v.r_rock_type = rt;
    }
    v.r_composition = td.composition ?? '';
    v.r_formation_context = td.formation_context ?? '';
  } else if (s.type === 'meteorite') {
    const td = (s.type_data ?? {}) as MeteoriteData;
    v.me_classification = td.classification ?? '';
    const ff = td.fall_or_find ?? '';
    if (ff === 'fall' || ff === 'find') v.me_fall_or_find = ff;
    v.me_fall_or_find_date = td.fall_or_find_date ? toDateInputValue(td.fall_or_find_date) : '';
    v.me_official_name = td.official_name ?? '';
    v.me_total_known_weight_g =
      td.total_known_weight_g == null ? '' : String(td.total_known_weight_g);
    v.me_metbull_ref = td.metbull_ref ?? '';
  } else {
    const td = (s.type_data ?? {}) as FossilData;
    v.f_taxon = td.taxon ?? '';
    v.f_taxonomic_group = td.taxonomic_group ?? '';
    v.f_geologic_period = td.geologic_period ?? '';
    v.f_formation = td.formation ?? '';
    v.f_locality = td.locality ?? '';
    v.f_preservation_type = td.preservation_type ?? '';
    v.f_completeness = td.completeness ?? '';
    v.f_prepared = Boolean(td.prepared);
    v.f_prep_notes = td.prep_notes ?? '';
  }
  return v;
}

// Build a CreateSpecimenBody from validated form values. Empty
// optional fields are omitted so the server uses defaults.
export function formToCreateBody(values: SpecimenFormValues): CreateBody {
  const body: CreateBody = { type: values.type, name: values.name.trim() };
  if (values.catalog_number) body.catalog_number = values.catalog_number;
  if (values.description) body.description = values.description;
  body.visibility = values.visibility;
  const acquiredAt = toAcquiredAtWire(values.acquired_at);
  if (acquiredAt) body.acquired_at = acquiredAt;
  if (values.acquired_from) body.acquired_from = values.acquired_from;
  if (values.source_notes) body.source_notes = values.source_notes;
  if (values.locality_text) body.locality_text = values.locality_text;

  const cents = priceDollarsToCents(values.price_dollars);
  if (cents !== null) body.price_cents = cents;
  const massG = parseOptionalFloat(values.mass_g);
  if (massG !== null) body.mass_g = massG;

  const dims = buildDimensions(values);
  if (dims) body.dimensions = dims;

  const loc = buildLocality(values);
  if (loc) body.locality = loc;

  const td = buildTypeData(values);
  if (td) body.type_data = td;

  // tagged is owner metadata — include it explicitly so a new
  // specimen created via the form reflects the toggle state.
  if (values.tagged) body.tagged = true;

  return body;
}

// Build a PatchSpecimenBody containing only fields whose value
// changed from `initial`. type_data is sent as a partial overlay
// per the §10 contract: present keys overwrite, omitted keys are
// preserved server-side.
export function formToPatchBody(initial: SpecimenView, values: SpecimenFormValues): PatchBody {
  const body: PatchBody = {};

  if (values.name.trim() !== initial.name) body.name = values.name.trim();

  if (values.catalog_number !== (initial.catalog_number ?? '')) {
    body.catalog_number = values.catalog_number || (null as unknown as string);
  }
  if (values.description !== initial.description) body.description = values.description;
  if (values.visibility !== initial.visibility) body.visibility = values.visibility;

  const newAcquiredAt = toAcquiredAtWire(values.acquired_at);
  const initAcquiredAt = initial.acquired_at ? toDateInputValue(initial.acquired_at) : '';
  if (newAcquiredAt !== initAcquiredAt) {
    if (newAcquiredAt) body.acquired_at = newAcquiredAt;
  }
  if (values.acquired_from !== (initial.acquired_from ?? '')) {
    body.acquired_from = values.acquired_from;
  }
  if (values.source_notes !== (initial.source_notes ?? '')) {
    body.source_notes = values.source_notes;
  }
  if (values.locality_text !== (initial.locality_text ?? '')) {
    body.locality_text = values.locality_text;
  }

  const newCents = priceDollarsToCents(values.price_dollars);
  const initCents = initial.price_cents ?? null;
  if (newCents !== initCents && newCents !== null) {
    body.price_cents = newCents;
  }

  const newMass = parseOptionalFloat(values.mass_g);
  const initMass = initial.mass_g ?? null;
  if (newMass !== initMass && newMass !== null) {
    body.mass_g = newMass;
  }

  const dims = buildDimensions(values);
  const initDims = initial.dimensions ?? {};
  if (dims && !dimsEqual(dims, initDims)) {
    body.dimensions = dims;
  }

  const loc = buildLocality(values);
  const initLoc = initial.locality ?? {};
  if (loc && !localityEqual(loc, initLoc)) {
    body.locality = loc;
  }

  const td = buildTypeData(values);
  if (td && !typeDataEqual(td, initial.type_data ?? {}, initial.type)) {
    body.type_data = td;
  }

  // Per-field visibility overrides (mi-fo8 #7). __inherit__ in the
  // form maps to JSON null on the wire (clear the override; chain
  // falls through to the owner default). Explicit enum maps to the
  // value. Unchanged keys are omitted so the backend leaves them
  // alone.
  const priceDiff = diffVisibilityField(values.visibility_price, initial.visibility_price ?? null);
  if (priceDiff !== UNCHANGED) body.visibility_price = priceDiff;
  const afDiff = diffVisibilityField(
    values.visibility_acquired_from,
    initial.visibility_acquired_from ?? null,
  );
  if (afDiff !== UNCHANGED) body.visibility_acquired_from = afDiff;
  const imgDiff = diffVisibilityField(values.visibility_images, initial.visibility_images ?? null);
  if (imgDiff !== UNCHANGED) body.visibility_images = imgDiff;

  // tagged: only send when changed (mi-n28q). initial.tagged is
  // undefined for non-owners (field redacted); the edit form is
  // only reachable by the owner so treat undefined-initial as false.
  const initTagged = initial.tagged ?? false;
  if (values.tagged !== initTagged) body.tagged = values.tagged;

  return body;
}

// Sentinel for "no change" so the diff helper can distinguish it from
// the wire shape (`null | enum`). Using a Symbol keeps it
// indistinguishable from any valid wire value.
const UNCHANGED: unique symbol = Symbol('unchanged');

// diffVisibilityField returns the value to send on the wire for one
// visibility_* PATCH key. UNCHANGED skips the key in the body
// entirely; null clears the override; an enum value sets it.
function diffVisibilityField(
  current: VisibilityFieldValue,
  initial: 'private' | 'unlisted' | 'public' | null,
): 'private' | 'unlisted' | 'public' | null | typeof UNCHANGED {
  const currentWire: 'private' | 'unlisted' | 'public' | null =
    current === VISIBILITY_INHERIT ? null : current;
  if (currentWire === initial) return UNCHANGED;
  return currentWire;
}

// --- private helpers ---------------------------------------------

function parseOptionalFloat(raw: string | number | null | undefined): number | null {
  if (raw === null || raw === undefined) return null;
  if (typeof raw === 'number') return Number.isFinite(raw) ? raw : null;
  if (raw === '' || raw.trim() === '') return null;
  const n = Number(raw);
  return Number.isFinite(n) ? n : null;
}

function priceDollarsToCents(s: string | number | null | undefined): number | null {
  const dollars = parseOptionalFloat(s);
  if (dollars === null) return null;
  return Math.round(dollars * 100);
}

// toAcquiredAtWire normalizes the form's <input type="date"> value
// ("YYYY-MM-DD", "" or null) into the API's RFC 3339 full-date wire
// shape. The acquired_at field uses `format: date` (mi-s2ma): a
// time-of-day is meaningless for a calendar acquisition date, and
// strict date-time previously rejected the SPA's own input.
function toAcquiredAtWire(dateInput: string | null | undefined): string {
  if (!dateInput) return '';
  return dateInput;
}

function toRfc3339(dateInput: string | null | undefined): string {
  // dateInput is the value of <input type="date"> ("YYYY-MM-DD"),
  // empty, or null (felte coerces an empty date input to null).
  if (!dateInput) return '';
  // Append a midday-UTC time so timezone wobble doesn't flip the calendar day.
  return `${dateInput}T12:00:00Z`;
}

function toDateInputValue(rfc3339OrDate: string): string {
  // Pull just YYYY-MM-DD off either an RFC 3339 string or a full-date
  // ("YYYY-MM-DD") for prefilling <input type="date">.
  const m = /^(\d{4}-\d{2}-\d{2})/.exec(rfc3339OrDate);
  return m ? m[1]! : '';
}

function buildDimensions(values: SpecimenFormValues): components['schemas']['Dimensions'] | null {
  const length_mm = parseOptionalFloat(values.length_mm);
  const width_mm = parseOptionalFloat(values.width_mm);
  const height_mm = parseOptionalFloat(values.height_mm);
  if (length_mm === null && width_mm === null && height_mm === null) return null;
  const out: components['schemas']['Dimensions'] = {};
  if (length_mm !== null) out.length_mm = length_mm;
  if (width_mm !== null) out.width_mm = width_mm;
  if (height_mm !== null) out.height_mm = height_mm;
  return out;
}

function buildLocality(values: SpecimenFormValues): components['schemas']['Locality'] | null {
  const lat = parseOptionalFloat(values.locality_lat);
  const lon = parseOptionalFloat(values.locality_lon);
  const country = values.locality_country.trim();
  const region = values.locality_region.trim();
  const site = values.locality_site.trim();
  const mindat_id = values.locality_mindat_id.trim();
  if (!country && !region && !site && !mindat_id && lat === null && lon === null) return null;
  const out: components['schemas']['Locality'] = {};
  if (country) out.country = country;
  if (region) out.region = region;
  if (site) out.site = site;
  if (lat !== null) out.lat = lat;
  if (lon !== null) out.lon = lon;
  if (mindat_id) out.mindat_id = mindat_id;
  return out;
}

function buildTypeData(
  v: SpecimenFormValues,
): MineralData | RockData | MeteoriteData | FossilData | null {
  if (v.type === 'mineral') {
    const out: MineralData = {};
    if (v.m_chemical_formula.trim()) out.chemical_formula = v.m_chemical_formula.trim();
    const species = v.m_mineral_species
      .split(',')
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
    if (species.length > 0) out.mineral_species = species;
    if (v.m_crystal_system.trim()) out.crystal_system = v.m_crystal_system.trim();
    const hardness = parseOptionalFloat(v.m_mohs_hardness);
    if (hardness !== null) out.mohs_hardness = hardness;
    if (v.m_color.trim()) out.color = v.m_color.trim();
    if (v.m_luster.trim()) out.luster = v.m_luster.trim();
    if (v.m_fluorescence_sw.length > 0) out.fluorescence_sw = [...v.m_fluorescence_sw];
    if (v.m_fluorescence_mw.length > 0) out.fluorescence_mw = [...v.m_fluorescence_mw];
    if (v.m_fluorescence_lw.length > 0) out.fluorescence_lw = [...v.m_fluorescence_lw];
    if (v.m_radioactive) out.radioactive = true;
    if (v.m_magnetic) out.magnetic = true;
    if (v.m_reacts_to_acid) out.reacts_to_acid = true;
    if (v.m_mindat_id.trim()) out.mindat_id = v.m_mindat_id.trim();
    return Object.keys(out).length === 0 ? null : out;
  }
  if (v.type === 'rock') {
    const out: RockData = {};
    if (v.r_rock_type) out.rock_type = v.r_rock_type;
    if (v.r_composition.trim()) out.composition = v.r_composition.trim();
    if (v.r_formation_context.trim()) out.formation_context = v.r_formation_context.trim();
    return Object.keys(out).length === 0 ? null : out;
  }
  if (v.type === 'meteorite') {
    const out: MeteoriteData = {};
    if (v.me_classification.trim()) out.classification = v.me_classification.trim();
    if (v.me_fall_or_find) out.fall_or_find = v.me_fall_or_find;
    const fdate = toRfc3339(v.me_fall_or_find_date);
    if (fdate) out.fall_or_find_date = fdate;
    if (v.me_official_name.trim()) out.official_name = v.me_official_name.trim();
    const tkw = parseOptionalFloat(v.me_total_known_weight_g);
    if (tkw !== null) out.total_known_weight_g = tkw;
    if (v.me_metbull_ref.trim()) out.metbull_ref = v.me_metbull_ref.trim();
    return Object.keys(out).length === 0 ? null : out;
  }
  const out: FossilData = {};
  if (v.f_taxon.trim()) out.taxon = v.f_taxon.trim();
  if (v.f_taxonomic_group.trim()) out.taxonomic_group = v.f_taxonomic_group.trim();
  if (v.f_geologic_period.trim()) out.geologic_period = v.f_geologic_period.trim();
  if (v.f_formation.trim()) out.formation = v.f_formation.trim();
  if (v.f_locality.trim()) out.locality = v.f_locality.trim();
  if (v.f_preservation_type.trim()) out.preservation_type = v.f_preservation_type.trim();
  if (v.f_completeness.trim()) out.completeness = v.f_completeness.trim();
  if (v.f_prepared) out.prepared = true;
  if (v.f_prep_notes.trim()) out.prep_notes = v.f_prep_notes.trim();
  return Object.keys(out).length === 0 ? null : out;
}

function dimsEqual(
  a: components['schemas']['Dimensions'],
  b: components['schemas']['Dimensions'],
): boolean {
  return (
    (a.length_mm ?? null) === (b.length_mm ?? null) &&
    (a.width_mm ?? null) === (b.width_mm ?? null) &&
    (a.height_mm ?? null) === (b.height_mm ?? null)
  );
}

function localityEqual(
  a: components['schemas']['Locality'],
  b: components['schemas']['Locality'],
): boolean {
  return (
    (a.country ?? '') === (b.country ?? '') &&
    (a.region ?? '') === (b.region ?? '') &&
    (a.site ?? '') === (b.site ?? '') &&
    (a.lat ?? null) === (b.lat ?? null) &&
    (a.lon ?? null) === (b.lon ?? null) &&
    (a.mindat_id ?? '') === (b.mindat_id ?? '')
  );
}

function typeDataEqual(
  a: MineralData | RockData | MeteoriteData | FossilData,
  b: MineralData | RockData | MeteoriteData | FossilData,
  type: SpecimenView['type'],
): boolean {
  if (type === 'mineral') {
    const aa = a as MineralData;
    const bb = b as MineralData;
    return (
      (aa.chemical_formula ?? '') === (bb.chemical_formula ?? '') &&
      arraysEqual(aa.mineral_species ?? [], bb.mineral_species ?? []) &&
      (aa.crystal_system ?? '') === (bb.crystal_system ?? '') &&
      (aa.mohs_hardness ?? null) === (bb.mohs_hardness ?? null) &&
      (aa.color ?? '') === (bb.color ?? '') &&
      (aa.luster ?? '') === (bb.luster ?? '') &&
      arraysEqual(aa.fluorescence_sw ?? [], bb.fluorescence_sw ?? []) &&
      arraysEqual(aa.fluorescence_mw ?? [], bb.fluorescence_mw ?? []) &&
      arraysEqual(aa.fluorescence_lw ?? [], bb.fluorescence_lw ?? []) &&
      Boolean(aa.radioactive) === Boolean(bb.radioactive) &&
      Boolean(aa.magnetic) === Boolean(bb.magnetic) &&
      Boolean(aa.reacts_to_acid) === Boolean(bb.reacts_to_acid) &&
      (aa.mindat_id ?? '') === (bb.mindat_id ?? '')
    );
  }
  if (type === 'rock') {
    const aa = a as RockData;
    const bb = b as RockData;
    return (
      (aa.rock_type ?? '') === (bb.rock_type ?? '') &&
      (aa.composition ?? '') === (bb.composition ?? '') &&
      (aa.formation_context ?? '') === (bb.formation_context ?? '')
    );
  }
  if (type === 'meteorite') {
    const aa = a as MeteoriteData;
    const bb = b as MeteoriteData;
    return (
      (aa.classification ?? '') === (bb.classification ?? '') &&
      (aa.fall_or_find ?? '') === (bb.fall_or_find ?? '') &&
      (aa.fall_or_find_date ?? '') === (bb.fall_or_find_date ?? '') &&
      (aa.official_name ?? '') === (bb.official_name ?? '') &&
      (aa.total_known_weight_g ?? null) === (bb.total_known_weight_g ?? null) &&
      (aa.metbull_ref ?? '') === (bb.metbull_ref ?? '')
    );
  }
  const aa = a as FossilData;
  const bb = b as FossilData;
  return (
    (aa.taxon ?? '') === (bb.taxon ?? '') &&
    (aa.taxonomic_group ?? '') === (bb.taxonomic_group ?? '') &&
    (aa.geologic_period ?? '') === (bb.geologic_period ?? '') &&
    (aa.formation ?? '') === (bb.formation ?? '') &&
    (aa.locality ?? '') === (bb.locality ?? '') &&
    (aa.preservation_type ?? '') === (bb.preservation_type ?? '') &&
    (aa.completeness ?? '') === (bb.completeness ?? '') &&
    Boolean(aa.prepared) === Boolean(bb.prepared) &&
    (aa.prep_notes ?? '') === (bb.prep_notes ?? '')
  );
}

// sanitizeFluorescence drops anything outside the closed enum so the
// edit form doesn't trip its own validator on legacy/foreign data
// (mi-qas). Returns a fresh array to break aliasing with the API view.
function sanitizeFluorescence(raw: readonly string[] | null | undefined): FluorescenceColor[] {
  if (!raw) return [];
  const out: FluorescenceColor[] = [];
  for (const c of raw) {
    if (fluorescenceColorSet.has(c)) out.push(c as FluorescenceColor);
  }
  return out;
}

function arraysEqual<T>(a: readonly T[] | null, b: readonly T[] | null): boolean {
  const aa = a ?? [];
  const bb = b ?? [];
  if (aa.length !== bb.length) return false;
  for (let i = 0; i < aa.length; i++) if (aa[i] !== bb[i]) return false;
  return true;
}
