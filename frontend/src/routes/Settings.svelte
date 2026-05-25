<script lang="ts">
  import { onMount } from 'svelte';
  import { client } from '../lib/api';
  import { toastSuccess } from '../lib/toasts';
  import type { components } from '../lib/api/schema';

  type Visibility = 'private' | 'unlisted' | 'public';
  type FieldKey = 'price' | 'acquired_at' | 'acquired_from' | 'catalog_number' | 'images';
  type FieldDefaults = components['schemas']['FieldDefaultsView'];

  // Sentinel for the "no user default" selection. The select's
  // value attribute can't be JSON null, and an empty string would
  // collide if backend ever added it as an enum value. A reserved
  // literal makes the unset state unambiguous.
  const UNSET = '__unset__';

  // Display order matches the acceptance criteria on mi-z3d0:
  // Price, Acquired date, Acquired from, Catalog number, Images.
  // Helper text describes the field the dropdown governs in
  // privacy-policy terms; the legend + lede already explain the
  // overall behavior so each row stays a single sentence.
  const FIELDS: { key: FieldKey; label: string; description: string }[] = [
    {
      key: 'price',
      label: 'Price',
      description:
        'Asking price or purchase price (price_cents). Controls who sees the monetary value on the specimen.',
    },
    {
      key: 'acquired_at',
      label: 'Acquired date',
      description:
        'When you acquired the specimen (acquired_at). Controls who sees the acquisition date.',
    },
    {
      key: 'acquired_from',
      label: 'Acquired from',
      description:
        'Where the specimen came from — dealer, show, collector, etc. (acquired_from). Controls who sees the source.',
    },
    {
      key: 'catalog_number',
      label: 'Catalog number',
      description:
        'Your private catalog identifier (catalog_number). Controls who sees the catalog number on the specimen.',
    },
    {
      key: 'images',
      label: 'Images',
      description:
        'Photos attached to the specimen. Controls who sees photos that do not carry their own per-photo visibility setting.',
    },
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
    acquired_at: UNSET,
    acquired_from: UNSET,
    catalog_number: UNSET,
    images: UNSET,
  });
  let current: Record<FieldKey, SelectValue> = $state({
    price: UNSET,
    acquired_at: UNSET,
    acquired_from: UNSET,
    catalog_number: UNSET,
    images: UNSET,
  });

  // 'New specimens' default whole-specimen visibility (mi-q2d8).
  // Separate from the per-field defaults above: this is the value the
  // create form pre-fills the specimen's visibility with. UNSET means
  // "no preference; the create form falls back to the system default".
  let initialSpecimenVis: SelectValue = $state(UNSET);
  let currentSpecimenVis: SelectValue = $state(UNSET);

  function toSelectValue(v: Visibility | undefined | null): SelectValue {
    return v ?? UNSET;
  }

  function loadInto(
    defaults: FieldDefaults | null | undefined,
    specimenVis: Visibility | null | undefined,
  ): void {
    const fd = defaults ?? {};
    for (const { key } of FIELDS) {
      const v = toSelectValue(fd[key]);
      initial[key] = v;
      current[key] = v;
    }
    const sv = toSelectValue(specimenVis);
    initialSpecimenVis = sv;
    currentSpecimenVis = sv;
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
    loadInto(data.field_defaults, data.default_specimen_visibility);
  });

  // dirty drives the Save button — disable when nothing changed
  // so an accidental click can't fire an empty PATCH.
  const dirty = $derived(
    FIELDS.some(({ key }) => current[key] !== initial[key]) ||
      currentSpecimenVis !== initialSpecimenVis,
  );

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
    // Only send keys that changed. field_defaults is omitted entirely
    // when no per-field row moved; default_specimen_visibility is sent
    // (value, or null to clear) only when the 'New specimens' dropdown
    // changed.
    const fdPatch = buildPatch();
    const body: {
      field_defaults?: Record<FieldKey, Visibility | null>;
      default_specimen_visibility?: Visibility | null;
    } = {};
    if (Object.keys(fdPatch).length > 0) {
      body.field_defaults = fdPatch;
    }
    if (currentSpecimenVis !== initialSpecimenVis) {
      body.default_specimen_visibility =
        currentSpecimenVis === UNSET ? null : (currentSpecimenVis as Visibility);
    }
    const { data, error } = await client.PATCH('/api/v1/profile', { body });
    saving = false;
    if (error || !data) {
      // Toast middleware already surfaced the error; keep current
      // selections so the user can retry without losing input.
      return;
    }
    loadInto(data.field_defaults, data.default_specimen_visibility);
    toastSuccess('Settings saved');
  }

  // --- Danger zone: account deletion (GDPR right-to-erasure, mi-nwg5) -
  // The backend requires the literal confirmation phrase "DELETE" in
  // the request body; the UI mirrors that with a typed confirmation so
  // the irreversible action can't fire on a stray click.
  const DELETE_PHRASE = 'DELETE';
  let deleteConfirm = $state('');
  let deleting = $state(false);
  const canDelete = $derived(deleteConfirm === DELETE_PHRASE);

  async function deleteAccount(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (deleting || !canDelete) return;
    deleting = true;
    const { response, error } = await client.DELETE('/api/v1/account', {
      body: { confirm: DELETE_PHRASE },
    });
    if (error || !response.ok) {
      // Toast middleware already surfaced the error; re-enable the
      // button so the user can retry.
      deleting = false;
      return;
    }
    // Account + session are gone server-side. Hard-navigate to reboot
    // the SPA as an anonymous visitor; the now-revoked session cookie
    // is cleared by the backend on the next request.
    window.location.assign('/');
  }
</script>

<section class="mx-auto max-w-3xl py-12" data-testid="settings">
  <header class="mb-6">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Settings</h1>
  </header>

  <form onsubmit={save} class="space-y-6" data-testid="settings-field-defaults-form">
    <fieldset class="space-y-4" disabled={loading || saving}>
      <legend class="text-lg font-medium text-[var(--color-text)]">Field defaults</legend>
      <p class="text-sm text-[var(--color-text-muted)]">
        These defaults apply to all your specimens — both existing and new — whenever a specimen
        doesn't have its own per-field setting. To override for a specific specimen, set the field's
        visibility on that specimen's edit page.
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

      <ul
        class="divide-y divide-[var(--color-border)] border-t border-b border-[var(--color-border)]"
        data-testid="settings-field-defaults-list"
      >
        {#each FIELDS as { key, label, description } (key)}
          <li
            class="grid grid-cols-1 gap-2 py-3 sm:grid-cols-[14rem_1fr] sm:items-start sm:gap-4"
            data-testid={`settings-field-defaults-row-${key}`}
          >
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
            <p class="text-sm text-[var(--color-text-muted)] sm:pt-7">
              {description}
            </p>
          </li>
        {/each}
      </ul>
    </fieldset>

    <fieldset class="space-y-4" disabled={loading || saving} data-testid="settings-new-specimens">
      <legend class="text-lg font-medium text-[var(--color-text)]">New specimens</legend>
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-[14rem_1fr] sm:items-start sm:gap-4">
        <div>
          <label
            for="settings-default-specimen-visibility"
            class="mb-1 block text-sm font-medium text-[var(--color-text)]"
          >
            Default visibility for new specimens
          </label>
          <select
            id="settings-default-specimen-visibility"
            data-testid="settings-default-specimen-visibility"
            bind:value={currentSpecimenVis}
            class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
          >
            <option value={UNSET}>System default (private)</option>
            <option value="private">Private — only you can see it</option>
            <option value="unlisted">Unlisted — anyone with the link</option>
            <option value="public">Public — listed for everyone</option>
          </select>
        </div>
        <p class="text-sm text-[var(--color-text-muted)] sm:pt-7">
          New specimens use this visibility unless you change it on the create form. Existing
          specimens are unaffected.
        </p>
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

  <section
    class="mt-12 rounded-md border border-[var(--color-danger)] p-6"
    data-testid="settings-danger-zone"
  >
    <h2 class="text-lg font-medium text-[var(--color-danger)]">Delete account</h2>
    <p class="mt-2 text-sm text-[var(--color-text-muted)]">
      Permanently deletes your account and <strong>all</strong> of your data — every specimen, photo,
      journal entry, attachment, collector, uploaded file, and QR sheet. Your sign-in identity is removed
      and your sessions are ended. This cannot be undone.
    </p>
    <form
      onsubmit={deleteAccount}
      class="mt-4 space-y-3"
      data-testid="settings-delete-account-form"
    >
      <label for="settings-delete-confirm" class="block text-sm text-[var(--color-text)]">
        Type <code class="font-semibold">{DELETE_PHRASE}</code> to confirm:
      </label>
      <input
        id="settings-delete-confirm"
        data-testid="settings-delete-confirm"
        type="text"
        autocomplete="off"
        bind:value={deleteConfirm}
        disabled={deleting}
        class="w-full max-w-xs rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-danger)] focus:outline-none"
      />
      <button
        type="submit"
        data-testid="settings-delete-account"
        disabled={deleting || !canDelete}
        class="inline-flex items-center justify-center rounded-md bg-[var(--color-danger)] px-4 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:opacity-50"
      >
        {deleting ? 'Deleting…' : 'Delete my account'}
      </button>
    </form>
  </section>
</section>
