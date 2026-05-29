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
  import { canAccessAdminConsole, canEditDevops } from '../lib/auth';
  import type { components } from '../lib/api/schema';

  type Overview = components['schemas']['AdminOverviewBody'];
  type ContentItem = components['schemas']['AdminContentView'];
  type AdminUser = components['schemas']['AdminUserView'];

  let loading = $state(true);
  let denied = $state(false);
  let overview: Overview | null = $state(null);

  // Runtime registration toggle (mi-pkn2). Loaded only when the
  // site-management section reports "available" (a settings store is
  // wired). regEnabled is null until the GET resolves.
  let regEnabled: boolean | null = $state(null);
  let regBusy = $state(false);
  let regError: string | null = $state(null);

  // Moderation panel (mi-jjzc). Loaded only when the moderation section
  // reports "available". It reuses the published-content review feed
  // (mi-gtkp) as the surface the operator acts on: take a specimen down
  // (force private), or remove a photo / journal entry — regardless of
  // owner. Each action is audit-logged server-side.
  let modItems: ContentItem[] = $state([]);
  let modLoading = $state(false);
  let modError: string | null = $state(null);
  // id of the row whose action is in flight, so only its button shows a
  // busy state and the rest stay clickable.
  let modBusyId: string | null = $state(null);

  // Users surface + account suspension (mi-3gxz). Loaded only when the
  // "users" section reports "available". users is null until the GET
  // resolves; suspendBusy tracks the in-flight per-user action.
  let users: AdminUser[] | null = $state(null);
  let usersError: string | null = $state(null);
  let suspendBusy: string | null = $state(null);

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
      const moderation = data.sections?.find((s) => s.key === 'moderation');
      if (moderation?.status === 'available') {
        await loadModeration();
      }
      const usersSection = data.sections?.find((s) => s.key === 'users');
      if (usersSection?.status === 'available') {
        await loadUsers();
      }
    }
  });

  async function loadModeration() {
    modLoading = true;
    modError = null;
    const { data, error } = await client.GET('/api/v1/admin/published-content', {
      headers: { 'x-suppress-toast': '1' },
    });
    modLoading = false;
    if (error || !data) {
      modError = 'Failed to load published content for review. Try again.';
      return;
    }
    modItems = data.items ?? [];
  }

  // moderate applies the action appropriate to the item's kind: a
  // specimen is forced private (takedown); a photo or journal entry is
  // removed. On success the row leaves the feed (a private specimen is
  // no longer published; removed content is gone).
  async function moderate(item: ContentItem) {
    if (modBusyId) return;
    const verb = item.kind === 'specimen' ? 'take down' : 'remove';
    if (
      !window.confirm(
        `${verb === 'take down' ? 'Take down' : 'Remove'} "${item.title}"? ` +
          (item.kind === 'specimen'
            ? 'It will be forced private and removed from public view.'
            : 'This permanently deletes it and cannot be undone.'),
      )
    ) {
      return;
    }
    modBusyId = item.id;
    modError = null;
    let ok: boolean;
    if (item.kind === 'specimen') {
      const { response } = await client.POST('/api/v1/admin/specimens/{id}/takedown', {
        params: { path: { id: item.id } },
        body: {},
      });
      ok = response.ok;
    } else if (item.kind === 'photo') {
      const { response } = await client.POST('/api/v1/admin/photos/{id}/remove', {
        params: { path: { id: item.id } },
        body: {},
      });
      ok = response.ok;
    } else {
      const { response } = await client.POST('/api/v1/admin/journal/{id}/remove', {
        params: { path: { id: item.id } },
        body: {},
      });
      ok = response.ok;
    }
    modBusyId = null;
    if (!ok) {
      modError = `Failed to ${verb} "${item.title}". Try again.`;
      return;
    }
    modItems = modItems.filter((i) => !(i.kind === item.kind && i.id === item.id));
  }

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

  async function loadUsers() {
    usersError = null;
    const { data } = await client.GET('/api/v1/admin/users', {
      headers: { 'x-suppress-toast': '1' },
    });
    if (data) {
      users = data.items ?? [];
    } else {
      usersError = 'Failed to load users.';
    }
  }

  // Suspend or unsuspend an account. The button is shown only to
  // devops-edit roles, but the backend is the real gate. On success the
  // row's status is patched in place from the response so the table
  // reflects the new state without a full reload.
  async function setSuspended(user: AdminUser, suspend: boolean) {
    if (suspendBusy) return;
    suspendBusy = user.id;
    usersError = null;
    const result = suspend
      ? await client.POST('/api/v1/admin/users/{id}/suspend', {
          params: { path: { id: user.id } },
          body: {},
        })
      : await client.POST('/api/v1/admin/users/{id}/unsuspend', {
          params: { path: { id: user.id } },
        });
    suspendBusy = null;
    if (result.error || !result.data) {
      usersError = suspend
        ? 'Failed to suspend — the identity provider may be unreachable. Try again.'
        : 'Failed to lift suspension — the identity provider may be unreachable. Try again.';
      return;
    }
    const updated = result.data;
    users = (users ?? []).map((u) => (u.id === updated.id ? { ...u, status: updated.status } : u));
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
          {#if section.key === 'users' && section.status === 'available' && users !== null}
            <div
              class="mt-3 border-t border-[var(--color-border)] pt-3"
              data-testid="admin-users-panel"
            >
              {#if users.length === 0}
                <p class="text-sm text-[var(--color-text-muted)]">No users.</p>
              {:else}
                <ul class="divide-y divide-[var(--color-border)]">
                  {#each users as user (user.id)}
                    <li
                      class="flex items-center justify-between gap-2 py-2"
                      data-testid={`admin-user-${user.id}`}
                    >
                      <div class="min-w-0">
                        <p class="truncate text-sm text-[var(--color-text)]">
                          {user.display_name ?? '(no name)'}
                        </p>
                        <p
                          class="text-xs text-[var(--color-text-muted)]"
                          data-testid="admin-user-status"
                        >
                          {user.status}
                        </p>
                      </div>
                      {#if $canEditDevops && (user.status === 'active' || user.status === 'suspended')}
                        <button
                          type="button"
                          class="shrink-0 rounded-md border border-[var(--color-border)] px-3 py-1 text-xs font-medium text-[var(--color-text)] hover:bg-[var(--color-surface-hover)] disabled:opacity-50"
                          disabled={suspendBusy !== null}
                          onclick={() => setSuspended(user, user.status === 'active')}
                          data-testid="admin-user-suspend-button"
                        >
                          {#if suspendBusy === user.id}
                            Saving…
                          {:else if user.status === 'active'}
                            Suspend
                          {:else}
                            Unsuspend
                          {/if}
                        </button>
                      {/if}
                    </li>
                  {/each}
                </ul>
              {/if}
              {#if usersError}
                <p class="mt-2 text-xs text-[var(--color-danger)]" role="alert">{usersError}</p>
              {/if}
            </div>
          {/if}
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

    {#if overview.sections?.find((s) => s.key === 'moderation')?.status === 'available'}
      <section class="mt-10" data-testid="moderation-panel">
        <h2 class="text-lg font-semibold tracking-tight text-[var(--color-text)]">Moderation</h2>
        <p class="mt-1 text-sm text-[var(--color-text-muted)]">
          Published content across all users, for usage-policy review. Take a specimen down (forces
          it private) or remove a photo or journal entry — regardless of owner. Every action is
          logged.
        </p>

        {#if modError}
          <p
            class="mt-3 text-sm text-[var(--color-danger)]"
            role="alert"
            data-testid="moderation-error"
          >
            {modError}
          </p>
        {/if}

        {#if modLoading}
          <p class="mt-4 text-sm text-[var(--color-text-muted)]" data-testid="moderation-loading">
            Loading published content…
          </p>
        {:else if modItems.length === 0}
          <p class="mt-4 text-sm text-[var(--color-text-muted)]" data-testid="moderation-empty">
            No published content to review.
          </p>
        {:else}
          <ul
            class="mt-4 divide-y divide-[var(--color-border)] rounded-lg border border-[var(--color-border)]"
            data-testid="moderation-list"
          >
            {#each modItems as item (item.kind + ':' + item.id)}
              <li
                class="flex items-center justify-between gap-4 p-3"
                data-testid={`moderation-item-${item.id}`}
              >
                <div class="min-w-0">
                  <div class="flex items-center gap-2">
                    <span
                      class="rounded-full border border-[var(--color-border)] px-2 py-0.5 text-xs text-[var(--color-text-muted)]"
                    >
                      {item.kind}
                    </span>
                    <span class="truncate text-sm font-medium text-[var(--color-text)]"
                      >{item.title}</span
                    >
                  </div>
                  <p class="mt-0.5 truncate text-xs text-[var(--color-text-muted)]">
                    {item.owner_display_name ?? 'Unknown owner'} · {item.visibility}{#if item.preview}
                      · {item.preview}{/if}
                  </p>
                </div>
                <button
                  type="button"
                  class="shrink-0 rounded-md border border-[var(--color-danger)] px-3 py-1 text-xs font-medium text-[var(--color-danger)] hover:bg-[var(--color-danger)]/10 disabled:opacity-50"
                  disabled={modBusyId !== null}
                  onclick={() => moderate(item)}
                  data-testid={`moderation-action-${item.id}`}
                >
                  {#if modBusyId === item.id}
                    Working…
                  {:else if item.kind === 'specimen'}
                    Take down
                  {:else}
                    Remove
                  {/if}
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/if}
  {/if}
</section>
