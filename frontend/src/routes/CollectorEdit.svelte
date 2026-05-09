<script lang="ts">
  import { link, push } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import type { components } from '../lib/api/schema';
  import CollectorForm from '../lib/CollectorForm.svelte';
  import type { CollectorFormSubmitResult } from '../lib/CollectorForm.svelte';

  type Collector = components['schemas']['CollectorView'];

  interface Props {
    params?: { id?: string };
  }
  const { params }: Props = $props();

  type LoadState =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'loaded' }
    | { kind: 'error'; message: string };

  let collector: Collector | null = $state(null);
  let loadState: LoadState = $state({ kind: 'idle' });

  function envelopeMessage(
    error: { error?: { code?: string; message?: string } } | undefined,
    status: number,
  ): string {
    return error?.error?.message || error?.error?.code || `HTTP ${status}`;
  }

  async function load(id: string): Promise<void> {
    loadState = { kind: 'loading' };
    try {
      const { data, error, response } = await client.GET('/api/v1/collectors/{id}', {
        params: { path: { id } },
      });
      if (error) {
        loadState = { kind: 'error', message: envelopeMessage(error, response.status) };
        return;
      }
      collector = data ?? null;
      loadState = { kind: 'loaded' };
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      loadState = { kind: 'error', message };
    }
  }

  $effect(() => {
    const id = params?.id;
    if (!id) {
      loadState = { kind: 'error', message: 'Missing collector id' };
      return;
    }
    void load(id);
  });

  async function saveCollector(values: {
    name: string;
    notes: string;
  }): Promise<CollectorFormSubmitResult> {
    if (!collector) return { kind: 'error', message: 'No collector loaded' };
    const body: { name?: string; notes?: string } = {};
    if (values.name !== collector.name) body.name = values.name;
    // Always send notes (even empty) — server treats omitted as unchanged,
    // empty as "clear". Trim already happened in CollectorForm.
    if (values.notes !== (collector.notes ?? '')) body.notes = values.notes;
    if (Object.keys(body).length === 0) {
      // Nothing to save; treat as success.
      push('/collectors');
      return { kind: 'ok' };
    }
    const { error, response } = await client.PATCH('/api/v1/collectors/{id}', {
      params: { path: { id: collector.id } },
      body,
    });
    if (error) {
      if (response.status === 409) return { kind: 'duplicate' };
      return { kind: 'error', message: envelopeMessage(error, response.status) };
    }
    push('/collectors');
    return { kind: 'ok' };
  }

  function cancel() {
    push('/collectors');
  }
</script>

<section>
  <header class="mb-4">
    <a
      href="/collectors"
      use:link
      class="text-xs text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
    >
      ← All collectors
    </a>
    <h1 class="mt-1 text-2xl font-semibold tracking-tight text-[var(--color-text)]">
      Edit collector
    </h1>
  </header>

  {#if loadState.kind === 'loading' || loadState.kind === 'idle'}
    <div
      data-testid="loading"
      class="h-40 animate-pulse rounded-md bg-[var(--color-surface-2)]"
    ></div>
  {:else if loadState.kind === 'error'}
    <div
      role="alert"
      data-testid="error"
      class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6"
    >
      <p class="text-sm font-medium text-[var(--color-text)]">Couldn't load collector.</p>
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">{loadState.message}</p>
    </div>
  {:else if collector}
    <div class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
      <CollectorForm
        initial={{ name: collector.name, notes: collector.notes ?? '' }}
        submitLabel="Save"
        onSubmit={saveCollector}
        onCancel={cancel}
      />
    </div>
  {/if}
</section>
