<script lang="ts">
  import { link, push } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import type { components } from '../lib/api/schema';
  import SpecimenForm from '../lib/SpecimenForm.svelte';
  import type { SpecimenFormSubmitResult } from '../lib/SpecimenForm.svelte';
  import {
    formToPatchBody,
    specimenToFormValues,
    type SpecimenFormValues,
  } from '../lib/schemas/specimen';
  import { toastSuccess } from '../lib/toasts';

  type Specimen = components['schemas']['SpecimenView'];

  interface Props {
    params?: { id?: string };
  }
  const { params }: Props = $props();

  type LoadState =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'loaded' }
    | { kind: 'error'; message: string };

  let specimen: Specimen | null = $state(null);
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
      const { data, error, response } = await client.GET('/api/v1/specimens/{id}', {
        params: { path: { id } },
      });
      if (error) {
        loadState = { kind: 'error', message: envelopeMessage(error, response.status) };
        return;
      }
      specimen = data ?? null;
      loadState = { kind: 'loaded' };
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      loadState = { kind: 'error', message };
    }
  }

  $effect(() => {
    const id = params?.id;
    if (!id) {
      loadState = { kind: 'error', message: 'Missing specimen id' };
      return;
    }
    void load(id);
  });

  async function saveSpecimen(values: SpecimenFormValues): Promise<SpecimenFormSubmitResult> {
    if (!specimen) return { kind: 'error', message: 'No specimen loaded' };

    const body = formToPatchBody(specimen, values);
    if (Object.keys(body).length === 0) {
      // Nothing changed; treat as success without a toast — there
      // was nothing to save, so silently navigating back is the
      // least surprising outcome.
      push(`/specimens/${specimen.id}`);
      return { kind: 'ok' };
    }

    const { error, response } = await client.PATCH('/api/v1/specimens/{id}', {
      params: { path: { id: specimen.id } },
      body,
    });
    if (error) {
      const code = error.error?.code ?? '';
      const details = (error.error?.details ?? {}) as Record<string, unknown>;
      if (response.status === 409) {
        if (details.field === 'catalog_number' || code.includes('catalog_number')) {
          return { kind: 'duplicate_catalog_number' };
        }
      }
      if (typeof details.field === 'string' && details.field.length > 0) {
        return {
          kind: 'field_error',
          field: String(details.field),
          message: envelopeMessage(error, response.status),
        };
      }
      return { kind: 'error', message: envelopeMessage(error, response.status) };
    }
    toastSuccess('Specimen saved');
    push(`/specimens/${specimen.id}`);
    return { kind: 'ok' };
  }

  function cancel() {
    if (specimen) push(`/specimens/${specimen.id}`);
    else push('/specimens');
  }
</script>

<section>
  <header class="mb-4">
    {#if specimen}
      <a
        href={`/specimens/${specimen.id}`}
        use:link
        class="text-xs text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
      >
        ← Back to specimen
      </a>
    {:else}
      <a
        href="/specimens"
        use:link
        class="text-xs text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
      >
        ← All specimens
      </a>
    {/if}
    <h1 class="mt-1 text-2xl font-semibold tracking-tight text-[var(--color-text)]">
      Edit specimen
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
      <p class="text-sm font-medium text-[var(--color-text)]">Couldn't load specimen.</p>
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">{loadState.message}</p>
    </div>
  {:else if specimen}
    <div class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
      <SpecimenForm
        mode="edit"
        initial={specimenToFormValues(specimen)}
        submitLabel="Save"
        onSubmit={saveSpecimen}
        onCancel={cancel}
      />
    </div>
  {/if}
</section>
