<script lang="ts">
  import { onMount, type Snippet } from 'svelte';
  import { link } from 'svelte-spa-router';
  import ThemeToggle from './ThemeToggle.svelte';
  import LoginButton from './LoginButton.svelte';
  import ProfileMenu from './ProfileMenu.svelte';
  import { qrSheetState, refreshQrSheet } from './qrSheet';
  import { authStore } from './auth';

  interface Props {
    children?: Snippet;
  }
  const { children }: Props = $props();

  // Conditional "QR Sticker Sheet" nav item — only present when
  // the user has an active sheet. The store is the single source of
  // truth so add/remove/delete mutations elsewhere in the app
  // toggle the nav item without an extra fetch.
  const sheetState = $derived($qrSheetState);
  const showQrSheetLink = $derived(sheetState.status === 'loaded');

  // V2 BFF cookie flow (mi-3vc4): the SPA learns its auth state by
  // probing GET /api/v1/profile in main.ts. Show the profile menu
  // when the probe resolved to a user; otherwise show the Login
  // button. The Login button is a plain `<a href="/auth/login">`
  // anchor, so we don't need to gate it on runtime-config — clicking
  // it always navigates to the backend, which decides whether OIDC
  // is configured for this deployment.
  const auth = $derived($authStore);
  const showProfileMenu = $derived(auth.user !== null);
  const showLoginButton = $derived(!showProfileMenu);

  onMount(() => {
    // Probe once on app load. The store ignores 404s and keeps the
    // nav item hidden when no sheet exists.
    void refreshQrSheet();
  });
</script>

<div class="flex min-h-screen flex-col bg-[var(--color-bg)] text-[var(--color-text)]">
  <header class="border-b border-[var(--color-border)] bg-[var(--color-surface)]/80 backdrop-blur">
    <div class="mx-auto flex max-w-6xl items-center justify-between gap-3 px-4 py-3 sm:px-6">
      <a
        href="/specimens"
        use:link
        class="flex items-center gap-2 text-base font-semibold tracking-tight text-[var(--color-text)] hover:text-[var(--color-accent)]"
      >
        <span class="inline-block h-5 w-5 rounded-sm bg-[var(--color-accent)]" aria-hidden="true"
        ></span>
        Minerals
      </a>
      <nav class="flex items-center gap-3 text-sm">
        <a
          href="/specimens"
          use:link
          class="text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
        >
          Specimens
        </a>
        {#if showQrSheetLink}
          <a
            href="/specimens/qr"
            use:link
            data-testid="nav-qr-sheet"
            class="text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
          >
            QR Sticker Sheet
          </a>
        {/if}
        <a
          href="/collectors"
          use:link
          data-testid="nav-collectors"
          class="text-[var(--color-text-muted)] hover:text-[var(--color-accent)]"
        >
          Collectors
        </a>
        {#if showProfileMenu}
          <ProfileMenu />
        {:else if showLoginButton}
          <LoginButton />
        {/if}
        <ThemeToggle />
      </nav>
    </div>
  </header>

  <main class="mx-auto w-full max-w-6xl flex-1 px-4 py-6 sm:px-6 sm:py-8">
    {#if children}
      {@render children()}
    {/if}
  </main>

  <footer
    class="border-t border-[var(--color-border)] bg-[var(--color-surface)]/60 px-4 py-3 text-center text-xs text-[var(--color-text-muted)] sm:px-6"
  >
    Minerals · personal collection
  </footer>
</div>
