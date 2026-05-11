<script lang="ts">
  import { link } from 'svelte-spa-router';
  import { client } from './api';
  import type { components } from './api/schema';

  type Specimen = components['schemas']['SpecimenView'];

  interface Props {
    specimen: Specimen;
  }
  const { specimen }: Props = $props();

  // Lazy-load the first photo's thumbnail. The list endpoint
  // doesn't embed photo URLs (PhotoView lives at a sibling
  // endpoint), so each card resolves its own thumb. This is N+1
  // and should be replaced with an embedded thumb_url on
  // SpecimenView when the API grows it; tracked as discovered work.
  let thumbUrl: string | null = $state(null);
  let thumbFailed = $state(false);

  $effect(() => {
    const ctrl = new AbortController();
    let alive = true;
    client
      .GET('/api/v1/specimens/{id}/photos', {
        params: { path: { id: specimen.id }, query: { limit: 1 } },
        signal: ctrl.signal,
      })
      .then(({ data, error }) => {
        if (!alive) return;
        if (error || !data?.items || data.items.length === 0) {
          thumbFailed = true;
          return;
        }
        const first = data.items[0];
        if (!first) {
          thumbFailed = true;
          return;
        }
        thumbUrl = `/api/v1/photos/${first.id}/thumb`;
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
</script>

<a
  href={`/specimens/${specimen.id}`}
  use:link
  data-testid="specimen-card"
  class="group flex flex-col overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] transition hover:border-[var(--color-accent)] hover:shadow-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
>
  <div
    class="relative flex aspect-[4/3] items-center justify-center overflow-hidden bg-[var(--color-surface-2)]"
  >
    {#if thumbUrl && !thumbFailed}
      <img
        src={thumbUrl}
        alt={`Photo of ${specimen.name}`}
        class="h-full w-full object-cover"
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

  <div class="flex flex-1 flex-col gap-2 p-3">
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
