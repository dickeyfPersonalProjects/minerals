import '@testing-library/jest-dom/vitest';
import { expect } from 'vitest';
import * as axeMatchers from 'vitest-axe/matchers';
import type { AxeMatchers } from 'vitest-axe/matchers';

// vitest-axe v0.1 only augments the legacy `Vi` namespace; vitest 3.x
// resolves matchers through `declare module 'vitest'`. Re-augment here.
declare module 'vitest' {
  /* eslint-disable @typescript-eslint/no-explicit-any, @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-object-type */
  interface Assertion<T = any> extends AxeMatchers {}
  interface AsymmetricMatchersContaining extends AxeMatchers {}
  /* eslint-enable @typescript-eslint/no-explicit-any, @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-object-type */
}

expect.extend(axeMatchers);

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
