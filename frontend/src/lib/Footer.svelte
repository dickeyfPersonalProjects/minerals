<script lang="ts">
  import { commitUrl, formatBuildDate } from './buildInfo';

  // Deploy marker (mi-c0sv). __GIT_SHA__ / __BUILD_DATE__ are compile-time
  // constants injected by Vite's `define` from CI build args; they default
  // to 'dev' / build-time `now` for local dev. Showing them in the footer
  // lets any operator confirm what's actually running by loading the page —
  // the human-visible detector for "tag moved but the pod didn't roll".
  const sha = __GIT_SHA__;
  const url = commitUrl(sha);
  const builtAt = formatBuildDate(__BUILD_DATE__);
</script>

<footer
  class="border-t border-[var(--color-border)] bg-[var(--color-surface)]/60 px-4 py-3 text-center text-xs text-[var(--color-text-muted)] sm:px-6"
>
  <span>Minerals · personal collection</span>
  <span aria-hidden="true"> · </span>
  <span data-testid="build-info">
    {#if url}
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        class="hover:text-[var(--color-accent)] hover:underline"
        data-testid="build-sha-link">v{sha}</a
      >
    {:else}
      <span data-testid="build-sha">v{sha}</span>
    {/if}
    · built {builtAt}
  </span>
</footer>
