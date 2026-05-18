<script lang="ts">
  // Drop-in replacement for <img> against authenticated backend
  // photo endpoints (mi-lrqt). Fetches the bytes via the wrapped
  // client (so the Authorization header gets attached), creates a
  // blob: URL, and renders an <img> against that URL. Revokes the
  // URL on src change and on unmount to release the bytes.
  //
  // `data-src` on the rendered <img> mirrors the server path so
  // tests can assert against meaningful URLs without inspecting
  // opaque blob: strings.
  //
  // When the underlying fetch fails (404, network error, etc.) the
  // component invokes the caller-supplied `onerror` callback once
  // with a synthetic Event — there is no real <img> error event to
  // bubble up because we never give the browser a broken src.

  import { loadAuthedBlobUrl } from './blob-url';

  interface Props {
    src: string;
    alt: string;
    class?: string;
    loading?: 'lazy' | 'eager';
    'data-testid'?: string;
    onload?: (e: Event) => void;
    onerror?: (e: Event) => void;
  }

  const props: Props = $props();

  let blobUrl: string | null = $state(null);

  $effect(() => {
    const path = props.src;
    blobUrl = null;
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
        blobUrl = url;
      })
      .catch(() => {
        if (!alive || ctrl.signal.aborted) return;
        props.onerror?.(new Event('error'));
      });
    return () => {
      alive = false;
      ctrl.abort();
      if (createdUrl) URL.revokeObjectURL(createdUrl);
    };
  });
</script>

{#if blobUrl}
  <img
    src={blobUrl}
    alt={props.alt}
    class={props.class}
    loading={props.loading}
    data-testid={props['data-testid']}
    data-src={props.src}
    onload={props.onload}
    onerror={props.onerror}
  />
{/if}
