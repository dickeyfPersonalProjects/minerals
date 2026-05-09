// SPA route map (CONTRACT.md §7b Routing). Hash-based via
// svelte-spa-router; the SvelteSPA `<Router>` component reads
// this object directly.
//
// Keep this map lean — one entry per top-level route, the
// component implementations live in `routes/`.
import type { RouteDefinition } from 'svelte-spa-router';
import Specimens from './routes/Specimens.svelte';
import SpecimenDetail from './routes/SpecimenDetail.svelte';
import Collectors from './routes/Collectors.svelte';
import CollectorEdit from './routes/CollectorEdit.svelte';

export const routes: RouteDefinition = {
  '/': Specimens,
  '/specimens': Specimens,
  '/specimens/:id': SpecimenDetail,
  '/collectors': Collectors,
  '/collectors/:id': CollectorEdit,
  '*': Specimens,
};
