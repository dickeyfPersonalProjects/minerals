<script lang="ts">
  import { link } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import { isProfileSetupRedirect, SUPPRESS_TOAST_HEADERS } from '../lib/api/wrapper';
  import type { components } from '../lib/api/schema';
  import CollectorChainEditor from '../lib/CollectorChainEditor.svelte';
  import ConfirmModal from '../lib/ConfirmModal.svelte';
  import JournalAttachments from '../lib/JournalAttachments.svelte';
  import JournalEntryForm, {
    type JournalEntryFormSubmitResult,
  } from '../lib/JournalEntryForm.svelte';
  import type { JournalEntryFormValues } from '../lib/schemas/journal';
  import ImageCropModal from '../lib/ImageCropModal.svelte';
  import Lightbox from '../lib/Lightbox.svelte';
  import PhotoUploader from '../lib/PhotoUploader.svelte';
  import { isAuthenticated } from '../lib/auth';
  import { formatLocal } from '../lib/time';
  import { toastError, toastSuccess } from '../lib/toasts';
  import { resolveImage, type OwnerLike, type Visibility } from '../lib/api/visibility';

  type Specimen = components['schemas']['SpecimenView'];
  type Photo = components['schemas']['PhotoView'];
  type Profile = components['schemas']['ProfileBody'];
  type Journal = components['schemas']['JournalView'];
  type MineralData = components['schemas']['MineralData'];
  type RockData = components['schemas']['RockData'];
  type MeteoriteData = components['schemas']['MeteoriteData'];
  type FossilData = components['schemas']['FossilData'];
  type CollectorLink = components['schemas']['SpecimenCollectorLinkView'];

  interface Props {
    params?: { id?: string };
  }
  const { params }: Props = $props();

  type LoadState =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'loaded' }
    | { kind: 'error'; message: string };

  let specimen: Specimen | null = $state(null);
  let photos: Photo[] = $state([]);
  let journal: Journal[] = $state([]);
  let collectors: CollectorLink[] = $state([]);
  let loadState: LoadState = $state({ kind: 'idle' });
  let lightboxIndex: number | null = $state(null);
  let cropTarget: Photo | null = $state(null);
  // "Edit type" modal target (hq-6lrd) — null when closed. The
  // separate `savingKind` guard prevents a double-submit from
  // racing the optimistic update.
  let editKindTarget: Photo | null = $state(null);
  let savingKind = $state(false);
  // "Edit visibility" modal target (mi-fo8 #8) — null when closed.
  // The matching `savingVisibility` guard prevents a double-submit
  // from racing the refetch.
  let editVisibilityTarget: Photo | null = $state(null);
  let savingVisibility = $state(false);
  // Owner profile used by the per-image visibility selector to render
  // the "currently: X" chip on the 'use specimen images-default'
  // option. Loaded concurrently with the specimen; a failed/anonymous
  // fetch degrades the chip to the system default. mi-fo8 #8.
  let ownerProfile: OwnerLike | null = $state(null);
  let journalCreating = $state(false);
  let editingEntryId: string | null = $state(null);
  let editingChain = $state(false);

  // Confirm-delete state — at most one entity is in the
  // confirm-delete flow at a time, so a discriminated union keeps
  // the call sites simple.
  type DeleteTarget = { kind: 'photo'; id: string } | { kind: 'journal'; id: string };
  let deleteTarget = $state<DeleteTarget | null>(null);
  let deleting = $state(false);

  function errorMessage(
    error: { error?: { code?: string; message?: string } } | undefined,
    status: number,
  ): string {
    return error?.error?.message || error?.error?.code || `HTTP ${status}`;
  }

  async function refetchPhotos(id: string): Promise<void> {
    try {
      const p = await client.GET('/api/v1/specimens/{id}/photos', {
        params: { path: { id }, query: { limit: 100 } },
      });
      photos = p.data?.items ?? [];
    } catch {
      // Auxiliary fetch — leave the existing list in place rather
      // than blanking the gallery on a transient network error.
    }
  }

  async function refetchCollectors(id: string): Promise<void> {
    try {
      const c = await client.GET('/api/v1/specimens/{id}/collectors', {
        params: { path: { id } },
      });
      collectors = c.data?.items ?? [];
    } catch {
      // Auxiliary fetch — keep existing list on transient failure.
    }
  }

  async function refetchJournal(id: string): Promise<void> {
    try {
      const j = await client.GET('/api/v1/specimens/{id}/journal', {
        params: { path: { id }, query: { limit: 100 } },
      });
      journal = j.data?.items ?? [];
    } catch {
      // Same auxiliary-fetch policy as photos.
    }
  }

  async function handleCreateEntry(
    values: JournalEntryFormValues,
  ): Promise<JournalEntryFormSubmitResult> {
    if (!specimen) return { kind: 'error', message: 'No specimen loaded' };
    const { error, response } = await client.POST('/api/v1/specimens/{id}/journal', {
      params: { path: { id: specimen.id } },
      body: { body_md: values.body_md },
    });
    if (error) {
      return { kind: 'error', message: errorMessage(error, response.status) };
    }
    toastSuccess('Journal entry added');
    journalCreating = false;
    await refetchJournal(specimen.id);
    return { kind: 'ok' };
  }

  function makeEditHandler(entryId: string) {
    return async (values: JournalEntryFormValues): Promise<JournalEntryFormSubmitResult> => {
      if (!specimen) return { kind: 'error', message: 'No specimen loaded' };
      const { error, response } = await client.PATCH('/api/v1/journal/{id}', {
        params: { path: { id: entryId } },
        body: { body_md: values.body_md },
      });
      if (error) {
        return { kind: 'error', message: errorMessage(error, response.status) };
      }
      toastSuccess('Journal entry saved');
      editingEntryId = null;
      await refetchJournal(specimen.id);
      return { kind: 'ok' };
    };
  }

  async function load(id: string): Promise<void> {
    loadState = { kind: 'loading' };

    // Specimen fetch is required — failure aborts the page.
    const specimenP = client.GET('/api/v1/specimens/{id}', {
      params: { path: { id } },
    });
    // Photos, journal, and collectors are auxiliary — failures
    // degrade to empty arrays so the page still renders the core
    // specimen data.
    const photosP = client.GET('/api/v1/specimens/{id}/photos', {
      params: { path: { id }, query: { limit: 100 } },
    });
    const journalP = client.GET('/api/v1/specimens/{id}/journal', {
      params: { path: { id }, query: { limit: 100 } },
    });
    const collectorsP = client.GET('/api/v1/specimens/{id}/collectors', {
      params: { path: { id } },
    });
    // Profile fetch drives the per-image visibility selector chip
    // (mi-fo8 #8). Anonymous callers can't edit photos so the
    // affordance is gated behind $isAuthenticated; for authenticated
    // viewers who happen not to own this specimen the PATCH will
    // fail at the backend — the chip text will just reflect the
    // viewer's own defaults instead of the owner's, which is
    // acceptable for the loose-coupled affordance.
    const profileP = client.GET('/api/v1/profile', {
      headers: SUPPRESS_TOAST_HEADERS,
    });

    try {
      const [s, p, j, c, pr] = await Promise.all([
        specimenP,
        photosP,
        journalP,
        collectorsP,
        profileP,
      ]);
      if (s.error) {
        // mi-4p4: wrapper is redirecting to /profile/setup; stay
        // in `loading` so no "access forbidden" banner flashes.
        if (isProfileSetupRedirect(s.error)) return;
        loadState = { kind: 'error', message: errorMessage(s.error, s.response.status) };
        return;
      }
      specimen = s.data ?? null;
      photos = p.data?.items ?? [];
      journal = j.data?.items ?? [];
      collectors = c.data?.items ?? [];
      ownerProfile = pr.error ? null : ((pr.data as Profile | undefined) ?? null);
      loadState = { kind: 'loaded' };
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      loadState = { kind: 'error', message };
    }
  }

  $effect(() => {
    const id = params?.id;
    if (!id) {
      loadState = { kind: 'error', message: 'missing specimen id' };
      return;
    }
    void load(id);
  });

  function openLightbox(idx: number) {
    if (visiblePhotos.length === 0) return;
    lightboxIndex = Math.max(0, Math.min(idx, visiblePhotos.length - 1));
  }

  // openLightboxForPhoto resolves a photo to its position in
  // visiblePhotos so the lightbox starts on the clicked image.
  // Used by the hero + gallery thumbs after mi-m8q decoupled the
  // hero slot from "visiblePhotos[0]" (the hero now floats to the
  // designated main image).
  function openLightboxForPhoto(p: Photo) {
    const idx = visiblePhotos.findIndex((q) => q.id === p.id);
    if (idx < 0) return;
    openLightbox(idx);
  }

  function closeLightbox() {
    lightboxIndex = null;
  }

  function requestCropPhoto(id: string) {
    const target = photos.find((p) => p.id === id);
    if (!target) return;
    // Close the lightbox so the crop modal is the only overlay; the
    // gallery indexes shift after crop apply.
    lightboxIndex = null;
    cropTarget = target;
  }

  function closeCrop() {
    cropTarget = null;
  }

  async function handleCropApplied() {
    if (!specimen) return;
    await refetchPhotos(specimen.id);
  }

  function requestEditKind(id: string) {
    const target = photos.find((p) => p.id === id);
    if (!target) return;
    // Close the lightbox so the picker is the only overlay.
    lightboxIndex = null;
    editKindTarget = target;
  }

  function closeEditKind() {
    if (!savingKind) editKindTarget = null;
  }

  async function applyEditKind(kind: PhotoKind): Promise<void> {
    const target = editKindTarget;
    if (!target || !specimen || savingKind) return;
    const current: PhotoKind = (target.kind as PhotoKind | undefined) ?? 'visible';
    if (kind === current) {
      editKindTarget = null;
      return;
    }
    savingKind = true;
    try {
      const { error, response } = await client.PATCH('/api/v1/photos/{id}', {
        params: { path: { id: target.id } },
        body: { kind },
      });
      if (error) {
        toastError(errorMessage(error, response.status));
        return;
      }
      toastSuccess('Photo type updated');
      editKindTarget = null;
      await refetchPhotos(specimen.id);
    } finally {
      savingKind = false;
    }
  }

  function requestEditVisibility(id: string) {
    const target = photos.find((p) => p.id === id);
    if (!target) return;
    lightboxIndex = null;
    editVisibilityTarget = target;
  }

  function closeEditVisibility() {
    if (!savingVisibility) editVisibilityTarget = null;
  }

  // applyEditVisibility runs the PATCH that switches a single photo's
  // visibility (mi-fo8 #8). Wire shape mirrors the specimen patch:
  // null clears the per-photo override (chain falls through to the
  // specimen's images default); an enum value sets it. No-op when
  // the user picks the value already stored.
  async function applyEditVisibility(value: Visibility | null): Promise<void> {
    const target = editVisibilityTarget;
    if (!target || !specimen || savingVisibility) return;
    const current = (target.visibility as Visibility | null | undefined) ?? null;
    if (value === current) {
      editVisibilityTarget = null;
      return;
    }
    savingVisibility = true;
    try {
      const { error, response } = await client.PATCH('/api/v1/photos/{id}', {
        params: { path: { id: target.id } },
        body: { visibility: value },
      });
      if (error) {
        toastError(errorMessage(error, response.status));
        return;
      }
      toastSuccess('Photo privacy updated');
      editVisibilityTarget = null;
      await refetchPhotos(specimen.id);
    } finally {
      savingVisibility = false;
    }
  }

  function requestDeletePhoto(id: string) {
    deleteTarget = { kind: 'photo', id };
  }

  function requestDeleteJournal(id: string) {
    deleteTarget = { kind: 'journal', id };
  }

  function cancelDelete() {
    if (!deleting) deleteTarget = null;
  }

  async function confirmDelete() {
    const target = deleteTarget;
    if (!target || !specimen || deleting) return;
    deleting = true;
    try {
      if (target.kind === 'photo') {
        const { error, response } = await client.DELETE('/api/v1/photos/{id}', {
          params: { path: { id: target.id } },
          headers: SUPPRESS_TOAST_HEADERS,
        });
        if (error) {
          toastError(errorMessage(error, response.status));
          return;
        }
        toastSuccess('Photo deleted');
        deleteTarget = null;
        // Close the lightbox so it doesn't try to render the deleted
        // photo on the next refetch — the bound index would shift.
        lightboxIndex = null;
        await refetchPhotos(specimen.id);
        return;
      }
      if (target.kind === 'journal') {
        const { error, response } = await client.DELETE('/api/v1/journal/{id}', {
          params: { path: { id: target.id } },
          headers: SUPPRESS_TOAST_HEADERS,
        });
        if (error) {
          if (response.status === 409) {
            toastError(error.error?.message || 'This entry has attachments. Delete those first.');
          } else {
            toastError(errorMessage(error, response.status));
          }
          return;
        }
        toastSuccess('Journal entry deleted');
        deleteTarget = null;
        if (editingEntryId === target.id) editingEntryId = null;
        await refetchJournal(specimen.id);
        return;
      }
    } finally {
      deleting = false;
    }
  }

  const deleteDialogProps = $derived(
    deleteTarget?.kind === 'photo'
      ? {
          title: 'Delete photo?',
          message: 'Delete this photo? This cannot be undone.',
        }
      : deleteTarget?.kind === 'journal'
        ? {
            title: 'Delete journal entry?',
            message: 'Delete this journal entry? Attachments will also be deleted.',
          }
        : null,
  );

  function isEdited(j: Journal): boolean {
    const created = new Date(j.created_at).getTime();
    const updated = new Date(j.updated_at).getTime();
    return Number.isFinite(created) && Number.isFinite(updated) && updated - created > 1000;
  }

  function fmtDate(iso: string | null | undefined): string {
    if (!iso) return '';
    // RFC 3339 full-date ("YYYY-MM-DD") must render as the literal
    // calendar day — `new Date("2024-01-02")` parses as UTC midnight,
    // which shifts back a day under negative UTC offsets. Parse as a
    // local-time date instead so the user-facing label always matches
    // the wire value. RFC 3339 date-time strings still flow through
    // formatLocal unchanged (type_data still uses date-time for
    // fall_or_find_date).
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
    if (m) {
      const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
      return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(d);
    }
    try {
      return formatLocal(iso, { dateStyle: 'medium' });
    } catch {
      return '';
    }
  }

  function fmtDateTime(iso: string): string {
    try {
      return formatLocal(iso, { dateStyle: 'medium', timeStyle: 'short' });
    } catch {
      return iso;
    }
  }

  // Pretty labels for type_data keys. Anything not in this map
  // falls back to title-casing the key.
  const TYPE_DATA_LABELS: Record<string, string> = {
    chemical_formula: 'Chemical formula',
    mineral_species: 'Mineral species',
    mohs_hardness: 'Hardness (Mohs)',
    crystal_system: 'Crystal system',
    color: 'Color',
    luster: 'Luster',
    fluorescence: 'Fluorescence', // legacy free-text key — rendered as-is for unmigrated data
    fluorescence_sw: 'Fluorescence (SW 254 nm)',
    fluorescence_mw: 'Fluorescence (MW ~312 nm)',
    fluorescence_lw: 'Fluorescence (LW ~365 nm)',
    radioactive: 'Radioactive',
    magnetic: 'Magnetic',
    reacts_to_acid: 'Reacts to acid',
    mindat_id: 'mindat ID',
    rock_type: 'Rock type',
    composition: 'Composition',
    formation_context: 'Formation',
    classification: 'Classification',
    fall_or_find: 'Fall or find',
    fall_or_find_date: 'Fall/find date',
    metbull_ref: 'Met. Bulletin ref',
    official_name: 'Official name',
    total_known_weight_g: 'Total known weight (g)',
    taxon: 'Taxon',
    taxonomic_group: 'Taxonomic group',
    geologic_period: 'Geologic period',
    formation: 'Formation',
    locality: 'Stratigraphic locality',
    preservation_type: 'Preservation type',
    completeness: 'Completeness',
    prepared: 'Prep work done',
    prep_notes: 'Prep notes',
  };

  function titleCase(key: string): string {
    return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }

  const FLUORESCENCE_KEYS = new Set(['fluorescence_sw', 'fluorescence_mw', 'fluorescence_lw']);

  type TypeDatum = {
    key: string;
    label: string;
    value: string;
    // When set, the row renders these as color chips instead of plain text.
    chips?: string[];
  };

  function typeDataEntries(s: Specimen): TypeDatum[] {
    const td = (s.type_data ?? {}) as Partial<MineralData & RockData & MeteoriteData & FossilData> &
      Record<string, unknown>;
    const out: TypeDatum[] = [];
    for (const [key, raw] of Object.entries(td)) {
      if (raw === null || raw === undefined || raw === '') continue;
      let value: string;
      let chips: string[] | undefined;
      if (Array.isArray(raw)) {
        if (raw.length === 0) continue;
        const strs = raw.map((x) => String(x));
        value = strs.join(', ');
        if (FLUORESCENCE_KEYS.has(key)) chips = strs;
      } else if (typeof raw === 'boolean') {
        value = raw ? 'Yes' : 'No';
      } else if (key === 'fall_or_find_date' && typeof raw === 'string') {
        value = fmtDate(raw);
        if (!value) continue;
      } else {
        value = String(raw);
      }
      out.push({ key, label: TYPE_DATA_LABELS[key] ?? titleCase(key), value, chips });
    }
    return out;
  }

  // Tailwind-class lookup for fluorescence color chips. Anything outside
  // the closed enum falls back to a neutral chip — that lets legacy
  // free-text 'fluorescence' values still render harmlessly.
  const FLUORESCENCE_CHIP_CLASS: Record<string, string> = {
    Red: 'bg-red-500/15 text-red-700 border-red-500/40 dark:text-red-300',
    Orange: 'bg-orange-500/15 text-orange-700 border-orange-500/40 dark:text-orange-300',
    Yellow: 'bg-yellow-400/20 text-yellow-800 border-yellow-500/40 dark:text-yellow-200',
    Green: 'bg-green-500/15 text-green-700 border-green-500/40 dark:text-green-300',
    Blue: 'bg-blue-500/15 text-blue-700 border-blue-500/40 dark:text-blue-300',
    Violet: 'bg-violet-500/15 text-violet-700 border-violet-500/40 dark:text-violet-300',
    Pink: 'bg-pink-500/15 text-pink-700 border-pink-500/40 dark:text-pink-300',
    White: 'bg-slate-100 text-slate-800 border-slate-300 dark:bg-slate-200/20 dark:text-slate-100',
    Cream: 'bg-amber-100 text-amber-900 border-amber-300 dark:bg-amber-200/20 dark:text-amber-100',
    'Blue-green': 'bg-teal-500/15 text-teal-700 border-teal-500/40 dark:text-teal-300',
    'Blue-violet': 'bg-indigo-500/15 text-indigo-700 border-indigo-500/40 dark:text-indigo-300',
    'Red-orange': 'bg-orange-600/15 text-orange-800 border-orange-600/40 dark:text-orange-300',
    'Orange-yellow': 'bg-amber-400/20 text-amber-800 border-amber-500/40 dark:text-amber-200',
    'Greenish-yellow': 'bg-lime-400/20 text-lime-800 border-lime-500/40 dark:text-lime-200',
    'Cherry red': 'bg-rose-600/15 text-rose-700 border-rose-600/40 dark:text-rose-300',
  };

  function chipClass(color: string): string {
    return (
      FLUORESCENCE_CHIP_CLASS[color] ??
      'bg-[var(--color-surface-2)] text-[var(--color-text)] border-[var(--color-border)]'
    );
  }

  function localityEntries(
    loc: components['schemas']['Locality'] | null | undefined,
  ): { label: string; value: string }[] {
    if (!loc) return [];
    const out: { label: string; value: string }[] = [];
    if (loc.site) out.push({ label: 'Site', value: loc.site });
    if (loc.region) out.push({ label: 'Region', value: loc.region });
    if (loc.country) out.push({ label: 'Country', value: loc.country });
    if (typeof loc.lat === 'number' && typeof loc.lon === 'number') {
      out.push({
        label: 'Coordinates',
        value: `${loc.lat.toFixed(4)}, ${loc.lon.toFixed(4)}`,
      });
    }
    if (loc.mindat_id) out.push({ label: 'mindat ID', value: loc.mindat_id });
    return out;
  }

  function physicalEntries(s: Specimen): { label: string; value: string }[] {
    const out: { label: string; value: string }[] = [];
    if (typeof s.mass_g === 'number') {
      out.push({ label: 'Mass', value: `${s.mass_g} g` });
    }
    const d = s.dimensions ?? {};
    const dims: string[] = [];
    if (typeof d.length_mm === 'number') dims.push(`${d.length_mm}`);
    if (typeof d.width_mm === 'number') dims.push(`${d.width_mm}`);
    if (typeof d.height_mm === 'number') dims.push(`${d.height_mm}`);
    if (dims.length > 0) {
      out.push({ label: 'Dimensions', value: `${dims.join(' × ')} mm` });
    }
    if (s.acquired_at) {
      out.push({ label: 'Acquired', value: fmtDate(s.acquired_at) });
    }
    if (s.acquired_from) {
      out.push({ label: 'Acquired from', value: s.acquired_from });
    }
    return out;
  }

  // Visibility chip colour, mirrors SpecimenCard logic.
  const visibilityClass: Record<Specimen['visibility'], string> = {
    private: '',
    unlisted: 'bg-[var(--color-surface-2)] text-[var(--color-text-muted)]',
    public: 'bg-[var(--color-accent)] text-[var(--color-accent-fg)]',
  };

  const typeColorClass: Record<Specimen['type'], string> = {
    mineral: 'bg-[var(--color-mineral)] text-[var(--color-accent-fg)]',
    rock: 'bg-[var(--color-rock)] text-[var(--color-accent-fg)]',
    meteorite: 'bg-[var(--color-meteorite)] text-[var(--color-accent-fg)]',
    fossil: 'bg-[var(--color-fossil)] text-[var(--color-accent-fg)]',
  };

  // Photo-kind UI metadata (mi-5b6, hq-6lrd). Mirrors the backend
  // enum; 'visible' badges are suppressed because that's the v1
  // default and would clutter the gallery.
  type PhotoKind = NonNullable<Photo['kind']>;
  const PHOTO_KIND_LABELS: Record<PhotoKind, string> = {
    visible: 'Visible',
    uv_sw: 'UV SW',
    uv_mw: 'UV MW',
    uv_lw: 'UV LW',
    other: 'Other',
  };
  // Editable kinds offered by the "Edit type" modal (hq-6lrd). The
  // backend keeps 'other' for legacy / IR / polarised photos, but
  // the picker only exposes the four mineralogically meaningful
  // lighting conditions the bead asks for.
  const EDITABLE_PHOTO_KINDS: PhotoKind[] = ['visible', 'uv_sw', 'uv_mw', 'uv_lw'];

  // Per-photo visibility modal (mi-fo8 #8) — labels for the explicit
  // enum options + a helper that resolves what the chain would
  // produce when the per-photo override is CLEARED (the chip text
  // on the 'use specimen images-default' row).
  const VISIBILITY_LABELS: Record<Visibility, string> = {
    private: 'Private',
    unlisted: 'Unlisted',
    public: 'Public',
  };

  function resolveImageInherit(target: Photo | null): Visibility {
    if (!target || !specimen) return 'private';
    // Compute the chain WITHOUT the per-photo override so the chip
    // shows what 'use specimen images-default' would resolve to.
    // SpecimenLike is the same shape as Specimen for the fields the
    // resolver consumes.
    const spec = {
      visibility: specimen.visibility,
      visibility_images: specimen.visibility_images ?? null,
    };
    const owner: OwnerLike = ownerProfile ?? {};
    return resolveImage(spec, owner, {}).visibility;
  }
  // Filter chip state; 'all' shows the whole gallery, anything else
  // narrows to a single kind. Resets when navigating to a new
  // specimen via the load $effect.
  let kindFilter: 'all' | PhotoKind = $state('all');

  $effect(() => {
    void params?.id;
    kindFilter = 'all';
  });

  // Counts per kind drive the filter chip labels and visibility
  // (we hide chips for kinds with zero photos to avoid noise).
  const kindCounts = $derived.by(() => {
    const counts: Record<PhotoKind, number> = {
      visible: 0,
      uv_sw: 0,
      uv_mw: 0,
      uv_lw: 0,
      other: 0,
    };
    for (const p of photos) {
      const k: PhotoKind = (p.kind as PhotoKind) ?? 'visible';
      counts[k] = (counts[k] ?? 0) + 1;
    }
    return counts;
  });

  // The gallery uses this filtered list everywhere — the lightbox
  // navigates within the visible subset so prev/next stays
  // consistent with what the user sees.
  const visiblePhotos = $derived(
    kindFilter === 'all' ? photos : photos.filter((p) => (p.kind ?? 'visible') === kindFilter),
  );

  const lightboxPhotos = $derived(
    visiblePhotos.map((p) => ({
      id: p.id,
      alt: specimen ? `Photo of ${specimen.name}` : 'Photo',
      kind: (p.kind as PhotoKind | undefined) ?? 'visible',
    })),
  );

  const kindFilterOptions = $derived<{ value: 'all' | PhotoKind; label: string; count: number }[]>([
    { value: 'all', label: 'All', count: photos.length },
    { value: 'visible', label: 'Visible', count: kindCounts.visible },
    { value: 'uv_sw', label: 'UV SW', count: kindCounts.uv_sw },
    { value: 'uv_mw', label: 'UV MW', count: kindCounts.uv_mw },
    { value: 'uv_lw', label: 'UV LW', count: kindCounts.uv_lw },
    { value: 'other', label: 'Other', count: kindCounts.other },
  ]);

  // Designated main image (mi-m8q). When the user has picked a
  // photo, its file_id sits on the specimen; we resolve it to a
  // Photo inside the *currently visible* set so kind-filtering
  // doesn't surface a hidden photo as the hero. When no main is
  // set, or the designated photo isn't in the visible set,
  // heroPhoto falls back to the first-by-position photo — the
  // pre-mi-m8q behaviour.
  const mainPhoto = $derived.by<Photo | undefined>(() => {
    const fid = specimen?.main_image_id ?? null;
    if (!fid) return undefined;
    return visiblePhotos.find((p) => p.file_id === fid);
  });
  const heroPhoto = $derived<Photo | undefined>(mainPhoto ?? visiblePhotos[0]);
  const restPhotos = $derived(visiblePhotos.filter((p) => p !== heroPhoto));

  // "Set as main" mutation. Optimistic: flip specimen.main_image_id
  // immediately so the badge/button toggles before the server
  // round-trip; roll back on failure.
  let settingMain = $state(false);

  async function setAsMain(fileID: string): Promise<void> {
    if (!specimen || settingMain) return;
    settingMain = true;
    const prior = specimen.main_image_id;
    specimen = { ...specimen, main_image_id: fileID };
    try {
      const { error, response } = await client.PATCH('/api/v1/specimens/{id}', {
        params: { path: { id: specimen.id } },
        body: { main_image_id: fileID },
      });
      if (error) {
        // Roll back the optimistic change.
        if (specimen) specimen = { ...specimen, main_image_id: prior };
        toastError(errorMessage(error, response.status));
        return;
      }
      toastSuccess('Main image updated');
    } finally {
      settingMain = false;
    }
  }
</script>

{#if loadState.kind === 'loading' || loadState.kind === 'idle'}
  <div data-testid="loading" class="space-y-6">
    <div class="h-10 w-64 animate-pulse rounded bg-[var(--color-surface-2)]"></div>
    <div class="aspect-[16/9] animate-pulse rounded-lg bg-[var(--color-surface-2)]"></div>
    <div class="h-4 w-full animate-pulse rounded bg-[var(--color-surface-2)]"></div>
    <div class="h-4 w-5/6 animate-pulse rounded bg-[var(--color-surface-2)]"></div>
    <div class="h-4 w-2/3 animate-pulse rounded bg-[var(--color-surface-2)]"></div>
  </div>
{:else if loadState.kind === 'error'}
  <div
    class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center"
    data-testid="error"
    role="alert"
  >
    <p class="text-sm font-medium text-[var(--color-text)]">Couldn't load this specimen.</p>
    <p class="mt-1 text-xs text-[var(--color-text-muted)]">{loadState.message}</p>
    <a
      href="/specimens"
      use:link
      class="mt-4 inline-block text-sm text-[var(--color-accent)] hover:underline"
    >
      ← back to specimens
    </a>
  </div>
{:else if specimen}
  {@const td = typeDataEntries(specimen)}
  {@const loc = localityEntries(specimen.locality)}
  {@const phys = physicalEntries(specimen)}
  {@const specimenId = specimen.id}

  <article class="space-y-8" data-testid="specimen-detail">
    <header class="space-y-3">
      <a
        href="/specimens"
        use:link
        class="inline-block text-xs text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
      >
        ← Specimens
      </a>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <h1
          class="font-serif text-3xl font-semibold tracking-tight text-[var(--color-text)] sm:text-4xl"
          data-testid="specimen-name"
        >
          {specimen.name}
        </h1>
        {#if $isAuthenticated}
          <a
            href={`/specimens/${specimen.id}/edit`}
            use:link
            data-testid="edit-specimen"
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
          >
            Edit
          </a>
        {/if}
      </div>
      <div class="flex flex-wrap items-start gap-3">
        <div class="flex flex-wrap items-center gap-2 pt-2">
          <span
            class="rounded-full px-2.5 py-0.5 text-[11px] font-semibold uppercase tracking-wide {typeColorClass[
              specimen.type
            ]}"
            data-testid="type-badge"
          >
            {specimen.type}
          </span>
          {#if specimen.visibility !== 'private'}
            <span
              class="rounded-full px-2.5 py-0.5 text-[11px] font-medium uppercase tracking-wide {visibilityClass[
                specimen.visibility
              ]}"
              data-testid="visibility-chip"
            >
              {specimen.visibility}
            </span>
          {/if}
          {#if specimen.catalog_number}
            <span
              class="rounded-md border border-[var(--color-border)] px-2 py-0.5 font-mono text-[11px] text-[var(--color-text-muted)]"
              data-testid="catalog-number"
            >
              {specimen.catalog_number}
            </span>
          {/if}
        </div>
      </div>
    </header>

    <!--
      Two-column layout (mi-tjij): the DETAILS / INFORMATION column
      leads; images follow. The images column is kept FIRST in source
      so the DOM / tab order is unchanged from before this reorder, but
      it is pushed to the second visual slot with `order-2` while the
      info column gets `order-1`. On narrow viewports the single-column
      grid stacks the children by `order` (info first, images below); on
      wide viewports the two-column grid places info left, images right.
    -->
    <div class="grid gap-8 lg:grid-cols-[3fr_2fr]">
      <div class="order-2 space-y-6 lg:self-start" data-testid="images-column">
        {#if photos.length > 0}
          <div
            class="flex flex-wrap items-center gap-2"
            role="group"
            aria-label="Filter photos by kind"
            data-testid="photo-kind-filter"
          >
            {#each kindFilterOptions as opt (opt.value)}
              {#if opt.value === 'all' || opt.count > 0}
                {@const active = kindFilter === opt.value}
                <button
                  type="button"
                  onclick={() => (kindFilter = opt.value)}
                  data-testid={`photo-kind-filter-${opt.value}`}
                  data-active={active}
                  aria-pressed={active}
                  class="rounded-full border px-2.5 py-0.5 text-[11px] font-medium transition {active
                    ? 'border-[var(--color-accent)] bg-[var(--color-accent)] text-[var(--color-accent-fg)]'
                    : 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text-muted)] hover:border-[var(--color-accent)]'}"
                >
                  {opt.label}
                  <span class="ml-1 font-mono text-[10px] opacity-70">{opt.count}</span>
                </button>
              {/if}
            {/each}
          </div>
        {/if}

        {#if heroPhoto}
          {@const heroKind: PhotoKind = (heroPhoto.kind as PhotoKind | undefined) ?? 'visible'}
          {@const heroIsMain =
            specimen.main_image_id != null && heroPhoto.file_id === specimen.main_image_id}
          <div class="group relative">
            <button
              type="button"
              class="block w-full overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
              onclick={() => openLightboxForPhoto(heroPhoto)}
              aria-label="Open photo viewer"
              data-testid="hero-photo"
            >
              <img
                src={`/api/v1/photos/${heroPhoto.id}/display`}
                alt={`Photo of ${specimen.name}`}
                class="block h-auto w-full transition group-hover:opacity-95"
                loading="eager"
              />
            </button>
            {#if heroKind !== 'visible'}
              <span
                class="pointer-events-none absolute left-2 top-2 rounded-full bg-black/65 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-white"
                data-testid="hero-photo-kind-badge"
                data-kind={heroKind}
              >
                {PHOTO_KIND_LABELS[heroKind]}
              </span>
            {/if}
            {#if heroIsMain}
              <span
                class="pointer-events-none absolute bottom-2 left-2 inline-flex items-center gap-1 rounded-full bg-amber-500/90 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-white shadow"
                data-testid="hero-photo-main-badge"
                aria-label="Main image"
              >
                ★ Main
              </span>
            {:else if $isAuthenticated}
              <button
                type="button"
                onclick={() => setAsMain(heroPhoto.file_id)}
                disabled={settingMain}
                data-testid="hero-photo-set-main"
                class="absolute bottom-2 left-2 rounded-full bg-black/55 px-2 py-1 text-xs text-white opacity-0 transition-opacity hover:bg-amber-500 focus-visible:opacity-100 group-hover:opacity-100 disabled:cursor-not-allowed disabled:opacity-50"
              >
                ★ Set as main
              </button>
            {/if}
            {#if $isAuthenticated}
              <div
                class="absolute right-2 top-2 flex items-center gap-2 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100"
                data-testid="hero-photo-actions"
              >
                <button
                  type="button"
                  onclick={() => requestEditVisibility(heroPhoto.id)}
                  aria-label="Edit photo privacy"
                  data-testid="hero-photo-edit-visibility"
                  class="rounded-full bg-black/55 px-2 py-1 text-xs text-white hover:bg-[var(--color-accent)] hover:text-[var(--color-accent-fg)]"
                >
                  Privacy
                </button>
                <button
                  type="button"
                  onclick={() => requestEditKind(heroPhoto.id)}
                  aria-label="Edit photo type"
                  data-testid="hero-photo-edit-kind"
                  class="rounded-full bg-black/55 px-2 py-1 text-xs text-white hover:bg-[var(--color-accent)] hover:text-[var(--color-accent-fg)]"
                >
                  Edit type
                </button>
                <button
                  type="button"
                  onclick={() => requestCropPhoto(heroPhoto.id)}
                  aria-label="Crop / Rotate photo"
                  data-testid="hero-photo-crop"
                  class="rounded-full bg-black/55 px-2 py-1 text-xs text-white hover:bg-[var(--color-accent)] hover:text-[var(--color-accent-fg)]"
                >
                  Crop / Rotate
                </button>
                <button
                  type="button"
                  onclick={() => requestDeletePhoto(heroPhoto.id)}
                  aria-label="Delete photo"
                  data-testid="hero-photo-delete"
                  class="rounded-full bg-black/55 px-2 py-1 text-xs text-white hover:bg-red-600"
                >
                  ✕
                </button>
              </div>
            {/if}
          </div>

          {#if restPhotos.length > 0}
            <ul class="flex flex-wrap gap-3" data-testid="photo-gallery">
              {#each restPhotos as photo (photo.id)}
                {@const thumbKind: PhotoKind = (photo.kind as PhotoKind | undefined) ?? 'visible'}
                {@const isMain =
                  specimen.main_image_id != null && photo.file_id === specimen.main_image_id}
                <li class="contents">
                  <div class="group relative">
                    <button
                      type="button"
                      class="block h-20 w-20 overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] transition hover:border-[var(--color-accent)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
                      onclick={() => openLightboxForPhoto(photo)}
                      aria-label="View photo"
                      data-testid="gallery-thumb"
                    >
                      <img
                        src={`/api/v1/photos/${photo.id}/thumb`}
                        alt=""
                        class="h-full w-full object-cover"
                        loading="lazy"
                      />
                    </button>
                    {#if thumbKind !== 'visible'}
                      <span
                        class="pointer-events-none absolute bottom-1 left-1 rounded bg-black/70 px-1 text-[9px] font-semibold uppercase leading-3 tracking-wide text-white"
                        data-testid="gallery-thumb-kind-badge"
                        data-kind={thumbKind}
                      >
                        {PHOTO_KIND_LABELS[thumbKind]}
                      </span>
                    {/if}
                    {#if isMain}
                      <span
                        class="pointer-events-none absolute right-1 bottom-1 rounded bg-amber-500/90 px-1 text-[9px] font-semibold uppercase leading-4 tracking-wide text-white shadow"
                        data-testid="gallery-thumb-main-badge"
                        aria-label="Main image"
                      >
                        ★ Main
                      </span>
                    {:else if $isAuthenticated}
                      <button
                        type="button"
                        onclick={() => setAsMain(photo.file_id)}
                        disabled={settingMain}
                        aria-label="Set as main image"
                        data-testid="gallery-thumb-set-main"
                        class="absolute right-1 bottom-1 rounded-full bg-black/65 px-1.5 text-[10px] leading-5 text-white opacity-0 transition-opacity hover:bg-amber-500 focus-visible:opacity-100 group-hover:opacity-100 disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        ★
                      </button>
                    {/if}
                    {#if $isAuthenticated}
                      <button
                        type="button"
                        onclick={() => requestEditVisibility(photo.id)}
                        aria-label="Edit photo privacy"
                        data-testid="gallery-thumb-edit-visibility"
                        class="absolute left-1 top-1 rounded-full bg-black/65 px-1.5 text-[10px] leading-5 text-white opacity-0 transition-opacity hover:bg-[var(--color-accent)] hover:text-[var(--color-accent-fg)] focus-visible:opacity-100 group-hover:opacity-100"
                      >
                        Privacy
                      </button>
                      <button
                        type="button"
                        onclick={() => requestDeletePhoto(photo.id)}
                        aria-label="Delete photo"
                        data-testid="gallery-thumb-delete"
                        class="absolute right-1 top-1 rounded-full bg-black/65 px-1.5 text-[11px] leading-5 text-white opacity-0 transition-opacity hover:bg-red-600 focus-visible:opacity-100 group-hover:opacity-100"
                      >
                        ✕
                      </button>
                    {/if}
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        {/if}

        {#if $isAuthenticated}
          <PhotoUploader {specimenId} onUploaded={() => refetchPhotos(specimenId)} />
        {/if}
      </div>

      <div class="order-1 space-y-8" data-testid="info-column">
        {#if specimen.description.trim().length > 0}
          <section data-testid="description">
            <h2 class="mb-2 font-serif text-lg font-semibold text-[var(--color-text)]">
              Description
            </h2>
            <p
              class="whitespace-pre-wrap text-sm leading-relaxed text-[var(--color-text)]"
              data-testid="description-body"
            >
              {specimen.description}
            </p>
          </section>
        {/if}

        <section data-testid="journal-section">
          <div class="mb-3 flex items-center justify-between gap-2">
            <h2 class="font-serif text-lg font-semibold text-[var(--color-text)]">
              Observation journal
            </h2>
            {#if $isAuthenticated && !journalCreating}
              <button
                type="button"
                onclick={() => (journalCreating = true)}
                data-testid="journal-add-button"
                class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 py-1 text-xs text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
              >
                Add entry
              </button>
            {/if}
          </div>

          {#if $isAuthenticated && journalCreating}
            <div
              class="mb-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
              data-testid="journal-create-panel"
            >
              <JournalEntryForm
                submitLabel="Add entry"
                autofocus
                onSubmit={handleCreateEntry}
                onCancel={() => (journalCreating = false)}
              />
            </div>
          {/if}

          {#if journal.length === 0}
            <p class="text-sm text-[var(--color-text-muted)]" data-testid="journal-empty">
              No entries yet.
            </p>
          {:else}
            <ol class="space-y-4" data-testid="journal-list">
              {#each journal as entry (entry.id)}
                <li
                  class="group rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
                  data-testid="journal-entry"
                  data-entry-id={entry.id}
                >
                  <div class="mb-2 flex items-center gap-2 text-xs text-[var(--color-text-muted)]">
                    <time datetime={entry.created_at}>{fmtDateTime(entry.created_at)}</time>
                    {#if isEdited(entry)}
                      <span data-testid="edited-indicator" class="italic">· edited</span>
                    {/if}
                    <span class="ml-auto flex items-center gap-1">
                      {#if $isAuthenticated && editingEntryId !== entry.id}
                        <button
                          type="button"
                          onclick={() => (editingEntryId = entry.id)}
                          data-testid="journal-edit-button"
                          class="rounded-md px-2 py-0.5 text-[11px] text-[var(--color-text-muted)] opacity-0 transition-opacity hover:text-[var(--color-accent)] focus-visible:opacity-100 group-hover:opacity-100"
                          aria-label="Edit entry"
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          onclick={() => requestDeleteJournal(entry.id)}
                          data-testid="journal-delete-button"
                          class="rounded-md px-2 py-0.5 text-[11px] text-[var(--color-text-muted)] opacity-0 transition-opacity hover:text-red-500 focus-visible:opacity-100 group-hover:opacity-100"
                          aria-label="Delete entry"
                        >
                          ✕
                        </button>
                      {/if}
                    </span>
                  </div>
                  {#if editingEntryId === entry.id}
                    <JournalEntryForm
                      initial={{ body_md: entry.body_md }}
                      submitLabel="Save"
                      autofocus
                      onSubmit={makeEditHandler(entry.id)}
                      onCancel={() => (editingEntryId = null)}
                    />
                  {:else}
                    <div
                      class="prose-sm max-w-none text-sm leading-relaxed text-[var(--color-text)] [&>*+*]:mt-3 [&_a]:text-[var(--color-accent)] [&_a]:underline [&_blockquote]:border-l-2 [&_blockquote]:border-[var(--color-border)] [&_blockquote]:pl-3 [&_blockquote]:text-[var(--color-text-muted)] [&_code]:rounded [&_code]:bg-[var(--color-surface-2)] [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs [&_h1]:font-serif [&_h1]:text-base [&_h1]:font-semibold [&_h2]:font-serif [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:font-serif [&_h3]:text-sm [&_h3]:font-semibold [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-[var(--color-surface-2)] [&_pre]:p-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5"
                    >
                      <!--
                        body_html is server-sanitized via the
                        CONTRACT.md §17 markdown pipeline (goldmark
                        → bluemonday allowlist). Direct {@html} is
                        the contract's prescribed sink for this
                        pipeline output.
                      -->
                      <!-- eslint-disable-next-line svelte/no-at-html-tags -->
                      {@html entry.body_html}
                    </div>
                  {/if}
                  <div class="mt-3">
                    <JournalAttachments entryId={entry.id} />
                  </div>
                </li>
              {/each}
            </ol>
          {/if}
        </section>

        {#if phys.length > 0}
          <section data-testid="properties-section">
            <h2 class="mb-2 font-serif text-base font-semibold text-[var(--color-text)]">
              Properties
            </h2>
            <dl class="space-y-1 text-sm">
              {#each phys as row (row.label)}
                <div class="flex justify-between gap-2">
                  <dt class="text-[var(--color-text-muted)]">{row.label}</dt>
                  <dd class="text-right text-[var(--color-text)]">{row.value}</dd>
                </div>
              {/each}
            </dl>
          </section>
        {/if}

        {#if td.length > 0}
          <section data-testid="type-data-section">
            <h2 class="mb-2 font-serif text-base font-semibold text-[var(--color-text)]">
              {specimen.type === 'mineral'
                ? 'Mineralogy'
                : specimen.type === 'rock'
                  ? 'Petrology'
                  : 'Classification'}
            </h2>
            <dl class="space-y-1 text-sm">
              {#each td as row (row.key)}
                <div class="flex justify-between gap-2">
                  <dt class="text-[var(--color-text-muted)]">{row.label}</dt>
                  <dd class="text-right text-[var(--color-text)]">
                    {#if row.chips && row.chips.length > 0}
                      <span
                        class="inline-flex flex-wrap justify-end gap-1"
                        data-testid={`type-data-${row.key}`}
                      >
                        {#each row.chips as color (color)}
                          <span class="rounded-full border px-2 py-0.5 text-xs {chipClass(color)}">
                            {color}
                          </span>
                        {/each}
                      </span>
                    {:else}
                      {row.value}
                    {/if}
                  </dd>
                </div>
              {/each}
            </dl>
          </section>
        {/if}

        {#if specimen.locality_text || loc.length > 0}
          <section data-testid="locality-section">
            <h2 class="mb-2 font-serif text-base font-semibold text-[var(--color-text)]">
              Locality
            </h2>
            {#if specimen.locality_text}
              <p class="mb-2 text-sm text-[var(--color-text)]" data-testid="locality-text">
                {specimen.locality_text}
              </p>
            {/if}
            {#if loc.length > 0}
              <dl class="space-y-1 text-sm">
                {#each loc as row (row.label)}
                  <div class="flex justify-between gap-2">
                    <dt class="text-[var(--color-text-muted)]">{row.label}</dt>
                    <dd class="text-right text-[var(--color-text)]">{row.value}</dd>
                  </div>
                {/each}
              </dl>
            {/if}
          </section>
        {/if}

        <section data-testid="provenance-section">
          <div class="mb-2 flex items-center justify-between gap-2">
            <h2 class="font-serif text-base font-semibold text-[var(--color-text)]">
              Provenance chain
            </h2>
            {#if $isAuthenticated && !editingChain}
              <button
                type="button"
                onclick={() => (editingChain = true)}
                data-testid="edit-chain-button"
                class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 py-1 text-xs text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
              >
                Edit chain
              </button>
            {/if}
          </div>

          {#if $isAuthenticated && editingChain}
            <CollectorChainEditor
              {specimenId}
              initial={collectors.map((l) => ({ id: l.collector.id, name: l.collector.name }))}
              onSaved={async () => {
                await refetchCollectors(specimenId);
                editingChain = false;
              }}
              onCancel={() => (editingChain = false)}
            />
          {:else if collectors.length > 0}
            <ol class="space-y-1 text-sm" data-testid="provenance-list">
              {#each collectors as link (link.collector.id)}
                <li class="flex items-baseline gap-2" data-testid="provenance-entry">
                  <span class="font-mono text-xs text-[var(--color-text-muted)]"
                    >{link.position}.</span
                  >
                  <span class="text-[var(--color-text)]">{link.collector.name}</span>
                </li>
              {/each}
            </ol>
          {:else}
            <p class="text-sm text-[var(--color-text-muted)]" data-testid="provenance-empty">
              No collectors recorded.
            </p>
          {/if}
        </section>

        {#if specimen.source_notes}
          <section data-testid="provenance-notes-section">
            <h2 class="mb-2 font-serif text-base font-semibold text-[var(--color-text)]">
              Provenance notes
            </h2>
            <p class="whitespace-pre-wrap text-sm text-[var(--color-text)]">
              {specimen.source_notes}
            </p>
          </section>
        {/if}
      </div>
    </div>
  </article>

  {#if lightboxIndex !== null && visiblePhotos.length > 0}
    <Lightbox
      photos={lightboxPhotos}
      startIndex={lightboxIndex}
      onClose={closeLightbox}
      onDelete={$isAuthenticated ? requestDeletePhoto : undefined}
      onCrop={$isAuthenticated ? requestCropPhoto : undefined}
    />
  {/if}

  {#if cropTarget && specimen}
    <ImageCropModal
      specimenId={specimen.id}
      photoId={cropTarget.id}
      position={cropTarget.position}
      takenAt={cropTarget.taken_at ?? null}
      onClose={closeCrop}
      onApplied={handleCropApplied}
    />
  {/if}

  {#if editKindTarget}
    {@const currentKind: PhotoKind =
      (editKindTarget.kind as PhotoKind | undefined) ?? 'visible'}
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="edit-kind-title"
      data-testid="edit-kind-modal"
      onclick={(e) => {
        if (e.target === e.currentTarget) closeEditKind();
      }}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeEditKind();
      }}
      tabindex="-1"
    >
      <div
        class="w-full max-w-sm rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-5 shadow-xl"
      >
        <h2 id="edit-kind-title" class="font-serif text-lg font-semibold text-[var(--color-text)]">
          Photo type
        </h2>
        <p class="mt-1 text-xs text-[var(--color-text-muted)]">
          Lighting condition this photo was taken under.
        </p>
        <ul class="mt-4 space-y-2">
          {#each EDITABLE_PHOTO_KINDS as kind (kind)}
            {@const isCurrent = kind === currentKind}
            <li>
              <button
                type="button"
                onclick={() => applyEditKind(kind)}
                disabled={savingKind}
                data-testid={`edit-kind-option-${kind}`}
                aria-pressed={isCurrent}
                class="flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm transition disabled:cursor-not-allowed disabled:opacity-60 {isCurrent
                  ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)] text-[var(--color-text)]'
                  : 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text)] hover:border-[var(--color-accent)]'}"
              >
                <span>{PHOTO_KIND_LABELS[kind]}</span>
                {#if isCurrent}
                  <span
                    class="text-[10px] font-semibold uppercase tracking-wide text-[var(--color-accent)]"
                  >
                    Current
                  </span>
                {/if}
              </button>
            </li>
          {/each}
        </ul>
        <div class="mt-5 flex justify-end">
          <button
            type="button"
            onclick={closeEditKind}
            disabled={savingKind}
            data-testid="edit-kind-cancel"
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  {/if}

  {#if editVisibilityTarget}
    {@const currentVisibility =
      (editVisibilityTarget.visibility as Visibility | null | undefined) ?? null}
    {@const inheritLabel = VISIBILITY_LABELS[resolveImageInherit(editVisibilityTarget)]}
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="edit-visibility-title"
      data-testid="edit-visibility-modal"
      onclick={(e) => {
        if (e.target === e.currentTarget) closeEditVisibility();
      }}
      onkeydown={(e) => {
        if (e.key === 'Escape') closeEditVisibility();
      }}
      tabindex="-1"
    >
      <div
        class="w-full max-w-sm rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-5 shadow-xl"
      >
        <h2
          id="edit-visibility-title"
          class="font-serif text-lg font-semibold text-[var(--color-text)]"
        >
          Photo privacy
        </h2>
        <p class="mt-1 text-xs text-[var(--color-text-muted)]">
          Who can see this photo. Clearing the override falls back to this specimen's image default,
          then to your account default.
        </p>
        <ul class="mt-4 space-y-2">
          <li>
            <button
              type="button"
              onclick={() => applyEditVisibility(null)}
              disabled={savingVisibility}
              data-testid="edit-visibility-option-inherit"
              aria-pressed={currentVisibility === null}
              class="flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm transition disabled:cursor-not-allowed disabled:opacity-60 {currentVisibility ===
              null
                ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)] text-[var(--color-text)]'
                : 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text)] hover:border-[var(--color-accent)]'}"
            >
              <span>
                Use specimen images-default
                <span
                  class="ml-1 text-xs text-[var(--color-text-muted)]"
                  data-testid="edit-visibility-inherit-chip"
                >
                  (currently: {inheritLabel})
                </span>
              </span>
              {#if currentVisibility === null}
                <span
                  class="text-[10px] font-semibold uppercase tracking-wide text-[var(--color-accent)]"
                >
                  Current
                </span>
              {/if}
            </button>
          </li>
          {#each ['private', 'unlisted', 'public'] as Visibility[] as v (v)}
            {@const isCurrent = v === currentVisibility}
            <li>
              <button
                type="button"
                onclick={() => applyEditVisibility(v)}
                disabled={savingVisibility}
                data-testid={`edit-visibility-option-${v}`}
                aria-pressed={isCurrent}
                class="flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm transition disabled:cursor-not-allowed disabled:opacity-60 {isCurrent
                  ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)] text-[var(--color-text)]'
                  : 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text)] hover:border-[var(--color-accent)]'}"
              >
                <span>{VISIBILITY_LABELS[v]}</span>
                {#if isCurrent}
                  <span
                    class="text-[10px] font-semibold uppercase tracking-wide text-[var(--color-accent)]"
                  >
                    Current
                  </span>
                {/if}
              </button>
            </li>
          {/each}
        </ul>
        <div class="mt-5 flex justify-end">
          <button
            type="button"
            onclick={closeEditVisibility}
            disabled={savingVisibility}
            data-testid="edit-visibility-cancel"
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  {/if}

  {#if deleteTarget && deleteDialogProps}
    <ConfirmModal
      title={deleteDialogProps.title}
      message={deleteDialogProps.message}
      confirmLabel="Delete"
      busy={deleting}
      onConfirm={confirmDelete}
      onCancel={cancelDelete}
    />
  {/if}
{/if}
