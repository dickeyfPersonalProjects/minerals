<!--
  Admin/devops console shell (mi-agff foundation).

  This is the FOUNDATION pass: a gated shell + placeholder landing. The
  data-bearing surfaces (view-all-users-non-personal, published-content
  review, moderation, the Law 25 incident register, site management)
  land as follow-up sub-beads. Each is advertised here as a "planned"
  card sourced from the backend's manifest.

  Two layers of gating, by design:
   1. Client-side hint: `canAccessAdminConsole` hides the shell from
      users without an admin/devops role so they don't see a dead page.
   2. Authoritative gate: the page fetches GET /api/v1/admin/overview,
      which the backend gates with Casbin `devops:view`. A 403 there is
      what actually protects the console — the client gate is cosmetic.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { client } from '../lib/api';
  import { canAccessAdminConsole } from '../lib/auth';
  import type { components } from '../lib/api/schema';

  type Overview = components['schemas']['AdminOverviewBody'];

  let loading = $state(true);
  let denied = $state(false);
  let overview: Overview | null = $state(null);

  // Runtime registration toggle (mi-pkn2). Loaded only when the
  // site-management section reports "available" (a settings store is
  // wired). regEnabled is null until the GET resolves.
  let regEnabled: boolean | null = $state(null);
  let regBusy = $state(false);
  let regError: string | null = $state(null);

  // Only probe the backend when the client-side role hint says the
  // user might have access — an anonymous/normal user gets the inline
  // access-denied panel without a guaranteed-403 round-trip.
  onMount(async () => {
    if (!$canAccessAdminConsole) {
      loading = false;
      denied = true;
      return;
    }
    const { data, response } = await client.GET('/api/v1/admin/overview', {
      // Access-denied is rendered inline below; don't also flash a toast.
      headers: { 'x-suppress-toast': '1' },
    });
    loading = false;
    if (response.status === 401 || response.status === 403) {
      denied = true;
      return;
    }
    if (data) {
      overview = data;
      const siteMgmt = data.sections?.find((s) => s.key === 'site-management');
      if (siteMgmt?.status === 'available') {
        await loadRegistration();
      }
    }
  });

  async function loadRegistration() {
    const { data } = await client.GET('/api/v1/admin/registration', {
      headers: { 'x-suppress-toast': '1' },
    });
    if (data) {
      regEnabled = data.enabled;
    }
  }

  async function toggleRegistration() {
    if (regEnabled === null || regBusy) return;
    regBusy = true;
    regError = null;
    const target = !regEnabled;
    const { data, error } = await client.PUT('/api/v1/admin/registration', {
      body: { enabled: target },
    });
    regBusy = false;
    if (error || !data) {
      regError = 'Failed to update — the identity provider may be unreachable. Try again.';
      return;
    }
    regEnabled = data.enabled;
  }
</script>

<section class="mx-auto max-w-4xl py-12" data-testid="admin-console">
  <header class="mb-6">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Admin console</h1>
    <p class="mt-1 text-sm text-[var(--color-text-muted)]">
      Operator's window into the instance. Restricted to the admin/devops role.
    </p>
  </header>

  {#if loading}
    <p class="text-sm text-[var(--color-text-muted)]" data-testid="admin-console-loading">
      Loading…
    </p>
  {:else if denied}
    <div
      role="alert"
      data-testid="admin-console-denied"
      class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 text-sm text-[var(--color-text-muted)]"
    >
      <p class="font-medium text-[var(--color-text)]">Access denied</p>
      <p class="mt-1">This console requires the admin/devops role.</p>
    </div>
  {:else if overview}
    <p class="mb-6 text-sm text-[var(--color-text-muted)]" data-testid="admin-console-message">
      {overview.message}
    </p>
    <ul class="grid grid-cols-1 gap-4 sm:grid-cols-2" data-testid="admin-console-sections">
      {#each overview.sections as section (section.key)}
        <li
          class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
          data-testid={`admin-console-section-${section.key}`}
        >
          <div class="flex items-center justify-between gap-2">
            <h2 class="text-sm font-medium text-[var(--color-text)]">{section.title}</h2>
            <span
              class="rounded-full border border-[var(--color-border)] px-2 py-0.5 text-xs text-[var(--color-text-muted)]"
            >
              {section.status}
            </span>
          </div>
          <p class="mt-2 text-sm text-[var(--color-text-muted)]">{section.description}</p>
          {#if section.key === 'site-management' && section.status === 'available' && regEnabled !== null}
            <div
              class="mt-3 border-t border-[var(--color-border)] pt-3"
              data-testid="registration-toggle"
            >
              <div class="flex items-center justify-between gap-2">
                <span class="text-sm text-[var(--color-text)]">
                  Self-registration is
                  <strong>{regEnabled ? 'enabled' : 'disabled'}</strong>
                </span>
                <button
                  type="button"
                  class="rounded-md border border-[var(--color-border)] px-3 py-1 text-xs font-medium text-[var(--color-text)] hover:bg-[var(--color-surface-hover)] disabled:opacity-50"
                  disabled={regBusy}
                  onclick={toggleRegistration}
                  data-testid="registration-toggle-button"
                >
                  {regBusy ? 'Saving…' : regEnabled ? 'Disable' : 'Enable'}
                </button>
              </div>
              {#if regError}
                <p class="mt-2 text-xs text-[var(--color-danger)]" role="alert">{regError}</p>
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
