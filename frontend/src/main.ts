import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { installToastMiddleware } from './lib/api/wrapper';
import { themeStore } from './lib/theme';

// Initialise the theme store synchronously before mount so the
// document gets the correct `.dark` class before first paint.
themeStore();

// Install the auto-toast middleware on the shared API client
// (E-4). Side-effecting; safe to call repeatedly.
installToastMiddleware();

const target = document.getElementById('app');
if (!target) {
  throw new Error('mount target #app not found in index.html');
}

const app = mount(App, { target });

export default app;
