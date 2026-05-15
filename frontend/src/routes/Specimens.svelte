<script lang="ts">
  import { link, replace, router } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import type { components } from '../lib/api/schema';
  import SpecimenCard from '../lib/SpecimenCard.svelte';
  import SpecimenFilters, {
    activeFilterCount,
    type SpecimenFiltersValue,
  } from '../lib/SpecimenFilters.svelte';
  import { isAuthenticated } from '../lib/oidc/auth';

  type Specimen = components['schemas']['SpecimenView'];

  // URL ↔ filter state. The URL hash querystring is the single
  // source of truth — `replace()` updates the URL, the derived
  // value re-parses, and the fetch effect re-runs. Reload + back
  // button "just work" because state lives in the URL.
  const filters: SpecimenFiltersValue = $derived.by(() => parseFilters(router.querystring));

  function parseFilters(qs: string | undefined): SpecimenFiltersValue {
    if (!qs) return {};
    const params = new URLSearchParams(qs);
    const out: SpecimenFiltersValue = {};
    const q = params.get('q');
    if (q) out.q = q;
    const type = params.get('type');
    if (type === 'mineral' || type === 'rock' || type === 'meteorite') out.type = type;
    const visibility = params.get('visibility');
    if (visibility === 'private' || visibility === 'unlisted' || visibility === 'public') {
      out.visibility = visibility;
    }
    const hasCat = params.get('has_catalog_number');
    if (hasCat === 'true' || hasCat === 'false') out.has_catalog_number = hasCat;
    const after = params.get('acquired_after');
    if (after) out.acquired_after = after;
    const before = params.get('acquired_before');
    if (before) out.acquired_before = before;
    const collectorId = params.get('collector_id');
    if (collectorId) out.collector_id = collectorId;
    return out;
  }

  function serializeFilters(value: SpecimenFiltersValue): string {
    // Local-only string builder — not reactive state, so the plain
    // URLSearchParams is fine here.
    // eslint-disable-next-line svelte/prefer-svelte-reactivity
    const params = new URLSearchParams();
    // Iterate in a stable order so the resulting URL is
    // deterministic — keeps tests and bookmarks predictable.
    if (value.q) params.set('q', value.q);
    if (value.type) params.set('type', value.type);
    if (value.visibility) params.set('visibility', value.visibility);
    if (value.has_catalog_number) params.set('has_catalog_number', value.has_catalog_number);
    if (value.acquired_after) params.set('acquired_after', value.acquired_after);
    if (value.acquired_before) params.set('acquired_before', value.acquired_before);
    if (value.collector_id) params.set('collector_id', value.collector_id);
    const qs = params.toString();
    return qs ? `/specimens?${qs}` : '/specimens';
  }

  function applyFilters(next: SpecimenFiltersValue) {
    void replace(serializeFilters(next));
  }

  type LoadState =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'loaded'; nextCursor: string | null }
    | { kind: 'loading-more' }
    | { kind: 'error'; message: string };

  let items: Specimen[] = $state([]);
  let loadState: LoadState = $state({ kind: 'idle' });

  type ListQuery = {
    cursor?: string;
    q?: string;
    type?: 'mineral' | 'rock' | 'meteorite';
    visibility?: 'private' | 'unlisted' | 'public';
    has_catalog_number?: 'true' | 'false';
    acquired_after?: string;
    acquired_before?: string;
    collector_id?: string;
  };

  function buildQuery(active: SpecimenFiltersValue, cursor?: string): ListQuery {
    const q: ListQuery = {};
    if (cursor) q.cursor = cursor;
    if (active.q) q.q = active.q;
    if (active.type) q.type = active.type;
    if (active.visibility) q.visibility = active.visibility;
    if (active.has_catalog_number) q.has_catalog_number = active.has_catalog_number;
    if (active.acquired_after) q.acquired_after = active.acquired_after;
    if (active.acquired_before) q.acquired_before = active.acquired_before;
    if (active.collector_id) q.collector_id = active.collector_id;
    return q;
  }

  async function fetchPage(active: SpecimenFiltersValue, cursor?: string): Promise<void> {
    const isFirst = cursor === undefined;
    loadState = isFirst ? { kind: 'loading' } : { kind: 'loading-more' };

    try {
      const { data, error, response } = await client.GET('/api/v1/specimens', {
        params: { query: buildQuery(active, cursor) },
      });
      if (error) {
        const body = error.error;
        loadState = {
          kind: 'error',
          message: body?.message || body?.code || `HTTP ${response.status}`,
        };
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

  // Refetch whenever filters change. Cursor pagination resets on
  // every filter change — the previous cursor is invalid under
  // new filters (CONTRACT.md §10.3, and the API explicitly rejects
  // cursors when q transitions in/out of relevance ordering).
  $effect(() => {
    const active = filters;
    items = [];
    void fetchPage(active);
  });

  function loadMore() {
    if (loadState.kind !== 'loaded' || !loadState.nextCursor) return;
    void fetchPage(filters, loadState.nextCursor);
  }

  function retry() {
    items = [];
    void fetchPage(filters);
  }

  const hasFilters = $derived(activeFilterCount(filters) > 0);
</script>

<section>
  <header class="mb-4 flex flex-wrap items-end justify-between gap-3">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Specimens</h1>
    {#if $isAuthenticated}
      <a
        href="/specimens/new"
        use:link
        data-testid="new-specimen"
        class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90"
      >
        New specimen
      </a>
    {/if}
  </header>

  <SpecimenFilters value={filters} onChange={applyFilters} />

  {#if filters.q}
    <p class="mb-3 text-xs text-[var(--color-text-muted)]" data-testid="relevance-hint">
      Sorted by relevance
    </p>
  {/if}

  {#if loadState.kind === 'loading'}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3" data-testid="loading">
      {#each Array.from({ length: 6 }, (_, i) => i) as i (i)}
        <div
          class="aspect-[4/3] animate-pulse rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)]"
        ></div>
      {/each}
    </div>
  {:else if loadState.kind === 'error'}
    <div
      class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center"
      data-testid="error"
      role="alert"
    >
      <p class="text-sm font-medium text-[var(--color-text)]">Couldn't load specimens.</p>
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
    {#if hasFilters}
      <div
        class="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] p-10 text-center"
        data-testid="empty-filtered"
      >
        <p class="text-sm text-[var(--color-text)]">No specimens match these filters.</p>
        <button
          type="button"
          onclick={() => applyFilters({})}
          data-testid="empty-clear-filters"
          class="mt-3 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm text-[var(--color-accent-fg)] hover:opacity-90"
        >
          Clear filters
        </button>
      </div>
    {:else}
      <div
        class="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] p-10 text-center"
        data-testid="empty"
      >
        <p class="text-sm text-[var(--color-text)]">No specimens yet.</p>
        <p class="mt-1 text-xs text-[var(--color-text-muted)]">
          Add your first specimen to get started.
        </p>
      </div>
    {/if}
  {:else}
    <ul class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3" data-testid="specimen-grid">
      {#each items as s (s.id)}
        <li class="contents">
          <SpecimenCard specimen={s} />
        </li>
      {/each}
    </ul>

    {#if loadState.kind === 'loaded' && loadState.nextCursor}
      <div class="mt-6 flex justify-center">
        <button
          type="button"
          onclick={loadMore}
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
