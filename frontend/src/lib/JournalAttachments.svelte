<script lang="ts" module>
  // Allowlist mirrors CONTRACT.md §12 (journal attachments) and the
  // backend's accepted set for /api/v1/journal/{id}/files.
  export const ALLOWED_ATTACHMENT_TYPES = [
    'image/jpeg',
    'image/png',
    'image/webp',
    'application/pdf',
    'text/plain',
    'text/csv',
    'text/markdown',
    'application/json',
  ] as const;

  // Server cap; matches MAX_UPLOAD_BYTES (CONTRACT.md §12, §15).
  export const MAX_ATTACHMENT_BYTES = 100 * 1024 * 1024;

  const CONCURRENCY = 3;

  export type AttachmentUploadStatus = 'pending' | 'uploading' | 'success' | 'error';

  export interface AttachmentUploadItem {
    id: number;
    name: string;
    size: number;
    status: AttachmentUploadStatus;
    message?: string;
  }

  function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
    if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
    return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
  }

  // Some browsers leave file.type empty for less common types
  // (.md). Fall back to extension before rejecting.
  function inferType(file: File): string {
    if (file.type) return file.type;
    const name = file.name.toLowerCase();
    if (name.endsWith('.jpg') || name.endsWith('.jpeg')) return 'image/jpeg';
    if (name.endsWith('.png')) return 'image/png';
    if (name.endsWith('.webp')) return 'image/webp';
    if (name.endsWith('.pdf')) return 'application/pdf';
    if (name.endsWith('.csv')) return 'text/csv';
    if (name.endsWith('.md') || name.endsWith('.markdown')) return 'text/markdown';
    if (name.endsWith('.json')) return 'application/json';
    if (name.endsWith('.txt')) return 'text/plain';
    return '';
  }

  export function validateAttachment(file: File): { ok: true } | { ok: false; message: string } {
    const type = inferType(file);
    if (!ALLOWED_ATTACHMENT_TYPES.includes(type as (typeof ALLOWED_ATTACHMENT_TYPES)[number])) {
      return {
        ok: false,
        message: `Unsupported type (${type || 'unknown'}). Allowed: JPEG, PNG, WebP, PDF, plain text, CSV, Markdown, JSON.`,
      };
    }
    if (file.size > MAX_ATTACHMENT_BYTES) {
      return {
        ok: false,
        message: `File is ${formatBytes(file.size)}; max is ${formatBytes(MAX_ATTACHMENT_BYTES)}.`,
      };
    }
    return { ok: true };
  }

  export { formatBytes };
</script>

<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { client } from './api';
  import { SUPPRESS_TOAST_HEADERS } from './api/wrapper';
  import type { components } from './api/schema';
  import { isAuthenticated } from './auth';
  import { toastError, toastSuccess } from './toasts';

  type Attachment = components['schemas']['JournalFileView'];

  interface Props {
    entryId: string;
    // Initial list. If omitted the component fetches its own.
    initial?: Attachment[];
  }
  const { entryId, initial }: Props = $props();

  // Capture initial seed once at mount; the component owns its
  // attachment list state thereafter.
  const seed: Attachment[] | undefined = untrack(() => initial);
  let attachments: Attachment[] = $state(seed ?? []);
  // Stable snapshot of the auth store for non-reactive contexts
  // (drop handlers). Reactive UI gating still uses $isAuthenticated.
  const isAuthed = $derived($isAuthenticated);
  let uploads: AttachmentUploadItem[] = $state([]);
  let dragDepth = $state(0);
  let isDragging = $derived(dragDepth > 0);
  let fileInput: HTMLInputElement | undefined = $state();
  let nextUploadId = 0;
  let busy = $state(false);
  let listError: string | null = $state(null);
  let deletingId: string | null = $state(null);

  async function refetch(): Promise<void> {
    try {
      const result = await client.GET('/api/v1/journal/{id}/files', {
        params: { path: { id: entryId } },
      });
      if (result.error) {
        listError =
          result.error.error?.message ||
          result.error.error?.code ||
          `HTTP ${result.response.status}`;
        return;
      }
      attachments = result.data?.items ?? [];
      listError = null;
    } catch (err: unknown) {
      listError = err instanceof Error ? err.message : String(err);
    }
  }

  // Fetch only when no initial list was provided. Otherwise trust
  // the caller's seed — they likely already loaded everything in
  // one batch.
  onMount(() => {
    if (seed === undefined) void refetch();
  });

  function patch(id: number, fields: Partial<AttachmentUploadItem>): void {
    const i = uploads.findIndex((u) => u.id === id);
    if (i < 0) return;
    Object.assign(uploads[i] as AttachmentUploadItem, fields);
  }

  async function uploadOne(id: number, file: File): Promise<void> {
    patch(id, { status: 'uploading' });
    try {
      const result = await client.POST('/api/v1/journal/{id}/files', {
        params: { path: { id: entryId } },
        // openapi-fetch detects FormData and skips the JSON
        // Content-Type header. Same trick as PhotoUploader.
        body: { file: file as unknown as string },
        bodySerializer: () => {
          const fd = new FormData();
          fd.append('file', file, file.name);
          return fd;
        },
        // Per-file errors render inline on the upload row; the
        // batch summary toast covers global feedback.
        headers: SUPPRESS_TOAST_HEADERS,
      });
      if (result.error) {
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
      // need to appear in the list.
      await refetch();
      // Summary toasts (E-4): one success + one error per batch.
      const ids = toUpload.map((u) => u.id);
      const finished = uploads.filter((u) => ids.includes(u.id));
      const ok = finished.filter((u) => u.status === 'success').length;
      const failed = finished.filter((u) => u.status === 'error').length;
      if (ok > 0) toastSuccess(`Uploaded ${ok} attachment${ok === 1 ? '' : 's'}`);
      if (failed > 0) toastError(`${failed} attachment upload${failed === 1 ? '' : 's'} failed`);
    } finally {
      busy = false;
    }
  }

  function ingest(files: FileList | File[] | null): void {
    // Drop handlers stay wired even for anonymous users (the
    // listeners are attached at compile time), so we no-op the
    // ingest path — anonymous users have no upload affordance and
    // any drop should be silently ignored rather than 401.
    if (!isAuthed) return;
    if (!files) return;
    const list = Array.from(files);
    if (list.length === 0) return;

    const queue: { id: number; file: File }[] = [];
    const newItems: AttachmentUploadItem[] = [];
    for (const file of list) {
      const id = ++nextUploadId;
      const validation = validateAttachment(file);
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
      newItems.push({ id, name: file.name, size: file.size, status: 'pending' });
      queue.push({ id, file });
    }
    uploads = [...uploads, ...newItems];
    if (queue.length > 0) void processAll(queue);
  }

  function onPick(event: Event): void {
    const input = event.currentTarget as HTMLInputElement;
    ingest(input.files);
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
    uploads = uploads.filter((u) => u.status === 'pending' || u.status === 'uploading');
  }

  // Native confirm() is fine for v1 per the bead.
  async function onDelete(att: Attachment): Promise<void> {
    const confirmed = window.confirm(`Delete "${displayName(att)}"? This cannot be undone.`);
    if (!confirmed) return;
    deletingId = att.file_id;
    try {
      const result = await client.DELETE('/api/v1/journal-files/{file_id}', {
        params: { path: { file_id: att.file_id } },
      });
      if (result.error) {
        listError =
          result.error.error?.message ||
          result.error.error?.code ||
          `HTTP ${result.response.status}`;
        return;
      }
      toastSuccess('Attachment deleted');
      await refetch();
    } catch (err: unknown) {
      listError = err instanceof Error ? err.message : String(err);
    } finally {
      deletingId = null;
    }
  }

  // The backend doesn't expose the original filename on the
  // JournalFileView; surface a stable, readable label that mixes
  // content type + a short id suffix. Good enough for v1; can be
  // refined when the backend exposes the original name.
  function displayName(att: Attachment): string {
    const ext = extensionFromContentType(att.content_type);
    const shortId = att.file_id.slice(0, 8);
    return ext ? `${shortId}.${ext}` : shortId;
  }

  function extensionFromContentType(ct: string): string {
    switch (ct) {
      case 'image/jpeg':
        return 'jpg';
      case 'image/png':
        return 'png';
      case 'image/webp':
        return 'webp';
      case 'application/pdf':
        return 'pdf';
      case 'text/plain':
        return 'txt';
      case 'text/csv':
        return 'csv';
      case 'text/markdown':
        return 'md';
      case 'application/json':
        return 'json';
      default:
        return '';
    }
  }
</script>

<section
  class="relative rounded-md border border-dashed transition-colors {isDragging
    ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/5'
    : 'border-[var(--color-border)] bg-[var(--color-surface-2)]/40'}"
  ondragenter={onDragEnter}
  ondragover={onDragOver}
  ondragleave={onDragLeave}
  ondrop={onDrop}
  data-testid="journal-attachments"
  data-entry-id={entryId}
  data-dragging={isDragging}
  aria-label="Attachments"
>
  {#if attachments.length > 0}
    <ul class="divide-y divide-[var(--color-border)]" data-testid="journal-attachment-list">
      {#each attachments as att (att.file_id)}
        <li
          class="flex items-center gap-3 px-3 py-2 text-xs"
          data-testid="journal-attachment-item"
          data-file-id={att.file_id}
        >
          <a
            href={`/api/v1/files/${att.file_id}`}
            download
            class="flex-1 truncate text-[var(--color-accent)] hover:underline"
            data-testid="journal-attachment-download"
          >
            {displayName(att)}
          </a>
          <span class="font-mono text-[10px] text-[var(--color-text-muted)]"
            >{formatBytes(att.byte_size)}</span
          >
          {#if $isAuthenticated}
            <button
              type="button"
              onclick={() => onDelete(att)}
              disabled={deletingId === att.file_id}
              data-testid="journal-attachment-delete"
              class="rounded-md px-2 py-0.5 text-[11px] text-[var(--color-text-muted)] hover:bg-red-500/10 hover:text-red-600 disabled:opacity-50"
              aria-label={`Delete ${displayName(att)}`}
            >
              {deletingId === att.file_id ? 'Deleting…' : 'Delete'}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  {#if $isAuthenticated}
    <div class="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
      <p class="text-[11px] text-[var(--color-text-muted)]">Drop files here, or</p>
      <input
        bind:this={fileInput}
        type="file"
        accept="image/jpeg,image/png,image/webp,application/pdf,text/plain,text/csv,text/markdown,.md,application/json,.json,.txt,.csv"
        multiple
        class="sr-only"
        data-testid="journal-attachment-file-input"
        onchange={onPick}
      />
      <button
        type="button"
        onclick={() => fileInput?.click()}
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2.5 py-1 text-[11px] text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
        data-testid="journal-attachment-browse"
      >
        Add files
      </button>
    </div>
  {/if}

  {#if isDragging}
    <div
      class="pointer-events-none absolute inset-0 flex items-center justify-center rounded-md bg-[var(--color-accent)]/10"
      data-testid="journal-attachment-drop-overlay"
    >
      <span
        class="rounded-md bg-[var(--color-accent)] px-2 py-0.5 text-xs font-medium text-[var(--color-accent-fg)]"
      >
        Drop here
      </span>
    </div>
  {/if}
</section>

{#if listError}
  <p class="mt-1 text-[11px] text-red-500" data-testid="journal-attachment-list-error" role="alert">
    {listError}
  </p>
{/if}

{#if uploads.length > 0}
  <ul class="mt-2 space-y-1" data-testid="journal-attachment-upload-list">
    {#each uploads as item (item.id)}
      <li
        class="flex items-center gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1 text-[11px]"
        data-testid="journal-attachment-upload-item"
        data-status={item.status}
      >
        <span class="flex-1 truncate text-[var(--color-text)]" title={item.name}>{item.name}</span>
        <span class="font-mono text-[10px] text-[var(--color-text-muted)]"
          >{formatBytes(item.size)}</span
        >
        {#if item.status === 'pending'}
          <span class="text-[var(--color-text-muted)]">Queued…</span>
        {:else if item.status === 'uploading'}
          <span class="text-[var(--color-text-muted)]">Uploading…</span>
        {:else if item.status === 'success'}
          <span class="font-medium text-emerald-600 dark:text-emerald-400">Uploaded</span>
        {:else}
          <span
            class="font-medium text-red-600 dark:text-red-400"
            data-testid="journal-attachment-upload-error"
            title={item.message ?? ''}
          >
            {item.message ?? 'Failed'}
          </span>
        {/if}
      </li>
    {/each}
  </ul>
  {#if !busy && uploads.some((u) => u.status === 'success' || u.status === 'error')}
    <div class="mt-1 text-right">
      <button
        type="button"
        onclick={clearFinished}
        class="text-[10px] text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
        data-testid="journal-attachment-clear"
      >
        Clear finished
      </button>
    </div>
  {/if}
{/if}
