import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { themeStore } from './lib/theme';

// Initialise the theme store synchronously before mount so the
// document gets the correct `.dark` class before first paint.
themeStore();

const target = document.getElementById('app');
if (!target) {
  throw new Error('mount target #app not found in index.html');
}

const app = mount(App, { target });

export default app;
