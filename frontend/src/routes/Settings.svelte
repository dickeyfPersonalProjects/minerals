<script lang="ts">
  import { onMount } from 'svelte';
  import { client } from '../lib/api';
  import { toastSuccess } from '../lib/toasts';
  import type { components } from '../lib/api/schema';

  type Visibility = 'private' | 'unlisted' | 'public';
  type FieldKey = 'price' | 'acquired_from' | 'images';
  type FieldDefaults = components['schemas']['FieldDefaultsView'];

  // Sentinel for the "no user default" selection. The select's
  // value attribute can't be JSON null, and an empty string would
  // collide if backend ever added it as an enum value. A reserved
  // literal makes the unset state unambiguous.
  const UNSET = '__unset__';

  const FIELDS: { key: FieldKey; label: string }[] = [
    { key: 'price', label: 'Price' },
    { key: 'acquired_from', label: 'Acquired from' },
    { key: 'images', label: 'Images' },
  ];

  type SelectValue = Visibility | typeof UNSET;

  let loading = $state(true);
  let loadError: string | null = $state(null);
  let saving = $state(false);
  // Initial values from the server — the diff for the PATCH body
  // is computed against this snapshot, and clearing back to it
  // means "no change" so nothing is sent for that key.
  let initial: Record<FieldKey, SelectValue> = $state({
    price: UNSET,
    acquired_from: UNSET,
    images: UNSET,
  });
  let current: Record<FieldKey, SelectValue> = $state({
    price: UNSET,
    acquired_from: UNSET,
    images: UNSET,
  });

  function toSelectValue(v: Visibility | undefined): SelectValue {
    return v ?? UNSET;
  }

  function loadInto(defaults: FieldDefaults | null | undefined): void {
    const fd = defaults ?? {};
    for (const { key } of FIELDS) {
      const v = toSelectValue(fd[key]);
      initial[key] = v;
      current[key] = v;
    }
  }

  onMount(async () => {
    const { data, error } = await client.GET('/api/v1/profile');
    loading = false;
    if (error || !data) {
      // Toast middleware already surfaced the error; show an
      // inline note so the form doesn't appear blank without
      // explanation.
      loadError = error?.error?.message ?? error?.error?.code ?? 'Failed to load profile';
      return;
    }
    loadInto(data.field_defaults);
  });

  // dirty drives the Save button — disable when nothing changed
  // so an accidental click can't fire an empty PATCH.
  const dirty = $derived(FIELDS.some(({ key }) => current[key] !== initial[key]));

  // buildPatch returns the field_defaults payload for the PATCH.
  // Only changed keys are included. A change from a value back to
  // UNSET sends explicit null (delete). A change from UNSET to a
  // value, or value→value, sends the new value. Unchanged keys
  // are omitted so the backend leaves them alone.
  function buildPatch(): Record<FieldKey, Visibility | null> {
    const out: Partial<Record<FieldKey, Visibility | null>> = {};
    for (const { key } of FIELDS) {
      if (current[key] === initial[key]) continue;
      out[key] = current[key] === UNSET ? null : (current[key] as Visibility);
    }
    return out as Record<FieldKey, Visibility | null>;
  }

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (saving || !dirty) return;
    saving = true;
    const { data, error } = await client.PATCH('/api/v1/profile', {
      body: { field_defaults: buildPatch() },
    });
    saving = false;
    if (error || !data) {
      // Toast middleware already surfaced the error; keep current
      // selections so the user can retry without losing input.
      return;
    }
    loadInto(data.field_defaults);
    toastSuccess('Field defaults saved');
  }
</script>

<section class="mx-auto max-w-xl py-12" data-testid="settings">
  <header class="mb-6">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Settings</h1>
  </header>

  <form onsubmit={save} class="space-y-6" data-testid="settings-field-defaults-form">
    <fieldset class="space-y-4" disabled={loading || saving}>
      <legend class="text-lg font-medium text-[var(--color-text)]">Field defaults</legend>
      <p class="text-sm text-[var(--color-text-muted)]">
        These defaults apply to new specimens you create unless you override per specimen. They
        never make existing data more visible — only an explicit per-specimen setting does.
      </p>

      {#if loadError}
        <p
          role="alert"
          data-testid="settings-field-defaults-error"
          class="text-sm text-[var(--color-danger)]"
        >
          {loadError}
        </p>
      {/if}

      <div class="grid gap-4 sm:grid-cols-3">
        {#each FIELDS as { key, label } (key)}
          <div>
            <label
              for={`settings-default-${key}`}
              class="mb-1 block text-sm font-medium text-[var(--color-text)]"
            >
              {label}
            </label>
            <select
              id={`settings-default-${key}`}
              data-testid={`settings-default-${key}`}
              bind:value={current[key]}
              class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
            >
              <option value={UNSET}>System default (owner-only)</option>
              <option value="private">Private</option>
              <option value="unlisted">Unlisted</option>
              <option value="public">Public</option>
            </select>
          </div>
        {/each}
      </div>
    </fieldset>

    <button
      type="submit"
      data-testid="settings-field-defaults-save"
      disabled={loading || saving || !dirty}
      class="inline-flex items-center justify-center rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-on-accent)] disabled:cursor-not-allowed disabled:opacity-50"
    >
      {saving ? 'Saving…' : 'Save'}
    </button>
  </form>
</section>
