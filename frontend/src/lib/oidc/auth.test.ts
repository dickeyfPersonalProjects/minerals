import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import {
  __resetAuthStore,
  authStore,
  beginLogin,
  clearAuth,
  getAccessToken,
  handleAuthCallback,
  setAccessToken,
} from './auth';
import type { OidcConfig } from './config';

const config: OidcConfig = {
  issuerUrl: 'https://auth.example.com/realms/minerals',
  clientId: 'minerals-frontend',
  redirectUri: 'https://www.example.com/auth/callback',
};

class MemoryStorage implements Storage {
  private map = new Map<string, string>();
  get length(): number {
    return this.map.size;
  }
  clear(): void {
    this.map.clear();
  }
  getItem(key: string): string | null {
    return this.map.has(key) ? (this.map.get(key) as string) : null;
  }
  key(i: number): string | null {
    return Array.from(this.map.keys())[i] ?? null;
  }
  removeItem(key: string): void {
    this.map.delete(key);
  }
  setItem(key: string, value: string): void {
    this.map.set(key, value);
  }
}

beforeEach(() => {
  __resetAuthStore();
});

afterEach(() => {
  __resetAuthStore();
  vi.restoreAllMocks();
});

describe('token store', () => {
  it('starts unauthenticated', () => {
    expect(getAccessToken()).toBeNull();
    expect(get(authStore).accessToken).toBeNull();
  });

  it('setAccessToken populates the store with an expiry', () => {
    setAccessToken('tok-1', 300, () => 1_000);
    expect(getAccessToken(() => 1_000)).toBe('tok-1');
    expect(get(authStore).expiresAt).toBe(1_000 + 300_000);
  });

  it('returns null after the token has expired and resets the store', () => {
    setAccessToken('tok-1', 60, () => 0);
    expect(getAccessToken(() => 60_000)).toBeNull();
    expect(get(authStore).accessToken).toBeNull();
  });

  it('clearAuth wipes the token', () => {
    setAccessToken('tok-1', 60, () => 0);
    clearAuth();
    expect(getAccessToken(() => 0)).toBeNull();
  });
});

describe('beginLogin', () => {
  it('stashes PKCE state and redirects to the Keycloak auth endpoint', async () => {
    const storage = new MemoryStorage();
    const assign = vi.fn();
    await beginLogin('#/specimens/abc', {
      config,
      sessionStorage: storage,
      locationAssign: assign,
    });
    expect(assign).toHaveBeenCalledOnce();
    const assignCall = assign.mock.calls[0];
    if (!assignCall) throw new Error('expected location.assign to be called');
    const url = new URL(assignCall[0] as string);
    expect(url.origin + url.pathname).toBe(
      'https://auth.example.com/realms/minerals/protocol/openid-connect/auth',
    );
    expect(url.searchParams.get('response_type')).toBe('code');
    expect(url.searchParams.get('client_id')).toBe('minerals-frontend');
    expect(url.searchParams.get('redirect_uri')).toBe('https://www.example.com/auth/callback');
    expect(url.searchParams.get('scope')).toBe('openid email profile');
    expect(url.searchParams.get('code_challenge_method')).toBe('S256');
    expect(url.searchParams.get('code_challenge')?.length).toBeGreaterThan(0);
    expect(url.searchParams.get('state')?.length).toBeGreaterThan(0);
    expect(storage.getItem('minerals.oidc.code_verifier')?.length).toBeGreaterThan(0);
    expect(storage.getItem('minerals.oidc.state')).toBe(url.searchParams.get('state'));
    expect(storage.getItem('minerals.oidc.return_to')).toBe('#/specimens/abc');
  });

  it('throws when OIDC is not configured', async () => {
    await expect(
      beginLogin('#/', {
        config: null,
        sessionStorage: new MemoryStorage(),
        locationAssign: vi.fn(),
      }),
    ).rejects.toThrow(/not configured/);
  });
});

describe('handleAuthCallback', () => {
  function seed(storage: MemoryStorage, state = 's-1', verifier = 'v-1', returnTo = '#/done') {
    storage.setItem('minerals.oidc.state', state);
    storage.setItem('minerals.oidc.code_verifier', verifier);
    storage.setItem('minerals.oidc.return_to', returnTo);
  }

  function tokenJson(status: number, body: unknown): Response {
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json' },
    });
  }

  it('exchanges the code for a token and returns the saved returnTo', async () => {
    const storage = new MemoryStorage();
    seed(storage);
    const fetchStub = vi.fn(async () =>
      tokenJson(200, { access_token: 'at-1', expires_in: 600, token_type: 'Bearer' }),
    );
    const query = new URLSearchParams({ code: 'CODE-1', state: 's-1' });

    const result = await handleAuthCallback(query, {
      config,
      sessionStorage: storage,
      fetch: fetchStub as unknown as typeof fetch,
      now: () => 1_000,
    });

    expect(result.returnTo).toBe('#/done');
    expect(getAccessToken(() => 1_000)).toBe('at-1');
    expect(get(authStore).expiresAt).toBe(1_000 + 600_000);

    const call = fetchStub.mock.calls[0];
    if (!call) throw new Error('expected fetch to be called');
    const [url, init] = call as unknown as [string, RequestInit];
    expect(url).toBe('https://auth.example.com/realms/minerals/protocol/openid-connect/token');
    expect(init.method).toBe('POST');
    const body = new URLSearchParams(init.body as string);
    expect(body.get('grant_type')).toBe('authorization_code');
    expect(body.get('code')).toBe('CODE-1');
    expect(body.get('redirect_uri')).toBe(config.redirectUri);
    expect(body.get('client_id')).toBe(config.clientId);
    expect(body.get('code_verifier')).toBe('v-1');
  });

  it('clears the single-use verifier and state on success', async () => {
    const storage = new MemoryStorage();
    seed(storage);
    const fetchStub = vi.fn(async () => tokenJson(200, { access_token: 'at-1', expires_in: 60 }));
    await handleAuthCallback(new URLSearchParams({ code: 'C', state: 's-1' }), {
      config,
      sessionStorage: storage,
      fetch: fetchStub as unknown as typeof fetch,
    });
    expect(storage.getItem('minerals.oidc.code_verifier')).toBeNull();
    expect(storage.getItem('minerals.oidc.state')).toBeNull();
    expect(storage.getItem('minerals.oidc.return_to')).toBeNull();
  });

  it('rejects when the state parameter does not match the stored value', async () => {
    const storage = new MemoryStorage();
    seed(storage, 's-expected');
    await expect(
      handleAuthCallback(new URLSearchParams({ code: 'C', state: 's-attacker' }), {
        config,
        sessionStorage: storage,
        fetch: vi.fn() as unknown as typeof fetch,
      }),
    ).rejects.toThrow(/state/i);
    expect(storage.getItem('minerals.oidc.code_verifier')).toBeNull();
  });

  it('surfaces an OAuth error parameter from the redirect', async () => {
    const storage = new MemoryStorage();
    seed(storage);
    await expect(
      handleAuthCallback(
        new URLSearchParams({ error: 'access_denied', error_description: 'user said no' }),
        {
          config,
          sessionStorage: storage,
          fetch: vi.fn() as unknown as typeof fetch,
        },
      ),
    ).rejects.toThrow('user said no');
  });

  it('rejects when the token endpoint returns an error envelope', async () => {
    const storage = new MemoryStorage();
    seed(storage);
    const fetchStub = vi.fn(async () =>
      tokenJson(400, { error: 'invalid_grant', error_description: 'bad code' }),
    );
    await expect(
      handleAuthCallback(new URLSearchParams({ code: 'C', state: 's-1' }), {
        config,
        sessionStorage: storage,
        fetch: fetchStub as unknown as typeof fetch,
      }),
    ).rejects.toThrow('bad code');
    expect(getAccessToken()).toBeNull();
  });

  it('rejects when the response is missing access_token', async () => {
    const storage = new MemoryStorage();
    seed(storage);
    const fetchStub = vi.fn(async () => tokenJson(200, { token_type: 'Bearer' }));
    await expect(
      handleAuthCallback(new URLSearchParams({ code: 'C', state: 's-1' }), {
        config,
        sessionStorage: storage,
        fetch: fetchStub as unknown as typeof fetch,
      }),
    ).rejects.toThrow(/malformed/i);
  });

  it('rejects when the verifier is missing (storage cleared)', async () => {
    const storage = new MemoryStorage();
    storage.setItem('minerals.oidc.state', 's-1'); // state but no verifier
    await expect(
      handleAuthCallback(new URLSearchParams({ code: 'C', state: 's-1' }), {
        config,
        sessionStorage: storage,
        fetch: vi.fn() as unknown as typeof fetch,
      }),
    ).rejects.toThrow(/verifier/i);
  });
});
