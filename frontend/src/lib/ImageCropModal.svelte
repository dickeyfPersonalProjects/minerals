<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Cropper from 'cropperjs';
  import { client } from './api';
  import { SUPPRESS_TOAST_HEADERS, envelopeMessage } from './api/wrapper';
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
  // The rotation actually applied to the cropper-image so far. cropperjs v2's
  // `$rotate` is *relative* (unlike v1's absolute `rotateTo`), so we track the
  // applied angle and rotate by the shortest delta to reach a new target.
  let appliedRotation = 0;

  // cropperjs v2 is a web-component rewrite: there is no single `Cropper`
  // instance with options. We feed a template into `new Cropper(img, …)` and
  // then drive the `<cropper-image>` / `<cropper-selection>` elements. This
  // template mirrors the v1 config: `initial-coverage="1"` reproduces v1's
  // `autoCropArea: 1` (selection covers the whole image), and dropping the
  // `background` attribute reproduces v1's `background: false`. Free-form crop
  // (no `aspect-ratio`) is preserved.
  // The <cropper-canvas> doesn't auto-size to the image (its shadow CSS only
  // sets min-height: 100px), so we give it an explicit height matching v1's
  // `max-h-[60vh]` editor stage — otherwise it renders as a ~100px strip.
  const CROPPER_TEMPLATE =
    '<cropper-canvas style="height: 60vh; width: 100%;">' +
    '<cropper-image rotatable scalable translatable></cropper-image>' +
    '<cropper-shade hidden></cropper-shade>' +
    '<cropper-handle action="select" plain></cropper-handle>' +
    '<cropper-selection initial-coverage="1" movable resizable>' +
    '<cropper-grid role="grid" bordered covered></cropper-grid>' +
    '<cropper-crosshair centered></cropper-crosshair>' +
    '<cropper-handle action="move" theme-color="rgba(255, 255, 255, 0.35)"></cropper-handle>' +
    '<cropper-handle action="n-resize"></cropper-handle>' +
    '<cropper-handle action="e-resize"></cropper-handle>' +
    '<cropper-handle action="s-resize"></cropper-handle>' +
    '<cropper-handle action="w-resize"></cropper-handle>' +
    '<cropper-handle action="ne-resize"></cropper-handle>' +
    '<cropper-handle action="nw-resize"></cropper-handle>' +
    '<cropper-handle action="se-resize"></cropper-handle>' +
    '<cropper-handle action="sw-resize"></cropper-handle>' +
    '</cropper-selection>' +
    '</cropper-canvas>';

  // V2 BFF (mi-3vc4): cookies travel on <img> requests automatically,
  // so the image URL is the backend path directly — no auth header to
  // attach, no Blob URL workaround.
  const imagePath = $derived(`/api/v1/photos/${photoId}/display`);

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

  // Rotate the cropper-image to an absolute angle. v2's `$rotate` is relative
  // and angle units default to radians; we pass a `deg` string and rotate by
  // the shortest signed delta so the image never spins the long way round.
  function rotateTo(target: number) {
    const image = cropper?.getCropperImage();
    if (!image) return;
    const delta = normalizeAngle(target - appliedRotation);
    image.$rotate(`${delta}deg`);
    appliedRotation = target;
  }

  function rotateBy(delta: number) {
    if (!cropper) return;
    rotation = normalizeAngle(rotation + delta);
    rotateTo(rotation);
    markDirty();
  }

  function onSliderInput(e: Event) {
    if (!cropper) return;
    const val = Number((e.currentTarget as HTMLInputElement).value);
    rotation = val;
    rotateTo(val);
    markDirty();
  }

  function resetRotation() {
    if (!cropper) return;
    rotation = 0;
    rotateTo(0);
    markDirty();
  }

  function handleImageLoad() {
    if (!imgEl) return;
    cropper?.destroy();
    rotation = 0;
    appliedRotation = 0;
    cropper = new Cropper(imgEl, { template: CROPPER_TEMPLATE });
    // v1 fired a `cropend` option callback when the user finished dragging the
    // crop box. v2's analog is the `actionend` event the <cropper-canvas>
    // emits when a pointer interaction (move/resize/select) finishes. Unlike
    // the selection's `change` event, `actionend` only fires on real user
    // interaction, so it won't mark the modal dirty on initial render.
    cropper.getCropperCanvas()?.addEventListener('actionend', markDirty);
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
      const selection = cropper.getCropperSelection();
      // v1's `getCroppedCanvas()` returned a canvas synchronously; v2's
      // `$toCanvas()` is async and lives on the <cropper-selection>. It draws
      // only the selected area, honouring the image's rotation transform.
      const canvas = await selection?.$toCanvas({
        beforeDraw: (ctx) => {
          ctx.imageSmoothingEnabled = true;
          ctx.imageSmoothingQuality = 'high';
        },
      });
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
      {:else}
        <!-- cropperjs replaces this <img> with its own DOM after init.
             It needs a real <img> on first render. Cookies travel on
             the request automatically under the V2 BFF cookie flow. -->
        <img
          bind:this={imgEl}
          src={imagePath}
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
