<script lang="ts">
  // Profile is intentionally minimal post-mi-1ygd: the field-defaults
  // block moved to Settings.svelte, and name/email editing is a
  // follow-up (mi-j3kn). Until then this page is the landing surface
  // for "info about the user".
  import { authStore } from '../lib/auth';

  const user = $derived($authStore.user);
</script>

<section class="mx-auto max-w-xl py-12" data-testid="profile">
  <header class="mb-6">
    <h1 class="text-2xl font-semibold tracking-tight text-[var(--color-text)]">Profile</h1>
  </header>

  {#if user}
    <dl class="space-y-3 text-sm text-[var(--color-text)]">
      <div>
        <dt class="text-[var(--color-text-muted)]">Name</dt>
        <dd data-testid="profile-display-name">{user.display_name || '—'}</dd>
      </div>
      <div>
        <dt class="text-[var(--color-text-muted)]">Email</dt>
        <dd data-testid="profile-email">{user.email || '—'}</dd>
      </div>
    </dl>
  {/if}
</section>
