<script lang="ts" module>
  // Allowlist mirrors CONTRACT.md §12 (photos).
  export const ALLOWED_PHOTO_TYPES = [
    'image/jpeg',
    'image/png',
    'image/webp',
    'image/heic',
  ] as const;

  // Hardcoded to match the server's MAX_UPLOAD_BYTES default (CONTRACT.md
  // §12, §15). If the operator raises the server cap, bump this in
  // lockstep — there is no /api/v1/limits endpoint in v1.
  export const MAX_UPLOAD_BYTES = 100 * 1024 * 1024;

  // Concurrency cap so the UI stays responsive on multi-file drops.
  const CONCURRENCY = 3;

  export type FileStatus = 'pending' | 'uploading' | 'success' | 'error';

  export interface UploadItem {
    id: number;
    name: string;
    size: number;
    status: FileStatus;
    message?: string;
  }

  function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
    if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
    return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
  }

  // Some browsers leave file.type empty for HEIC. Fall back to the
  // extension before rejecting outright.
  function inferType(file: File): string {
    if (file.type) return file.type;
    const name = file.name.toLowerCase();
    if (name.endsWith('.heic') || name.endsWith('.heif')) return 'image/heic';
    if (name.endsWith('.jpg') || name.endsWith('.jpeg')) return 'image/jpeg';
    if (name.endsWith('.png')) return 'image/png';
    if (name.endsWith('.webp')) return 'image/webp';
    return '';
  }

  export function validateFile(file: File): { ok: true } | { ok: false; message: string } {
    const type = inferType(file);
    if (!ALLOWED_PHOTO_TYPES.includes(type as (typeof ALLOWED_PHOTO_TYPES)[number])) {
      return {
        ok: false,
        message: `Unsupported type (${type || 'unknown'}). Allowed: JPEG, PNG, WebP, HEIC.`,
      };
    }
    if (file.size > MAX_UPLOAD_BYTES) {
      return {
        ok: false,
        message: `File is ${formatBytes(file.size)}; max is ${formatBytes(MAX_UPLOAD_BYTES)}.`,
      };
    }
    return { ok: true };
  }
</script>

<script lang="ts">
  import { client } from './api';
  import { SUPPRESS_TOAST_HEADERS } from './api/wrapper';
  import { toastError, toastSuccess } from './toasts';

  interface Props {
    specimenId: string;
    onUploaded: () => void | Promise<void>;
  }
  const { specimenId, onUploaded }: Props = $props();

  let items: UploadItem[] = $state([]);
  let dragDepth = $state(0);
  let isDragging = $derived(dragDepth > 0);
  let fileInput: HTMLInputElement | undefined = $state();
  let nextId = 0;
  let busy = $state(false);

  // Mutate the proxied entry inside `items` (looked up by id) — not
  // a plain-object reference — so Svelte 5's deep $state proxy
  // observes the change and re-renders.
  function patch(id: number, fields: Partial<UploadItem>): void {
    const i = items.findIndex((it) => it.id === id);
    if (i < 0) return;
    Object.assign(items[i] as UploadItem, fields);
  }

  async function uploadOne(id: number, file: File): Promise<void> {
    patch(id, { status: 'uploading' });
    try {
      const result = await client.POST('/api/v1/specimens/{id}/photos', {
        params: { path: { id: specimenId } },
        // The OpenAPI spec types `file` as a string (binary). We
        // pass the File object and let the bodySerializer below
        // build a FormData — openapi-fetch detects FormData and
        // skips the JSON Content-Type header.
        body: { file: file as unknown as string },
        bodySerializer: () => {
          const fd = new FormData();
          fd.append('file', file, file.name);
          return fd;
        },
        // Per-file errors render inline on the upload row; a
        // summary toast fires once at end of batch instead.
        headers: SUPPRESS_TOAST_HEADERS,
      });
      if (result.error) {
        // The server's friendly `message` from the §10 envelope
        // (e.g. 415 unsupported_media_type, 413 photo_too_large).
        patch(id, {
          status: 'error',
          message:
            result.error.error?.message ||
            result.error.error?.code ||
            `HTTP ${result.response.status}`,
        });
        return;
      }
      patch(id, { status: 'success' });
    } catch (err: unknown) {
      patch(id, {
        status: 'error',
        message: err instanceof Error ? err.message : String(err),
      });
    }
  }

  async function processAll(toUpload: { id: number; file: File }[]): Promise<void> {
    busy = true;
    try {
      let cursor = 0;
      const workers = Array.from({ length: Math.min(CONCURRENCY, toUpload.length) }, async () => {
        while (cursor < toUpload.length) {
          const idx = cursor++;
          const next = toUpload[idx];
          if (!next) continue;
          await uploadOne(next.id, next.file);
        }
      });
      await Promise.all(workers);
      // Refetch even if some failed — successful uploads still
      // need to appear in the gallery (per the bead's acceptance
      // criteria: "On all uploads finished").
      await onUploaded();
      // Summary toasts (E-4): one success + one error per batch.
      const ids = toUpload.map((u) => u.id);
      const finished = items.filter((it) => ids.includes(it.id));
      const ok = finished.filter((it) => it.status === 'success').length;
      const failed = finished.filter((it) => it.status === 'error').length;
      if (ok > 0) toastSuccess(`Uploaded ${ok} photo${ok === 1 ? '' : 's'}`);
      if (failed > 0) toastError(`${failed} photo upload${failed === 1 ? '' : 's'} failed`);
    } finally {
      busy = false;
    }
  }

  function ingest(files: FileList | File[] | null): void {
    if (!files) return;
    const list = Array.from(files);
    if (list.length === 0) return;

    const queue: { id: number; file: File }[] = [];
    const newItems: UploadItem[] = [];
    for (const file of list) {
      const id = ++nextId;
      const validation = validateFile(file);
      if (!validation.ok) {
        newItems.push({
          id,
          name: file.name,
          size: file.size,
          status: 'error',
          message: validation.message,
        });
        continue;
      }
      newItems.push({
        id,
        name: file.name,
        size: file.size,
        status: 'pending',
      });
      queue.push({ id, file });
    }
    items = [...items, ...newItems];
    if (queue.length > 0) {
      void processAll(queue);
    }
  }

  function onPick(event: Event): void {
    const input = event.currentTarget as HTMLInputElement;
    ingest(input.files);
    // Reset so picking the same file twice still triggers change.
    input.value = '';
  }

  function onDragEnter(event: DragEvent): void {
    if (!event.dataTransfer?.types.includes('Files')) return;
    event.preventDefault();
    dragDepth += 1;
  }

  function onDragOver(event: DragEvent): void {
    if (!event.dataTransfer?.types.includes('Files')) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = 'copy';
  }

  function onDragLeave(event: DragEvent): void {
    if (!event.dataTransfer?.types.includes('Files')) return;
    event.preventDefault();
    dragDepth = Math.max(0, dragDepth - 1);
  }

  function onDrop(event: DragEvent): void {
    if (!event.dataTransfer?.types.includes('Files')) return;
    event.preventDefault();
    dragDepth = 0;
    ingest(event.dataTransfer.files);
  }

  function clearFinished(): void {
    items = items.filter((i) => i.status === 'pending' || i.status === 'uploading');
  }
</script>

<section
  class="relative rounded-lg border-2 border-dashed transition-colors {isDragging
    ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/5'
    : 'border-[var(--color-border)] bg-[var(--color-surface)]'}"
  ondragenter={onDragEnter}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={onDrop}
  data-testid="photo-uploader"
  data-dragging={isDragging}
  aria-label="Photo upload zone"
>
  <div class="p-6 text-center">
    <h3 class="font-serif text-base font-semibold text-[var(--color-text)]">Add photos</h3>
    <p class="mt-1 text-xs text-[var(--color-text-muted)]">
      JPEG, PNG, WebP, or HEIC · up to 100 MiB each
    </p>
    <p class="mt-3 hidden text-sm text-[var(--color-text-muted)] sm:block">
      Drag and drop here, or
    </p>
    <div class="mt-3">
      <input
        bind:this={fileInput}
        type="file"
        accept="image/jpeg,image/png,image/webp,image/heic,.heic,.heif"
        multiple
        class="sr-only"
        data-testid="photo-file-input"
        onchange={onPick}
      />
      <button
        type="button"
        onclick={() => fileInput?.click()}
        class="rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
        data-testid="photo-browse-button"
      >
        Browse files
      </button>
    </div>
  </div>

  {#if isDragging}
    <div
      class="pointer-events-none absolute inset-0 flex items-center justify-center rounded-lg bg-[var(--color-accent)]/10"
      data-testid="photo-drop-overlay"
    >
      <span
        class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)]"
      >
        Drop here
      </span>
    </div>
  {/if}
</section>

{#if items.length > 0}
  <ul class="mt-3 space-y-2" data-testid="photo-upload-list">
    {#each items as item (item.id)}
      <li
        class="flex items-center gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm"
        data-testid="photo-upload-item"
        data-status={item.status}
      >
        <span class="flex-1 truncate text-[var(--color-text)]" title={item.name}>{item.name}</span>
        <span class="font-mono text-xs text-[var(--color-text-muted)]"
          >{formatBytes(item.size)}</span
        >
        <div class="w-32">
          {#if item.status === 'pending'}
            <span class="text-xs text-[var(--color-text-muted)]">Queued…</span>
          {:else if item.status === 'uploading'}
            <div
              class="h-1.5 w-full overflow-hidden rounded-full bg-[var(--color-surface-2)]"
              role="progressbar"
              aria-label={`Uploading ${item.name}`}
            >
              <div class="h-full w-1/3 animate-pulse bg-[var(--color-accent)]"></div>
            </div>
          {:else if item.status === 'success'}
            <span class="text-xs font-medium text-emerald-600 dark:text-emerald-400">Uploaded</span>
          {:else}
            <span
              class="text-xs font-medium text-red-600 dark:text-red-400"
              data-testid="photo-upload-error"
              title={item.message ?? ''}
            >
              {item.message ?? 'Failed'}
            </span>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
  {#if !busy && items.some((i) => i.status === 'success' || i.status === 'error')}
    <div class="mt-2 text-right">
      <button
        type="button"
        onclick={clearFinished}
        class="text-xs text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
        data-testid="photo-upload-clear"
      >
        Clear finished
      </button>
    </div>
  {/if}
{/if}
