import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import Router from 'svelte-spa-router';
import { routes } from './routes';

// Specimens fetches a list on mount. Stub the API so the home/catch-all
// route can mount without a real backend.
vi.mock('./lib/api/client', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('./lib/api/client');
  return {
    ...actual,
    apiClient: {
      GET: vi.fn(async () => ({ data: { items: [] }, error: undefined })),
      POST: vi.fn(async () => ({ data: undefined, error: undefined })),
      PUT: vi.fn(async () => ({ data: undefined, error: undefined })),
      DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
    },
  };
});

function navigateHash(hash: string): void {
  window.location.hash = hash;
  window.dispatchEvent(new Event('hashchange'));
}

beforeEach(() => {
  window.location.hash = '';
});

afterEach(() => {
  cleanup();
  window.location.hash = '';
});

describe('Router integration with routes.ts (V2 BFF cookie flow, mi-3vc4)', () => {
  // /#/auth/callback is no longer a SPA route — Keycloak now
  // redirects back to the BACKEND's /auth/callback handler, which
  // sets the cookie and 302s into the SPA. Any /#/auth/* hash that
  // somehow reaches the router falls through to the catch-all
  // (Specimens) so the browser at least lands somewhere useful.
  it('does not register /auth/callback as a SPA route', () => {
    expect((routes as Record<string, unknown>)['/auth/callback']).toBeUndefined();
  });

  it('falls through to the catch-all when /#/auth/callback is somehow navigated', async () => {
    navigateHash('#/auth/callback');
    render(Router, { routes });
    // Catch-all is Specimens — render-mount succeeds without
    // throwing about an unknown route.
    await waitFor(() => {
      expect(screen.queryByTestId('auth-callback')).not.toBeInTheDocument();
    });
  });
});
