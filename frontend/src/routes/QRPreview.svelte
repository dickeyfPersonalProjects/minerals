<script lang="ts">
  // /specimens/qr — shared print-preview page (mi-c78.3).
  //
  // Two modes selected by the hash querystring:
  //   ?specimen={id}  → single mode: one large centred QR for the
  //                     specimen, encoded as the specimen's page
  //                     URL.
  //   (no query)      → sheet mode: render the user's active QR
  //                     sheet onto the chosen Avery template grid.
  //
  // The print area contains ONLY the QR(s); a `qr-screen-only`
  // wrapper holds the controls (back link, print button, "Add to
  // sheet"). `@media print` rules in the head hide everything else
  // so the sticker grid lands cleanly on the Avery sheet.
  //
  // Sheet builder UI (template selector, per-card add/remove,
  // navbar item) lands in mi-c78.4 — this bead intentionally only
  // exposes a "Print" button + a minimal "Add to sheet" affordance
  // off the single-mode preview.
  import { onMount, untrack } from 'svelte';
  import { link, router } from 'svelte-spa-router';
  import { client } from '../lib/api';
  import { SUPPRESS_TOAST_HEADERS } from '../lib/api/wrapper';
  import type { components } from '../lib/api/schema';
  import QrCode from '../lib/QrCode.svelte';
  import {
    qrTemplate,
    templateCapacity,
    templatePageCount,
    tryQrTemplate,
    type QRTemplate,
  } from '../lib/qrTemplates';
  import { toastError, toastSuccess } from '../lib/toasts';

  type Specimen = components['schemas']['SpecimenView'];
  type SheetView = components['schemas']['QRSheetView'];
  type SheetSpecimen = components['schemas']['QRSheetSpecimenView'];

  const FALLBACK_TEMPLATE = 'avery-5160';

  type Mode =
    | { kind: 'idle' }
    | { kind: 'loading' }
    | { kind: 'single'; specimen: Specimen }
    | { kind: 'sheet'; sheet: SheetView }
    | { kind: 'sheet-empty' }
    | { kind: 'error'; message: string };

  let view: Mode = $state({ kind: 'idle' });
  let adding = $state(false);
  // Local copy of whether the current user has an active sheet —
  // surfaces the Add-to-sheet affordance view without a second
  // fetch on every render. `null` means "haven't checked yet".
  let hasSheet: boolean | null = $state(null);

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
    // Probe sheet existence so the "Add to sheet" button can show
    // its accurate view. Failures degrade silently — the button
    // can still be used (the API will create-on-add via mi-c78.4).
    void probeSheet();
  }

  async function probeSheet(): Promise<void> {
    const { data, response } = await client.GET('/api/v1/qr-sheet', {
      headers: SUPPRESS_TOAST_HEADERS,
    });
    if (response.status === 404) {
      hasSheet = false;
      return;
    }
    hasSheet = data ? true : null;
  }

  async function loadSheet(): Promise<void> {
    view = { kind: 'loading' };
    const { data, error, response } = await client.GET('/api/v1/qr-sheet', {
      headers: SUPPRESS_TOAST_HEADERS,
    });
    if (response.status === 404) {
      hasSheet = false;
      view = { kind: 'sheet-empty' };
      return;
    }
    if (error) {
      view = { kind: 'error', message: errorMessage(error, response.status) };
      return;
    }
    if (!data) {
      view = { kind: 'sheet-empty' };
      return;
    }
    hasSheet = true;
    view = { kind: 'sheet', sheet: data };
  }

  async function addToSheet(specimenId: string): Promise<void> {
    if (adding) return;
    adding = true;
    try {
      // First attempt: append to an existing sheet. 404 means the
      // user has no sheet yet — create one with the v1 default
      // template (mi-c78.4 will replace this with a chooser).
      let resp = await client.POST('/api/v1/qr-sheet/specimens', {
        body: { specimen_id: specimenId },
        headers: SUPPRESS_TOAST_HEADERS,
      });
      if (resp.error && resp.response.status === 404) {
        const create = await client.POST('/api/v1/qr-sheet', {
          body: { template: FALLBACK_TEMPLATE },
          headers: SUPPRESS_TOAST_HEADERS,
        });
        if (create.error) {
          toastError(errorMessage(create.error, create.response.status));
          return;
        }
        resp = await client.POST('/api/v1/qr-sheet/specimens', {
          body: { specimen_id: specimenId },
          headers: SUPPRESS_TOAST_HEADERS,
        });
      }
      if (resp.error) {
        toastError(errorMessage(resp.error, resp.response.status));
        return;
      }
      hasSheet = true;
      toastSuccess('Added to QR sheet');
    } finally {
      adding = false;
    }
  }

  function doPrint(): void {
    if (typeof window !== 'undefined') window.print();
  }

  onMount(() => {
    // The print stylesheet matches `body[data-qr-print="true"]`
    // so we can hide the layout chrome only while this route is
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

  function templateForSheet(s: SheetView): QRTemplate {
    return tryQrTemplate(s.template) ?? qrTemplate(FALLBACK_TEMPLATE);
  }

  function paginate(specimens: SheetSpecimen[], capacity: number): SheetSpecimen[][] {
    if (specimens.length === 0) return [];
    const pages: SheetSpecimen[][] = [];
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
        <button
          type="button"
          onclick={() => addToSheet(sp.id)}
          disabled={adding}
          data-testid="qr-add-to-sheet"
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-50"
        >
          {hasSheet ? 'Add to sheet' : 'Start a sheet with this specimen'}
        </button>
        <a
          href="/specimens/qr"
          use:link
          data-testid="qr-view-sheet"
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
        >
          View sheet →
        </a>
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
        Start one by adding a specimen from its QR preview.
      </p>
      <a
        href="/specimens"
        use:link
        class="mt-4 inline-block text-sm text-[var(--color-accent)] hover:underline"
      >
        ← back to specimens
      </a>
    </div>
  {:else if view.kind === 'sheet'}
    {@const sheet = view.sheet}
    {@const tmpl = templateForSheet(sheet)}
    {@const specimens = sheet.specimens ?? []}
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
      <button
        type="button"
        onclick={doPrint}
        data-testid="qr-print-button"
        class="rounded-md bg-[var(--color-accent)] px-4 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90"
      >
        Print
      </button>
    </div>

    {#if specimens.length === 0}
      <div
        class="qr-screen-only rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-center text-sm text-[var(--color-text-muted)]"
        data-testid="qr-sheet-no-specimens"
      >
        Sheet is empty. Add specimens from any specimen's QR preview.
      </div>
    {:else}
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
