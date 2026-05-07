// SPA route map (CONTRACT.md §7b Routing). Hash-based via
// svelte-spa-router; the SvelteSPA `<Router>` component reads
// this object directly.
//
// Keep this map lean — one entry per top-level route, the
// component implementations live in `routes/`.
import type { RouteDefinition } from 'svelte-spa-router';
import Specimens from './routes/Specimens.svelte';
import SpecimenDetail from './routes/SpecimenDetail.svelte';

export const routes: RouteDefinition = {
  '/': Specimens,
  '/specimens': Specimens,
  '/specimens/:id': SpecimenDetail,
  '*': Specimens,
};
