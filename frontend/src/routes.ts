// SPA route map (CONTRACT.md §7b Routing). Hash-based via
// svelte-spa-router; the SvelteSPA `<Router>` component reads
// this object directly.
//
// Keep this map lean — one entry per top-level route, the
// component implementations live in `routes/`.
import type { RouteDefinition } from 'svelte-spa-router';
import Specimens from './routes/Specimens.svelte';
import SpecimenNew from './routes/SpecimenNew.svelte';
import SpecimenDetail from './routes/SpecimenDetail.svelte';
import SpecimenEdit from './routes/SpecimenEdit.svelte';
import Collectors from './routes/Collectors.svelte';
import CollectorEdit from './routes/CollectorEdit.svelte';
import QRPreview from './routes/QRPreview.svelte';
import ProfileSetup from './routes/ProfileSetup.svelte';
import Profile from './routes/Profile.svelte';

// V2 BFF cookie flow (mi-3vc4): /auth/callback is no longer a SPA
// route — Keycloak redirects back to the BACKEND's /auth/callback
// handler, which sets the cookie and 302s the browser into the SPA.
export const routes: RouteDefinition = {
  '/': Specimens,
  '/profile': Profile,
  '/profile/setup': ProfileSetup,
  '/specimens': Specimens,
  '/specimens/new': SpecimenNew,
  // Static routes must precede the `:id` capture (svelte-spa-router
  // matches in declaration order; otherwise `/specimens/qr` resolves
  // as a specimen id).
  '/specimens/qr': QRPreview,
  '/specimens/:id': SpecimenDetail,
  '/specimens/:id/edit': SpecimenEdit,
  '/collectors': Collectors,
  '/collectors/:id': CollectorEdit,
  '*': Specimens,
};
