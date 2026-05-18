<script lang="ts">
  import { link, push } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import { SUPPRESS_TOAST_HEADERS } from '../lib/api/wrapper';
  import type { components } from '../lib/api/schema';
  import ConfirmModal from '../lib/ConfirmModal.svelte';
  import SpecimenForm from '../lib/SpecimenForm.svelte';
  import type { SpecimenFormSubmitResult } from '../lib/SpecimenForm.svelte';
  import { isAuthenticated } from '../lib/auth';
  import {
    formToPatchBody,
    specimenToFormValues,
    type SpecimenFormValues,
  } from '../lib/schemas/specimen';
  import { toastError, toastSuccess } from '../lib/toasts';

  type Specimen = components['schemas']['SpecimenView'];
  type Profile = components['schemas']['ProfileBody'];

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
  // Owner profile drives the 'Use my account default (currently: X)'
  // affordance on the per-field visibility selectors (mi-fo8 #7). A
  // load failure is non-blocking — the selectors degrade to showing
  // the system default ('private') and we still save normally.
  let ownerProfile: Profile | null = $state(null);
  let loadState: LoadState = $state({ kind: 'idle' });
  let confirmingDelete = $state(false);
  let deleting = $state(false);

  function envelopeMessage(
    error: { error?: { code?: string; message?: string } } | undefined,
    status: number,
  ): string {
    return error?.error?.message || error?.error?.code || `HTTP ${status}`;
  }

  async function load(id: string): Promise<void> {
    loadState = { kind: 'loading' };
    try {
      // Load the specimen and owner profile concurrently — the
      // profile carries the field_defaults the visibility selectors
      // use to render their 'currently: X' affordance, but a profile
      // failure must not block editing the specimen.
      const [specimenRes, profileRes] = await Promise.all([
        client.GET('/api/v1/specimens/{id}', { params: { path: { id } } }),
        client.GET('/api/v1/profile'),
      ]);
      if (specimenRes.error) {
        loadState = {
          kind: 'error',
          message: envelopeMessage(specimenRes.error, specimenRes.response.status),
        };
        return;
      }
      specimen = specimenRes.data ?? null;
      ownerProfile = profileRes.data ?? null;
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

  function requestDelete() {
    if (!specimen) return;
    confirmingDelete = true;
  }

  function cancelDelete() {
    confirmingDelete = false;
  }

  // Compose a friendly conflict message. Backend returns
  // `specimen_referenced` with a generic message; if some future
  // version surfaces `photos`/`journal_entries` counts in `details`,
  // upgrade the toast text inline (no schema break).
  function conflictMessage(
    error: { error?: { code?: string; message?: string; details?: unknown } } | undefined,
  ): string {
    const details = (error?.error?.details ?? {}) as Record<string, unknown>;
    const photos = typeof details.photos === 'number' ? details.photos : null;
    const journal =
      typeof details.journal_entries === 'number'
        ? details.journal_entries
        : typeof details.journal === 'number'
          ? details.journal
          : null;
    if (photos !== null && journal !== null) {
      return `This specimen has ${photos} photo${photos === 1 ? '' : 's'} and ${journal} journal entr${journal === 1 ? 'y' : 'ies'}. Delete those first.`;
    }
    return (
      error?.error?.message || 'This specimen has photos or journal entries. Delete those first.'
    );
  }

  async function confirmDelete() {
    if (!specimen || deleting) return;
    deleting = true;
    try {
      const { error, response } = await client.DELETE('/api/v1/specimens/{id}', {
        params: { path: { id: specimen.id } },
        headers: SUPPRESS_TOAST_HEADERS,
      });
      if (error) {
        if (response.status === 409) {
          toastError(conflictMessage(error));
        } else {
          toastError(envelopeMessage(error, response.status));
        }
        return;
      }
      toastSuccess('Specimen deleted');
      confirmingDelete = false;
      push('/specimens');
    } finally {
      deleting = false;
    }
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
    {#if $isAuthenticated}
      <div class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
        <SpecimenForm
          mode="edit"
          initial={specimenToFormValues(specimen)}
          submitLabel="Save"
          onSubmit={saveSpecimen}
          onCancel={cancel}
          onDelete={requestDelete}
          {ownerProfile}
        />
      </div>

      {#if confirmingDelete}
        <ConfirmModal
          title="Delete specimen?"
          message={`Delete ${specimen.name}? This cannot be undone.`}
          confirmLabel="Delete"
          busy={deleting}
          onConfirm={confirmDelete}
          onCancel={cancelDelete}
        />
      {/if}
    {:else}
      <div
        class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center"
        data-testid="auth-required"
        role="alert"
      >
        <p class="text-sm text-[var(--color-text)]">Log in to edit this specimen.</p>
      </div>
    {/if}
  {/if}
</section>
