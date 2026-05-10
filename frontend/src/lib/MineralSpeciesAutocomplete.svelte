<!--
  MineralSpeciesAutocomplete (mi-dtg / F-1).

  Bound to GET /api/v1/mineral-species?q=. Debounced 300ms.
  Selecting a result calls onSelect with the chosen
  MineralSpeciesView so the parent form can pre-fill the
  type_data fields. Every field stays editable after pre-fill;
  this component does not touch the form values directly.

  When the API record carries `attribution`, the parent should
  render it small/italic next to the mineral fields per Mindat's
  CC-BY-NC-SA 4.0 terms — this component just exposes the
  selected record via onSelect.

  Without a Mindat key, the backend falls through to DB-only
  mode: the search still works for already-stored species.
-->
<script lang="ts" module>
  import type { components } from './api/schema';
  export type MineralSpeciesView = components['schemas']['MineralSpeciesView'];
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import { client } from './api/index';
  import { SUPPRESS_TOAST_HEADERS } from './api/wrapper';

  interface Props {
    onSelect: (s: MineralSpeciesView) => void;
    /**
     * Initial query value mirrored from the parent's name field.
     * The parent re-syncs by changing this prop; the component
     * owns the debounced fetch and result list internally.
     */
    initialQuery?: string;
    label?: string;
    placeholder?: string;
  }

  const {
    onSelect,
    initialQuery = '',
    label = 'Mindat lookup',
    placeholder = 'Search Mindat / your minerals',
  }: Props = $props();

  // initialQuery is intentionally read-once at mount: the form
  // owns the name field, this component owns its query state.
  let query = $state(untrack(() => initialQuery));
  let results: MineralSpeciesView[] = $state([]);
  let loading = $state(false);
  let open = $state(false);
  let activeIndex = $state(-1);
  let debounceHandle: ReturnType<typeof setTimeout> | null = null;
  let abortController: AbortController | null = null;

  async function runSearch(q: string): Promise<void> {
    // Abort any in-flight request so a stale response can't
    // overwrite a fresher one.
    if (abortController) abortController.abort();
    abortController = new AbortController();
    loading = true;
    try {
      const { data, error } = await client.GET('/api/v1/mineral-species', {
        params: { query: { q } },
        // Suppress global error toast on lookup failure: the
        // worst case is "no results" and that's already conveyed
        // by the empty list.
        headers: SUPPRESS_TOAST_HEADERS,
        signal: abortController.signal,
      });
      if (error) {
        results = [];
        return;
      }
      results = (data?.items ?? []) as MineralSpeciesView[];
      activeIndex = -1;
    } catch {
      // Aborted or network failure: keep previous results.
    } finally {
      loading = false;
    }
  }

  function scheduleSearch(q: string): void {
    if (debounceHandle) clearTimeout(debounceHandle);
    const trimmed = q.trim();
    if (!trimmed) {
      results = [];
      open = false;
      return;
    }
    debounceHandle = setTimeout(() => {
      open = true;
      void runSearch(trimmed);
    }, 300);
  }

  function handleInput(e: Event): void {
    const value = (e.target as HTMLInputElement).value;
    query = value;
    scheduleSearch(value);
  }

  function handleSelect(s: MineralSpeciesView): void {
    onSelect(s);
    query = s.name;
    open = false;
    results = [];
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (!open || results.length === 0) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      activeIndex = (activeIndex + 1) % results.length;
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      activeIndex = (activeIndex - 1 + results.length) % results.length;
    } else if (e.key === 'Enter') {
      if (activeIndex >= 0 && activeIndex < results.length) {
        e.preventDefault();
        handleSelect(results[activeIndex]!);
      }
    } else if (e.key === 'Escape') {
      open = false;
    }
  }
</script>

<div class="relative">
  <label
    for="mineral-species-autocomplete"
    class="mb-1 block text-xs text-[var(--color-text-muted)]"
  >
    {label}
  </label>
  <input
    id="mineral-species-autocomplete"
    type="text"
    role="combobox"
    autocomplete="off"
    aria-autocomplete="list"
    aria-expanded={open}
    aria-controls="mineral-species-autocomplete-listbox"
    aria-activedescendant={activeIndex >= 0
      ? `mineral-species-autocomplete-option-${activeIndex}`
      : undefined}
    {placeholder}
    value={query}
    oninput={handleInput}
    onkeydown={handleKeyDown}
    onfocus={() => {
      if (results.length > 0) open = true;
    }}
    onblur={() => {
      // Delay so a click on a list item registers before the
      // listbox unmounts.
      setTimeout(() => {
        open = false;
      }, 120);
    }}
    data-testid="mineral-species-autocomplete-input"
    class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
  />
  {#if open && (results.length > 0 || loading)}
    <ul
      id="mineral-species-autocomplete-listbox"
      role="listbox"
      data-testid="mineral-species-autocomplete-listbox"
      class="absolute z-10 mt-1 max-h-64 w-full overflow-y-auto rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] shadow-md"
    >
      {#if loading && results.length === 0}
        <li class="px-3 py-2 text-xs text-[var(--color-text-muted)]">Searching…</li>
      {/if}
      {#each results as r, i (r.id)}
        <li
          id="mineral-species-autocomplete-option-{i}"
          role="option"
          aria-selected={activeIndex === i}
          class="cursor-pointer px-3 py-2 text-sm text-[var(--color-text)] hover:bg-[var(--color-bg)] {activeIndex ===
          i
            ? 'bg-[var(--color-bg)]'
            : ''}"
          onmousedown={(e) => {
            // mousedown (not click) fires before the input's
            // blur, so the selection isn't lost to the blur
            // timeout above.
            e.preventDefault();
            handleSelect(r);
          }}
          data-testid="mineral-species-autocomplete-option"
        >
          <span class="font-medium">{r.name}</span>
          {#if r.source === 'mindat'}
            <span
              class="ml-2 rounded bg-blue-500/10 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-blue-600 dark:text-blue-300"
            >
              Mindat
            </span>
          {/if}
          {#if r.data?.chemical_formula}
            <span class="ml-2 font-mono text-xs text-[var(--color-text-muted)]">
              {r.data.chemical_formula}
            </span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>
