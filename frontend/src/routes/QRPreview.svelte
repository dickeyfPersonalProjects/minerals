<script lang="ts">
  // /specimens/qr — shared print-preview page (mi-c78.3 + mi-c78.4).
  //
  // Two modes selected by the hash querystring:
  //   ?specimen={id}  → single mode: one large centred QR for the
  //                     specimen, encoded as the specimen's page
  //                     URL.
  //   (no query)      → sheet mode: render the user's active QR
  //                     sheet onto the chosen Avery template grid,
  //                     plus the on-sheet specimen list, "Change
  //                     template" switcher, and "Clear sheet"
  //                     destructive action.
  //
  // The print area contains ONLY the QR(s); a `qr-screen-only`
  // wrapper holds the controls. `@media print` rules in the head
  // hide everything else so the sticker grid lands cleanly on the
  // Avery sheet.
  import { onMount, untrack } from 'svelte';
  import { link, push, router } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import type { components } from '../lib/api/schema';
  import ConfirmModal from '../lib/ConfirmModal.svelte';
  import QrCode from '../lib/QrCode.svelte';
  import TemplateSelector from '../lib/TemplateSelector.svelte';
  import {
    qrTemplate,
    templateCapacity,
    templatePageCount,
    tryQrTemplate,
    type QRTemplate,
    type QRTemplateID,
  } from '../lib/qrTemplates';
  import {
    addSpecimenToSheet,
    createSheet,
    deleteSheet,
    patchSheetTemplate,
    qrSheetState,
    refreshQrSheet,
    removeSpecimenFromSheet,
    type QRSheetView,
    type QRSheetSpecimenView,
  } from '../lib/qrSheet';
  import { isAuthenticated } from '../lib/oidc/auth';
  import { toastError, toastSuccess } from '../lib/toasts';

  type Specimen = components['schemas']['SpecimenView'];

  const FALLBACK_TEMPLATE: QRTemplateID = 'avery-5160';

  type Mode =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'single'; specimen: Specimen }
    | { kind: 'sheet' }
    | { kind: 'sheet-empty' }
    | { kind: 'error'; message: string };

  let view: Mode = $state({ kind: 'idle' });
  let busy = $state(false);
  let showTemplatePicker = $state(false);
  let templatePickerMode: 'create-then-add' | 'change' = $state('create-then-add');
  let showClearConfirm = $state(false);
  let clearing = $state(false);

  // Live sheet view backed by the store — sheet-mode reads from
  // here so add/remove/patch mutations in any component update
  // immediately.
  const sheet = $derived($qrSheetState);
  const hasSheet = $derived(sheet.status === 'loaded');
  const currentSheet = $derived<QRSheetView | null>(sheet.status === 'loaded' ? sheet.sheet : null);

  const specimenIdFromQuery = $derived.by(() =>
    new URLSearchParams(router.querystring ?? '').get('specimen'),
  );

  function specimenURL(id: string): string {
    // QR encodes the in-app specimen URL so a phone scan lands on
    // the live page (CONTRACT.md §7b hash routing).
    if (typeof window === 'undefined') return `/#/specimens/${id}`;
    return `${window.location.origin}/#/specimens/${id}`;
  }

  function errorMessage(
    error: { error?: { code?: string; message?: string } } | undefined,
    status: number,
  ): string {
    return error?.error?.message || error?.error?.code || `HTTP ${status}`;
  }

  async function loadSingle(id: string): Promise<void> {
    view = { kind: 'loading' };
    const { data, error, response } = await client.GET('/api/v1/specimens/{id}', {
      params: { path: { id } },
    });
    if (error) {
      view = { kind: 'error', message: errorMessage(error, response.status) };
      return;
    }
    if (!data) {
      view = { kind: 'error', message: 'Specimen not found' };
      return;
    }
    view = { kind: 'single', specimen: data };
    // Probe sheet existence so the "Add to QR code sheet" button shows
    // its accurate label. Errors are swallowed by the store; the
    // button degrades to the "start a sheet" copy on failure.
    void refreshQrSheet();
  }

  async function loadSheet(): Promise<void> {
    view = { kind: 'loading' };
    const next = await refreshQrSheet();
    if (next.status === 'none') {
      view = { kind: 'sheet-empty' };
      return;
    }
    if (next.status === 'loaded') {
      view = { kind: 'sheet' };
      return;
    }
    view = { kind: 'error', message: 'Could not load the sheet' };
  }

  function openCreateAndAdd(): void {
    templatePickerMode = 'create-then-add';
    showTemplatePicker = true;
  }

  function openChangeTemplate(): void {
    templatePickerMode = 'change';
    showTemplatePicker = true;
  }

  async function addToExistingSheet(specimenId: string): Promise<void> {
    const result = await addSpecimenToSheet(specimenId);
    if (result) toastSuccess('Added to QR sheet');
  }

  async function onAddCurrentSpecimenClick(specimenId: string): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      if (hasSheet) {
        await addToExistingSheet(specimenId);
      } else {
        openCreateAndAdd();
      }
    } finally {
      busy = false;
    }
  }

  async function handleTemplateConfirm(template: QRTemplateID): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      if (templatePickerMode === 'change') {
        const updated = await patchSheetTemplate(template);
        if (updated) toastSuccess('Template updated');
      } else {
        // create-then-add path. The "current specimen" is whatever
        // single-mode is showing right now — if the dialog was
        // opened from sheet mode somehow there's nothing to add,
        // so we just create the (empty) sheet.
        const newSheet = await createSheet(template);
        if (!newSheet) return;
        if (view.kind === 'single') {
          await addSpecimenToSheet(view.specimen.id);
          toastSuccess('Added to QR sheet');
        }
      }
    } finally {
      busy = false;
      showTemplatePicker = false;
    }
  }

  function handleTemplateCancel(): void {
    if (busy) return;
    showTemplatePicker = false;
  }

  async function onRemoveFromSheet(specimenId: string): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      await removeSpecimenFromSheet(specimenId);
    } finally {
      busy = false;
    }
  }

  function openClearConfirm(): void {
    showClearConfirm = true;
  }

  async function confirmClearSheet(): Promise<void> {
    if (clearing) return;
    clearing = true;
    try {
      const ok = await deleteSheet();
      if (ok) {
        toastSuccess('QR sheet cleared');
        view = { kind: 'sheet-empty' };
        showClearConfirm = false;
        void push('/specimens');
      } else {
        toastError('Could not clear the sheet');
      }
    } finally {
      clearing = false;
    }
  }

  function cancelClearSheet(): void {
    if (clearing) return;
    showClearConfirm = false;
  }

  function doPrint(): void {
    if (typeof window !== 'undefined') window.print();
  }

  onMount(() => {
    // The print stylesheet matches `body[data-qr-print="true"]`
    // so we hide the layout chrome only while this route is
    // mounted — every other page keeps its default print
    // behaviour.
    document.body.setAttribute('data-qr-print', 'true');
    return () => {
      document.body.removeAttribute('data-qr-print');
    };
  });

  // Drive the (re)fetch off the querystring. Reading `view` is
  // wrapped in `untrack` so writes inside loadSingle/loadSheet
  // don't re-trigger this effect — the only intended dep is the
  // query param.
  $effect(() => {
    const id = specimenIdFromQuery;
    untrack(() => {
      if (id) {
        if (view.kind !== 'single' || view.specimen.id !== id) {
          void loadSingle(id);
        }
      } else if (view.kind !== 'sheet' && view.kind !== 'sheet-empty') {
        void loadSheet();
      }
    });
  });

  // When the store transitions away from 'loaded' while we're on
  // the sheet view (e.g. another component clears the sheet), the
  // page should fall back to the empty state instead of holding
  // stale data.
  $effect(() => {
    const status = sheet.status;
    untrack(() => {
      if (specimenIdFromQuery) return;
      if (status === 'none' && view.kind === 'sheet') {
        view = { kind: 'sheet-empty' };
      } else if (status === 'loaded' && view.kind === 'sheet-empty') {
        view = { kind: 'sheet' };
      }
    });
  });

  function templateForSheet(s: QRSheetView): QRTemplate {
    return tryQrTemplate(s.template) ?? qrTemplate(FALLBACK_TEMPLATE);
  }

  function paginate(specimens: QRSheetSpecimenView[], capacity: number): QRSheetSpecimenView[][] {
    if (specimens.length === 0) return [];
    const pages: QRSheetSpecimenView[][] = [];
    for (let i = 0; i < specimens.length; i += capacity) {
      pages.push(specimens.slice(i, i + capacity));
    }
    return pages;
  }
</script>

<svelte:head>
  <title>QR preview · Minerals</title>
  <!--
    Print rules — only injected while this route is mounted, so
    the rest of the SPA keeps its normal print behaviour. `@page
    margin: 0` is required for the sticker grid to align with the
    physical labels (any browser-default margin pushes the cells
    off-axis).
  -->
  <style>
    @media print {
      @page {
        margin: 0;
      }
      html,
      body {
        background: white !important;
        margin: 0 !important;
      }
      body[data-qr-print='true'] header,
      body[data-qr-print='true'] footer,
      body[data-qr-print='true'] [data-testid='toaster'] {
        display: none !important;
      }
      body[data-qr-print='true'] main {
        max-width: none !important;
        padding: 0 !important;
        margin: 0 !important;
      }
      .qr-screen-only {
        display: none !important;
      }
      .qr-page-break {
        break-after: page;
        page-break-after: always;
      }
      .qr-page-break:last-child {
        break-after: auto;
        page-break-after: auto;
      }
    }
  </style>
</svelte:head>

<section data-testid="qr-preview" class="space-y-6">
  {#if view.kind === 'idle' || view.kind === 'loading'}
    <div data-testid="qr-loading" class="qr-screen-only">
      <div class="h-6 w-48 animate-pulse rounded bg-[var(--color-surface-2)]"></div>
      <div class="mt-4 aspect-square w-72 animate-pulse rounded bg-[var(--color-surface-2)]"></div>
    </div>
  {:else if view.kind === 'error'}
    <div
      class="qr-screen-only rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center"
      data-testid="qr-error"
      role="alert"
    >
      <p class="text-sm font-medium text-[var(--color-text)]">Couldn't load the QR preview.</p>
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">{view.message}</p>
      <a
        href="/specimens"
        use:link
        class="mt-4 inline-block text-sm text-[var(--color-accent)] hover:underline"
      >
        ← back to specimens
      </a>
    </div>
  {:else if view.kind === 'single'}
    {@const sp = view.specimen}
    <!-- PRINT AREA: one large QR centred for single-sticker print. -->
    <div
      class="mx-auto flex aspect-square w-full max-w-[6in] items-center justify-center bg-white p-4 print:max-w-none print:p-0"
      data-testid="qr-print-area"
    >
      <QrCode value={specimenURL(sp.id)} alt={`QR code for ${sp.name}`} />
    </div>

    <!-- Screen-only controls: name + Print + Add. -->
    <div class="qr-screen-only space-y-3 text-center" data-testid="qr-controls">
      <h1 class="font-serif text-xl font-semibold text-[var(--color-text)]" data-testid="qr-name">
        {sp.name}
      </h1>
      {#if sp.catalog_number}
        <p class="font-mono text-xs text-[var(--color-text-muted)]" data-testid="qr-catalog">
          {sp.catalog_number}
        </p>
      {/if}
      <div class="flex flex-wrap items-center justify-center gap-2">
        <button
          type="button"
          onclick={doPrint}
          data-testid="qr-print-button"
          class="rounded-md bg-[var(--color-accent)] px-4 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90"
        >
          Print
        </button>
        {#if $isAuthenticated}
          <button
            type="button"
            onclick={() => onAddCurrentSpecimenClick(sp.id)}
            disabled={busy}
            data-testid="qr-add-to-sheet"
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-50"
          >
            {hasSheet ? 'Add to QR code sheet' : 'Start a sheet with this specimen'}
          </button>
          <a
            href="/specimens/qr"
            use:link
            data-testid="qr-view-sheet"
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
          >
            View sheet →
          </a>
        {/if}
        <a
          href={`/specimens/${sp.id}`}
          use:link
          class="text-sm text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
        >
          ← back to specimen
        </a>
      </div>
    </div>
  {:else if view.kind === 'sheet-empty'}
    <div
      class="qr-screen-only rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center"
      data-testid="qr-sheet-empty"
    >
      <p class="text-sm font-medium text-[var(--color-text)]">No QR sheet yet.</p>
      <p class="mt-1 text-xs text-[var(--color-text-muted)]">
        Start one by adding a specimen from any specimen card or QR preview.
      </p>
      <a
        href="/specimens"
        use:link
        class="mt-4 inline-block text-sm text-[var(--color-accent)] hover:underline"
      >
        ← back to specimens
      </a>
    </div>
  {:else if view.kind === 'sheet' && currentSheet}
    {@const sheetData = currentSheet}
    {@const tmpl = templateForSheet(sheetData)}
    {@const specimens = sheetData.specimens ?? []}
    {@const cap = templateCapacity(tmpl)}
    {@const pageCount = templatePageCount(tmpl, specimens.length)}
    {@const pages = paginate(specimens, cap)}

    <!-- Screen-only sheet header. -->
    <div class="qr-screen-only flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="font-serif text-xl font-semibold text-[var(--color-text)]">QR sticker sheet</h1>
        <p class="text-xs text-[var(--color-text-muted)]" data-testid="qr-sheet-summary">
          {tmpl.name} · {specimens.length}
          {specimens.length === 1 ? 'specimen' : 'specimens'} · {pageCount}
          {pageCount === 1 ? 'page' : 'pages'}
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#if $isAuthenticated}
          <button
            type="button"
            onclick={openChangeTemplate}
            disabled={busy}
            data-testid="qr-sheet-change-template"
            class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-50"
          >
            Change template
          </button>
          <button
            type="button"
            onclick={openClearConfirm}
            data-testid="qr-sheet-clear"
            class="rounded-md border border-red-500/40 bg-[var(--color-surface)] px-3 py-1.5 text-sm text-red-600 hover:bg-red-500/10 dark:text-red-400"
          >
            Clear sheet
          </button>
        {/if}
        <button
          type="button"
          onclick={doPrint}
          data-testid="qr-print-button"
          class="rounded-md bg-[var(--color-accent)] px-4 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90"
        >
          Print
        </button>
      </div>
    </div>

    {#if specimens.length === 0}
      <div
        class="qr-screen-only rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center text-sm text-[var(--color-text-muted)]"
        data-testid="qr-sheet-no-specimens"
      >
        Sheet is empty. Add specimens from any specimen's QR preview or card.
      </div>
    {:else}
      <!-- Screen-only specimen sidebar: ordered list with remove
           buttons. Hidden in print so only the sticker grid hits
           paper. -->
      <ol
        class="qr-screen-only space-y-1 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-3"
        data-testid="qr-sheet-specimen-list"
      >
        {#each specimens as sp (sp.specimen_id)}
          <li
            class="flex items-center justify-between gap-2 rounded px-2 py-1 text-sm hover:bg-[var(--color-surface-2)]"
            data-testid="qr-sheet-specimen-row"
            data-specimen-id={sp.specimen_id}
          >
            <span class="flex min-w-0 items-center gap-2">
              <span
                class="inline-block w-6 shrink-0 text-right font-mono text-xs text-[var(--color-text-muted)]"
              >
                {sp.position}.
              </span>
              <a
                href={`/specimens/${sp.specimen_id}`}
                use:link
                class="truncate text-[var(--color-text)] hover:text-[var(--color-accent)]"
              >
                {sp.name}
              </a>
            </span>
            {#if $isAuthenticated}
              <button
                type="button"
                onclick={() => onRemoveFromSheet(sp.specimen_id)}
                disabled={busy}
                aria-label={`Remove ${sp.name} from sheet`}
                data-testid="qr-sheet-specimen-remove"
                class="rounded-full px-1.5 py-0.5 text-xs text-[var(--color-text-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text)] disabled:cursor-not-allowed disabled:opacity-50"
              >
                ✕
              </button>
            {/if}
          </li>
        {/each}
      </ol>

      <!-- PRINT AREA: one DOM node per physical page. -->
      <div data-testid="qr-sheet-pages">
        {#each pages as pageSpecimens, pageIdx (pageIdx)}
          {@const cells = Array.from({ length: cap }, (_, i) => pageSpecimens[i] ?? null)}
          <div
            class="qr-page-break mx-auto bg-white shadow-sm print:shadow-none"
            data-testid="qr-sheet-page"
            data-page-index={pageIdx}
            style="width: {tmpl.pageWidthMm}mm; height: {tmpl.pageHeightMm}mm; padding: {tmpl.marginTopMm}mm 0 0 {tmpl.marginLeftMm}mm; box-sizing: border-box;"
          >
            <div
              class="grid"
              style="grid-template-columns: repeat({tmpl.cols}, {tmpl.labelWidthMm}mm); grid-template-rows: repeat({tmpl.rows}, {tmpl.labelHeightMm}mm); column-gap: {tmpl.colGapMm}mm; row-gap: {tmpl.rowGapMm}mm;"
            >
              {#each cells as cell, cellIdx (cellIdx)}
                <div
                  class="flex items-center justify-center overflow-hidden p-1"
                  data-testid="qr-sheet-cell"
                  data-cell-empty={cell == null}
                >
                  {#if cell}
                    <div class="flex h-full w-full items-center gap-2">
                      <div class="aspect-square h-full shrink-0">
                        <QrCode
                          value={specimenURL(cell.specimen_id)}
                          alt={`QR code for ${cell.name}`}
                        />
                      </div>
                      <div class="min-w-0 flex-1 text-[8pt] leading-tight text-black">
                        <p
                          class="truncate font-semibold"
                          title={cell.name}
                          data-testid="qr-sheet-name"
                        >
                          {cell.name}
                        </p>
                      </div>
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</section>

{#if showTemplatePicker}
  <TemplateSelector
    title={templatePickerMode === 'change' ? 'Change label template' : 'Choose label template'}
    initial={(currentSheet && tryQrTemplate(currentSheet.template)?.id) || FALLBACK_TEMPLATE}
    confirmLabel={templatePickerMode === 'change' ? 'Update template' : 'Use this template'}
    onConfirm={handleTemplateConfirm}
    onCancel={handleTemplateCancel}
    {busy}
  />
{/if}

{#if showClearConfirm}
  <ConfirmModal
    title="Clear QR sheet?"
    message="This removes every specimen from your sheet and deletes it. You can build a new sheet at any time."
    confirmLabel="Clear sheet"
    busy={clearing}
    onConfirm={confirmClearSheet}
    onCancel={cancelClearSheet}
  />
{/if}
