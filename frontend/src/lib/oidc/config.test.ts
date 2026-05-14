import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));
vi.mock('../api', () => ({ client: { GET: mockGet } }));

import { __resetOidcConfig, getOidcConfig, loadOidcConfig, oidcConfigStore } from './config';

function ok(body: unknown) {
  return {
    data: body,
    error: undefined,
    response: new Response(JSON.stringify(body), { status: 200 }),
  };
}

function envelope(status: number, code: string, message: string) {
  return {
    data: undefined,
    error: { error: { code, message } },
    response: new Response(null, { status }),
  };
}

beforeEach(() => {
  __resetOidcConfig();
  mockGet.mockReset();
});

afterEach(() => {
  __resetOidcConfig();
});

describe('loadOidcConfig', () => {
  it('returns the OIDC sub-config when the backend has it configured', async () => {
    mockGet.mockResolvedValue(
      ok({
        oidc: {
          issuer_url: 'https://auth.example.com/realms/minerals',
          client_id: 'minerals-frontend',
          redirect_uri: 'https://www.example.com/auth/callback',
        },
      }),
    );
    const cfg = await loadOidcConfig();
    expect(cfg).toEqual({
      issuerUrl: 'https://auth.example.com/realms/minerals',
      clientId: 'minerals-frontend',
      redirectUri: 'https://www.example.com/auth/callback',
    });
    expect(get(oidcConfigStore)).toEqual({ kind: 'ready', config: cfg });
  });

  it('returns null when the backend omits the oidc block', async () => {
    mockGet.mockResolvedValue(ok({}));
    expect(await loadOidcConfig()).toBeNull();
    expect(get(oidcConfigStore)).toEqual({ kind: 'ready', config: null });
  });

  it('strips a trailing slash from the issuer URL', async () => {
    mockGet.mockResolvedValue(
      ok({
        oidc: {
          issuer_url: 'https://auth.example.com/realms/minerals/',
          client_id: 'minerals-frontend',
          redirect_uri: 'https://www.example.com/auth/callback',
        },
      }),
    );
    const cfg = await loadOidcConfig();
    expect(cfg?.issuerUrl).toBe('https://auth.example.com/realms/minerals');
  });

  it('caches the result so a second call does not re-fetch', async () => {
    mockGet.mockResolvedValue(ok({ oidc: null }));
    await loadOidcConfig();
    await loadOidcConfig();
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it('deduplicates concurrent calls into one fetch', async () => {
    mockGet.mockResolvedValue(ok({ oidc: null }));
    const [a, b] = await Promise.all([loadOidcConfig(), loadOidcConfig()]);
    expect(a).toBeNull();
    expect(b).toBeNull();
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it('returns null and parks the store in error state on a non-2xx response', async () => {
    mockGet.mockResolvedValue(envelope(500, 'internal', 'something blew up'));
    expect(await loadOidcConfig()).toBeNull();
    const state = get(oidcConfigStore);
    expect(state.kind).toBe('error');
    if (state.kind === 'error') expect(state.message).toBe('something blew up');
  });

  it('returns null and surfaces a network error on the error path', async () => {
    mockGet.mockRejectedValue(new Error('network down'));
    expect(await loadOidcConfig()).toBeNull();
    const state = get(oidcConfigStore);
    expect(state.kind).toBe('error');
    if (state.kind === 'error') expect(state.message).toBe('network down');
  });
});

describe('getOidcConfig', () => {
  it('returns null before loadOidcConfig has resolved', () => {
    expect(getOidcConfig()).toBeNull();
  });

  it('returns the cached config after loadOidcConfig resolves', async () => {
    mockGet.mockResolvedValue(
      ok({
        oidc: {
          issuer_url: 'https://auth.example.com/realms/minerals',
          client_id: 'minerals-frontend',
          redirect_uri: 'https://www.example.com/auth/callback',
        },
      }),
    );
    await loadOidcConfig();
    expect(getOidcConfig()?.clientId).toBe('minerals-frontend');
  });
});
