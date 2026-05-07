import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  STORAGE_KEY,
  __resetThemeStore,
  applyTheme,
  persistTheme,
  resolveTheme,
  themeStore,
  toggleTheme,
} from './theme';

function fakeStorage(initial: Record<string, string> = {}): Storage {
  const data = new Map(Object.entries(initial));
  return {
    get length() {
      return data.size;
    },
    clear: () => data.clear(),
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => {
      data.set(k, v);
    },
    removeItem: (k) => {
      data.delete(k);
    },
    key: (i) => Array.from(data.keys())[i] ?? null,
  };
}

function fakePrefersLight(matches: boolean): MediaQueryList {
  // Only the `matches` field is used by resolveTheme.
  return { matches } as unknown as MediaQueryList;
}

describe('resolveTheme', () => {
  it('returns dark by default when nothing is stored and no system preference', () => {
    expect(resolveTheme()).toBe('dark');
  });

  it('returns dark when system prefers neither (prefers-light is false)', () => {
    expect(resolveTheme({ storage: fakeStorage(), prefersLight: fakePrefersLight(false) })).toBe(
      'dark',
    );
  });

  it('returns light when system prefers light and nothing is stored', () => {
    expect(resolveTheme({ storage: fakeStorage(), prefersLight: fakePrefersLight(true) })).toBe(
      'light',
    );
  });

  it('persisted dark wins over system prefers-light', () => {
    expect(
      resolveTheme({
        storage: fakeStorage({ [STORAGE_KEY]: 'dark' }),
        prefersLight: fakePrefersLight(true),
      }),
    ).toBe('dark');
  });

  it('persisted light wins over default dark', () => {
    expect(
      resolveTheme({
        storage: fakeStorage({ [STORAGE_KEY]: 'light' }),
        prefersLight: fakePrefersLight(false),
      }),
    ).toBe('light');
  });

  it('ignores garbage values in storage and falls through to system / default', () => {
    expect(
      resolveTheme({
        storage: fakeStorage({ [STORAGE_KEY]: 'sepia' }),
        prefersLight: fakePrefersLight(true),
      }),
    ).toBe('light');
  });
});

describe('applyTheme', () => {
  beforeEach(() => {
    document.documentElement.className = '';
    delete document.documentElement.dataset.theme;
  });

  it('adds .dark and data-theme=dark for dark', () => {
    applyTheme('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(document.documentElement.dataset.theme).toBe('dark');
  });

  it('removes .dark and sets data-theme=light for light', () => {
    document.documentElement.classList.add('dark');
    applyTheme('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(document.documentElement.dataset.theme).toBe('light');
  });
});

describe('persistTheme', () => {
  it('writes to the configured storage under the stable key', () => {
    const s = fakeStorage();
    persistTheme('light', s);
    expect(s.getItem(STORAGE_KEY)).toBe('light');
  });
});

describe('themeStore + toggleTheme integration', () => {
  beforeEach(() => {
    __resetThemeStore();
    window.localStorage.clear();
    document.documentElement.className = '';
  });
  afterEach(() => {
    __resetThemeStore();
    window.localStorage.clear();
  });

  it('initialises to dark by default and applies the .dark class', () => {
    const store = themeStore();
    let value = '' as string;
    store.subscribe((v) => (value = v))();
    expect(value).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('toggle flips to light, removes .dark, and persists', () => {
    themeStore();
    toggleTheme();

    const store = themeStore();
    let value = '' as string;
    store.subscribe((v) => (value = v))();

    expect(value).toBe('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('light');
  });

  it('toggle twice returns to dark', () => {
    themeStore();
    toggleTheme();
    toggleTheme();
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });
});
