// Theme management for the SPA (CONTRACT.md §7b Theming).
//
// Resolution order on first visit:
//   1. Persisted choice in localStorage  → wins.
//   2. `prefers-color-scheme: light`     → light.
//   3. Default                           → dark.
//
// The toggle records an explicit override; once persisted it wins
// over system preference forever (until the user clears it).

import { writable, type Writable } from 'svelte/store';

export type Theme = 'light' | 'dark';

export const STORAGE_KEY = 'minerals.theme';

function readStored(storage: Storage | undefined): Theme | null {
  if (!storage) return null;
  const v = storage.getItem(STORAGE_KEY);
  return v === 'light' || v === 'dark' ? v : null;
}

function systemPrefersLight(mq: MediaQueryList | undefined): boolean {
  return Boolean(mq?.matches);
}

export interface ResolveDeps {
  storage?: Storage;
  prefersLight?: MediaQueryList;
}

/**
 * Resolve the theme that should be applied right now, given the
 * persisted choice and the user's system preference.
 *
 * Pure function — no DOM mutation. Used by both the runtime and
 * the unit tests.
 */
export function resolveTheme(deps: ResolveDeps = {}): Theme {
  const stored = readStored(deps.storage);
  if (stored) return stored;
  return systemPrefersLight(deps.prefersLight) ? 'light' : 'dark';
}

/**
 * Apply a theme to the document by toggling `class="dark"` on
 * `<html>`. Tailwind's `@custom-variant dark` keys off this class.
 */
export function applyTheme(theme: Theme, doc: Document = document): void {
  const root = doc.documentElement;
  if (theme === 'dark') {
    root.classList.add('dark');
  } else {
    root.classList.remove('dark');
  }
  root.dataset.theme = theme;
}

/**
 * Persist the user's explicit choice. Once written this wins over
 * `prefers-color-scheme` on subsequent visits.
 */
export function persistTheme(theme: Theme, storage: Storage = localStorage): void {
  storage.setItem(STORAGE_KEY, theme);
}

let storeSingleton: Writable<Theme> | null = null;

/**
 * Lazily-initialised global theme store. Importing modules read
 * the current theme from this store and subscribe to changes.
 *
 * Initialisation runs once and reads from `localStorage` +
 * `matchMedia` synchronously; the result is applied to `<html>`
 * before the first paint to avoid a flash of the wrong theme
 * (the call site in `main.ts` runs before `mount()`).
 */
export function themeStore(): Writable<Theme> {
  if (storeSingleton) return storeSingleton;

  const initial =
    typeof window === 'undefined'
      ? 'dark'
      : resolveTheme({
          storage: window.localStorage,
          prefersLight: window.matchMedia('(prefers-color-scheme: light)'),
        });

  if (typeof document !== 'undefined') {
    applyTheme(initial);
  }

  const store = writable<Theme>(initial);
  store.subscribe((next) => {
    if (typeof document !== 'undefined') applyTheme(next);
  });
  storeSingleton = store;
  return store;
}

/**
 * Toggle the current theme and persist the explicit choice. Safe
 * to call from event handlers; the `<html>` class flips
 * synchronously via the store subscription above.
 */
export function toggleTheme(): void {
  const store = themeStore();
  let current: Theme = 'dark';
  store.subscribe((v) => (current = v))();
  const next: Theme = current === 'dark' ? 'light' : 'dark';
  persistTheme(next);
  store.set(next);
}

// Test-only helper to drop the singleton between cases. Not
// exported through the package barrel; reachable as
// `import { __resetThemeStore } from './theme';`.
export function __resetThemeStore(): void {
  storeSingleton = null;
}
