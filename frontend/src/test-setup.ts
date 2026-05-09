import '@testing-library/jest-dom/vitest';

// jsdom does not implement Element.animate (the Web Animations
// API), which Svelte 5's transitions (e.g. transition:fly) call.
// Stub a no-op Animation-like object so components that use
// transitions don't crash under tests.
if (typeof Element !== 'undefined' && typeof Element.prototype.animate !== 'function') {
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    writable: true,
    value: () => {
      const finished = Promise.resolve();
      return {
        cancel: () => {},
        finish: () => {},
        pause: () => {},
        play: () => {},
        reverse: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        finished,
        ready: finished,
        playState: 'finished',
        currentTime: 0,
        startTime: 0,
        playbackRate: 1,
        effect: null,
        timeline: null,
        onfinish: null,
        oncancel: null,
        onremove: null,
      };
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
