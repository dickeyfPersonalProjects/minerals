<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import { readPostSetupReturn, SUPPRESS_TOAST_HEADERS } from '../lib/api/wrapper';
  import { toastSuccess } from '../lib/toasts';

  // MAX_LEN must match MaxDisplayNameLen on the backend
  // (internal/api/profile.go). Keeping the constant duplicated is
  // cheaper than threading another runtime-config field — the
  // backend re-validates on every submit anyway.
  const MAX_LEN = 80;

  let displayName: string = $state('');
  let submitting: boolean = $state(false);
  let errorMessage: string | null = $state(null);

  function trimmedLen(): number {
    return displayName.trim().length;
  }

  function isValid(): boolean {
    const n = trimmedLen();
    return n > 0 && n <= MAX_LEN;
  }

  function destFor(returnTo: string | null): string {
    if (!returnTo) return '/';
    const stripped = returnTo.startsWith('#') ? returnTo.slice(1) : returnTo;
    return stripped === '' ? '/' : stripped;
  }

  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    if (!isValid() || submitting) return;
    submitting = true;
    errorMessage = null;

    // Suppress the global auto-toast for this call — the form
    // surfaces validation errors inline. Network/server errors
    // still surface via toast on top, suppressed only on success.
    const { error, response } = await client.POST('/api/v1/profile', {
      body: { display_name: displayName.trim() },
      headers: SUPPRESS_TOAST_HEADERS,
    });

    submitting = false;
    if (error) {
      const code = error.error?.code;
      if (code === 'invalid_display_name') {
        errorMessage = error.error?.message ?? 'Invalid display name';
      } else {
        errorMessage = error.error?.message ?? error.error?.code ?? `HTTP ${response.status}`;
      }
      return;
    }
    toastSuccess('Profile saved');
    const returnTo = readPostSetupReturn();
    void push(destFor(returnTo));
  }
</script>

<section class="mx-auto max-w-md py-12" data-testid="profile-setup">
  <header class="mb-6">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">
      Welcome — finish setting up your profile
    </h1>
    <p class="mt-2 text-sm text-[var(--color-text-muted)]">
      Pick a display name to continue. You can change it later in settings.
    </p>
  </header>

  <form onsubmit={submit} class="space-y-4">
    <div>
      <label for="profile-display-name" class="block text-sm font-medium text-[var(--color-text)]">
        Display name
      </label>
      <input
        id="profile-display-name"
        data-testid="profile-display-name"
        type="text"
        bind:value={displayName}
        maxlength={MAX_LEN}
        autocomplete="nickname"
        required
        disabled={submitting}
        class="mt-1 w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
      />
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">
        {trimmedLen()} / {MAX_LEN} characters
      </p>
    </div>

    {#if errorMessage}
      <p role="alert" data-testid="profile-setup-error" class="text-sm text-[var(--color-danger)]">
        {errorMessage}
      </p>
    {/if}

    <button
      type="submit"
      data-testid="profile-setup-submit"
      disabled={!isValid() || submitting}
      class="inline-flex items-center justify-center rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-on-accent)] disabled:cursor-not-allowed disabled:opacity-50"
    >
      {submitting ? 'Saving…' : 'Continue'}
    </button>
  </form>
</section>
