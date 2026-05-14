import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { installToastMiddleware } from './lib/api/wrapper';
import { installAuthHeaderMiddleware } from './lib/oidc/middleware';
import { themeStore } from './lib/theme';

// Keycloak redirects back to a path-based callback URL
// (`/auth/callback?code=...&state=...`), but svelte-spa-router is
// hash-based. Rewrite the location before mount so the SPA router
// can match the `/auth/callback` route normally.
if (window.location.pathname === '/auth/callback') {
  const search = window.location.search;
  window.history.replaceState(null, '', `/#/auth/callback${search}`);
}

// Initialise the theme store synchronously before mount so the
// document gets the correct `.dark` class before first paint.
themeStore();

// Install the auto-toast middleware on the shared API client
// (E-4). Side-effecting; safe to call repeatedly.
installToastMiddleware();

// Attach the OIDC bearer token to outgoing API calls when present.
installAuthHeaderMiddleware();

const target = document.getElementById('app');
if (!target) {
  throw new Error('mount target #app not found in index.html');
}

const app = mount(App, { target });

export default app;
