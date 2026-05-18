import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { installToastMiddleware } from './lib/api/wrapper';
import { probeAuth } from './lib/auth';
import { fetchCsrfToken } from './lib/csrf';
import { themeStore } from './lib/theme';

// Initialise the theme store synchronously before mount so the
// document gets the correct `.dark` class before first paint.
themeStore();

// Install the auto-toast + CSRF middleware on the shared API
// client (E-4 / mi-3vc4). Side-effecting; safe to call repeatedly.
installToastMiddleware();

// V2 BFF cookie flow (mi-1d5i): probe the session cookie to decide
// whether the user is logged in, then fetch a CSRF token so the
// wrapper can attach it on the first non-safe call. Anonymous boot
// keeps the auth store empty and the csrf store null — fetchCsrfToken
// short-circuits on the 401 path. Sequencing matters: the CSRF
// endpoint requires a session, so the probe must resolve first.
void probeAuth().then((user) => {
  if (user !== null) void fetchCsrfToken();
});

const target = document.getElementById('app');
if (!target) {
  throw new Error('mount target #app not found in index.html');
}

const app = mount(App, { target });

export default app;
