<script lang="ts">
  import { onMount } from 'svelte';
  import { replace as routerReplace } from 'svelte-spa-router';
  import { handleAuthCallback } from '../lib/oidc/auth';

  type State = { kind: 'busy' } | { kind: 'error'; message: string };

  let state: State = $state({ kind: 'busy' });

  function parseQuery(): URLSearchParams {
    // After the path→hash rewrite in main.ts the URL looks like
    // `/#/auth/callback?code=...&state=...`. Pull the query off the
    // hash directly so this works regardless of how the router
    // exposes its querystring.
    const hash = window.location.hash;
    const i = hash.indexOf('?');
    return new URLSearchParams(i >= 0 ? hash.slice(i + 1) : '');
  }

  function destFor(returnTo: string): string {
    // `returnTo` is a stored hash route such as `#/specimens/abc`.
    // svelte-spa-router's `replace` expects the path-only form
    // (`/specimens/abc`); strip the leading `#` and default empty
    // values to the home route.
    const stripped = returnTo.startsWith('#') ? returnTo.slice(1) : returnTo;
    return stripped === '' ? '/' : stripped;
  }

  onMount(async () => {
    try {
      const result = await handleAuthCallback(parseQuery());
      await routerReplace(destFor(result.returnTo));
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      state = { kind: 'error', message };
    }
  });
</script>

<section class="mx-auto max-w-md py-12 text-center" data-testid="auth-callback">
  {#if state.kind === 'busy'}
    <p class="text-sm text-[var(--color-text-muted)]">Signing you in…</p>
  {:else}
    <h1 class="mb-2 text-lg font-semibold text-[var(--color-text)]">Sign-in failed</h1>
    <p class="mb-4 text-sm text-[var(--color-text-muted)]" data-testid="auth-callback-error">
      {state.message}
    </p>
    <a
      href="#/"
      data-testid="auth-callback-home"
      class="text-sm text-[var(--color-accent)] underline hover:no-underline"
    >
      Back to home
    </a>
  {/if}
</section>
