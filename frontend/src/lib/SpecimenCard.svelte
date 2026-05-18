<script lang="ts">
  import { link } from 'svelte-spa-router';
  import { client } from './api';
  import AuthedImage from './photos/AuthedImage.svelte';
  import type { components } from './api/schema';
  import {
    addSpecimenToSheet,
    createSheet,
    qrSheetState,
    removeSpecimenFromSheet,
    setSheet,
    specimenOnSheet,
  } from './qrSheet';
  import TemplateSelector from './TemplateSelector.svelte';
  import { isAuthenticated } from './oidc/auth';
  import type { QRTemplateID } from './qrTemplates';

  type Specimen = components['schemas']['SpecimenView'];

  interface Props {
    specimen: Specimen;
  }
  const { specimen }: Props = $props();

  // Lazy-load the card thumbnail. Preference (mi-m8q): the
  // photo whose file_id matches specimen.main_image_id; fall back
  // to the first photo by position when no main is set or the
  // designated photo has since been deleted. The list endpoint
  // doesn't embed photo URLs (PhotoView lives at a sibling
  // endpoint), so each card resolves its own thumb. This is N+1
  // and should be replaced with an embedded thumb_url on
  // SpecimenView when the API grows it; tracked as discovered work.
  let thumbUrl: string | null = $state(null);
  let thumbFailed = $state(false);

  $effect(() => {
    const ctrl = new AbortController();
    let alive = true;
    const mainImageID = specimen.main_image_id;
    // Pulling 100 keeps the request count at one per card while
    // letting us find the main image (which may not be position 1)
    // without a second round-trip. v1 caps photos per specimen well
    // below 100 in practice; revisit when the wire shape embeds a
    // dedicated main-thumb URL.
    client
      .GET('/api/v1/specimens/{id}/photos', {
        params: {
          path: { id: specimen.id },
          query: { limit: mainImageID ? 100 : 1 },
        },
        signal: ctrl.signal,
      })
      .then(({ data, error }) => {
        if (!alive) return;
        if (error || !data?.items || data.items.length === 0) {
          thumbFailed = true;
          return;
        }
        const items = data.items;
        const main = mainImageID ? items.find((p) => p.file_id === mainImageID) : undefined;
        const chosen = main ?? items[0];
        if (!chosen) {
          thumbFailed = true;
          return;
        }
        thumbUrl = `/api/v1/photos/${chosen.id}/thumb`;
      })
      .catch((err: unknown) => {
        if (!alive || ctrl.signal.aborted) return;
        if (err instanceof Error && err.name === 'AbortError') return;
        thumbFailed = true;
      });
    return () => {
      alive = false;
      ctrl.abort();
    };
  });

  const typeColorClass: Record<Specimen['type'], string> = {
    mineral: 'bg-[var(--color-mineral)] text-[var(--color-accent-fg)]',
    rock: 'bg-[var(--color-rock)] text-[var(--color-accent-fg)]',
    meteorite: 'bg-[var(--color-meteorite)] text-[var(--color-accent-fg)]',
    fossil: 'bg-[var(--color-fossil)] text-[var(--color-accent-fg)]',
  };

  const truncate = (s: string | null | undefined, max: number): string => {
    if (!s) return '';
    return s.length > max ? `${s.slice(0, max - 1)}…` : s;
  };

  // QR sheet membership — derived from the global store so the
  // badge/button toggles in lockstep with every other card and
  // the navbar.
  const sheetSnapshot = $derived($qrSheetState);
  const onSheet = $derived(specimenOnSheet(sheetSnapshot, specimen.id));

  // While a mutation is in flight we disable the button. Add/remove
  // run optimistically against the store and roll back on failure.
  let busy = $state(false);
  let showTemplatePicker = $state(false);

  async function onAddClick(): Promise<void> {
    if (busy) return;
    if (sheetSnapshot.status !== 'loaded') {
      // No sheet yet → user picks a template first.
      showTemplatePicker = true;
      return;
    }
    busy = true;
    try {
      await addSpecimenToSheet(specimen.id);
    } finally {
      busy = false;
    }
  }

  async function onRemoveClick(): Promise<void> {
    if (busy) return;
    busy = true;
    // Optimistic: drop the row from the store immediately, then
    // restore the prior sheet if the API rejects (CONTRACT.md §10
    // keeps the server authoritative).
    const before = sheetSnapshot;
    if (before.status === 'loaded') {
      const filtered = (before.sheet.specimens ?? []).filter((s) => s.specimen_id !== specimen.id);
      setSheet({ ...before.sheet, specimens: filtered });
    }
    try {
      const ok = await removeSpecimenFromSheet(specimen.id);
      if (!ok && before.status === 'loaded') {
        setSheet(before.sheet);
      }
    } finally {
      busy = false;
    }
  }

  async function onTemplateConfirm(template: QRTemplateID): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      const sheet = await createSheet(template);
      if (!sheet) return;
      await addSpecimenToSheet(specimen.id);
    } finally {
      busy = false;
      showTemplatePicker = false;
    }
  }

  function onTemplateCancel(): void {
    if (busy) return;
    showTemplatePicker = false;
  }
</script>

<!--
  Card is a <div>, not an <a> — buttons inside an anchor are invalid
  HTML and Svelte 5's event delegation makes stopPropagation
  ineffective against the link's bubble-phase handler. The
  photo + title region carry their own link element; the footer
  with QR-sheet controls is outside that link.
-->
<div
  data-testid="specimen-card"
  class="group flex flex-col overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] transition hover:border-[var(--color-accent)] hover:shadow-md focus-within:outline focus-within:outline-2 focus-within:outline-[var(--color-accent)]"
>
  <a
    href={`/specimens/${specimen.id}`}
    use:link
    data-testid="specimen-card-link"
    class="flex flex-1 flex-col"
  >
    <div
      class="relative flex aspect-[4/3] items-center justify-center overflow-hidden bg-[var(--color-surface-2)]"
    >
      {#if thumbUrl && !thumbFailed}
        <AuthedImage
          src={thumbUrl}
          alt={`Photo of ${specimen.name}`}
          class="h-full w-full bg-black object-contain"
          loading="lazy"
          onerror={() => (thumbFailed = true)}
        />
      {:else}
        <!-- placeholder rock glyph -->
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="40"
          height="40"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
          class="text-[var(--color-text-muted)] opacity-60"
          aria-hidden="true"
        >
          <path d="M3 16.5 9 7l4 6 3-3 5 6.5z" stroke-linecap="round" stroke-linejoin="round" />
          <path d="M3 19h18" stroke-linecap="round" />
        </svg>
      {/if}
      {#if specimen.visibility !== 'private'}
        <span
          class="absolute right-2 top-2 rounded-full bg-black/60 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-white"
          data-testid="visibility-chip">{specimen.visibility}</span
        >
      {/if}
    </div>

    <div class="flex flex-1 flex-col gap-2 p-3 pb-1">
      <div class="flex items-start justify-between gap-2">
        <h2
          class="text-sm font-semibold leading-tight text-[var(--color-text)] group-hover:text-[var(--color-accent)]"
        >
          {specimen.name}
        </h2>
        <span
          class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide {typeColorClass[
            specimen.type
          ]}"
          data-testid="type-badge"
        >
          {specimen.type}
        </span>
      </div>
      {#if specimen.locality_text}
        <p class="text-xs text-[var(--color-text-muted)]">
          {truncate(specimen.locality_text, 80)}
        </p>
      {/if}
    </div>
  </a>

  {#if $isAuthenticated}
    <div class="flex items-center justify-end gap-1 px-3 pb-3 pt-1">
      {#if onSheet}
        <span
          data-testid="qr-sheet-badge"
          class="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300"
        >
          On QR sheet
        </span>
        <button
          type="button"
          onclick={onRemoveClick}
          disabled={busy}
          aria-label="Remove from QR sheet"
          data-testid="qr-sheet-remove"
          class="rounded-full px-1.5 py-0.5 text-xs text-[var(--color-text-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          ✕
        </button>
      {:else}
        <button
          type="button"
          onclick={onAddClick}
          disabled={busy}
          data-testid="qr-sheet-add"
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-0.5 text-[11px] text-[var(--color-text-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text)] disabled:cursor-not-allowed disabled:opacity-50"
        >
          + Add to QR code sheet
        </button>
      {/if}
    </div>
  {/if}
</div>

{#if $isAuthenticated && showTemplatePicker}
  <TemplateSelector onConfirm={onTemplateConfirm} onCancel={onTemplateCancel} {busy} />
{/if}
