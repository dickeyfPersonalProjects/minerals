// Silent-renewal iframe bridge (mi-ct2) — MUST run before anything
// else, including path-to-hash. When the hidden iframe lands back on
// /auth/callback, this short-circuits and postMessages the params
// to the parent window instead of bootstrapping a second SPA.
import './lib/oidc/silent-callback-bridge';
// OIDC path→hash rewrite — MUST be the first import after the
// silent-renewal bridge. It runs before svelte-spa-router loads so
// the router's one-shot URL read sees the rewritten hash URL.
// See `lib/oidc/path-to-hash.ts` for the why.
import './lib/oidc/path-to-hash';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { installToastMiddleware } from './lib/api/wrapper';
import { installAuthHeaderMiddleware } from './lib/oidc/middleware';
import { themeStore } from './lib/theme';

// If the silent-renewal bridge already handled this load, skip the
// SPA bootstrap entirely — the iframe's sole job was postMessaging
// the OIDC params up and going away.
if (!window.__mineralsSilentBridgeAborted) {
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

  mount(App, { target });
}
