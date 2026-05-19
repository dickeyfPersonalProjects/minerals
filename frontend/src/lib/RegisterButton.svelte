<script lang="ts">
  // Self-registration entry point (mi-eb3b): a plain anchor to the
  // backend's /auth/register endpoint. The backend generates OAuth
  // state and 302s the browser to Keycloak's registration form;
  // after the user submits it (and any realm-required email
  // verification completes) the same /auth/callback handler that
  // serves login creates the session.
  //
  // `return_to` mirrors LoginButton so a user who hit Register from
  // a deep link lands back there after signup.
  //
  // Visibility: rendered unconditionally — the backend is the
  // authoritative gate (REGISTRATION_ENABLED=false → 404 on click).
  // Following the same "don't gate the link on runtime-config"
  // pattern Layout uses for LoginButton.
  let href = $derived.by(() => {
    if (typeof window === 'undefined') return '/auth/register';
    const current = window.location.hash || '#/';
    return `/auth/register?return_to=${encodeURIComponent(current)}`;
  });
</script>

<a
  {href}
  data-testid="register-link"
  class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
>
  Register
</a>
