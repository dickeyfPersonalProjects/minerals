<script lang="ts">
  // C-5 builds the real detail page — this route exists so list
  // cards have a real anchor to link to in C-4. Anything beyond
  // a stub is out of scope for mi-9i9.
  import { link } from 'svelte-spa-router';

  interface Props {
    params?: { id?: string };
  }
  const { params }: Props = $props();
</script>

<section class="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6">
  <h1 class="text-xl font-semibold text-[var(--color-text)]">Specimen detail</h1>
  <p class="mt-2 text-sm text-[var(--color-text-muted)]">
    Detail page placeholder. The real page lands in C-5.
  </p>
  {#if params?.id}
    <p class="mt-3 font-mono text-xs text-[var(--color-text-muted)]">id: {params.id}</p>
  {/if}
  <a
    href="/specimens"
    use:link
    class="mt-4 inline-block text-sm text-[var(--color-accent)] hover:underline"
    >← back to specimens</a
  >
</section>
