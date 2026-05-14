<script lang="ts">
  import { beginLogin } from './oidc/auth';
  import { toastError } from './toasts';

  let busy = $state(false);

  async function onClick(): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      await beginLogin();
    } catch (err: unknown) {
      busy = false;
      const message = err instanceof Error ? err.message : String(err);
      toastError(message);
    }
  }
</script>

<button
  type="button"
  onclick={onClick}
  disabled={busy}
  data-testid="login-button"
  class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)] disabled:opacity-60"
>
  Log in
</button>
