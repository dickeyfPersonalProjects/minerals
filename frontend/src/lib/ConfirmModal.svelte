<script lang="ts">
  // Reusable confirm modal used by all destructive actions in the
  // SPA (E-2). Render conditionally from the parent — on mount it
  // traps focus and listens for Escape (cancel). Enter submits ONLY
  // after the user has explicitly focused the confirm button, so
  // hitting Enter on modal open never triggers the destructive
  // action.
  import { onMount, tick } from 'svelte';

  interface Props {
    title: string;
    message: string;
    confirmLabel?: string;
    cancelLabel?: string;
    destructive?: boolean;
    busy?: boolean;
    onConfirm: () => void | Promise<void>;
    onCancel: () => void;
  }

  const {
    title,
    message,
    confirmLabel = 'Delete',
    cancelLabel = 'Cancel',
    destructive = true,
    busy = false,
    onConfirm,
    onCancel,
  }: Props = $props();

  let dialog: HTMLDivElement | null = $state(null);
  let cancelBtn: HTMLButtonElement | null = $state(null);

  function focusables(): HTMLElement[] {
    if (!dialog) return [];
    const sel =
      'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]';
    return Array.from(dialog.querySelectorAll<HTMLElement>(sel)).filter(
      (el) => el.getAttribute('tabindex') !== '-1',
    );
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      if (!busy) onCancel();
      return;
    }
    if (e.key === 'Tab') {
      const items = focusables();
      if (items.length === 0) return;
      const first = items[0]!;
      const last = items[items.length - 1]!;
      const active = document.activeElement as HTMLElement | null;
      if (e.shiftKey && active === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }

  onMount(() => {
    // Default focus lands on Cancel so Enter doesn't accidentally
    // confirm a destructive action — the user must explicitly tab
    // (or click) onto Confirm before Enter fires it.
    void tick().then(() => cancelBtn?.focus());
  });

  async function handleConfirm() {
    if (busy) return;
    await onConfirm();
  }

  function handleBackdrop() {
    if (!busy) onCancel();
  }
</script>

<svelte:window onkeydown={onKey} />

<div
  bind:this={dialog}
  role="dialog"
  aria-modal="true"
  aria-labelledby="confirm-modal-title"
  aria-describedby="confirm-modal-message"
  data-testid="confirm-modal"
  class="fixed inset-0 z-50 flex items-center justify-center p-4"
>
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-black/60 backdrop-blur-sm"
    onclick={handleBackdrop}
    aria-label="Close dialog"
    tabindex="-1"
    data-testid="confirm-modal-backdrop"
  ></button>

  <div
    class="relative z-10 w-full max-w-sm rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-5 shadow-xl"
  >
    <h2
      id="confirm-modal-title"
      class="font-serif text-lg font-semibold text-[var(--color-text)]"
      data-testid="confirm-modal-title"
    >
      {title}
    </h2>
    <p
      id="confirm-modal-message"
      class="mt-2 text-sm text-[var(--color-text-muted)]"
      data-testid="confirm-modal-message"
    >
      {message}
    </p>
    <div class="mt-5 flex justify-end gap-2">
      <button
        bind:this={cancelBtn}
        type="button"
        onclick={onCancel}
        disabled={busy}
        data-testid="confirm-modal-cancel"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
      >
        {cancelLabel}
      </button>
      <button
        type="button"
        onclick={handleConfirm}
        disabled={busy}
        data-testid="confirm-modal-confirm"
        class={destructive
          ? 'rounded-md bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-60'
          : 'rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60'}
      >
        {busy ? 'Working…' : confirmLabel}
      </button>
    </div>
  </div>
</div>
