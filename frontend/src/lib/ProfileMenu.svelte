<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { authStore } from './auth';
  import { csrfStore } from './csrf';
  import { toastError } from './toasts';

  // V2 BFF cookie flow (mi-3vc4): user identity comes from the
  // /api/v1/profile probe (auth.ts); the JWT is no longer in the
  // browser. Sign-out is a fetch POST to /auth/logout with the
  // CSRF token in the X-CSRF-Token header — the backend invalidates
  // the session row, clears the cookie, and (when configured) 302s
  // to Keycloak's end-session endpoint. fetch can't navigate the
  // tab cross-origin, so we POST and then hard-navigate the tab to
  // `/` for a clean reboot. Keycloak's own SSO cookie may persist
  // (mild) — the app cookie is gone, which is what the user asked
  // for; the next click on Log in will silently mint a new session.

  const auth = $derived($authStore);
  const csrf = $derived($csrfStore);
  const user = $derived(auth.user);
  const label = $derived(user?.display_name?.trim() || user?.email || 'Account');
  const initials = $derived(initialsFor());

  let open = $state(false);
  let busy = $state(false);
  let container: HTMLDivElement;

  function initialsFor(): string | null {
    const name = user?.display_name?.trim();
    if (name) {
      const parts = name.split(/\s+/).filter(Boolean);
      const first = parts[0];
      const last = parts[parts.length - 1];
      if (first && last && parts.length >= 2) {
        return (first.charAt(0) + last.charAt(0)).toUpperCase();
      }
      if (first) return first.slice(0, 2).toUpperCase();
    }
    const email = user?.email?.trim();
    if (email) return email.slice(0, 2).toUpperCase();
    return null;
  }

  function toggle(): void {
    open = !open;
  }

  function goProfile(): void {
    open = false;
    void push('/profile');
  }

  function goSettings(): void {
    open = false;
    void push('/settings');
  }

  async function signOut(): Promise<void> {
    if (busy) return;
    busy = true;
    open = false;
    try {
      await fetch('/auth/logout', {
        method: 'POST',
        credentials: 'include',
        redirect: 'manual',
        headers: csrf ? { 'X-CSRF-Token': csrf } : undefined,
      });
      // Hard-navigate even if the POST failed: the user clicked
      // Sign out and clearing local state is the right UX. The
      // session cookie is HttpOnly so we can't delete it from
      // here — only the backend can — but `/` reboots the SPA and
      // the next probe will surface the current truth.
      window.location.assign('/');
    } catch (err: unknown) {
      busy = false;
      toastError(err instanceof Error ? err.message : String(err));
    }
  }

  // Close the dropdown on an outside click or Escape so it behaves
  // like a normal menu without trapping focus.
  $effect(() => {
    if (!open) return;
    function onPointerDown(event: MouseEvent): void {
      if (container && !container.contains(event.target as Node)) open = false;
    }
    function onKeydown(event: KeyboardEvent): void {
      if (event.key === 'Escape') open = false;
    }
    window.addEventListener('mousedown', onPointerDown);
    window.addEventListener('keydown', onKeydown);
    return () => {
      window.removeEventListener('mousedown', onPointerDown);
      window.removeEventListener('keydown', onKeydown);
    };
  });
</script>

<div class="relative" bind:this={container}>
  <button
    type="button"
    onclick={toggle}
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label={label}
    title={label}
    data-testid="profile-menu-button"
    class="flex h-8 w-8 items-center justify-center rounded-full border border-[var(--color-border)] bg-[var(--color-surface)] text-xs font-semibold text-[var(--color-text)] hover:bg-[var(--color-surface-2)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
  >
    {#if initials}
      <span aria-hidden="true">{initials}</span>
    {:else}
      <!-- generic person icon when no name/email present -->
      <svg
        xmlns="http://www.w3.org/2000/svg"
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2" />
        <circle cx="12" cy="7" r="4" />
      </svg>
    {/if}
  </button>

  {#if open}
    <div
      role="menu"
      data-testid="profile-menu"
      class="absolute right-0 z-10 mt-2 w-44 overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] py-1 shadow-lg"
    >
      <button
        type="button"
        role="menuitem"
        onclick={goProfile}
        data-testid="profile-menu-profile"
        class="block w-full px-3 py-1.5 text-left text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
      >
        Profile
      </button>
      <button
        type="button"
        role="menuitem"
        onclick={goSettings}
        data-testid="profile-menu-settings"
        class="block w-full px-3 py-1.5 text-left text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)]"
      >
        Settings
      </button>
      <button
        type="button"
        role="menuitem"
        onclick={signOut}
        disabled={busy}
        data-testid="profile-menu-signout"
        class="block w-full px-3 py-1.5 text-left text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-60"
      >
        Sign out
      </button>
    </div>
  {/if}
</div>
