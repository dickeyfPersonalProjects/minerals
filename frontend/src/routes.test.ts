import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import Router from 'svelte-spa-router';
import { routes } from './routes';

// Mock handleAuthCallback so AuthCallback doesn't actually try to hit
// Keycloak when the router successfully mounts it — we only care here
// about *which* route the real <Router> matches against the hash URL.
vi.mock('./lib/oidc/auth', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('./lib/oidc/auth');
  return {
    ...actual,
    handleAuthCallback: vi.fn(() => new Promise(() => {})),
  };
});

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

describe('Router integration with routes.ts', () => {
  it('mounts AuthCallback for /#/auth/callback with no query', async () => {
    navigateHash('#/auth/callback');
    render(Router, { routes });
    await waitFor(() => {
      expect(screen.queryByTestId('auth-callback')).toBeInTheDocument();
    });
  });

  it('mounts AuthCallback for /#/auth/callback?code=X&state=Y (with query)', async () => {
    // Regression test for mi-0ag: the real Keycloak round-trip lands on
    // a hash URL that includes the authorization code + state query
    // string. The router MUST match `/auth/callback` here — falling
    // through to the catch-all leaves the user stuck on the home page,
    // anonymous, with the PKCE verifier silently abandoned.
    navigateHash(
      '#/auth/callback?state=abc&session_state=xyz&iss=https%3A%2F%2Fkeycloak.example%2Frealms%2Fminerals&code=def',
    );
    render(Router, { routes });
    await waitFor(() => {
      expect(screen.queryByTestId('auth-callback')).toBeInTheDocument();
    });
  });
});
