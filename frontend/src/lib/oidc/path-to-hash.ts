// OIDC redirect URL rewrite (mi-0ag).
//
// Keycloak (and the OIDC discovery URLs we register) sends users back
// to a path-based callback: `/auth/callback?code=...&state=...`. The
// SPA uses svelte-spa-router in hash mode, so we rewrite the URL to
// `/#/auth/callback?...` before the router initialises.
//
// CRITICAL: this MUST run BEFORE svelte-spa-router is loaded. The
// router reads `window.location.href` exactly once in its module-level
// constructor and only updates on `hashchange` events — `replaceState`
// is silent. If the router loads first it caches `location = '/'`,
// matches the catch-all route, and the post-rewrite hash URL is never
// seen. Import this module as the FIRST import in `main.ts`.
if (window.location.pathname === '/auth/callback') {
  const search = window.location.search;
  window.history.replaceState(null, '', `/#/auth/callback${search}`);
}

export {};
