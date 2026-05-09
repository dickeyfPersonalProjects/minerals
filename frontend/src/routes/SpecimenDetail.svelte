<script lang="ts">
  import { link } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import { SUPPRESS_TOAST_HEADERS } from '../lib/api/wrapper';
  import type { components } from '../lib/api/schema';
  import CollectorChainEditor from '../lib/CollectorChainEditor.svelte';
  import ConfirmModal from '../lib/ConfirmModal.svelte';
  import JournalAttachments from '../lib/JournalAttachments.svelte';
  import JournalEntryForm, {
    type JournalEntryFormSubmitResult,
  } from '../lib/JournalEntryForm.svelte';
  import type { JournalEntryFormValues } from '../lib/schemas/journal';
  import Lightbox from '../lib/Lightbox.svelte';
  import PhotoUploader from '../lib/PhotoUploader.svelte';
  import { formatLocal } from '../lib/time';
  import { toastError, toastSuccess } from '../lib/toasts';

  type Specimen = components['schemas']['SpecimenView'];
  type Photo = components['schemas']['PhotoView'];
  type Journal = components['schemas']['JournalView'];
  type MineralData = components['schemas']['MineralData'];
  type RockData = components['schemas']['RockData'];
  type MeteoriteData = components['schemas']['MeteoriteData'];
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

    try {
      const [s, p, j, c] = await Promise.all([specimenP, photosP, journalP, collectorsP]);
      if (s.error) {
        loadState = { kind: 'error', message: errorMessage(s.error, s.response.status) };
        return;
      }
      specimen = s.data ?? null;
      photos = p.data?.items ?? [];
      journal = j.data?.items ?? [];
      collectors = c.data?.items ?? [];
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
    if (photos.length === 0) return;
    lightboxIndex = Math.max(0, Math.min(idx, photos.length - 1));
  }

  function closeLightbox() {
    lightboxIndex = null;
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
    fluorescence: 'Fluorescence',
    radioactive: 'Radioactive',
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
  };

  function titleCase(key: string): string {
    return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
  }

  type TypeDatum = { key: string; label: string; value: string };

  function typeDataEntries(s: Specimen): TypeDatum[] {
    const td = (s.type_data ?? {}) as Partial<MineralData & RockData & MeteoriteData> &
      Record<string, unknown>;
    const out: TypeDatum[] = [];
    for (const [key, raw] of Object.entries(td)) {
      if (raw === null || raw === undefined || raw === '') continue;
      let value: string;
      if (Array.isArray(raw)) {
        if (raw.length === 0) continue;
        value = raw.join(', ');
      } else if (typeof raw === 'boolean') {
        value = raw ? 'Yes' : 'No';
      } else if (key === 'fall_or_find_date' && typeof raw === 'string') {
        value = fmtDate(raw);
        if (!value) continue;
      } else {
        value = String(raw);
      }
      out.push({ key, label: TYPE_DATA_LABELS[key] ?? titleCase(key), value });
    }
    return out;
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
  };

  const lightboxPhotos = $derived(
    photos.map((p) => ({ id: p.id, alt: specimen ? `Photo of ${specimen.name}` : 'Photo' })),
  );
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
  {@const heroPhoto = photos[0]}
  {@const restPhotos = photos.slice(1)}
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
        <a
          href={`/specimens/${specimen.id}/edit`}
          use:link
          data-testid="edit-specimen"
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
        >
          Edit
        </a>
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

    {#if heroPhoto}
      <div class="group relative">
        <button
          type="button"
          class="block w-full overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
          onclick={() => openLightbox(0)}
          aria-label="Open photo viewer"
          data-testid="hero-photo"
        >
          <img
            src={`/api/v1/photos/${heroPhoto.id}/display`}
            alt={`Photo of ${specimen.name}`}
            class="aspect-[16/9] w-full object-cover transition group-hover:opacity-95"
            loading="eager"
          />
        </button>
        <button
          type="button"
          onclick={() => requestDeletePhoto(heroPhoto.id)}
          aria-label="Delete photo"
          data-testid="hero-photo-delete"
          class="absolute right-2 top-2 rounded-full bg-black/55 px-2 py-1 text-xs text-white opacity-0 transition-opacity hover:bg-red-600 focus-visible:opacity-100 group-hover:opacity-100"
        >
          ✕
        </button>
      </div>

      {#if restPhotos.length > 0}
        <ul class="flex flex-wrap gap-3" data-testid="photo-gallery">
          {#each restPhotos as photo, i (photo.id)}
            <li class="contents">
              <div class="group relative">
                <button
                  type="button"
                  class="block h-20 w-20 overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] transition hover:border-[var(--color-accent)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
                  onclick={() => openLightbox(i + 1)}
                  aria-label={`View photo ${i + 2}`}
                  data-testid="gallery-thumb"
                >
                  <img
                    src={`/api/v1/photos/${photo.id}/thumb`}
                    alt=""
                    class="h-full w-full object-cover"
                    loading="lazy"
                  />
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
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}

    <PhotoUploader {specimenId} onUploaded={() => refetchPhotos(specimenId)} />

    <div class="grid gap-8 lg:grid-cols-[2fr_1fr]">
      <div class="space-y-8">
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
            {#if !journalCreating}
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

          {#if journalCreating}
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
                      {#if editingEntryId !== entry.id}
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
      </div>

      <aside class="space-y-6">
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
                  <dd class="text-right text-[var(--color-text)]">{row.value}</dd>
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
            {#if !editingChain}
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

          {#if editingChain}
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
      </aside>
    </div>
  </article>

  {#if lightboxIndex !== null && photos.length > 0}
    <Lightbox
      photos={lightboxPhotos}
      startIndex={lightboxIndex}
      onClose={closeLightbox}
      onDelete={requestDeletePhoto}
    />
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
