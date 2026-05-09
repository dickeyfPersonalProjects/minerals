<script lang="ts">
  import Router from 'svelte-spa-router';
  import Layout from './lib/Layout.svelte';
  import Toaster from './lib/Toaster.svelte';
  import { routes } from './routes';
</script>

<Layout>
  <Router {routes} />
</Layout>
<Toaster />
