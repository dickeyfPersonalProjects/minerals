<script lang="ts">
  // V2 BFF login (mi-3vc4): a plain anchor to the backend's
  // /auth/login endpoint. The backend generates OAuth state,
  // 302s to Keycloak, and on callback sets the HttpOnly session
  // cookie. No JS, no PKCE, no in-memory token.
  //
  // `return_to` carries the SPA hash route the user was viewing
  // so the backend can bounce them back after sign-in completes.
  let href = $derived.by(() => {
    if (typeof window === 'undefined') return '/auth/login';
    const current = window.location.hash || '#/';
    return `/auth/login?return_to=${encodeURIComponent(current)}`;
  });
</script>

<a
  {href}
  data-testid="login-button"
  class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
>
  Log in
</a>
