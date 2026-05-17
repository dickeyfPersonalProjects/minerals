<script lang="ts" module>
  // Result of submitting the form. The route page handles the
  // actual API call and reports back via these variants.
  export type SpecimenFormSubmitResult =
    | { kind: 'ok' }
    | { kind: 'duplicate_catalog_number' }
    | { kind: 'field_error'; field: string; message: string }
    | { kind: 'error'; message: string };
</script>

<script lang="ts">
  import { createForm } from 'felte';
  import { validator } from '@felte/validator-zod';
  import { untrack } from 'svelte';
  import {
    emptyFormValues,
    resetTypeDataDefaults,
    specimenFormSchema,
    FLUORESCENCE_COLORS,
    VISIBILITY_INHERIT,
    type FluorescenceColor,
    type SpecimenFormValues,
    type SpecimenType,
    type VisibilityFieldValue,
  } from './schemas/specimen';
  import {
    resolveImage,
    resolveScalar,
    type OwnerLike,
    type Resolution,
    type SpecimenLike,
    type Visibility,
  } from './api/visibility';
  import MineralSpeciesLookup, { type MineralSpeciesView } from './MineralSpeciesLookup.svelte';

  interface Props {
    initial?: Partial<SpecimenFormValues>;
    mode: 'create' | 'edit';
    submitLabel: string;
    onSubmit: (values: SpecimenFormValues) => Promise<SpecimenFormSubmitResult>;
    onCancel?: () => void;
    cancelLabel?: string;
    onDelete?: () => void;
    deleteLabel?: string;
    // Owner profile (field_defaults) used by the per-field visibility
    // selectors to render their 'currently: X' affordance. Absent on
    // the create page (no specimen yet); the selectors degrade to the
    // system default ('private') in that case. CONTRACT.md §13b.
    ownerProfile?: OwnerLike | null;
  }

  const {
    initial,
    mode,
    submitLabel,
    onSubmit,
    onCancel,
    cancelLabel = 'Cancel',
    onDelete,
    deleteLabel = 'Delete',
    ownerProfile = null,
  }: Props = $props();

  // Capture initial values once at mount so the form owns its
  // state thereafter.
  const initialValues: SpecimenFormValues = untrack(() => ({
    ...emptyFormValues(initial?.type ?? 'mineral'),
    ...(initial ?? {}),
  }));

  let bannerError: string | null = $state(null);
  let catalogNumberError: string | null = $state(null);
  let fieldErrors: Record<string, string> = $state({});

  const { form, errors, isSubmitting, data, setData, setFields } = createForm<SpecimenFormValues>({
    initialValues,
    extend: validator({ schema: specimenFormSchema }),
    onSubmit: async (values) => {
      bannerError = null;
      catalogNumberError = null;
      fieldErrors = {};
      const result = await onSubmit(values);
      if (result.kind === 'duplicate_catalog_number') {
        catalogNumberError = 'A specimen with this catalog number already exists.';
        return;
      }
      if (result.kind === 'field_error') {
        fieldErrors = { [result.field]: result.message };
        bannerError = result.message;
        return;
      }
      if (result.kind === 'error') {
        bannerError = result.message;
        return;
      }
    },
  });

  // Type radio: when the user toggles type in create mode, swap
  // the type_data fields back to defaults for the new type. Edit
  // mode disables the type radio so this never fires there.
  let lastType = $state(initialValues.type);
  $effect(() => {
    const t = $data.type;
    if (t !== lastType) {
      const next = resetTypeDataDefaults($data, t as SpecimenType);
      setData(next);
      lastType = t;
    }
  });

  // Clear catalog-number duplicate error when the user edits the
  // catalog_number field.
  let lastCatalog = $state(initialValues.catalog_number);
  $effect(() => {
    if ($data.catalog_number !== lastCatalog) {
      lastCatalog = $data.catalog_number;
      if (catalogNumberError) catalogNumberError = null;
      if (fieldErrors.catalog_number) {
        const next = { ...fieldErrors };
        delete next.catalog_number;
        fieldErrors = next;
      }
    }
  });

  // Attribution string from the most recently selected mineral
  // species. Mindat's CC-BY-NC-SA 4.0 terms require this to be
  // shown next to the data — rendered below the mineral fieldset.
  let mineralAttribution: string | null = $state(null);

  function prefillMineralFromSpecies(s: MineralSpeciesView): void {
    const d = s.data ?? {};
    const next: SpecimenFormValues = {
      ...$data,
      m_chemical_formula: d.chemical_formula ?? '',
      m_crystal_system: d.crystal_system ?? '',
      m_mohs_hardness: d.mohs_hardness != null ? String(d.mohs_hardness) : '',
      m_color: d.color ?? '',
      m_luster: d.luster ?? '',
      // Mindat returns prose fluorescence text; the structured per-wavelength
      // model has no safe mapping for it. Leave the user to fill in by hand.
      m_fluorescence_sw: $data.m_fluorescence_sw,
      m_fluorescence_mw: $data.m_fluorescence_mw,
      m_fluorescence_lw: $data.m_fluorescence_lw,
      m_radioactive: Boolean(d.radioactive),
      m_magnetic: Boolean(d.magnetic),
      m_reacts_to_acid: Boolean(d.reacts_to_acid),
      m_mindat_id: d.mindat_id ?? '',
      m_mineral_species: (d.mineral_species ?? []).join(', '),
    };
    // setFields (not setData) — setData only updates the felte store,
    // leaving the rendered <input> values untouched. setFields also
    // pushes the new values into the DOM so the user can see (and
    // edit) what was fetched before saving.
    setFields(next);
    mineralAttribution = s.attribution ?? null;
  }

  function showError(name: keyof SpecimenFormValues): string | null {
    // felte runs the zod validator on every input/blur/submit, so
    // errors only surface after the user has interacted with a
    // field (matching the "only after touched" UX in the bead).
    const e = $errors[name];
    if (Array.isArray(e) && e.length > 0) return e[0]!;
    return null;
  }

  // Per-field visibility selectors (mi-fo8 #7).
  //
  // Tooltip text shared by all three selectors — explains the
  // resolution chain at a glance without redirecting to docs.
  const VIS_TOOLTIP =
    'This setting applies to this specimen. Your account default applies when no specimen-level value is set. ' +
    'Individual images can override the specimen default.';

  // formToSpecimenLike projects the live form's scalar overrides into
  // the SpecimenLike shape the resolver consumes. Used only for the
  // resolveImage chain, where the specimen.visibility column also
  // participates and so the live `$data.visibility` matters.
  function formToSpecimenLike(d: SpecimenFormValues): SpecimenLike {
    return {
      visibility: d.visibility as Visibility,
      visibility_price: wireFromSelect(d.visibility_price),
      visibility_acquired_from: wireFromSelect(d.visibility_acquired_from),
      visibility_images: wireFromSelect(d.visibility_images),
    };
  }

  function wireFromSelect(v: VisibilityFieldValue): Visibility | null {
    return v === VISIBILITY_INHERIT ? null : v;
  }

  // resolveInherit computes what the chain would resolve to for
  // `field` IF the user picks 'Use my account default' — i.e. with
  // the specimen-level override CLEARED. That's the value rendered
  // beside the inherit option as 'currently: X', so the user can
  // see exactly what the SPA will display.
  //
  // For scalar fields the answer is `owner.field_defaults[field]
  // ?? 'private'`. For images the chain still considers the
  // specimen's overall visibility (CONTRACT.md §13b), so we resolve
  // with visibility_images explicitly null and the rest from the
  // live form.
  function resolveInherit(field: 'price' | 'acquired_from' | 'images'): Resolution {
    const owner = ownerProfile ?? {};
    if (field === 'images') {
      const specForResolve: SpecimenLike = {
        ...formToSpecimenLike($data),
        visibility_images: null,
      };
      return resolveImage(specForResolve, owner, {});
    }
    const specForResolve: SpecimenLike = {
      visibility_price: null,
      visibility_acquired_from: null,
    };
    return resolveScalar(field, specForResolve, owner);
  }

  // Three reactive snapshots — re-compute when the form's visibility
  // changes (only relevant to images, but cheap for scalars too).
  const inheritPrice = $derived(resolveInherit('price'));
  const inheritAcquiredFrom = $derived(resolveInherit('acquired_from'));
  const inheritImages = $derived(resolveInherit('images'));

  function visibilityLabel(v: Visibility): string {
    switch (v) {
      case 'private':
        return 'Private';
      case 'unlisted':
        return 'Unlisted';
      case 'public':
        return 'Public';
    }
  }
</script>

<form use:form data-testid="specimen-form" class="space-y-6" novalidate>
  {#if bannerError}
    <div
      role="alert"
      data-testid="form-error"
      class="rounded-md border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300"
    >
      {bannerError}
    </div>
  {/if}

  <fieldset class="space-y-2" data-testid="type-fieldset" disabled={mode === 'edit'}>
    <legend class="block text-sm font-medium text-[var(--color-text)]">
      Type <span class="text-red-500" aria-hidden="true">*</span>
    </legend>
    <div class="flex flex-wrap gap-3">
      {#each ['mineral', 'rock', 'meteorite', 'fossil'] as t (t)}
        <label class="flex items-center gap-2 text-sm text-[var(--color-text)]">
          <input
            type="radio"
            name="type"
            value={t}
            checked={$data.type === t}
            disabled={mode === 'edit'}
            class="text-[var(--color-accent)]"
          />
          <span class="capitalize">{t}</span>
        </label>
      {/each}
    </div>
    {#if mode === 'edit'}
      <p class="text-xs text-[var(--color-text-muted)]" data-testid="type-immutable-hint">
        Type is immutable after creation.
      </p>
    {/if}
  </fieldset>

  <div class="grid gap-4 sm:grid-cols-2">
    <div>
      <label for="specimen-name" class="mb-1 block text-sm font-medium text-[var(--color-text)]">
        Name <span class="text-red-500" aria-hidden="true">*</span>
      </label>
      <input
        id="specimen-name"
        name="name"
        type="text"
        autocomplete="off"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        aria-invalid={Boolean(showError('name')) || Boolean(fieldErrors.name)}
        aria-describedby="specimen-name-error"
      />
      {#if showError('name')}
        <p id="specimen-name-error" data-testid="name-error" class="mt-1 text-xs text-red-500">
          {showError('name')}
        </p>
      {:else if fieldErrors.name}
        <p
          id="specimen-name-error"
          data-testid="name-error"
          class="mt-1 text-xs text-red-500"
          role="alert"
        >
          {fieldErrors.name}
        </p>
      {/if}
    </div>

    <div>
      <label
        for="specimen-catalog-number"
        class="mb-1 block text-sm font-medium text-[var(--color-text)]"
      >
        Catalog number
      </label>
      <input
        id="specimen-catalog-number"
        name="catalog_number"
        type="text"
        autocomplete="off"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        aria-invalid={Boolean(showError('catalog_number')) || Boolean(catalogNumberError)}
        aria-describedby="specimen-catalog-number-error"
      />
      {#if showError('catalog_number')}
        <p
          id="specimen-catalog-number-error"
          data-testid="catalog-number-error"
          class="mt-1 text-xs text-red-500"
        >
          {showError('catalog_number')}
        </p>
      {:else if catalogNumberError}
        <p
          id="specimen-catalog-number-error"
          data-testid="catalog-number-error"
          class="mt-1 text-xs text-red-500"
          role="alert"
        >
          {catalogNumberError}
        </p>
      {/if}
    </div>
  </div>

  <div>
    <label
      for="specimen-description"
      class="mb-1 block text-sm font-medium text-[var(--color-text)]"
    >
      Description
    </label>
    <textarea
      id="specimen-description"
      name="description"
      rows="5"
      class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 font-mono text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      aria-invalid={Boolean(showError('description'))}
    ></textarea>
    <p class="mt-1 text-xs text-[var(--color-text-muted)]">
      Markdown is rendered server-side (basic formatting, links, lists).
    </p>
    {#if showError('description')}
      <p data-testid="description-error" class="mt-1 text-xs text-red-500">
        {showError('description')}
      </p>
    {/if}
  </div>

  <div class="grid gap-4 sm:grid-cols-3">
    <div>
      <label
        for="specimen-visibility"
        class="mb-1 block text-sm font-medium text-[var(--color-text)]"
      >
        Visibility
      </label>
      <select
        id="specimen-visibility"
        name="visibility"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      >
        <option value="private">Private</option>
        <option value="unlisted">Unlisted</option>
        <option value="public">Public</option>
      </select>
    </div>

    <div>
      <label
        for="specimen-acquired-at"
        class="mb-1 block text-sm font-medium text-[var(--color-text)]"
      >
        Acquired (date)
      </label>
      <input
        id="specimen-acquired-at"
        name="acquired_at"
        type="date"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        aria-invalid={Boolean(showError('acquired_at'))}
      />
      {#if showError('acquired_at')}
        <p class="mt-1 text-xs text-red-500">{showError('acquired_at')}</p>
      {/if}
    </div>

    <div>
      <label
        for="specimen-acquired-from"
        class="mb-1 block text-sm font-medium text-[var(--color-text)]"
      >
        Acquired from
      </label>
      <input
        id="specimen-acquired-from"
        name="acquired_from"
        type="text"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
      <div class="mt-1.5">
        <label
          for="specimen-visibility-acquired-from"
          class="mb-0.5 block text-xs text-[var(--color-text-muted)]"
        >
          Who can see this field?
        </label>
        <select
          id="specimen-visibility-acquired-from"
          name="visibility_acquired_from"
          data-testid="visibility-acquired-from"
          title={VIS_TOOLTIP}
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-xs text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        >
          <option value={VISIBILITY_INHERIT}>
            Use my account default (currently: {visibilityLabel(inheritAcquiredFrom.visibility)})
          </option>
          <option value="private">Private</option>
          <option value="unlisted">Unlisted</option>
          <option value="public">Public</option>
        </select>
      </div>
    </div>
  </div>

  <div class="grid gap-4 sm:grid-cols-2">
    <div>
      <label
        for="specimen-price-dollars"
        class="mb-1 block text-sm font-medium text-[var(--color-text)]"
      >
        Price (USD)
      </label>
      <input
        id="specimen-price-dollars"
        name="price_dollars"
        type="number"
        inputmode="decimal"
        step="0.01"
        min="0"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        aria-invalid={Boolean(showError('price_dollars'))}
      />
      {#if showError('price_dollars')}
        <p class="mt-1 text-xs text-red-500">{showError('price_dollars')}</p>
      {/if}
      <div class="mt-1.5">
        <label
          for="specimen-visibility-price"
          class="mb-0.5 block text-xs text-[var(--color-text-muted)]"
        >
          Who can see this field?
        </label>
        <select
          id="specimen-visibility-price"
          name="visibility_price"
          data-testid="visibility-price"
          title={VIS_TOOLTIP}
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-xs text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        >
          <option value={VISIBILITY_INHERIT}>
            Use my account default (currently: {visibilityLabel(inheritPrice.visibility)})
          </option>
          <option value="private">Private</option>
          <option value="unlisted">Unlisted</option>
          <option value="public">Public</option>
        </select>
      </div>
    </div>

    <div>
      <label
        for="specimen-source-notes"
        class="mb-1 block text-sm font-medium text-[var(--color-text)]"
      >
        Source notes
      </label>
      <input
        id="specimen-source-notes"
        name="source_notes"
        type="text"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
    </div>
  </div>

  <fieldset class="space-y-2" data-testid="image-privacy-fieldset">
    <legend class="text-sm font-medium text-[var(--color-text)]">Image privacy</legend>
    <p class="text-xs text-[var(--color-text-muted)]">
      Controls the default visibility for this specimen's photos when an individual photo doesn't
      set its own.
    </p>
    <div>
      <label
        for="specimen-visibility-images"
        class="mb-0.5 block text-xs text-[var(--color-text-muted)]"
      >
        Default for new and uninherited photos
      </label>
      <select
        id="specimen-visibility-images"
        name="visibility_images"
        data-testid="visibility-images"
        title={VIS_TOOLTIP}
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-xs text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none sm:max-w-sm"
      >
        <option value={VISIBILITY_INHERIT}>
          Use my account default (currently: {visibilityLabel(inheritImages.visibility)})
        </option>
        <option value="private">Private</option>
        <option value="unlisted">Unlisted</option>
        <option value="public">Public</option>
      </select>
    </div>
  </fieldset>

  <fieldset class="space-y-3">
    <legend class="text-sm font-medium text-[var(--color-text)]">Locality</legend>
    <div>
      <label for="specimen-locality-text" class="mb-1 block text-xs text-[var(--color-text-muted)]">
        Free-form locality
      </label>
      <input
        id="specimen-locality-text"
        name="locality_text"
        type="text"
        placeholder="e.g. Tsumeb, Namibia"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
    </div>

    <details class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)]/40">
      <summary class="cursor-pointer px-3 py-2 text-xs text-[var(--color-text-muted)]">
        Structured locality (optional)
      </summary>
      <div class="grid gap-3 p-3 sm:grid-cols-2">
        <div>
          <label
            for="specimen-locality-country"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Country
          </label>
          <input
            id="specimen-locality-country"
            name="locality_country"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-locality-region"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Region
          </label>
          <input
            id="specimen-locality-region"
            name="locality_region"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-locality-site"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Site
          </label>
          <input
            id="specimen-locality-site"
            name="locality_site"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-locality-mindat"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            mindat ID
          </label>
          <input
            id="specimen-locality-mindat"
            name="locality_mindat_id"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-locality-lat"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Latitude
          </label>
          <input
            id="specimen-locality-lat"
            name="locality_lat"
            type="number"
            step="any"
            min="-90"
            max="90"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            aria-invalid={Boolean(showError('locality_lat'))}
          />
          {#if showError('locality_lat')}
            <p class="mt-1 text-xs text-red-500">{showError('locality_lat')}</p>
          {/if}
        </div>
        <div>
          <label
            for="specimen-locality-lon"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Longitude
          </label>
          <input
            id="specimen-locality-lon"
            name="locality_lon"
            type="number"
            step="any"
            min="-180"
            max="180"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            aria-invalid={Boolean(showError('locality_lon'))}
          />
          {#if showError('locality_lon')}
            <p class="mt-1 text-xs text-red-500">{showError('locality_lon')}</p>
          {/if}
        </div>
      </div>
    </details>
  </fieldset>

  <fieldset class="space-y-3">
    <legend class="text-sm font-medium text-[var(--color-text)]">Physical</legend>
    <div class="grid gap-3 sm:grid-cols-4">
      <div>
        <label for="specimen-mass-g" class="mb-1 block text-xs text-[var(--color-text-muted)]">
          Mass (g)
        </label>
        <input
          id="specimen-mass-g"
          name="mass_g"
          type="number"
          step="any"
          min="0"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          aria-invalid={Boolean(showError('mass_g'))}
        />
        {#if showError('mass_g')}<p class="mt-1 text-xs text-red-500">{showError('mass_g')}</p>{/if}
      </div>
      <div>
        <label for="specimen-length-mm" class="mb-1 block text-xs text-[var(--color-text-muted)]">
          Length (mm)
        </label>
        <input
          id="specimen-length-mm"
          name="length_mm"
          type="number"
          step="any"
          min="0"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        />
      </div>
      <div>
        <label for="specimen-width-mm" class="mb-1 block text-xs text-[var(--color-text-muted)]">
          Width (mm)
        </label>
        <input
          id="specimen-width-mm"
          name="width_mm"
          type="number"
          step="any"
          min="0"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        />
      </div>
      <div>
        <label for="specimen-height-mm" class="mb-1 block text-xs text-[var(--color-text-muted)]">
          Height (mm)
        </label>
        <input
          id="specimen-height-mm"
          name="height_mm"
          type="number"
          step="any"
          min="0"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
        />
      </div>
    </div>
  </fieldset>

  {#if $data.type === 'mineral'}
    <fieldset class="space-y-3" data-testid="mineral-fields">
      <legend class="text-sm font-medium text-[var(--color-text)]">Mineralogy</legend>
      <MineralSpeciesLookup initialQuery={$data.name} onSelect={prefillMineralFromSpecies} />
      <div class="grid gap-3 sm:grid-cols-2">
        <div>
          <label
            for="specimen-m-chemical-formula"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Chemical formula
          </label>
          <input
            id="specimen-m-chemical-formula"
            name="m_chemical_formula"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 font-mono text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-m-mineral-species"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Mineral species (comma-separated)
          </label>
          <input
            id="specimen-m-mineral-species"
            name="m_mineral_species"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-m-crystal-system"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Crystal system
          </label>
          <input
            id="specimen-m-crystal-system"
            name="m_crystal_system"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-m-mohs-hardness"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Hardness (Mohs, 0–10)
          </label>
          <input
            id="specimen-m-mohs-hardness"
            name="m_mohs_hardness"
            type="number"
            step="any"
            min="0"
            max="10"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            aria-invalid={Boolean(showError('m_mohs_hardness'))}
          />
          {#if showError('m_mohs_hardness')}
            <p class="mt-1 text-xs text-red-500">{showError('m_mohs_hardness')}</p>
          {/if}
        </div>
        <div>
          <label for="specimen-m-color" class="mb-1 block text-xs text-[var(--color-text-muted)]">
            Color
          </label>
          <input
            id="specimen-m-color"
            name="m_color"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label for="specimen-m-luster" class="mb-1 block text-xs text-[var(--color-text-muted)]">
            Luster
          </label>
          <input
            id="specimen-m-luster"
            name="m_luster"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-m-mindat-id"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            mindat ID
          </label>
          <input
            id="specimen-m-mindat-id"
            name="m_mindat_id"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
      </div>

      <fieldset class="space-y-2" data-testid="fluorescence-fieldset">
        <legend class="text-xs font-medium text-[var(--color-text-muted)]">
          UV fluorescence (select 'None' if non-fluorescent under that wavelength)
        </legend>
        {#each [{ wave: 'sw', label: 'Shortwave (254 nm)', field: 'm_fluorescence_sw' }, { wave: 'mw', label: 'Midwave (~312 nm)', field: 'm_fluorescence_mw' }, { wave: 'lw', label: 'Longwave (~365 nm)', field: 'm_fluorescence_lw' }] as { wave, label, field } (wave)}
          {@const selected = $data[field as keyof SpecimenFormValues] as FluorescenceColor[]}
          {@const none = selected.length === 0}
          <div
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)]/40 p-3"
            data-testid={`fluorescence-row-${wave}`}
          >
            <div class="mb-2 flex items-center justify-between">
              <span class="text-xs font-medium text-[var(--color-text)]">{label}</span>
              <label class="flex items-center gap-1 text-xs text-[var(--color-text-muted)]">
                <input
                  type="checkbox"
                  checked={none}
                  data-testid={`fluorescence-${wave}-none`}
                  onchange={(e) => {
                    if ((e.currentTarget as HTMLInputElement).checked) {
                      setData(field as keyof SpecimenFormValues, [] as FluorescenceColor[]);
                    }
                  }}
                />
                None
              </label>
            </div>
            <div class="flex flex-wrap gap-2">
              {#each FLUORESCENCE_COLORS as color (color)}
                {@const on = selected.includes(color)}
                <button
                  type="button"
                  data-testid={`fluorescence-${wave}-${color}`}
                  aria-pressed={on}
                  onclick={() => {
                    const cur = $data[field as keyof SpecimenFormValues] as FluorescenceColor[];
                    const next = cur.includes(color)
                      ? cur.filter((c) => c !== color)
                      : [...cur, color];
                    setData(field as keyof SpecimenFormValues, next);
                  }}
                  class="rounded-full border px-2.5 py-0.5 text-xs transition-colors {on
                    ? 'border-[var(--color-accent)] bg-[var(--color-accent)] text-[var(--color-accent-fg)]'
                    : 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text)] hover:bg-[var(--color-surface-2)]'}"
                >
                  {color}
                </button>
              {/each}
            </div>
          </div>
        {/each}
      </fieldset>

      <label class="flex items-center gap-2 text-sm text-[var(--color-text)]">
        <input
          type="checkbox"
          name="m_radioactive"
          checked={$data.m_radioactive}
          class="text-[var(--color-accent)]"
        />
        Radioactive
      </label>
      <label class="flex items-center gap-2 text-sm text-[var(--color-text)]">
        <input
          type="checkbox"
          name="m_magnetic"
          checked={$data.m_magnetic}
          class="text-[var(--color-accent)]"
        />
        Magnetic
      </label>
      <label class="flex items-center gap-2 text-sm text-[var(--color-text)]">
        <input
          type="checkbox"
          name="m_reacts_to_acid"
          checked={$data.m_reacts_to_acid}
          class="text-[var(--color-accent)]"
        />
        Reacts to acid
      </label>
      {#if mineralAttribution}
        <p class="text-xs italic text-[var(--color-text-muted)]" data-testid="mineral-attribution">
          {mineralAttribution}
        </p>
      {/if}
    </fieldset>
  {:else if $data.type === 'rock'}
    <fieldset class="space-y-3" data-testid="rock-fields">
      <legend class="text-sm font-medium text-[var(--color-text)]">Petrology</legend>
      <div class="grid gap-3 sm:grid-cols-2">
        <div>
          <label
            for="specimen-r-rock-type"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Rock type
          </label>
          <select
            id="specimen-r-rock-type"
            name="r_rock_type"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          >
            <option value="">—</option>
            <option value="igneous">Igneous</option>
            <option value="sedimentary">Sedimentary</option>
            <option value="metamorphic">Metamorphic</option>
          </select>
        </div>
        <div>
          <label
            for="specimen-r-composition"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Composition
          </label>
          <input
            id="specimen-r-composition"
            name="r_composition"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div class="sm:col-span-2">
          <label
            for="specimen-r-formation-context"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Formation context
          </label>
          <input
            id="specimen-r-formation-context"
            name="r_formation_context"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
      </div>
    </fieldset>
  {:else if $data.type === 'meteorite'}
    <fieldset class="space-y-3" data-testid="meteorite-fields">
      <legend class="text-sm font-medium text-[var(--color-text)]">Classification</legend>
      <div class="grid gap-3 sm:grid-cols-2">
        <div>
          <label
            for="specimen-me-classification"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Classification (e.g. L6, CV3)
          </label>
          <input
            id="specimen-me-classification"
            name="me_classification"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 font-mono text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-me-fall-or-find"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Fall or find
          </label>
          <select
            id="specimen-me-fall-or-find"
            name="me_fall_or_find"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          >
            <option value="">—</option>
            <option value="fall">Fall</option>
            <option value="find">Find</option>
          </select>
        </div>
        <div>
          <label
            for="specimen-me-fall-or-find-date"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Fall/find date
          </label>
          <input
            id="specimen-me-fall-or-find-date"
            name="me_fall_or_find_date"
            type="date"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            aria-invalid={Boolean(showError('me_fall_or_find_date'))}
          />
          {#if showError('me_fall_or_find_date')}
            <p class="mt-1 text-xs text-red-500">{showError('me_fall_or_find_date')}</p>
          {/if}
        </div>
        <div>
          <label
            for="specimen-me-official-name"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Official name
          </label>
          <input
            id="specimen-me-official-name"
            name="me_official_name"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-me-total-known-weight-g"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Total known weight (g)
          </label>
          <input
            id="specimen-me-total-known-weight-g"
            name="me_total_known_weight_g"
            type="number"
            step="any"
            min="0"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            aria-invalid={Boolean(showError('me_total_known_weight_g'))}
          />
          {#if showError('me_total_known_weight_g')}
            <p class="mt-1 text-xs text-red-500">{showError('me_total_known_weight_g')}</p>
          {/if}
        </div>
        <div>
          <label
            for="specimen-me-metbull-ref"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Met. Bulletin ref
          </label>
          <input
            id="specimen-me-metbull-ref"
            name="me_metbull_ref"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
      </div>
    </fieldset>
  {:else}
    <fieldset class="space-y-3" data-testid="fossil-fields">
      <legend class="text-sm font-medium text-[var(--color-text)]">Paleontology</legend>
      <div class="grid gap-3 sm:grid-cols-2">
        <div>
          <label for="specimen-f-taxon" class="mb-1 block text-xs text-[var(--color-text-muted)]">
            Taxon
          </label>
          <input
            id="specimen-f-taxon"
            name="f_taxon"
            type="text"
            placeholder="e.g. Tyrannosaurus rex"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-f-taxonomic-group"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Taxonomic group
          </label>
          <input
            id="specimen-f-taxonomic-group"
            name="f_taxonomic_group"
            type="text"
            placeholder="e.g. Dinosauria"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-f-geologic-period"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Geologic period
          </label>
          <input
            id="specimen-f-geologic-period"
            name="f_geologic_period"
            type="text"
            placeholder="e.g. Cretaceous"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-f-formation"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Formation
          </label>
          <input
            id="specimen-f-formation"
            name="f_formation"
            type="text"
            placeholder="e.g. Hell Creek Formation"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div class="sm:col-span-2">
          <label
            for="specimen-f-locality"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Stratigraphic locality
          </label>
          <input
            id="specimen-f-locality"
            name="f_locality"
            type="text"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-f-preservation-type"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Preservation type
          </label>
          <input
            id="specimen-f-preservation-type"
            name="f_preservation_type"
            type="text"
            placeholder="e.g. Permineralized"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div>
          <label
            for="specimen-f-completeness"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Completeness
          </label>
          <input
            id="specimen-f-completeness"
            name="f_completeness"
            type="text"
            placeholder="e.g. Complete, Partial, Fragment"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          />
        </div>
        <div class="sm:col-span-2">
          <label
            for="specimen-f-prep-notes"
            class="mb-1 block text-xs text-[var(--color-text-muted)]"
          >
            Prep notes
          </label>
          <textarea
            id="specimen-f-prep-notes"
            name="f_prep_notes"
            rows="3"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          ></textarea>
        </div>
      </div>
      <label class="flex items-center gap-2 text-sm text-[var(--color-text)]">
        <input
          type="checkbox"
          name="f_prepared"
          checked={$data.f_prepared}
          class="text-[var(--color-accent)]"
        />
        Prep work done
      </label>
    </fieldset>
  {/if}

  <div class="flex items-center gap-2 pt-2">
    <button
      type="submit"
      disabled={$isSubmitting}
      data-testid="submit-button"
      class="rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
    >
      {$isSubmitting ? 'Saving…' : submitLabel}
    </button>
    {#if onCancel}
      <button
        type="button"
        onclick={onCancel}
        disabled={$isSubmitting}
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-60"
      >
        {cancelLabel}
      </button>
    {/if}
    {#if onDelete}
      <button
        type="button"
        onclick={onDelete}
        disabled={$isSubmitting}
        data-testid="delete-button"
        class="ml-auto rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {deleteLabel}
      </button>
    {/if}
  </div>
</form>
