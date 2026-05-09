<script lang="ts">
  // Shared create/edit form for specimens (CONTRACT.md §7b — felte
  // + @felte/validator-zod). Parameterized by `mode` and a
  // `submit` handler so the route component owns the API call and
  // the post-success navigation.
  //
  // - In `create` mode, the user picks a type and the type_data
  //   block resets to that type's defaults whenever they switch.
  // - In `edit` mode, the type radio is disabled (CONTRACT.md §10:
  //   type is immutable on PATCH).
  //
  // 4xx error envelopes from the backend are surfaced in two
  // layers: a banner for the top-level message, and field-level
  // errors when the envelope includes a `details.field` pointer.
  import { untrack } from 'svelte';
  import { createForm } from 'felte';
  import { validator } from '@felte/validator-zod';
  import {
    specimenFormSchema,
    emptyFormInput,
    emptyTypeData,
    fromSpecimenView,
    type SpecimenFormInput,
    type SpecimenFormValues,
  } from './schemas/specimen';
  import type { components } from './api/schema';

  type SpecimenView = components['schemas']['SpecimenView'];
  type ApiErrorBody = components['schemas']['ApiErrorBody'];

  interface Props {
    mode: 'create' | 'edit';
    initial?: SpecimenView;
    submit: (
      values: SpecimenFormValues,
    ) => Promise<{ ok: true } | { ok: false; error: ApiErrorBody | null; status: number }>;
    cancelHref: string;
  }

  const { mode, initial, submit, cancelHref }: Props = $props();

  // Submission feedback state, kept here (not in felte) so we can
  // surface the backend's error envelope alongside zod errors.
  let banner = $state<{ message: string } | null>(null);
  let fieldErrors = $state<Record<string, string>>({});

  const initialFormValues = untrack(() => (initial ? fromSpecimenView(initial) : emptyFormInput()));

  const { form, errors, touched, isSubmitting, data, setFields } = createForm<SpecimenFormInput>({
    initialValues: initialFormValues,
    extend: validator({ schema: specimenFormSchema }),
    onSubmit: async (values) => {
      banner = null;
      fieldErrors = {};
      // Validation already passed via the validator extension; we
      // re-parse to obtain the typed (post-coercion) shape.
      const parsed = specimenFormSchema.parse(values);
      const result = await submit(parsed);
      if (!result.ok) {
        const env = result.error;
        const message = env?.message ?? `Request failed (HTTP ${result.status})`;
        const field =
          env?.details && typeof env.details === 'object' && 'field' in env.details
            ? String((env.details as { field?: unknown }).field ?? '')
            : '';
        if (field) {
          fieldErrors = { ...fieldErrors, [field]: env?.message ?? 'invalid' };
        }
        banner = { message };
        // Throw so felte clears `isSubmitting`; our own state
        // already drives the UI.
        throw new Error(message);
      }
    },
    onError: () => {
      // Swallow — banner already surfaces the failure.
    },
  });

  // In create mode, switching `type` swaps the relevant type_data
  // shape — clear the block so stale fields from the prior type
  // don't ride along on submit.
  let lastType: SpecimenFormInput['type'] | null = null;
  $effect(() => {
    const current = $data.type;
    if (mode === 'create' && lastType !== null && current !== lastType) {
      setFields('type_data', emptyTypeData(), false);
    }
    lastType = current;
  });

  // Helpers for templates.
  function showError(name: string): string | undefined {
    // Field-level API error first (4xx mapping), then zod errors
    // — but only after the field is touched.
    if (fieldErrors[name]) return fieldErrors[name];
    if (!isTouched(name)) return undefined;
    const e = readError(name);
    return e;
  }

  function isTouched(path: string): boolean {
    return readPath($touched, path) === true;
  }

  function readError(path: string): string | undefined {
    const v = readPath($errors, path);
    if (Array.isArray(v) && v.length > 0) return String(v[0]);
    if (typeof v === 'string') return v;
    return undefined;
  }

  function readPath(obj: unknown, path: string): unknown {
    const parts = path.split('.');
    let cur: unknown = obj;
    for (const p of parts) {
      if (cur && typeof cur === 'object' && p in (cur as Record<string, unknown>)) {
        cur = (cur as Record<string, unknown>)[p];
      } else {
        return undefined;
      }
    }
    return cur;
  }

  const typeOptions: { value: SpecimenFormInput['type']; label: string }[] = [
    { value: 'mineral', label: 'Mineral' },
    { value: 'rock', label: 'Rock' },
    { value: 'meteorite', label: 'Meteorite' },
  ];

  const visibilityOptions: { value: SpecimenFormInput['visibility']; label: string }[] = [
    { value: 'private', label: 'Private' },
    { value: 'unlisted', label: 'Unlisted' },
    { value: 'public', label: 'Public' },
  ];
</script>

<form use:form data-testid="specimen-form" class="space-y-8" novalidate>
  {#if banner}
    <div
      role="alert"
      data-testid="form-error-banner"
      class="rounded-md border border-[var(--color-danger,#b91c1c)] bg-[var(--color-danger,#b91c1c)]/10 px-3 py-2 text-sm text-[var(--color-text)]"
    >
      {banner.message}
    </div>
  {/if}

  <fieldset class="space-y-3">
    <legend class="font-serif text-base font-semibold text-[var(--color-text)]">Type</legend>
    <div class="flex flex-wrap gap-3" role="radiogroup" aria-label="Specimen type">
      {#each typeOptions as opt (opt.value)}
        <label
          class="inline-flex cursor-pointer items-center gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm has-checked:border-[var(--color-accent)] has-checked:bg-[var(--color-accent)]/10 has-disabled:cursor-not-allowed has-disabled:opacity-60"
        >
          <input
            type="radio"
            name="type"
            value={opt.value}
            disabled={mode === 'edit'}
            data-testid={`type-radio-${opt.value}`}
            class="accent-[var(--color-accent)]"
          />
          <span>{opt.label}</span>
        </label>
      {/each}
    </div>
    {#if mode === 'edit'}
      <p class="text-xs text-[var(--color-text-muted)]" data-testid="type-locked">
        Type is immutable; reclassify by deleting and re-creating the specimen.
      </p>
    {/if}
  </fieldset>

  <fieldset class="grid gap-4 sm:grid-cols-2">
    <label class="block text-sm">
      <span class="mb-1 block text-[var(--color-text-muted)]">
        Name <span class="text-[var(--color-danger,#b91c1c)]" aria-hidden="true">*</span>
      </span>
      <input
        type="text"
        name="name"
        required
        aria-invalid={showError('name') ? 'true' : undefined}
        data-testid="field-name"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
      {#if showError('name')}
        <span class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]" data-testid="error-name"
          >{showError('name')}</span
        >
      {/if}
    </label>

    <label class="block text-sm">
      <span class="mb-1 block text-[var(--color-text-muted)]">Catalog number</span>
      <input
        type="text"
        name="catalog_number"
        aria-invalid={showError('catalog_number') ? 'true' : undefined}
        data-testid="field-catalog-number"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 font-mono text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
      {#if showError('catalog_number')}
        <span
          class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]"
          data-testid="error-catalog-number">{showError('catalog_number')}</span
        >
      {/if}
    </label>

    <label class="block text-sm sm:col-span-2">
      <span class="mb-1 flex items-baseline justify-between gap-2 text-[var(--color-text-muted)]">
        <span>Description</span>
        <span class="text-[10px] text-[var(--color-text-muted)]">Markdown rendered server-side</span
        >
      </span>
      <textarea
        name="description"
        rows="6"
        data-testid="field-description"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 font-mono text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      ></textarea>
    </label>

    <fieldset class="space-y-2 sm:col-span-2">
      <legend class="text-sm text-[var(--color-text-muted)]">Visibility</legend>
      <div class="flex flex-wrap gap-3" role="radiogroup" aria-label="Visibility">
        {#each visibilityOptions as opt (opt.value)}
          <label class="inline-flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="visibility"
              value={opt.value}
              data-testid={`visibility-${opt.value}`}
              class="accent-[var(--color-accent)]"
            />
            <span>{opt.label}</span>
          </label>
        {/each}
      </div>
    </fieldset>
  </fieldset>

  <fieldset class="grid gap-4 sm:grid-cols-2">
    <legend class="col-span-full font-serif text-base font-semibold text-[var(--color-text)]">
      Acquisition
    </legend>
    <label class="block text-sm">
      <span class="mb-1 block text-[var(--color-text-muted)]">Acquired</span>
      <input
        type="date"
        name="acquired_at"
        data-testid="field-acquired-at"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
    </label>
    <label class="block text-sm">
      <span class="mb-1 block text-[var(--color-text-muted)]">Acquired from</span>
      <input
        type="text"
        name="acquired_from"
        data-testid="field-acquired-from"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
    </label>
    <label class="block text-sm">
      <span class="mb-1 block text-[var(--color-text-muted)]">Price (USD)</span>
      <input
        type="number"
        step="0.01"
        min="0"
        name="price_dollars"
        aria-invalid={showError('price_dollars') ? 'true' : undefined}
        data-testid="field-price"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
      {#if showError('price_dollars')}
        <span
          class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]"
          data-testid="error-price">{showError('price_dollars')}</span
        >
      {/if}
    </label>
    <label class="block text-sm sm:col-span-2">
      <span class="mb-1 block text-[var(--color-text-muted)]">Source notes</span>
      <textarea
        name="source_notes"
        rows="2"
        data-testid="field-source-notes"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      ></textarea>
    </label>
  </fieldset>

  <fieldset class="grid gap-4 sm:grid-cols-2">
    <legend class="col-span-full font-serif text-base font-semibold text-[var(--color-text)]">
      Physical
    </legend>
    <label class="block text-sm">
      <span class="mb-1 block text-[var(--color-text-muted)]">Mass (g)</span>
      <input
        type="number"
        step="0.001"
        min="0"
        name="mass_g"
        aria-invalid={showError('mass_g') ? 'true' : undefined}
        data-testid="field-mass"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
      {#if showError('mass_g')}
        <span class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]" data-testid="error-mass"
          >{showError('mass_g')}</span
        >
      {/if}
    </label>
    <fieldset class="grid grid-cols-3 gap-2">
      <legend class="col-span-3 mb-1 text-sm text-[var(--color-text-muted)]">Dimensions (mm)</legend
      >
      <input
        type="number"
        step="0.1"
        min="0"
        name="dimensions.length_mm"
        placeholder="L"
        aria-label="Length in millimeters"
        data-testid="field-dim-length"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)]"
      />
      <input
        type="number"
        step="0.1"
        min="0"
        name="dimensions.width_mm"
        placeholder="W"
        aria-label="Width in millimeters"
        data-testid="field-dim-width"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)]"
      />
      <input
        type="number"
        step="0.1"
        min="0"
        name="dimensions.height_mm"
        placeholder="H"
        aria-label="Height in millimeters"
        data-testid="field-dim-height"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)]"
      />
    </fieldset>
  </fieldset>

  <details
    class="space-y-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
  >
    <summary class="cursor-pointer font-serif text-base font-semibold text-[var(--color-text)]">
      Locality
    </summary>
    <div class="grid gap-3 pt-2 sm:grid-cols-2">
      <label class="block text-sm sm:col-span-2">
        <span class="mb-1 block text-[var(--color-text-muted)]">Locality (free text)</span>
        <input
          type="text"
          name="locality_text"
          data-testid="field-locality-text"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
        />
      </label>
      <label class="block text-sm">
        <span class="mb-1 block text-[var(--color-text-muted)]">Country</span>
        <input
          type="text"
          name="locality.country"
          data-testid="field-locality-country"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
        />
      </label>
      <label class="block text-sm">
        <span class="mb-1 block text-[var(--color-text-muted)]">Region</span>
        <input
          type="text"
          name="locality.region"
          data-testid="field-locality-region"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
        />
      </label>
      <label class="block text-sm sm:col-span-2">
        <span class="mb-1 block text-[var(--color-text-muted)]">Site</span>
        <input
          type="text"
          name="locality.site"
          data-testid="field-locality-site"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
        />
      </label>
      <label class="block text-sm">
        <span class="mb-1 block text-[var(--color-text-muted)]">Latitude</span>
        <input
          type="number"
          step="0.000001"
          name="locality.lat"
          aria-invalid={showError('locality.lat') ? 'true' : undefined}
          data-testid="field-locality-lat"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
        />
        {#if showError('locality.lat')}
          <span class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]"
            >{showError('locality.lat')}</span
          >
        {/if}
      </label>
      <label class="block text-sm">
        <span class="mb-1 block text-[var(--color-text-muted)]">Longitude</span>
        <input
          type="number"
          step="0.000001"
          name="locality.lon"
          aria-invalid={showError('locality.lon') ? 'true' : undefined}
          data-testid="field-locality-lon"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
        />
        {#if showError('locality.lon')}
          <span class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]"
            >{showError('locality.lon')}</span
          >
        {/if}
      </label>
      <label class="block text-sm">
        <span class="mb-1 block text-[var(--color-text-muted)]">mindat ID</span>
        <input
          type="text"
          name="locality.mindat_id"
          data-testid="field-locality-mindat"
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
        />
      </label>
    </div>
  </details>

  <fieldset
    class="space-y-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
  >
    <legend class="font-serif text-base font-semibold text-[var(--color-text)]">
      {$data.type === 'mineral'
        ? 'Mineralogy'
        : $data.type === 'rock'
          ? 'Petrology'
          : 'Classification'}
    </legend>

    {#if $data.type === 'mineral'}
      <div class="grid gap-3 sm:grid-cols-2" data-testid="type-data-mineral">
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Chemical formula</span>
          <input
            type="text"
            name="type_data.chemical_formula"
            data-testid="field-mineral-formula"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 font-mono text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]"
            >Mineral species (comma-separated)</span
          >
          <input
            type="text"
            name="type_data.mineral_species"
            data-testid="field-mineral-species"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Crystal system</span>
          <input
            type="text"
            name="type_data.crystal_system"
            data-testid="field-mineral-crystal"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Mohs hardness (0–10)</span>
          <input
            type="number"
            step="0.1"
            min="0"
            max="10"
            name="type_data.mohs_hardness"
            aria-invalid={showError('type_data.mohs_hardness') ? 'true' : undefined}
            data-testid="field-mineral-hardness"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
          {#if showError('type_data.mohs_hardness')}
            <span
              class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]"
              data-testid="error-mineral-hardness">{showError('type_data.mohs_hardness')}</span
            >
          {/if}
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Color</span>
          <input
            type="text"
            name="type_data.color"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Luster</span>
          <input
            type="text"
            name="type_data.luster"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Fluorescence</span>
          <input
            type="text"
            name="type_data.fluorescence"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">mindat ID</span>
          <input
            type="text"
            name="type_data.mindat_id"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="inline-flex items-center gap-2 text-sm sm:col-span-2">
          <input
            type="checkbox"
            name="type_data.radioactive"
            data-testid="field-mineral-radioactive"
            class="accent-[var(--color-accent)]"
          />
          <span>Radioactive</span>
        </label>
      </div>
    {:else if $data.type === 'rock'}
      <div class="grid gap-3 sm:grid-cols-2" data-testid="type-data-rock">
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Rock type</span>
          <select
            name="type_data.rock_type"
            data-testid="field-rock-type"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          >
            <option value="">— select —</option>
            <option value="igneous">Igneous</option>
            <option value="sedimentary">Sedimentary</option>
            <option value="metamorphic">Metamorphic</option>
          </select>
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Composition</span>
          <input
            type="text"
            name="type_data.composition"
            data-testid="field-rock-composition"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm sm:col-span-2">
          <span class="mb-1 block text-[var(--color-text-muted)]">Formation context</span>
          <input
            type="text"
            name="type_data.formation_context"
            data-testid="field-rock-formation"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
      </div>
    {:else}
      <div class="grid gap-3 sm:grid-cols-2" data-testid="type-data-meteorite">
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Classification</span>
          <input
            type="text"
            name="type_data.classification"
            data-testid="field-meteorite-class"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 font-mono text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Fall or find</span>
          <select
            name="type_data.fall_or_find"
            data-testid="field-meteorite-fof"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          >
            <option value="">— select —</option>
            <option value="fall">Fall</option>
            <option value="find">Find</option>
          </select>
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Fall/find date</span>
          <input
            type="date"
            name="type_data.fall_or_find_date"
            data-testid="field-meteorite-date"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Official name</span>
          <input
            type="text"
            name="type_data.official_name"
            data-testid="field-meteorite-name"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Total known weight (g)</span>
          <input
            type="number"
            step="0.001"
            min="0"
            name="type_data.total_known_weight_g"
            aria-invalid={showError('type_data.total_known_weight_g') ? 'true' : undefined}
            data-testid="field-meteorite-tkw"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
          {#if showError('type_data.total_known_weight_g')}
            <span class="mt-1 block text-xs text-[var(--color-danger,#b91c1c)]"
              >{showError('type_data.total_known_weight_g')}</span
            >
          {/if}
        </label>
        <label class="block text-sm">
          <span class="mb-1 block text-[var(--color-text-muted)]">Met. Bulletin ref</span>
          <input
            type="text"
            name="type_data.metbull_ref"
            data-testid="field-meteorite-metbull"
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-[var(--color-text)]"
          />
        </label>
      </div>
    {/if}
  </fieldset>

  <div class="flex flex-wrap items-center gap-3">
    <button
      type="submit"
      disabled={$isSubmitting}
      data-testid="form-submit"
      class="rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
    >
      {#if $isSubmitting}
        <span data-testid="form-submitting">{mode === 'create' ? 'Creating…' : 'Saving…'}</span>
      {:else}
        {mode === 'create' ? 'Create specimen' : 'Save changes'}
      {/if}
    </button>
    <a
      href={cancelHref}
      data-testid="form-cancel"
      class="text-sm text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
    >
      Cancel
    </a>
  </div>
</form>
