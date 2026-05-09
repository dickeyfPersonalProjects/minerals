<script lang="ts">
  // /specimens/:id/edit — edit an existing specimen.
  //
  // Loads the current specimen, then reuses SpecimenForm with the
  // existing values projected into the form-input shape. Submit
  // PATCHes /api/v1/specimens/{id}.
  import { push, link } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import type { components } from '../lib/api/schema';
  import SpecimenForm from '../lib/SpecimenForm.svelte';
  import { toPatchBody, type SpecimenFormValues } from '../lib/schemas/specimen';

  type SpecimenView = components['schemas']['SpecimenView'];
  type ApiErrorBody = components['schemas']['ApiErrorBody'];

  interface Props {
    params?: { id?: string };
  }
  const { params }: Props = $props();

  type LoadState =
    | { kind: 'loading' }
    | { kind: 'loaded'; specimen: SpecimenView }
    | { kind: 'error'; message: string };

  let loadState: LoadState = $state({ kind: 'loading' });

  $effect(() => {
    const id = params?.id;
    if (!id) {
      loadState = { kind: 'error', message: 'missing specimen id' };
      return;
    }
    void load(id);
  });

  async function load(id: string): Promise<void> {
    loadState = { kind: 'loading' };
    try {
      const { data, error, response } = await client.GET('/api/v1/specimens/{id}', {
        params: { path: { id } },
      });
      if (error) {
        const body = error.error;
        loadState = {
          kind: 'error',
          message: body?.message || body?.code || `HTTP ${response.status}`,
        };
        return;
      }
      if (!data) {
        loadState = { kind: 'error', message: 'no data returned' };
        return;
      }
      loadState = { kind: 'loaded', specimen: data };
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      loadState = { kind: 'error', message };
    }
  }

  async function submit(
    values: SpecimenFormValues,
  ): Promise<{ ok: true } | { ok: false; error: ApiErrorBody | null; status: number }> {
    const id = params?.id;
    if (!id) return { ok: false, error: null, status: 0 };
    const body = toPatchBody(values);
    const result = await client.PATCH('/api/v1/specimens/{id}', {
      params: { path: { id } },
      body,
    });
    if (result.error) {
      return { ok: false, error: result.error.error ?? null, status: result.response.status };
    }
    void push(`/specimens/${id}`);
    return { ok: true };
  }
</script>

<section class="space-y-6" data-testid="specimen-edit">
  {#if loadState.kind === 'loading'}
    <div data-testid="loading" class="space-y-4">
      <div class="h-8 w-48 animate-pulse rounded bg-[var(--color-surface-2)]"></div>
      <div class="h-64 animate-pulse rounded-lg bg-[var(--color-surface-2)]"></div>
    </div>
  {:else if loadState.kind === 'error'}
    <div
      class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center"
      data-testid="error"
      role="alert"
    >
      <p class="text-sm font-medium text-[var(--color-text)]">Couldn't load this specimen.</p>
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">{loadState.message}</p>
      <a
        href="/specimens"
        use:link
        class="mt-4 inline-block text-sm text-[var(--color-accent)] hover:underline"
      >
        ← back to specimens
      </a>
    </div>
  {:else}
    <header>
      <a
        href={`/specimens/${loadState.specimen.id}`}
        use:link
        class="inline-block text-xs text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
      >
        ← {loadState.specimen.name}
      </a>
      <h1
        class="mt-2 font-serif text-2xl font-semibold tracking-tight text-[var(--color-text)]"
        data-testid="edit-heading"
      >
        Edit specimen
      </h1>
    </header>

    <SpecimenForm
      mode="edit"
      initial={loadState.specimen}
      {submit}
      cancelHref={`/specimens/${loadState.specimen.id}`}
    />
  {/if}
</section>
