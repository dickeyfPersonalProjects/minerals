<script lang="ts" module>
  export type ChainEditorEntry = { id: string; name: string };
</script>

<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { client } from './api';
  import type { components } from './api/schema';
  import CollectorForm from './CollectorForm.svelte';
  import type { CollectorFormSubmitResult } from './CollectorForm.svelte';
  import { toastSuccess } from './toasts';

  type Collector = components['schemas']['CollectorView'];

  interface Props {
    specimenId: string;
    initial: ChainEditorEntry[];
    onSaved: () => void;
    onCancel: () => void;
  }

  const { specimenId, initial, onSaved, onCancel }: Props = $props();

  // Snapshot the parent's chain once at mount; the editor owns its
  // working copy after that. Cancel just unmounts us — discard is
  // implicit.
  let chain: ChainEditorEntry[] = $state(
    untrack(() => initial.map((e) => ({ id: e.id, name: e.name }))),
  );

  let searchInput = $state('');
  let activeQuery = $state('');
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;
  let suggestions: Collector[] = $state([]);
  let suggestLoading = $state(false);

  let showNewForm = $state(false);
  let saving = $state(false);
  let bannerError: string | null = $state(null);

  function envelopeMessage(
    error: { error?: { code?: string; message?: string } } | undefined,
    status: number,
  ): string {
    return error?.error?.message || error?.error?.code || `HTTP ${status}`;
  }

  function moveUp(index: number) {
    if (index <= 0 || index >= chain.length) return;
    const next = chain.slice();
    const a = next[index - 1];
    const b = next[index];
    if (!a || !b) return;
    next[index - 1] = b;
    next[index] = a;
    chain = next;
  }

  function moveDown(index: number) {
    if (index < 0 || index >= chain.length - 1) return;
    const next = chain.slice();
    const a = next[index];
    const b = next[index + 1];
    if (!a || !b) return;
    next[index] = b;
    next[index + 1] = a;
    chain = next;
  }

  function removeAt(index: number) {
    chain = chain.filter((_, i) => i !== index);
  }

  function appendCollector(c: { id: string; name: string }) {
    if (chain.some((e) => e.id === c.id)) return;
    chain = [...chain, { id: c.id, name: c.name }];
    searchInput = '';
    activeQuery = '';
    suggestions = [];
  }

  function onSearchInput(e: Event) {
    const value = (e.target as HTMLInputElement).value;
    searchInput = value;
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      activeQuery = value.trim();
    }, 300);
  }

  onDestroy(() => {
    if (debounceTimer) clearTimeout(debounceTimer);
  });

  $effect(() => {
    const q = activeQuery;
    if (!q) {
      suggestions = [];
      suggestLoading = false;
      return;
    }
    suggestLoading = true;
    let cancelled = false;
    void (async () => {
      try {
        const { data } = await client.GET('/api/v1/collectors', {
          params: { query: { q, limit: 10 } },
        });
        if (cancelled) return;
        const items = data?.items ?? [];
        suggestions = items.slice(0, 10);
      } catch {
        if (!cancelled) suggestions = [];
      } finally {
        if (!cancelled) suggestLoading = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  });

  const visibleSuggestions = $derived(suggestions.filter((s) => !chain.some((e) => e.id === s.id)));

  async function createNewCollector(values: {
    name: string;
    notes: string;
  }): Promise<CollectorFormSubmitResult> {
    const body: { name: string; notes?: string } = { name: values.name };
    if (values.notes) body.notes = values.notes;
    const { data, error, response } = await client.POST('/api/v1/collectors', { body });
    if (error) {
      if (response.status === 409) return { kind: 'duplicate' };
      return { kind: 'error', message: envelopeMessage(error, response.status) };
    }
    if (data) {
      appendCollector({ id: data.id, name: data.name });
      toastSuccess(`Created collector "${data.name}"`);
    }
    showNewForm = false;
    return { kind: 'ok' };
  }

  async function save() {
    saving = true;
    bannerError = null;
    const { error, response } = await client.PUT('/api/v1/specimens/{id}/collectors', {
      params: { path: { id: specimenId } },
      body: { collector_ids: chain.map((c) => c.id) },
    });
    saving = false;
    if (error) {
      bannerError = envelopeMessage(error, response.status);
      return;
    }
    toastSuccess('Collector chain saved');
    onSaved();
  }
</script>

<div
  class="space-y-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
  data-testid="chain-editor"
>
  {#if bannerError}
    <div
      role="alert"
      data-testid="chain-editor-error"
      class="rounded-md border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300"
    >
      {bannerError}
    </div>
  {/if}

  {#if chain.length === 0}
    <p class="text-sm text-[var(--color-text-muted)]" data-testid="chain-empty">
      Chain is empty — add a collector below.
    </p>
  {:else}
    <ol class="space-y-2" data-testid="chain-list">
      {#each chain as entry, i (entry.id)}
        <li
          class="flex items-center gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] p-2"
          data-testid="chain-row"
          data-collector-id={entry.id}
        >
          <span
            class="rounded-full bg-[var(--color-surface)] px-2 py-0.5 font-mono text-[11px] text-[var(--color-text-muted)]"
            aria-label="position"
          >
            {i + 1}
          </span>
          <span class="flex-1 text-sm text-[var(--color-text)]" data-testid="chain-name">
            {entry.name}
          </span>
          <button
            type="button"
            onclick={() => moveUp(i)}
            disabled={i === 0}
            data-testid="move-up"
            aria-label={`Move ${entry.name} up`}
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-xs text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            ↑
          </button>
          <button
            type="button"
            onclick={() => moveDown(i)}
            disabled={i === chain.length - 1}
            data-testid="move-down"
            aria-label={`Move ${entry.name} down`}
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-xs text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            ↓
          </button>
          <button
            type="button"
            onclick={() => removeAt(i)}
            data-testid="remove-row"
            aria-label={`Remove ${entry.name} from chain`}
            class="rounded-md border border-red-500/40 bg-red-500/10 px-2 py-1 text-xs text-red-700 hover:bg-red-500/20 dark:text-red-300"
          >
            ✕
          </button>
        </li>
      {/each}
    </ol>
  {/if}

  <div class="space-y-2">
    <label for="chain-collector-search" class="block text-sm font-medium text-[var(--color-text)]">
      Add collector
    </label>
    <input
      id="chain-collector-search"
      type="search"
      placeholder="Type to search…"
      autocomplete="off"
      value={searchInput}
      oninput={onSearchInput}
      data-testid="chain-search"
      class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
    />
    {#if activeQuery}
      {#if suggestLoading}
        <p class="text-xs text-[var(--color-text-muted)]" data-testid="chain-suggest-loading">
          Searching…
        </p>
      {:else if visibleSuggestions.length === 0}
        <p class="text-xs text-[var(--color-text-muted)]" data-testid="chain-suggest-empty">
          No matches.
        </p>
      {:else}
        <ul
          class="divide-y divide-[var(--color-border)] overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface)]"
          data-testid="chain-suggestions"
        >
          {#each visibleSuggestions as s (s.id)}
            <li>
              <button
                type="button"
                onclick={() => appendCollector({ id: s.id, name: s.name })}
                onkeydown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    appendCollector({ id: s.id, name: s.name });
                  }
                }}
                data-testid="chain-suggestion"
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

  {#if !showNewForm}
    <button
      type="button"
      onclick={() => (showNewForm = true)}
      data-testid="show-new-collector"
      class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
    >
      Add new collector
    </button>
  {:else}
    <div
      class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] p-3"
      data-testid="new-collector-panel"
    >
      <h3 class="mb-2 text-sm font-medium text-[var(--color-text)]">New collector</h3>
      <CollectorForm
        submitLabel="Create & add"
        onSubmit={createNewCollector}
        onCancel={() => (showNewForm = false)}
      />
    </div>
  {/if}

  <div class="flex items-center gap-2 border-t border-[var(--color-border)] pt-3">
    <button
      type="button"
      onclick={save}
      disabled={saving}
      data-testid="chain-save"
      class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
    >
      {saving ? 'Saving…' : 'Save'}
    </button>
    <button
      type="button"
      onclick={onCancel}
      disabled={saving}
      data-testid="chain-cancel"
      class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-60"
    >
      Cancel
    </button>
  </div>
</div>
