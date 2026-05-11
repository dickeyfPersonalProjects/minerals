<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';

  type LightboxPhotoKind = 'visible' | 'uv' | 'other';
  interface Props {
    photos: { id: string; alt: string; kind?: LightboxPhotoKind }[];
    startIndex: number;
    onClose: () => void;
    onDelete?: (photoId: string) => void;
    onCrop?: (photoId: string) => void;
  }

  // Mirror of SpecimenDetail's PHOTO_KIND_LABELS — the lightbox is
  // standalone enough that duplicating three strings beats threading
  // a label map through props.
  const KIND_LABELS: Record<LightboxPhotoKind, string> = {
    visible: 'Visible',
    uv: 'UV',
    other: 'Other',
  };

  const { photos, startIndex, onClose, onDelete, onCrop }: Props = $props();

  // The lightbox owns the active index after open; only the
  // initial value is read from props (parent remounts to reopen).
  let index = $state(untrack(() => startIndex));
  let dialog: HTMLDivElement | null = $state(null);

  $effect(() => {
    if (index >= photos.length) index = Math.max(0, photos.length - 1);
  });

  const current = $derived(photos[index] ?? null);

  function prev() {
    if (photos.length === 0) return;
    index = (index - 1 + photos.length) % photos.length;
  }

  function next() {
    if (photos.length === 0) return;
    index = (index + 1) % photos.length;
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault();
      prev();
    } else if (e.key === 'ArrowRight') {
      e.preventDefault();
      next();
    }
  }

  onMount(() => {
    void tick().then(() => dialog?.focus());
  });
</script>

<svelte:window onkeydown={onKey} />

<div
  bind:this={dialog}
  role="dialog"
  aria-modal="true"
  aria-label="Photo viewer"
  tabindex="-1"
  data-testid="lightbox"
  class="fixed inset-0 z-50 flex items-center justify-center p-4 outline-none"
>
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-black/85 backdrop-blur-sm"
    onclick={onClose}
    aria-label="Close photo viewer"
    data-testid="lightbox-backdrop"
  ></button>

  <button
    type="button"
    class="absolute right-4 top-4 z-10 rounded-md bg-white/10 px-3 py-1.5 text-sm text-white hover:bg-white/20"
    onclick={onClose}
    aria-label="Close"
    data-testid="lightbox-close"
  >
    ✕
  </button>

  {#if current}
    {@const target = current}
    <div class="absolute left-4 top-4 z-10 flex gap-2">
      {#if onCrop}
        <button
          type="button"
          class="rounded-md bg-white/15 px-3 py-1.5 text-sm font-medium text-white hover:bg-white/25"
          onclick={() => onCrop(target.id)}
          aria-label="Crop photo"
          data-testid="lightbox-crop"
        >
          Crop
        </button>
      {/if}
      {#if onDelete}
        <button
          type="button"
          class="rounded-md bg-red-600/90 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-600"
          onclick={() => onDelete(target.id)}
          aria-label="Delete photo"
          data-testid="lightbox-delete"
        >
          Delete
        </button>
      {/if}
    </div>
  {/if}

  {#if photos.length > 1}
    <button
      type="button"
      class="absolute left-2 top-1/2 z-10 -translate-y-1/2 rounded-full bg-white/10 p-3 text-white hover:bg-white/20 sm:left-4"
      onclick={prev}
      aria-label="Previous photo"
      data-testid="lightbox-prev"
    >
      ‹
    </button>
    <button
      type="button"
      class="absolute right-2 top-1/2 z-10 -translate-y-1/2 rounded-full bg-white/10 p-3 text-white hover:bg-white/20 sm:right-4"
      onclick={next}
      aria-label="Next photo"
      data-testid="lightbox-next"
    >
      ›
    </button>
  {/if}

  {#if current}
    {@const currentKind: LightboxPhotoKind = current.kind ?? 'visible'}
    <figure
      class="pointer-events-none relative z-10 flex max-h-full max-w-full flex-col items-center gap-3"
    >
      <img
        src={`/api/v1/photos/${current.id}/display`}
        alt={current.alt}
        class="max-h-[85vh] max-w-full rounded-md object-contain shadow-xl"
        data-testid="lightbox-image"
      />
      <figcaption class="flex items-center gap-2 text-xs text-white/70" data-testid="lightbox-meta">
        <span
          class="rounded bg-white/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-white"
          data-testid="lightbox-kind"
          data-kind={currentKind}
        >
          {KIND_LABELS[currentKind]}
        </span>
        {#if photos.length > 1}
          <span data-testid="lightbox-counter">{index + 1} / {photos.length}</span>
        {/if}
      </figcaption>
    </figure>
  {/if}
</div>
