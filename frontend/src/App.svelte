<script lang="ts">
  type Status = { kind: 'pending' } | { kind: 'up' } | { kind: 'down'; reason: string };

  let status: Status = $state({ kind: 'pending' });

  $effect(() => {
    const controller = new AbortController();

    fetch('/healthz', { signal: controller.signal })
      .then((res) => {
        if (res.ok) {
          status = { kind: 'up' };
        } else {
          status = { kind: 'down', reason: `HTTP ${res.status}` };
        }
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        const reason = err instanceof Error ? err.message : String(err);
        status = { kind: 'down', reason };
      });

    return () => controller.abort();
  });
</script>

<main>
  <h1>Minerals</h1>
  {#if status.kind === 'pending'}
    <p data-testid="health-status">Checking backend…</p>
  {:else if status.kind === 'up'}
    <p data-testid="health-status">Backend is up</p>
  {:else}
    <p data-testid="health-status">Backend is down: {status.reason}</p>
  {/if}
</main>

<style>
  main {
    font-family: system-ui, sans-serif;
    max-width: 40rem;
    margin: 2rem auto;
    padding: 0 1rem;
  }
</style>
