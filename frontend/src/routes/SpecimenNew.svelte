<script lang="ts">
  // /specimens/new — create a specimen.
  //
  // Owns the API call + navigation; the form component is reused
  // from the edit route.
  import { push } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import type { components } from '../lib/api/schema';
  import SpecimenForm from '../lib/SpecimenForm.svelte';
  import { toCreateBody, type SpecimenFormValues } from '../lib/schemas/specimen';

  type ApiErrorBody = components['schemas']['ApiErrorBody'];

  async function submit(
    values: SpecimenFormValues,
  ): Promise<{ ok: true } | { ok: false; error: ApiErrorBody | null; status: number }> {
    const body = toCreateBody(values);
    const result = await client.POST('/api/v1/specimens', { body });
    if (result.error) {
      return { ok: false, error: result.error.error ?? null, status: result.response.status };
    }
    // Type narrowing guarantees `data` is present when there's no
    // error (the 201 response always includes the SpecimenView).
    void push(`/specimens/${result.data.id}`);
    return { ok: true };
  }
</script>

<section class="space-y-6" data-testid="specimen-new">
  <header>
    <h1 class="font-serif text-2xl font-semibold tracking-tight text-[var(--color-text)]">
      New specimen
    </h1>
    <p class="mt-1 text-sm text-[var(--color-text-muted)]">
      Catalog a mineral, rock, or meteorite. Type cannot be changed after creation.
    </p>
  </header>

  <SpecimenForm mode="create" {submit} cancelHref="/specimens" />
</section>
