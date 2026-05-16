// OIDC path→hash rewrite — MUST be the first import. It runs before
// svelte-spa-router loads so the router's one-shot URL read sees the
// rewritten hash URL. See `lib/oidc/path-to-hash.ts` for the why.
import './lib/oidc/path-to-hash';
import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { installToastMiddleware } from './lib/api/wrapper';
import { installAuthHeaderMiddleware } from './lib/oidc/middleware';
import { themeStore } from './lib/theme';

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
