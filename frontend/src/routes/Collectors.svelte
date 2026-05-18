<script lang="ts">
  import { link } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import { isProfileSetupRedirect } from '../lib/api/wrapper';
  import type { components } from '../lib/api/schema';
  import CollectorForm from '../lib/CollectorForm.svelte';
  import type { CollectorFormSubmitResult } from '../lib/CollectorForm.svelte';
  import { isAuthenticated } from '../lib/auth';
  import { formatLocal } from '../lib/time';
  import { toastSuccess } from '../lib/toasts';

  type Collector = components['schemas']['CollectorView'];

  type LoadState =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'loaded'; nextCursor: string | null }
    | { kind: 'loading-more' }
    | { kind: 'error'; message: string };

  let items: Collector[] = $state([]);
  let loadState: LoadState = $state({ kind: 'idle' });

  let searchInput = $state('');
  // Debounced query — committed value that drives the fetch.
  let activeQuery = $state('');
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  let showCreateForm = $state(false);
  let deleteError: { collectorId: string; message: string; inUse: boolean } | null = $state(null);

  function envelopeMessage(
    error: { error?: { code?: string; message?: string } } | undefined,
    status: number,
  ): string {
    return error?.error?.message || error?.error?.code || `HTTP ${status}`;
  }

  async function fetchPage(cursor?: string): Promise<void> {
    const isFirst = cursor === undefined;
    loadState = isFirst ? { kind: 'loading' } : { kind: 'loading-more' };

    try {
      const query: { cursor?: string; q?: string } = {};
      if (cursor) query.cursor = cursor;
      if (activeQuery) query.q = activeQuery;
      const { data, error, response } = await client.GET('/api/v1/collectors', {
        params: { query },
      });
      if (error) {
        // mi-4p4: wrapper is mid-redirect to /profile/setup; stay
        // in `loading` to avoid an error-banner flash before the
        // hash route swaps.
        if (isProfileSetupRedirect(error)) return;
        loadState = { kind: 'error', message: envelopeMessage(error, response.status) };
        return;
      }
      const page = data?.items ?? [];
      items = isFirst ? page : [...items, ...page];
      loadState = { kind: 'loaded', nextCursor: data?.next_cursor ?? null };
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      loadState = { kind: 'error', message };
    }
  }

  function refetch(): void {
    items = [];
    void fetchPage();
  }

  $effect(() => {
    // Re-runs whenever `activeQuery` changes (including initial mount).
    void activeQuery;
    items = [];
    void fetchPage();
  });

  function onSearchInput(e: Event) {
    const value = (e.target as HTMLInputElement).value;
    searchInput = value;
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      activeQuery = value.trim();
    }, 300);
  }

  function loadMore() {
    if (loadState.kind !== 'loaded' || !loadState.nextCursor) return;
    void fetchPage(loadState.nextCursor);
  }

  function retry() {
    items = [];
    void fetchPage();
  }

  async function createCollector(values: {
    name: string;
    notes: string;
  }): Promise<CollectorFormSubmitResult> {
    const body: { name: string; notes?: string } = { name: values.name };
    if (values.notes) body.notes = values.notes;
    const { error, response } = await client.POST('/api/v1/collectors', {
      body,
    });
    if (error) {
      if (response.status === 409) return { kind: 'duplicate' };
      return { kind: 'error', message: envelopeMessage(error, response.status) };
    }
    toastSuccess('Collector created');
    showCreateForm = false;
    refetch();
    return { kind: 'ok' };
  }

  async function deleteCollector(collector: Collector) {
    deleteError = null;
    const ok = window.confirm(`Delete collector "${collector.name}"? This cannot be undone.`);
    if (!ok) return;
    const { error, response } = await client.DELETE('/api/v1/collectors/{id}', {
      params: { path: { id: collector.id } },
    });
    if (error) {
      if (response.status === 409) {
        deleteError = {
          collectorId: collector.id,
          inUse: true,
          message: `"${collector.name}" is referenced by one or more specimens. Remove it from those specimens first.`,
        };
      } else {
        deleteError = {
          collectorId: collector.id,
          inUse: false,
          message: envelopeMessage(error, response.status),
        };
      }
      return;
    }
    toastSuccess(`Deleted "${collector.name}"`);
    refetch();
  }

  const truncate = (s: string | null | undefined, max: number): string => {
    if (!s) return '';
    return s.length > max ? `${s.slice(0, max - 1)}…` : s;
  };
</script>

<section>
  <header class="mb-4 flex flex-wrap items-end justify-between gap-3">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Collectors</h1>
    {#if $isAuthenticated}
      <button
        type="button"
        data-testid="toggle-create"
        onclick={() => (showCreateForm = !showCreateForm)}
        class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90"
      >
        {showCreateForm ? 'Cancel' : 'Add collector'}
      </button>
    {/if}
  </header>

  {#if $isAuthenticated && showCreateForm}
    <div
      data-testid="create-form-wrapper"
      class="mb-6 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
    >
      <h2 class="mb-3 text-base font-medium text-[var(--color-text)]">New collector</h2>
      <CollectorForm
        submitLabel="Create"
        onSubmit={createCollector}
        onCancel={() => (showCreateForm = false)}
      />
    </div>
  {/if}

  <div class="mb-4">
    <label for="collector-search" class="sr-only">Filter collectors by name</label>
    <input
      id="collector-search"
      type="search"
      placeholder="Filter by name…"
      value={searchInput}
      oninput={onSearchInput}
      data-testid="search-input"
      class="w-full max-w-md rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
    />
  </div>

  {#if loadState.kind === 'loading'}
    <ul data-testid="loading" class="space-y-2">
      {#each Array.from({ length: 4 }, (_, i) => i) as i (i)}
        <li
          class="h-14 animate-pulse rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)]"
        ></li>
      {/each}
    </ul>
  {:else if loadState.kind === 'error'}
    <div
      role="alert"
      data-testid="error"
      class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center"
    >
      <p class="text-sm font-medium text-[var(--color-text)]">Couldn't load collectors.</p>
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">{loadState.message}</p>
      <button
        type="button"
        onclick={retry}
        class="mt-4 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm text-[var(--color-accent-fg)] hover:opacity-90"
      >
        Try again
      </button>
    </div>
  {:else if items.length === 0}
    <div
      data-testid="empty"
      class="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] p-10 text-center"
    >
      {#if activeQuery}
        <p class="text-sm text-[var(--color-text)]">No matching collectors.</p>
      {:else}
        <p class="text-sm text-[var(--color-text)]">No collectors yet — add your first.</p>
      {/if}
    </div>
  {:else}
    <ul data-testid="collector-list" class="space-y-2">
      {#each items as c (c.id)}
        <li
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] p-3"
          data-testid="collector-row"
        >
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <a
                href={`/collectors/${c.id}`}
                use:link
                class="text-sm font-semibold text-[var(--color-text)] hover:text-[var(--color-accent)]"
                data-testid="collector-name"
              >
                {c.name}
              </a>
              {#if c.notes}
                <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
                  {truncate(c.notes, 120)}
                </p>
              {/if}
              <p class="mt-1 text-[10px] uppercase tracking-wide text-[var(--color-text-muted)]">
                Added {formatLocal(c.created_at, { dateStyle: 'medium' })}
              </p>
            </div>
            {#if $isAuthenticated}
              <div class="flex shrink-0 gap-2">
                <a
                  href={`/collectors/${c.id}`}
                  use:link
                  class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 py-1 text-xs text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
                >
                  Edit
                </a>
                <button
                  type="button"
                  onclick={() => deleteCollector(c)}
                  data-testid="delete-button"
                  class="rounded-md border border-red-500/40 bg-red-500/10 px-2.5 py-1 text-xs text-red-700 hover:bg-red-500/20 dark:text-red-300"
                >
                  Delete
                </button>
              </div>
            {/if}
          </div>
          {#if deleteError && deleteError.collectorId === c.id}
            <div
              role="alert"
              data-testid="delete-error"
              class="mt-2 rounded-md border border-red-500/40 bg-red-500/10 p-2 text-xs text-red-700 dark:text-red-300"
            >
              <p>{deleteError.message}</p>
              {#if deleteError.inUse}
                <a
                  href={`/specimens?collector_id=${c.id}`}
                  use:link
                  class="mt-1 inline-block underline hover:no-underline"
                  data-testid="filtered-specimens-link"
                >
                  View specimens that reference this collector →
                </a>
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>

    {#if loadState.kind === 'loaded' && loadState.nextCursor}
      <div class="mt-6 flex justify-center">
        <button
          type="button"
          onclick={loadMore}
          data-testid="load-more"
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
        >
          Load more
        </button>
      </div>
    {:else if loadState.kind === 'loading-more'}
      <p class="mt-6 text-center text-sm text-[var(--color-text-muted)]">Loading more…</p>
    {/if}
  {/if}
</section>
