// OIDC path→hash rewrite — MUST be the first import. It runs before
// svelte-spa-router loads so the router's one-shot URL read sees the
// rewritten hash URL. See `lib/oidc/path-to-hash.ts` for the why.
import './lib/oidc/path-to-hash';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { installToastMiddleware } from './lib/api/wrapper';
import { installAuthHeaderMiddleware } from './lib/oidc/middleware';
import { attemptSilentRenewal } from './lib/oidc/auth';
import { themeStore } from './lib/theme';

// Initialise the theme store synchronously before mount so the
// document gets the correct `.dark` class before first paint.
themeStore();

// Install the auto-toast middleware on the shared API client
// (E-4). Side-effecting; safe to call repeatedly.
installToastMiddleware();

// Attach the OIDC bearer token to outgoing API calls when present.
installAuthHeaderMiddleware();

// Silent renewal on boot (mi-wmyc). If the browser has held a session
// before and we're not currently completing an interactive callback,
// redirect the whole tab to Keycloak with `prompt=none` so the SSO
// cookie can mint a new token without user interaction. This is a
// no-op for first-time/anonymous visitors and for the /auth/callback
// route itself. We do NOT await — the redirect navigates away on
// success, and the no-op cases are fast enough that the mount below
// proceeds without a visible delay.
void attemptSilentRenewal();

const target = document.getElementById('app');
if (!target) {
  throw new Error('mount target #app not found in index.html');
}

const app = mount(App, { target });

export default app;
