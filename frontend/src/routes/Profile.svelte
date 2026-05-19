<script lang="ts">
  // Profile is the post-setup editor for "info about the user".
  // v1 (mi-j3kn) surfaces Name (editable) and Email (read-only).
  // Future beads add bio, avatar, and an email change + verify flow.
  // Field-visibility defaults live on /settings (mi-1ygd).
  import { authStore, setAuthUser } from '../lib/auth';
  import { client } from '../lib/api';
  import { SUPPRESS_TOAST_HEADERS } from '../lib/api/wrapper';
  import { toastSuccess } from '../lib/toasts';

  // MAX_LEN must match MaxDisplayNameLen on the backend
  // (internal/api/profile.go). Duplicated rather than threaded
  // through runtime-config because the backend re-validates on every
  // submit — the client cap is a UX courtesy, not a security boundary.
  const MAX_LEN = 80;

  const user = $derived($authStore.user);
  const email = $derived(user?.email ?? '');

  // The input reflects the persisted name until edited, so authStore
  // updates (after save or re-login) flow into the field. We track
  // initial separately to compute the dirty state for Save.
  let initialName: string = $state('');
  let displayName: string = $state('');
  let saving: boolean = $state(false);
  let errorMessage: string | null = $state(null);

  // Reset the input whenever the authStore's display_name changes.
  // This catches both the first load (store hydrates after probeAuth)
  // and a successful save (setAuthUser refreshes the store) without
  // racing the user's typing — equality guards prevent overwriting a
  // dirty field with the same value.
  $effect(() => {
    const next = user?.display_name ?? '';
    if (next !== initialName) {
      initialName = next;
      displayName = next;
    }
  });

  function trimmedLen(): number {
    return displayName.trim().length;
  }

  const trimmedName = $derived(displayName.trim());
  const dirty = $derived(trimmedName !== initialName.trim());
  const valid = $derived(trimmedLen() > 0 && trimmedLen() <= MAX_LEN);

  async function save(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (saving || !dirty || !valid) return;
    saving = true;
    errorMessage = null;

    // Suppress the global auto-toast for this call — the form
    // surfaces validation errors inline. Success uses toastSuccess
    // for the standard 'Saved' confirmation.
    const { data, error } = await client.PATCH('/api/v1/profile', {
      body: { display_name: trimmedName },
      headers: SUPPRESS_TOAST_HEADERS,
    });

    saving = false;
    if (error || !data) {
      const code = error?.error?.code;
      errorMessage =
        code === 'invalid_display_name'
          ? (error?.error?.message ?? 'Invalid display name')
          : (error?.error?.message ?? code ?? 'Failed to save profile');
      return;
    }
    setAuthUser(data);
    toastSuccess('Profile saved');
  }
</script>

<section class="mx-auto max-w-xl py-12" data-testid="profile">
  <header class="mb-6">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Profile</h1>
  </header>

  {#if user}
    <form onsubmit={save} class="space-y-6" data-testid="profile-form">
      <div>
        <label
          for="profile-display-name"
          class="mb-1 block text-sm font-medium text-[var(--color-text)]"
        >
          Name
        </label>
        <input
          id="profile-display-name"
          data-testid="profile-display-name"
          type="text"
          bind:value={displayName}
          maxlength={MAX_LEN}
          autocomplete="nickname"
          required
          disabled={saving}
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] disabled:cursor-not-allowed disabled:opacity-50"
        />
        <p class="mt-1 text-xs text-[var(--color-text-muted)]">
          {trimmedLen()} / {MAX_LEN} characters
        </p>
      </div>

      <div>
        <label for="profile-email" class="mb-1 block text-sm font-medium text-[var(--color-text)]">
          Email
        </label>
        <input
          id="profile-email"
          data-testid="profile-email"
          type="email"
          value={email}
          readonly
          disabled
          class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-muted,var(--color-surface))] px-3 py-2 text-sm text-[var(--color-text-muted)] disabled:cursor-not-allowed"
        />
        <p class="mt-1 text-xs text-[var(--color-text-muted)]">
          Email changes coming soon — will require email verification.
        </p>
      </div>

      {#if errorMessage}
        <p role="alert" data-testid="profile-error" class="text-sm text-[var(--color-danger)]">
          {errorMessage}
        </p>
      {/if}

      <button
        type="submit"
        data-testid="profile-save"
        disabled={saving || !dirty || !valid}
        class="inline-flex items-center justify-center rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-on-accent)] disabled:cursor-not-allowed disabled:opacity-50"
      >
        {saving ? 'Saving…' : 'Save'}
      </button>
    </form>
  {/if}
</section>
