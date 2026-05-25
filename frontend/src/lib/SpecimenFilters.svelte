<script lang="ts" module>
  // Filter state shape — mirrors the API query params on
  // `GET /api/v1/specimens` (CONTRACT.md §10). All fields optional;
  // omitting a key disables that filter on the server side.
  export interface SpecimenFiltersValue {
    q?: string;
    type?: 'mineral' | 'rock' | 'meteorite';
    visibility?: 'private' | 'unlisted' | 'public';
    has_catalog_number?: 'true' | 'false';
    acquired_after?: string;
    acquired_before?: string;
    collector_id?: string;
    // tagged filter (mi-n28q). Owner-only — only shown in the
    // "my collection" view (scope=mine). 'false' = untagged /
    // "what still needs a label?"; 'true' = already tagged.
    tagged?: 'true' | 'false';
  }

  export const TYPE_OPTIONS: Array<{ value: 'mineral' | 'rock' | 'meteorite'; label: string }> = [
    { value: 'mineral', label: 'Mineral' },
    { value: 'rock', label: 'Rock' },
    { value: 'meteorite', label: 'Meteorite' },
  ];

  export const VISIBILITY_OPTIONS: Array<{
    value: 'private' | 'unlisted' | 'public';
    label: string;
  }> = [
    { value: 'private', label: 'Private' },
    { value: 'unlisted', label: 'Unlisted' },
    { value: 'public', label: 'Public' },
  ];

  // Count how many fields are populated. q counts as one; non-q
  // fields each count individually. Used for the "Filters (N
  // active)" badge and for the "Clear all" visibility toggle.
  export function activeFilterCount(value: SpecimenFiltersValue): number {
    let n = 0;
    if (value.q) n += 1;
    if (value.type) n += 1;
    if (value.visibility) n += 1;
    if (value.has_catalog_number) n += 1;
    if (value.acquired_after) n += 1;
    if (value.acquired_before) n += 1;
    if (value.collector_id) n += 1;
    if (value.tagged) n += 1;
    return n;
  }
</script>

<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { client } from './api';
  import type { components } from './api/schema';

  type Collector = components['schemas']['CollectorView'];

  interface Props {
    value: SpecimenFiltersValue;
    onChange: (next: SpecimenFiltersValue) => void;
    // When true, shows the "Physical label" (tagged) filter chips.
    // Only meaningful in scope=mine ("my collection") view since
    // tagged is owner-only metadata (mi-n28q).
    showTaggedFilter?: boolean;
  }

  const { value, onChange, showTaggedFilter = false }: Props = $props();

  // Search input is locally controlled and debounced — only the
  // settled value flows up to `onChange`. Initialised from the
  // current `value.q` so a deep-link with `?q=` shows the right
  // text in the input.
  let searchInput = $state(untrack(() => value.q ?? ''));
  let searchDebounce: ReturnType<typeof setTimeout> | null = null;

  // Sync local input when the parent's q changes from elsewhere
  // (e.g. browser back/forward, "Clear all" button). Skip the
  // re-sync if it matches what the user is already typing — that
  // would cause caret jumps on every applied keystroke.
  $effect(() => {
    const incoming = value.q ?? '';
    if (incoming !== searchInput.trim()) {
      searchInput = incoming;
    }
  });

  function emit(patch: Partial<SpecimenFiltersValue>) {
    const next: SpecimenFiltersValue = { ...value, ...patch };
    // Strip empty strings — the API treats absent and empty
    // string as "filter off", but the URL serializer is cleaner
    // when undefined wins.
    (Object.keys(next) as Array<keyof SpecimenFiltersValue>).forEach((k) => {
      if (next[k] === '' || next[k] === undefined) delete next[k];
    });
    onChange(next);
  }

  function onSearchInput(e: Event) {
    const v = (e.target as HTMLInputElement).value;
    searchInput = v;
    if (searchDebounce) clearTimeout(searchDebounce);
    searchDebounce = setTimeout(() => {
      const trimmed = v.trim();
      emit({ q: trimmed || undefined });
    }, 300);
  }

  function clearSearch() {
    if (searchDebounce) clearTimeout(searchDebounce);
    searchInput = '';
    emit({ q: undefined });
  }

  onDestroy(() => {
    if (searchDebounce) clearTimeout(searchDebounce);
    if (collectorDebounce) clearTimeout(collectorDebounce);
  });

  // --- Filter chip helpers ---------------------------------------

  function toggleType(t: 'mineral' | 'rock' | 'meteorite') {
    emit({ type: value.type === t ? undefined : t });
  }

  function toggleVisibility(v: 'private' | 'unlisted' | 'public') {
    emit({ visibility: value.visibility === v ? undefined : v });
  }

  function setCatalog(state: 'true' | 'false' | undefined) {
    emit({ has_catalog_number: state });
  }

  function setTagged(state: 'true' | 'false' | undefined) {
    emit({ tagged: state });
  }

  function setAcquiredAfter(e: Event) {
    const v = (e.target as HTMLInputElement).value;
    emit({ acquired_after: v || undefined });
  }

  function setAcquiredBefore(e: Event) {
    const v = (e.target as HTMLInputElement).value;
    emit({ acquired_before: v || undefined });
  }

  function clearAll() {
    if (searchDebounce) clearTimeout(searchDebounce);
    if (collectorDebounce) clearTimeout(collectorDebounce);
    searchInput = '';
    collectorQuery = '';
    collectorSuggestions = [];
    collectorName = null;
    onChange({});
  }

  // --- Collector typeahead --------------------------------------

  let collectorQuery = $state('');
  let activeCollectorQuery = $state('');
  let collectorDebounce: ReturnType<typeof setTimeout> | null = null;
  let collectorSuggestions: Collector[] = $state([]);
  let collectorSuggestLoading = $state(false);
  // Display name for the currently-selected collector. Lazy-loaded
  // when we land on the page with `?collector_id=` but no cached
  // name — falls back to the id in the chip if the lookup fails.
  let collectorName: string | null = $state(null);

  $effect(() => {
    const id = value.collector_id;
    if (!id) {
      collectorName = null;
      return;
    }
    if (collectorName !== null) return;
    let cancelled = false;
    void (async () => {
      const { data } = await client.GET('/api/v1/collectors/{id}', {
        params: { path: { id } },
      });
      if (cancelled) return;
      if (data) collectorName = data.name;
    })();
    return () => {
      cancelled = true;
    };
  });

  function onCollectorInput(e: Event) {
    const v = (e.target as HTMLInputElement).value;
    collectorQuery = v;
    if (collectorDebounce) clearTimeout(collectorDebounce);
    collectorDebounce = setTimeout(() => {
      activeCollectorQuery = v.trim();
    }, 300);
  }

  $effect(() => {
    const q = activeCollectorQuery;
    if (!q) {
      collectorSuggestions = [];
      collectorSuggestLoading = false;
      return;
    }
    collectorSuggestLoading = true;
    let cancelled = false;
    void (async () => {
      try {
        const { data } = await client.GET('/api/v1/collectors', {
          params: { query: { q, limit: 10 } },
        });
        if (cancelled) return;
        collectorSuggestions = (data?.items ?? []).slice(0, 10);
      } catch {
        if (!cancelled) collectorSuggestions = [];
      } finally {
        if (!cancelled) collectorSuggestLoading = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  });

  function pickCollector(c: Collector) {
    collectorName = c.name;
    collectorQuery = '';
    activeCollectorQuery = '';
    collectorSuggestions = [];
    emit({ collector_id: c.id });
  }

  function clearCollector() {
    collectorName = null;
    collectorQuery = '';
    activeCollectorQuery = '';
    collectorSuggestions = [];
    emit({ collector_id: undefined });
  }

  // --- Expand/collapse -------------------------------------------

  let expanded = $state(false);
  const activeCount = $derived(activeFilterCount(value));

  function chipClass(active: boolean): string {
    return [
      'rounded-full px-3 py-1 text-xs font-medium transition-colors',
      'border',
      active
        ? 'border-[var(--color-accent)] bg-[var(--color-accent)] text-[var(--color-accent-fg)]'
        : 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text-muted)] hover:bg-[var(--color-surface-2)]',
    ].join(' ');
  }
</script>

<div class="mb-4 space-y-3" data-testid="specimen-filters">
  <!-- Top row: search + toggle + clear-all -->
  <div class="flex flex-wrap items-center gap-2">
    <div class="relative flex-1 min-w-[200px]">
      <input
        type="search"
        placeholder="Search specimens…"
        autocomplete="off"
        value={searchInput}
        oninput={onSearchInput}
        data-testid="filter-search-input"
        aria-label="Search specimens"
        class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 pr-9 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      />
      {#if searchInput}
        <button
          type="button"
          onclick={clearSearch}
          data-testid="filter-search-clear"
          aria-label="Clear search"
          class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded px-2 py-0.5 text-xs text-[var(--color-text-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text)]"
        >
          ✕
        </button>
      {/if}
    </div>

    <button
      type="button"
      onclick={() => (expanded = !expanded)}
      data-testid="filter-toggle"
      aria-expanded={expanded}
      aria-controls="specimen-filters-panel"
      class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
    >
      {expanded ? 'Hide filters' : 'Filters'}{activeCount > 0 ? ` (${activeCount} active)` : ''}
    </button>

    {#if activeCount > 0}
      <button
        type="button"
        onclick={clearAll}
        data-testid="filter-clear-all"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text)]"
      >
        Clear all filters
      </button>
    {/if}
  </div>

  {#if expanded}
    <div
      id="specimen-filters-panel"
      data-testid="filter-panel"
      class="grid gap-4 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] p-4 sm:grid-cols-2"
    >
      <!-- Type -->
      <fieldset class="space-y-1.5">
        <legend class="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
          Type
        </legend>
        <div class="flex flex-wrap gap-1.5" role="group" aria-label="Filter by type">
          <button
            type="button"
            class={chipClass(!value.type)}
            onclick={() => emit({ type: undefined })}
            data-testid="filter-type-all"
            aria-pressed={!value.type}
          >
            All
          </button>
          {#each TYPE_OPTIONS as opt (opt.value)}
            <button
              type="button"
              class={chipClass(value.type === opt.value)}
              onclick={() => toggleType(opt.value)}
              data-testid={`filter-type-${opt.value}`}
              aria-pressed={value.type === opt.value}
            >
              {opt.label}
            </button>
          {/each}
        </div>
      </fieldset>

      <!-- Visibility -->
      <fieldset class="space-y-1.5">
        <legend class="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
          Visibility
        </legend>
        <div class="flex flex-wrap gap-1.5" role="group" aria-label="Filter by visibility">
          <button
            type="button"
            class={chipClass(!value.visibility)}
            onclick={() => emit({ visibility: undefined })}
            data-testid="filter-visibility-all"
            aria-pressed={!value.visibility}
          >
            All
          </button>
          {#each VISIBILITY_OPTIONS as opt (opt.value)}
            <button
              type="button"
              class={chipClass(value.visibility === opt.value)}
              onclick={() => toggleVisibility(opt.value)}
              data-testid={`filter-visibility-${opt.value}`}
              aria-pressed={value.visibility === opt.value}
            >
              {opt.label}
            </button>
          {/each}
        </div>
      </fieldset>

      <!-- Catalog number presence -->
      <fieldset class="space-y-1.5">
        <legend class="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
          Catalog number
        </legend>
        <div class="flex flex-wrap gap-1.5" role="group" aria-label="Filter by catalog number">
          <button
            type="button"
            class={chipClass(!value.has_catalog_number)}
            onclick={() => setCatalog(undefined)}
            data-testid="filter-catalog-any"
            aria-pressed={!value.has_catalog_number}
          >
            Any
          </button>
          <button
            type="button"
            class={chipClass(value.has_catalog_number === 'true')}
            onclick={() => setCatalog('true')}
            data-testid="filter-catalog-true"
            aria-pressed={value.has_catalog_number === 'true'}
          >
            Has catalog number
          </button>
          <button
            type="button"
            class={chipClass(value.has_catalog_number === 'false')}
            onclick={() => setCatalog('false')}
            data-testid="filter-catalog-false"
            aria-pressed={value.has_catalog_number === 'false'}
          >
            No catalog number
          </button>
        </div>
      </fieldset>

      <!-- Acquired range -->
      <fieldset class="space-y-1.5">
        <legend class="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
          Acquired between
        </legend>
        <div class="flex flex-wrap items-center gap-2">
          <label class="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
            From
            <input
              type="date"
              value={value.acquired_after ?? ''}
              oninput={setAcquiredAfter}
              data-testid="filter-acquired-after"
              aria-label="Acquired on or after"
              class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1 text-xs text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            />
          </label>
          <label class="flex items-center gap-1.5 text-xs text-[var(--color-text-muted)]">
            To
            <input
              type="date"
              value={value.acquired_before ?? ''}
              oninput={setAcquiredBefore}
              data-testid="filter-acquired-before"
              aria-label="Acquired on or before"
              class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1 text-xs text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            />
          </label>
        </div>
      </fieldset>

      <!-- Collector typeahead -->
      <fieldset class="space-y-1.5 sm:col-span-2">
        <legend class="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
          Collector
        </legend>
        {#if value.collector_id}
          <div
            class="flex items-center gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-1.5 text-sm text-[var(--color-text)]"
            data-testid="filter-collector-chip"
          >
            <span class="flex-1 truncate">
              {collectorName ?? value.collector_id}
            </span>
            <button
              type="button"
              onclick={clearCollector}
              data-testid="filter-collector-clear"
              aria-label="Remove collector filter"
              class="rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-0.5 text-xs text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
            >
              ✕
            </button>
          </div>
        {:else}
          <div class="space-y-1.5">
            <input
              type="search"
              placeholder="Search collectors…"
              autocomplete="off"
              value={collectorQuery}
              oninput={onCollectorInput}
              data-testid="filter-collector-input"
              aria-label="Search collectors"
              class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            />
            {#if activeCollectorQuery}
              {#if collectorSuggestLoading}
                <p
                  class="text-xs text-[var(--color-text-muted)]"
                  data-testid="filter-collector-loading"
                >
                  Searching…
                </p>
              {:else if collectorSuggestions.length === 0}
                <p
                  class="text-xs text-[var(--color-text-muted)]"
                  data-testid="filter-collector-empty"
                >
                  No matches.
                </p>
              {:else}
                <ul
                  class="divide-y divide-[var(--color-border)] overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface)]"
                  data-testid="filter-collector-suggestions"
                >
                  {#each collectorSuggestions as s (s.id)}
                    <li>
                      <button
                        type="button"
                        onclick={() => pickCollector(s)}
                        data-testid="filter-collector-suggestion"
                        data-collector-id={s.id}
                        class="block w-full px-3 py-2 text-left text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] focus:bg-[var(--color-surface-2)] focus:outline-none"
                      >
                        {s.name}
                      </button>
                    </li>
                  {/each}
                </ul>
              {/if}
            {/if}
          </div>
        {/if}
      </fieldset>

      <!-- Physical label (tagged) filter — owner-only (mi-n28q).
           Only shown in the "my collection" view where scope=mine is
           active, because the tagged field is owner-only metadata. -->
      {#if showTaggedFilter}
        <fieldset class="space-y-1.5 sm:col-span-2">
          <legend
            class="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]"
          >
            Physical label
          </legend>
          <div
            class="flex flex-wrap gap-1.5"
            role="group"
            aria-label="Filter by physical label status"
          >
            <button
              type="button"
              class={chipClass(!value.tagged)}
              onclick={() => setTagged(undefined)}
              data-testid="filter-tagged-all"
              aria-pressed={!value.tagged}
            >
              All
            </button>
            <button
              type="button"
              class={chipClass(value.tagged === 'false')}
              onclick={() => setTagged('false')}
              data-testid="filter-tagged-untagged"
              aria-pressed={value.tagged === 'false'}
            >
              Needs label
            </button>
            <button
              type="button"
              class={chipClass(value.tagged === 'true')}
              onclick={() => setTagged('true')}
              data-testid="filter-tagged-tagged"
              aria-pressed={value.tagged === 'true'}
            >
              Already labeled
            </button>
          </div>
        </fieldset>
      {/if}
    </div>
  {/if}
</div>
