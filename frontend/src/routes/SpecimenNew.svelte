<script lang="ts">
  import { link, push } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import SpecimenForm from '../lib/SpecimenForm.svelte';
  import type { SpecimenFormSubmitResult } from '../lib/SpecimenForm.svelte';
  import { formToCreateBody, type SpecimenFormValues } from '../lib/schemas/specimen';

  function envelopeMessage(
    error: { error?: { code?: string; message?: string } } | undefined,
    status: number,
  ): string {
    return error?.error?.message || error?.error?.code || `HTTP ${status}`;
  }

  async function createSpecimen(values: SpecimenFormValues): Promise<SpecimenFormSubmitResult> {
    const body = formToCreateBody(values);
    const { data, error, response } = await client.POST('/api/v1/specimens', { body });
    if (error) {
      const code = error.error?.code ?? '';
      const details = (error.error?.details ?? {}) as Record<string, unknown>;
      // 409 with details.field=catalog_number → duplicate; some
      // backends omit details so fall back to status+code.
      if (response.status === 409) {
        if (details.field === 'catalog_number' || code.includes('catalog_number')) {
          return { kind: 'duplicate_catalog_number' };
        }
      }
      // Field-scoped 400/422 → highlight the offending field.
      if (typeof details.field === 'string' && details.field.length > 0) {
        return {
          kind: 'field_error',
          field: String(details.field),
          message: envelopeMessage(error, response.status),
        };
      }
      return { kind: 'error', message: envelopeMessage(error, response.status) };
    }
    if (data?.id) {
      push(`/specimens/${data.id}`);
    } else {
      push('/specimens');
    }
    return { kind: 'ok' };
  }

  function cancel() {
    push('/specimens');
  }
</script>

<section>
  <header class="mb-4">
    <a
      href="/specimens"
      use:link
      class="text-xs text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
    >
      ← All specimens
    </a>
    <h1 class="mt-1 text-2xl font-semibold tracking-tight text-[var(--color-text)]">
      New specimen
    </h1>
  </header>

  <div class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
    <SpecimenForm mode="create" submitLabel="Create" onSubmit={createSpecimen} onCancel={cancel} />
  </div>
</section>
