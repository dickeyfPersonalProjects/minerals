<script lang="ts">
  // Global toast renderer (E-4). Mounted once in App.svelte;
  // subscribes to the `toasts` store and renders each entry as a
  // dismissable card stacked in the bottom-right corner.
  //
  // No third-party library — ~100 lines of Svelte per the bead's
  // contract.
  import { fly } from 'svelte/transition';
  import { dismissToast, toasts, type Toast } from '../stores/toasts';

  // Type-keyed glyphs. Unicode rather than an icon dependency,
  // since `lucide-svelte` is not in package.json.
  const ICONS: Record<Toast['type'], string> = {
    success: '✓',
    error: '✗',
    info: 'ℹ',
    warning: '⚠',
  };

  const ICON_LABELS: Record<Toast['type'], string> = {
    success: 'Success',
    error: 'Error',
    info: 'Info',
    warning: 'Warning',
  };

  // Tailwind classes per toast type. Colors chosen to read in both
  // light and dark themes; mirrors the inline-form palette used
  // elsewhere (red-500/40 borders, etc.).
  const STYLES: Record<Toast['type'], string> = {
    success: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-800 dark:text-emerald-200',
    error: 'border-red-500/40 bg-red-500/10 text-red-800 dark:text-red-200',
    info: 'border-sky-500/40 bg-sky-500/10 text-sky-800 dark:text-sky-200',
    warning: 'border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-200',
  };
</script>

<!--
  aria-live=polite + role=status: assistive tech announces new
  toasts without interrupting the user. Errors get role=alert via
  the inner card.
-->
<ol
  data-testid="toaster"
  class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-end gap-2 p-4 sm:inset-x-auto sm:right-0"
  aria-live="polite"
  aria-relevant="additions"
>
  {#each $toasts as toast (toast.id)}
    <li
      data-testid="toast"
      data-toast-id={toast.id}
      data-toast-type={toast.type}
      role={toast.type === 'error' || toast.type === 'warning' ? 'alert' : 'status'}
      class="pointer-events-auto flex w-full max-w-sm items-start gap-2 rounded-md border p-3 text-sm shadow-md backdrop-blur-sm {STYLES[
        toast.type
      ]}"
      in:fly={{ y: 12, duration: 180 }}
      out:fly={{ y: 12, duration: 140 }}
    >
      <span aria-label={ICON_LABELS[toast.type]} class="font-mono text-base leading-none"
        >{ICONS[toast.type]}</span
      >
      <p class="flex-1 leading-snug">{toast.message}</p>
      <button
        type="button"
        data-testid="toast-close"
        aria-label="Dismiss notification"
        class="-mt-0.5 -mr-0.5 rounded p-1 text-current opacity-70 hover:opacity-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-current"
        onclick={() => dismissToast(toast.id)}
      >
        ×
      </button>
    </li>
  {/each}
</ol>
