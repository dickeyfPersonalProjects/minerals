<script lang="ts">
  // Renders a static legal document (privacy policy / terms of
  // service) fetched from the public GET /api/v1/legal/{slug}
  // endpoint (mi-97kr). The backend returns sanitized HTML via the
  // CONTRACT.md §17 markdown pipeline (goldmark → bluemonday), so the
  // {@html} sink here is the contract's prescribed output — the same
  // pattern SpecimenDetail uses for journal body_html.
  import { onMount } from 'svelte';
  import { client } from './api';
  import { SUPPRESS_TOAST_HEADERS, envelopeMessage } from './api/wrapper';

  interface Props {
    // 'privacy' | 'terms' — the document slug, matching the API path
    // and the SPA route.
    slug: 'privacy' | 'terms';
  }
  const { slug }: Props = $props();

  type Status = 'loading' | 'loaded' | 'error';
  let status: Status = $state('loading');
  let html = $state('');
  let errorMessage = $state('');

  async function load(): Promise<void> {
    status = 'loading';
    // Suppress the global auto-toast — this page surfaces its own
    // inline error state, and a toast on a full-page document fetch
    // would be redundant noise.
    const { data, error, response } = await client.GET('/api/v1/legal/{slug}', {
      params: { path: { slug } },
      headers: SUPPRESS_TOAST_HEADERS,
    });
    if (error || !data) {
      status = 'error';
      errorMessage = envelopeMessage(error, response?.status ?? 0);
      return;
    }
    html = data.html;
    document.title = `${data.title} · Minerals`;
    status = 'loaded';
  }

  onMount(() => {
    void load();
  });
</script>

<article class="mx-auto max-w-3xl" data-testid="legal-doc">
  {#if status === 'loading'}
    <p class="text-sm text-[var(--color-text-muted)]" data-testid="legal-loading">Loading…</p>
  {:else if status === 'error'}
    <div data-testid="legal-error" class="space-y-3">
      <h1 class="font-serif text-xl font-semibold text-[var(--color-text)]">
        Unable to load document
      </h1>
      <p class="text-sm text-[var(--color-text-muted)]">{errorMessage}</p>
      <button
        type="button"
        onclick={load}
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
      >
        Retry
      </button>
    </div>
  {:else}
    <!--
      `html` is server-sanitized via the CONTRACT.md §17 markdown
      pipeline (goldmark → bluemonday allowlist). Direct {@html} is
      the contract's prescribed sink for this pipeline output, mirror-
      ing SpecimenDetail's journal body_html rendering.
    -->
    <div
      data-testid="legal-body"
      class="max-w-none text-sm leading-relaxed text-[var(--color-text)] [&>*+*]:mt-4 [&_a]:text-[var(--color-accent)] [&_a]:underline [&_blockquote]:border-l-2 [&_blockquote]:border-[var(--color-border)] [&_blockquote]:pl-3 [&_blockquote]:text-[var(--color-text-muted)] [&_code]:rounded [&_code]:bg-[var(--color-surface-2)] [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs [&_h1]:mb-2 [&_h1]:font-serif [&_h1]:text-2xl [&_h1]:font-semibold [&_h2]:mt-6 [&_h2]:font-serif [&_h2]:text-lg [&_h2]:font-semibold [&_h3]:font-serif [&_h3]:text-base [&_h3]:font-semibold [&_li]:mt-1 [&_ol]:list-decimal [&_ol]:pl-5 [&_table]:w-full [&_table]:border-collapse [&_table]:text-left [&_td]:border [&_td]:border-[var(--color-border)] [&_td]:p-2 [&_td]:align-top [&_th]:border [&_th]:border-[var(--color-border)] [&_th]:bg-[var(--color-surface-2)] [&_th]:p-2 [&_ul]:list-disc [&_ul]:pl-5"
    >
      <!-- eslint-disable-next-line svelte/no-at-html-tags -->
      {@html html}
    </div>
  {/if}
</article>
