import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import {
  __resetAuthStore,
  attemptSilentRenewal,
  authStore,
  beginLogin,
  beginLogout,
  clearAuth,
  decodeTokenClaims,
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
  // Tests that don't pass an explicit localStorage land on jsdom's
  // window.localStorage, which persists across cases — wipe it so
  // residual had_session markers don't leak between tests.
  window.localStorage.clear();
});

afterEach(() => {
  __resetAuthStore();
  window.localStorage.clear();
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

function makeJwt(payload: Record<string, unknown>): string {
  const b64 = (o: unknown) => Buffer.from(JSON.stringify(o)).toString('base64url');
  return `${b64({ alg: 'RS256', typ: 'JWT' })}.${b64(payload)}.signature`;
}

describe('decodeTokenClaims', () => {
  it('returns null for a missing token', () => {
    expect(decodeTokenClaims(null)).toBeNull();
  });

  it('returns null for a structurally invalid token', () => {
    expect(decodeTokenClaims('not-a-jwt')).toBeNull();
    expect(decodeTokenClaims('only.two')).toBeNull();
  });

  it('returns null when the payload is not valid base64url JSON', () => {
    expect(decodeTokenClaims('aaa.@@@.bbb')).toBeNull();
  });

  it('extracts the display claims from a well-formed token', () => {
    const token = makeJwt({
      name: 'Ada Lovelace',
      preferred_username: 'ada',
      email: 'ada@example.com',
      sub: 'abc-123',
    });
    expect(decodeTokenClaims(token)).toEqual({
      name: 'Ada Lovelace',
      preferredUsername: 'ada',
      email: 'ada@example.com',
    });
  });

  it('coerces missing or non-string claims to null', () => {
    const token = makeJwt({ preferred_username: 'ada', name: 42 });
    expect(decodeTokenClaims(token)).toEqual({
      name: null,
      preferredUsername: 'ada',
      email: null,
    });
  });

  it('decodes multi-byte UTF-8 claims', () => {
    const token = makeJwt({ name: 'Renée Müller' });
    expect(decodeTokenClaims(token)?.name).toBe('Renée Müller');
  });
});

describe('beginLogout', () => {
  it('clears the token and redirects to the Keycloak end-session endpoint', async () => {
    setAccessToken('tok-1', 600, () => 0);
    const assign = vi.fn();
    await beginLogout({ config, locationAssign: assign, appUrl: 'https://www.example.com/' });

    expect(getAccessToken(() => 0)).toBeNull();
    expect(assign).toHaveBeenCalledOnce();
    const url = new URL(assign.mock.calls[0]![0] as string);
    expect(url.origin + url.pathname).toBe(
      'https://auth.example.com/realms/minerals/protocol/openid-connect/logout',
    );
    expect(url.searchParams.get('client_id')).toBe('minerals-frontend');
    expect(url.searchParams.get('post_logout_redirect_uri')).toBe('https://www.example.com/');
  });

  it('still clears local state and navigates home when OIDC is unconfigured', async () => {
    setAccessToken('tok-1', 600, () => 0);
    const assign = vi.fn();
    await beginLogout({ config: null, locationAssign: assign, appUrl: 'https://www.example.com/' });

    expect(getAccessToken(() => 0)).toBeNull();
    expect(assign).toHaveBeenCalledWith('https://www.example.com/');
  });

  it('clears the had-session marker so the next boot does not silently renew', async () => {
    const local = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    await beginLogout({
      config,
      locationAssign: vi.fn(),
      appUrl: 'https://www.example.com/',
      localStorage: local,
    });
    expect(local.getItem('minerals.oidc.had_session')).toBeNull();
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

  it('sets the had-session marker on a successful interactive exchange', async () => {
    const storage = new MemoryStorage();
    const local = new MemoryStorage();
    seed(storage);
    const fetchStub = vi.fn(async () => tokenJson(200, { access_token: 'at-1', expires_in: 60 }));
    const result = await handleAuthCallback(new URLSearchParams({ code: 'C', state: 's-1' }), {
      config,
      sessionStorage: storage,
      localStorage: local,
      fetch: fetchStub as unknown as typeof fetch,
    });
    expect(local.getItem('minerals.oidc.had_session')).toBe('1');
    expect(result.silentRenewal).toBe(false);
    expect(result.sessionEnded).toBe(false);
  });

  it('flags silentRenewal when the silent flag was set, and still mints the token', async () => {
    const storage = new MemoryStorage();
    const local = new MemoryStorage();
    seed(storage);
    storage.setItem('minerals.oidc.silent_renewal', '1');
    const fetchStub = vi.fn(async () => tokenJson(200, { access_token: 'at-1', expires_in: 60 }));
    const result = await handleAuthCallback(new URLSearchParams({ code: 'C', state: 's-1' }), {
      config,
      sessionStorage: storage,
      localStorage: local,
      fetch: fetchStub as unknown as typeof fetch,
    });
    expect(result.silentRenewal).toBe(true);
    expect(result.sessionEnded).toBe(false);
    expect(getAccessToken()).toBe('at-1');
    expect(storage.getItem('minerals.oidc.silent_renewal')).toBeNull();
  });

  it('treats login_required on the silent path as a clean session-end (no throw)', async () => {
    const storage = new MemoryStorage();
    const local = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    storage.setItem('minerals.oidc.silent_renewal', '1');
    storage.setItem('minerals.oidc.state', 's-1');
    storage.setItem('minerals.oidc.code_verifier', 'v-1');
    storage.setItem('minerals.oidc.return_to', '#/specimens/abc');
    const result = await handleAuthCallback(new URLSearchParams({ error: 'login_required' }), {
      config,
      sessionStorage: storage,
      localStorage: local,
      fetch: vi.fn() as unknown as typeof fetch,
    });
    expect(result).toEqual({
      returnTo: '#/specimens/abc',
      silentRenewal: true,
      sessionEnded: true,
    });
    expect(local.getItem('minerals.oidc.had_session')).toBeNull();
    expect(getAccessToken()).toBeNull();
  });

  it('still throws on login_required when NOT a silent-renewal callback', async () => {
    const storage = new MemoryStorage();
    seed(storage); // no silent flag
    await expect(
      handleAuthCallback(
        new URLSearchParams({ error: 'login_required', error_description: 'login required' }),
        {
          config,
          sessionStorage: storage,
          fetch: vi.fn() as unknown as typeof fetch,
        },
      ),
    ).rejects.toThrow(/login required/);
  });
});

describe('attemptSilentRenewal', () => {
  it('skips when no prior session marker exists (anonymous browser)', async () => {
    const local = new MemoryStorage();
    const session = new MemoryStorage();
    const assign = vi.fn();
    const outcome = await attemptSilentRenewal({
      config,
      sessionStorage: session,
      localStorage: local,
      locationAssign: assign,
      currentHash: '#/specimens',
    });
    expect(outcome).toEqual({ kind: 'skipped', reason: 'no-prior-session' });
    expect(assign).not.toHaveBeenCalled();
  });

  it('skips when already authenticated', async () => {
    const local = new MemoryStorage();
    const session = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    setAccessToken('tok-x', 600, () => 0);
    const assign = vi.fn();
    const outcome = await attemptSilentRenewal({
      config,
      sessionStorage: session,
      localStorage: local,
      locationAssign: assign,
      currentHash: '#/specimens',
    });
    expect(outcome).toEqual({ kind: 'skipped', reason: 'already-authenticated' });
    expect(assign).not.toHaveBeenCalled();
  });

  it('skips when the current route IS the auth callback (avoid steal/loop)', async () => {
    const local = new MemoryStorage();
    const session = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    const assign = vi.fn();
    const outcome = await attemptSilentRenewal({
      config,
      sessionStorage: session,
      localStorage: local,
      locationAssign: assign,
      currentHash: '#/auth/callback?code=C&state=S',
    });
    expect(outcome).toEqual({ kind: 'skipped', reason: 'callback-in-progress' });
    expect(assign).not.toHaveBeenCalled();
    // and the silent flag was NOT written — would interfere with the
    // interactive callback that owns this round-trip
    expect(session.getItem('minerals.oidc.silent_renewal')).toBeNull();
  });

  it('skips when an interactive login is mid-flight (verifier still present)', async () => {
    const local = new MemoryStorage();
    const session = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    session.setItem('minerals.oidc.code_verifier', 'v-existing');
    const assign = vi.fn();
    const outcome = await attemptSilentRenewal({
      config,
      sessionStorage: session,
      localStorage: local,
      locationAssign: assign,
      currentHash: '#/specimens',
    });
    expect(outcome).toEqual({ kind: 'skipped', reason: 'interactive-login-in-flight' });
    expect(assign).not.toHaveBeenCalled();
    // verifier untouched — the in-flight interactive flow still owns it
    expect(session.getItem('minerals.oidc.code_verifier')).toBe('v-existing');
  });

  it('skips when OIDC is not configured', async () => {
    const local = new MemoryStorage();
    const session = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    const assign = vi.fn();
    const outcome = await attemptSilentRenewal({
      config: null,
      sessionStorage: session,
      localStorage: local,
      locationAssign: assign,
      currentHash: '#/specimens',
    });
    expect(outcome).toEqual({ kind: 'skipped', reason: 'not-configured' });
    expect(assign).not.toHaveBeenCalled();
  });

  it('redirects to /auth?prompt=none with PKCE params and stashes the silent flag', async () => {
    const local = new MemoryStorage();
    const session = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    const assign = vi.fn();
    const outcome = await attemptSilentRenewal({
      config,
      sessionStorage: session,
      localStorage: local,
      locationAssign: assign,
      currentHash: '#/specimens/abc',
    });
    expect(outcome).toEqual({ kind: 'redirecting' });
    expect(assign).toHaveBeenCalledOnce();
    const url = new URL(assign.mock.calls[0]![0] as string);
    expect(url.origin + url.pathname).toBe(
      'https://auth.example.com/realms/minerals/protocol/openid-connect/auth',
    );
    expect(url.searchParams.get('prompt')).toBe('none');
    expect(url.searchParams.get('response_type')).toBe('code');
    expect(url.searchParams.get('redirect_uri')).toBe('https://www.example.com/auth/callback');
    expect(url.searchParams.get('code_challenge_method')).toBe('S256');
    expect(url.searchParams.get('code_challenge')?.length).toBeGreaterThan(0);
    expect(url.searchParams.get('state')?.length).toBeGreaterThan(0);
    // sessionStorage seeded for the callback
    expect(session.getItem('minerals.oidc.code_verifier')?.length).toBeGreaterThan(0);
    expect(session.getItem('minerals.oidc.state')).toBe(url.searchParams.get('state'));
    expect(session.getItem('minerals.oidc.return_to')).toBe('#/specimens/abc');
    expect(session.getItem('minerals.oidc.silent_renewal')).toBe('1');
  });

  it('defaults returnTo to #/ when there is no current hash', async () => {
    const local = new MemoryStorage();
    const session = new MemoryStorage();
    local.setItem('minerals.oidc.had_session', '1');
    await attemptSilentRenewal({
      config,
      sessionStorage: session,
      localStorage: local,
      locationAssign: vi.fn(),
      currentHash: '',
    });
    expect(session.getItem('minerals.oidc.return_to')).toBe('#/');
  });
});
