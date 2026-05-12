<script lang="ts">
  // Template selector dialog (mi-c78.4).
  //
  // Shown when the user needs to pick a sticker-sheet template:
  //   1. They click "+ Add to QR code sheet" on a specimen card and have no
  //      sheet yet.
  //   2. They click "Add more specimens" from the single QR preview
  //      and have no sheet yet.
  //   3. They click "Change template" on the sheet page.
  //
  // Self-contained modal — mirrors ConfirmModal's focus trap and
  // Escape-to-cancel contract. The 5 template options are rendered
  // from the static qrTemplates map so adding a new template in the
  // backend + qrTemplates.ts auto-extends this dialog.
  import { onMount, tick } from 'svelte';
  import { qrTemplate, templateCapacity, type QRTemplate, type QRTemplateID } from './qrTemplates';

  interface Props {
    title?: string;
    initial?: QRTemplateID;
    confirmLabel?: string;
    busy?: boolean;
    onConfirm: (template: QRTemplateID) => void | Promise<void>;
    onCancel: () => void;
  }

  const {
    title = 'Choose label template',
    initial = 'avery-5160',
    confirmLabel = 'Use this template',
    busy = false,
    onConfirm,
    onCancel,
  }: Props = $props();

  const TEMPLATE_IDS: QRTemplateID[] = [
    'avery-5160',
    'avery-5163',
    'avery-5164',
    'avery-22806',
    'avery-l7160',
  ];
  const TEMPLATES: QRTemplate[] = TEMPLATE_IDS.map((id) => qrTemplate(id));

  // Intentionally seed `selected` from the prop's initial value
  // only — subsequent prop changes are ignored once the dialog
  // is open. The Svelte 5 compiler warns on this pattern; the
  // capture is deliberate, so we wrap it in an IIFE to silence
  // the lint without changing semantics.
  let selected: QRTemplateID = $state((() => initial)());
  let dialog: HTMLDivElement | null = $state(null);
  let cancelBtn: HTMLButtonElement | null = $state(null);

  function focusables(): HTMLElement[] {
    if (!dialog) return [];
    const sel =
      'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])';
    return Array.from(dialog.querySelectorAll<HTMLElement>(sel));
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
    // Default focus on Cancel so an accidental Enter dismisses
    // rather than confirms (same convention as ConfirmModal).
    void tick().then(() => cancelBtn?.focus());
  });

  async function handleConfirm() {
    if (busy) return;
    await onConfirm(selected);
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
  aria-labelledby="template-selector-title"
  data-testid="template-selector"
  class="fixed inset-0 z-50 flex items-center justify-center p-4"
>
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-black/60 backdrop-blur-sm"
    onclick={handleBackdrop}
    aria-label="Close dialog"
    tabindex="-1"
    data-testid="template-selector-backdrop"
  ></button>

  <div
    class="relative z-10 w-full max-w-xl rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-5 shadow-xl"
  >
    <h2
      id="template-selector-title"
      class="font-serif text-lg font-semibold text-[var(--color-text)]"
    >
      {title}
    </h2>
    <p class="mt-1 text-xs text-[var(--color-text-muted)]">
      Pick the Avery sticker sheet you're printing on.
    </p>

    <ul
      class="mt-4 grid max-h-[60vh] grid-cols-1 gap-3 overflow-y-auto sm:grid-cols-2"
      data-testid="template-selector-list"
    >
      {#each TEMPLATES as tmpl (tmpl.id)}
        {@const cap = templateCapacity(tmpl)}
        {@const isSelected = selected === tmpl.id}
        <li class="contents">
          <button
            type="button"
            onclick={() => (selected = tmpl.id)}
            data-testid="template-option"
            data-template-id={tmpl.id}
            data-selected={isSelected}
            aria-pressed={isSelected}
            class={`flex items-start gap-3 rounded-md border p-3 text-left transition ${
              isSelected
                ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)]'
                : 'border-[var(--color-border)] bg-[var(--color-surface)] hover:bg-[var(--color-surface-2)]'
            }`}
          >
            <!-- Mini grid preview: rows × cols swatches scaled to fit. -->
            <div
              class="shrink-0 rounded border border-[var(--color-border)] bg-white p-1"
              aria-hidden="true"
              style="width: 48px; height: 60px;"
            >
              <div
                class="grid h-full w-full"
                style={`grid-template-columns: repeat(${tmpl.cols}, 1fr); grid-template-rows: repeat(${tmpl.rows}, 1fr); gap: 1px;`}
              >
                {#each Array.from({ length: cap }, (_, i) => i) as i (i)}
                  <div class="bg-[var(--color-accent)] opacity-70"></div>
                {/each}
              </div>
            </div>
            <div class="min-w-0 flex-1">
              <p
                class="truncate text-sm font-medium text-[var(--color-text)]"
                data-testid="template-option-name"
              >
                {tmpl.name}
              </p>
              <p class="mt-0.5 text-xs text-[var(--color-text-muted)]">
                {tmpl.paperLabel} · {tmpl.cols}×{tmpl.rows} · {cap} per sheet
              </p>
            </div>
          </button>
        </li>
      {/each}
    </ul>

    <div class="mt-5 flex justify-end gap-2">
      <button
        bind:this={cancelBtn}
        type="button"
        onclick={onCancel}
        disabled={busy}
        data-testid="template-selector-cancel"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
      >
        Cancel
      </button>
      <button
        type="button"
        onclick={handleConfirm}
        disabled={busy}
        data-testid="template-selector-confirm"
        class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy ? 'Working…' : confirmLabel}
      </button>
    </div>
  </div>
</div>
