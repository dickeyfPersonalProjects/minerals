<!--
  MineralSpeciesLookup (mi-xly).

  Replaces the prior autocomplete-popup with an explicit Lookup
  button. Why: the popup version updated a hidden bound value but
  the visible form inputs didn't refresh, so users thought the
  feature was broken (it worked on save but produced no visible
  feedback before that).

  Flow:
    - User types a mineral name and presses the Lookup button
      (or hits Enter inside the input).
    - We GET /api/v1/mineral-species?q=… and use the top match.
    - On success: onSelect(species) so the parent can pre-fill
      every visible form field; we also toast a brief confirmation.
    - On no match or upstream error: inline error message; no form
      change.

  Without a Mindat key, the backend falls through to DB-only mode:
  the search still works for already-stored species.
-->
<script lang="ts" module>
  import type { components } from './api/schema';
  export type MineralSpeciesView = components['schemas']['MineralSpeciesView'];
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import { client } from './api/index';
  import { SUPPRESS_TOAST_HEADERS } from './api/wrapper';
  import { toastSuccess } from './toasts';

  interface Props {
    onSelect: (s: MineralSpeciesView) => void;
    /**
     * Initial query value mirrored from the parent's name field.
     * Read once at mount; the component owns the query state after.
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

  let query = $state(untrack(() => initialQuery));
  let loading = $state(false);
  let errorMessage: string | null = $state(null);

  async function runLookup(): Promise<void> {
    const q = query.trim();
    if (!q) {
      errorMessage = 'Enter a mineral name to look up.';
      return;
    }
    errorMessage = null;
    loading = true;
    try {
      const { data, error } = await client.GET('/api/v1/mineral-species', {
        params: { query: { q } },
        // We render our own inline error; the global toast would
        // be redundant.
        headers: SUPPRESS_TOAST_HEADERS,
      });
      if (error) {
        errorMessage = 'Lookup failed. Try again in a moment.';
        return;
      }
      const items = (data?.items ?? []) as MineralSpeciesView[];
      const top = items[0];
      if (!top) {
        errorMessage = `No match found for "${q}".`;
        return;
      }
      onSelect(top);
      query = top.name;
      toastSuccess(`Fetched data for ${top.name}`);
    } catch {
      errorMessage = 'Lookup failed. Try again in a moment.';
    } finally {
      loading = false;
    }
  }

  function handleInput(e: Event): void {
    query = (e.target as HTMLInputElement).value;
    if (errorMessage) errorMessage = null;
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (e.key === 'Enter') {
      // The lookup lives inside the parent form; pressing Enter
      // would otherwise submit the form before lookup runs.
      e.preventDefault();
      void runLookup();
    }
  }
</script>

<div class="space-y-1">
  <label for="mineral-species-lookup" class="mb-1 block text-xs text-[var(--color-text-muted)]">
    {label}
  </label>
  <div class="flex gap-2">
    <input
      id="mineral-species-lookup"
      type="text"
      autocomplete="off"
      {placeholder}
      value={query}
      oninput={handleInput}
      onkeydown={handleKeyDown}
      aria-invalid={Boolean(errorMessage)}
      aria-describedby={errorMessage ? 'mineral-species-lookup-error' : undefined}
      data-testid="mineral-species-lookup-input"
      class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
    />
    <button
      type="button"
      onclick={() => void runLookup()}
      disabled={loading}
      data-testid="mineral-species-lookup-button"
      class="shrink-0 rounded-md bg-[var(--color-accent)] px-3 py-2 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
    >
      {loading ? 'Looking up…' : 'Lookup'}
    </button>
  </div>
  {#if errorMessage}
    <p
      id="mineral-species-lookup-error"
      role="alert"
      data-testid="mineral-species-lookup-error"
      class="text-xs text-red-500"
    >
      {errorMessage}
    </p>
  {/if}
</div>
