<script lang="ts">
  // Renders the global toast queue. Mounted once in App.svelte
  // (per E-4 acceptance criteria) so toasts surface regardless
  // of route.
  //
  // Position: fixed bottom-right, stacked vertically with newest
  // appended at the bottom (call order = visual order).
  //
  // No third-party UI library: per the bead constraints we use
  // bare unicode glyphs as icons and tailwind utilities for color.
  import { fly } from 'svelte/transition';
  import { dismissToast, toasts, type Toast } from './toasts';

  // Glyphs are bare unicode so we don't pull in a new icon dep.
  // (lucide-svelte was not pre-approved per CONTRACT.md §16's
  // frontend table.)
  const GLYPHS: Record<Toast['type'], string> = {
    success: '✓',
    error: '✗',
    info: 'ℹ',
    warning: '⚠',
  };

  // Each variant gets a tailwind class set keyed off the type.
  // Both light and dark themes are covered with WCAG-AA-friendly
  // text-on-tinted-surface combinations (CONTRACT.md §7b a11y).
  const VARIANT_CLASSES: Record<Toast['type'], string> = {
    success: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-800 dark:text-emerald-200',
    error: 'border-red-500/40 bg-red-500/10 text-red-800 dark:text-red-200',
    info: 'border-blue-500/40 bg-blue-500/10 text-blue-800 dark:text-blue-200',
    warning: 'border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-200',
  };

  function ariaRoleFor(t: Toast['type']): 'alert' | 'status' {
    // Errors/warnings interrupt; success/info are polite.
    return t === 'error' || t === 'warning' ? 'alert' : 'status';
  }
</script>

<div
  class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-end gap-2 p-4 sm:inset-x-auto sm:right-0"
  data-testid="toaster"
  aria-live="polite"
  aria-atomic="false"
>
  {#each $toasts as t (t.id)}
    <div
      class="pointer-events-auto flex w-full max-w-sm items-start gap-2 rounded-md border px-3 py-2 text-sm shadow-md sm:w-auto sm:min-w-[16rem] {VARIANT_CLASSES[
        t.type
      ]}"
      data-testid="toast"
      data-toast-type={t.type}
      data-toast-id={t.id}
      role={ariaRoleFor(t.type)}
      in:fly={{ y: 16, duration: 180 }}
      out:fly={{ y: 16, duration: 140 }}
    >
      <span aria-hidden="true" class="select-none font-semibold leading-5">
        {GLYPHS[t.type]}
      </span>
      <span class="flex-1 leading-5" data-testid="toast-message">{t.message}</span>
      <button
        type="button"
        onclick={() => dismissToast(t.id)}
        aria-label="Dismiss notification"
        data-testid="toast-close"
        class="-mr-1 rounded-md px-1.5 text-base leading-none opacity-70 hover:opacity-100 focus-visible:opacity-100 focus-visible:outline-none"
      >
        ×
      </button>
    </div>
  {/each}
</div>
