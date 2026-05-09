import '@testing-library/jest-dom/vitest';

// jsdom does not implement Element.animate, which Svelte 5's
// transition runtime calls under the hood. Stub it so components
// using `transition:fly` / `transition:slide` (e.g. Toaster) do
// not crash in tests.
if (typeof Element !== 'undefined' && typeof Element.prototype.animate !== 'function') {
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    writable: true,
    value: function animate(): Animation {
      return {
        cancel: () => {},
        finish: () => {},
        play: () => {},
        pause: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        finished: Promise.resolve(),
      } as unknown as Animation;
    },
  });
}

// jsdom does not implement matchMedia. Stub it so theme bootstrap
// (and any future media-query code) can run under tests.
if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string): MediaQueryList =>
      ({
        matches: false,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }) as unknown as MediaQueryList,
  });
}
