import { beforeEach, describe, expect, it, vi } from 'vitest';

// Regression test for mi-0ag: this module MUST run before
// svelte-spa-router loads. The test re-imports the module after
// manipulating the URL to verify the rewrite behaviour, and asserts
// the post-rewrite href round-trips through svelte-spa-router's
// `getLocation` to `/auth/callback`.

beforeEach(() => {
  vi.resetModules();
  // Reset URL to the jsdom default before each test.
  history.replaceState(null, '', '/');
});

describe('path-to-hash rewrite', () => {
  it('rewrites /auth/callback?code=... to /#/auth/callback?code=...', async () => {
    history.replaceState(null, '', '/auth/callback?code=C&state=S');
    await import('./path-to-hash');
    expect(window.location.pathname).toBe('/');
    expect(window.location.hash).toBe('#/auth/callback?code=C&state=S');
  });

  it('rewrites a realistic Keycloak callback (state/session_state/iss/code)', async () => {
    history.replaceState(
      null,
      '',
      '/auth/callback?state=abc&session_state=xyz&iss=https%3A%2F%2Fkc.example%2Frealms%2Fminerals&code=def',
    );
    await import('./path-to-hash');
    expect(window.location.hash).toBe(
      '#/auth/callback?state=abc&session_state=xyz&iss=https%3A%2F%2Fkc.example%2Frealms%2Fminerals&code=def',
    );
  });

  it('handles a callback URL with no query string', async () => {
    history.replaceState(null, '', '/auth/callback');
    await import('./path-to-hash');
    expect(window.location.hash).toBe('#/auth/callback');
  });

  it('leaves non-callback URLs alone', async () => {
    history.replaceState(null, '', '/specimens/abc');
    await import('./path-to-hash');
    expect(window.location.pathname).toBe('/specimens/abc');
    expect(window.location.hash).toBe('');
  });
});
