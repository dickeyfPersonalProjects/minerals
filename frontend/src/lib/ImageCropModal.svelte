<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Cropper from 'cropperjs';
  import 'cropperjs/dist/cropper.css';
  import { client } from './api';
  import { SUPPRESS_TOAST_HEADERS, envelopeMessage } from './api/wrapper';
  import { loadAuthedBlobUrl } from './photos/blob-url';
  import { toastError, toastSuccess } from './toasts';

  interface Props {
    specimenId: string;
    photoId: string;
    position: number;
    takenAt: string | null;
    onClose: () => void;
    onApplied: () => void | Promise<void>;
  }

  const { specimenId, photoId, position, takenAt, onClose, onApplied }: Props = $props();

  let dialog: HTMLDivElement | null = $state(null);
  let imgEl: HTMLImageElement | null = $state(null);
  let cropper: Cropper | null = $state(null);
  let dirty = $state(false);
  let busy = $state(false);
  let imageError = $state(false);
  let rotation = $state(0);

  // Source path for the photo bytes — kept as a stable string for
  // `data-src` on the rendered <img> so tests can still assert
  // against a meaningful URL.
  const imagePath = $derived(`/api/v1/photos/${photoId}/display`);
  // Blob URL holding the authenticated bytes (mi-lrqt). Null while
  // the request is in flight; set to the URL once resolved. Revoked
  // in the cleanup return below when the component teardown or the
  // path changes.
  let imageUrl: string | null = $state(null);

  $effect(() => {
    const path = imagePath;
    imageUrl = null;
    const ctrl = new AbortController();
    let createdUrl: string | null = null;
    let alive = true;
    loadAuthedBlobUrl(path, { signal: ctrl.signal })
      .then((url) => {
        if (!alive || ctrl.signal.aborted) {
          URL.revokeObjectURL(url);
          return;
        }
        createdUrl = url;
        imageUrl = url;
      })
      .catch(() => {
        if (!alive || ctrl.signal.aborted) return;
        imageError = true;
      });
    return () => {
      alive = false;
      ctrl.abort();
      if (createdUrl) URL.revokeObjectURL(createdUrl);
    };
  });

  function markDirty() {
    if (!dirty) dirty = true;
  }

  // Wrap to (-180, 180], matching the slider's range. Both 180 and -180
  // would display identically; canonicalize to 180 so the slider knob
  // doesn't jump ends after a full revolution.
  function normalizeAngle(deg: number): number {
    let n = deg % 360;
    if (n > 180) n -= 360;
    else if (n <= -180) n += 360;
    return n;
  }

  function rotateBy(delta: number) {
    if (!cropper) return;
    rotation = normalizeAngle(rotation + delta);
    cropper.rotateTo(rotation);
    markDirty();
  }

  function onSliderInput(e: Event) {
    if (!cropper) return;
    const val = Number((e.currentTarget as HTMLInputElement).value);
    rotation = val;
    cropper.rotateTo(val);
    markDirty();
  }

  function resetRotation() {
    if (!cropper) return;
    rotation = 0;
    cropper.rotateTo(0);
    markDirty();
  }

  function handleImageLoad() {
    if (!imgEl) return;
    cropper?.destroy();
    cropper = new Cropper(imgEl, {
      viewMode: 1,
      autoCropArea: 1,
      background: false,
      responsive: true,
      checkOrientation: false,
      cropend: markDirty,
    });
  }

  function handleImageError() {
    imageError = true;
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      if (!busy) onClose();
    }
  }

  onMount(() => {
    void tick().then(() => dialog?.focus());
    return () => {
      cropper?.destroy();
      cropper = null;
    };
  });

  function canvasToBlob(canvas: HTMLCanvasElement): Promise<Blob> {
    return new Promise((resolve, reject) => {
      canvas.toBlob(
        (b) => (b ? resolve(b) : reject(new Error('Canvas produced no blob'))),
        'image/jpeg',
        0.95,
      );
    });
  }

  async function applyCrop() {
    if (!dirty || busy || !cropper) return;
    busy = true;
    try {
      const canvas = cropper.getCroppedCanvas({ imageSmoothingQuality: 'high' });
      if (!canvas) {
        toastError('Crop failed: no cropped canvas');
        return;
      }
      const blob = await canvasToBlob(canvas);
      const filename = `cropped-${photoId.slice(0, 8)}.jpg`;
      const file = new File([blob], filename, { type: 'image/jpeg' });

      const upload = await client.POST('/api/v1/specimens/{id}/photos', {
        params: { path: { id: specimenId } },
        body: { file: file as unknown as string },
        bodySerializer: () => {
          const fd = new FormData();
          fd.append('file', file, file.name);
          return fd;
        },
        headers: SUPPRESS_TOAST_HEADERS,
      });
      if (upload.error) {
        // Upload failed — original is intact. Surface and bail.
        toastError(`Crop failed: ${envelopeMessage(upload.error, upload.response.status)}`);
        return;
      }
      const newPhotoId = upload.data?.id;
      if (!newPhotoId) {
        toastError('Crop failed: upload returned no id');
        return;
      }

      // Best-effort: inherit position + taken_at so the cropped
      // photo replaces the original in the gallery layout. Failures
      // here don't roll back the upload — the cropped image still
      // exists, just at the end of the gallery.
      await client.PATCH('/api/v1/photos/{id}', {
        params: { path: { id: newPhotoId } },
        body: { position, taken_at: takenAt ?? undefined },
        headers: SUPPRESS_TOAST_HEADERS,
      });

      // Delete the original last so an upload failure leaves it intact.
      const del = await client.DELETE('/api/v1/photos/{id}', {
        params: { path: { id: photoId } },
        headers: SUPPRESS_TOAST_HEADERS,
      });
      if (del.error) {
        // Cropped photo exists; original still here too. Better than
        // silent half-success — tell the user.
        toastError(
          `Crop saved but original not removed: ${envelopeMessage(del.error, del.response.status)}`,
        );
      } else {
        toastSuccess('Photo cropped');
      }

      await onApplied();
      onClose();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      toastError(`Crop failed: ${msg}`);
    } finally {
      busy = false;
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<div
  bind:this={dialog}
  role="dialog"
  aria-modal="true"
  aria-label="Crop photo"
  tabindex="-1"
  data-testid="crop-modal"
  class="fixed inset-0 z-50 flex items-center justify-center p-4 outline-none"
>
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-black/70 backdrop-blur-sm"
    onclick={() => {
      if (!busy) onClose();
    }}
    aria-label="Close crop editor"
    tabindex="-1"
    data-testid="crop-modal-backdrop"
  ></button>

  <div
    class="relative z-10 flex max-h-full w-full max-w-3xl flex-col gap-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4 shadow-xl"
  >
    <header class="flex items-center justify-between">
      <h2 class="font-serif text-lg font-semibold text-[var(--color-text)]">Crop photo</h2>
      <button
        type="button"
        onclick={() => {
          if (!busy) onClose();
        }}
        aria-label="Close"
        data-testid="crop-modal-close"
        class="rounded-md px-2 py-1 text-sm text-[var(--color-text-muted)] hover:text-[var(--color-text)] disabled:cursor-not-allowed disabled:opacity-60"
        disabled={busy}
      >
        ✕
      </button>
    </header>

    <div
      class="relative max-h-[60vh] overflow-hidden rounded-md bg-[var(--color-surface-2)]"
      data-testid="crop-modal-stage"
    >
      {#if imageError}
        <p
          class="p-8 text-center text-sm text-[var(--color-text-muted)]"
          data-testid="crop-modal-image-error"
        >
          Couldn't load this image for cropping.
        </p>
      {:else if imageUrl}
        <!-- cropperjs replaces this <img> with its own DOM after init.
             It needs a real <img> on first render. We only render it
             once the authenticated blob URL is in hand (mi-lrqt). -->
        <img
          bind:this={imgEl}
          src={imageUrl}
          data-src={imagePath}
          alt="Crop preview"
          class="block max-h-[60vh] w-full"
          data-testid="crop-modal-image"
          onload={handleImageLoad}
          onerror={handleImageError}
        />
      {/if}
    </div>

    <div class="flex flex-wrap items-center gap-3" data-testid="crop-modal-rotate-controls">
      <div class="flex items-center gap-1">
        <button
          type="button"
          onclick={() => rotateBy(-90)}
          disabled={busy || imageError}
          aria-label="Rotate 90 degrees counter-clockwise"
          data-testid="crop-modal-rotate-left"
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
        >
          <span aria-hidden="true">↺</span>
        </button>
        <button
          type="button"
          onclick={() => rotateBy(90)}
          disabled={busy || imageError}
          aria-label="Rotate 90 degrees clockwise"
          data-testid="crop-modal-rotate-right"
          class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
        >
          <span aria-hidden="true">↻</span>
        </button>
      </div>

      <label class="flex flex-1 items-center gap-2 text-sm text-[var(--color-text)]">
        <span class="sr-only">Rotation angle</span>
        <input
          type="range"
          min="-180"
          max="180"
          step="1"
          value={rotation}
          oninput={onSliderInput}
          disabled={busy || imageError}
          aria-label="Rotation angle in degrees"
          data-testid="crop-modal-rotate-slider"
          class="flex-1 disabled:cursor-not-allowed disabled:opacity-60"
        />
      </label>

      <button
        type="button"
        onclick={resetRotation}
        disabled={busy || imageError}
        aria-label="Reset rotation to zero degrees"
        data-testid="crop-modal-rotate-readout"
        class="min-w-[3.5rem] rounded-md px-2 py-1 text-right font-mono text-sm tabular-nums text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
      >
        {rotation}°
      </button>
    </div>

    <div
      role="alert"
      class="rounded-md border border-amber-400/60 bg-amber-100 px-3 py-2 text-sm font-medium text-amber-900 dark:border-amber-500/40 dark:bg-amber-950/40 dark:text-amber-200"
      data-testid="crop-modal-warning"
    >
      This will permanently replace the original image. This cannot be undone.
    </div>

    <div class="flex justify-end gap-2">
      <button
        type="button"
        onclick={() => {
          if (!busy) onClose();
        }}
        disabled={busy}
        data-testid="crop-modal-cancel"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
      >
        Cancel
      </button>
      <button
        type="button"
        onclick={applyCrop}
        disabled={!dirty || busy || imageError}
        data-testid="crop-modal-apply"
        class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy ? 'Applying…' : 'Apply Crop'}
      </button>
    </div>
  </div>
</div>
