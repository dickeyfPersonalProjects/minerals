<script lang="ts">
  import { link, router } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import type { components } from '../lib/api/schema';
  import SpecimenCard from '../lib/SpecimenCard.svelte';

  type Specimen = components['schemas']['SpecimenView'];

  // Picks up `collector_id` from the route querystring so the
  // collector-management page (D-4) can deep-link a "specimens
  // referencing this collector" view. Backend filter lives at
  // `GET /api/v1/specimens?collector_id=` (mi-zv3).
  const collectorId = $derived.by(() => {
    const qs = router.querystring;
    if (!qs) return null;
    return new URLSearchParams(qs).get('collector_id');
  });

  type LoadState =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'loaded'; nextCursor: string | null }
    | { kind: 'loading-more' }
    | { kind: 'error'; message: string };

  let items: Specimen[] = $state([]);
  let loadState: LoadState = $state({ kind: 'idle' });

  async function fetchPage(cursor?: string): Promise<void> {
    const isFirst = cursor === undefined;
    loadState = isFirst ? { kind: 'loading' } : { kind: 'loading-more' };

    try {
      const query: { cursor?: string; collector_id?: string } = {};
      if (cursor) query.cursor = cursor;
      if (collectorId) query.collector_id = collectorId;
      const { data, error, response } = await client.GET('/api/v1/specimens', {
        params: { query },
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

  $effect(() => {
    // Re-runs whenever the collector_id filter changes (and on mount).
    void collectorId;
    items = [];
    void fetchPage();
  });

  function loadMore() {
    if (loadState.kind !== 'loaded' || !loadState.nextCursor) return;
    void fetchPage(loadState.nextCursor);
  }

  function retry() {
    items = [];
    void fetchPage();
  }
</script>

<section>
  <header class="mb-4 flex items-end justify-between">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Specimens</h1>
  </header>

  {#if collectorId}
    <div
      data-testid="collector-filter-banner"
      class="mb-4 flex flex-wrap items-center gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-xs text-[var(--color-text-muted)]"
    >
      <span>Filtered by collector.</span>
      <a
        href="/specimens"
        use:link
        class="underline hover:text-[var(--color-accent)]"
        data-testid="clear-collector-filter"
      >
        Clear filter
      </a>
    </div>
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
    <div
      class="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] p-10 text-center"
      data-testid="empty"
    >
      <p class="text-sm text-[var(--color-text)]">No specimens yet.</p>
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">
        Add your first specimen to get started.
      </p>
    </div>
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
