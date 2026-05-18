import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));
vi.mock('./api', () => ({
  client: { GET: mockGet },
}));

import {
  __authenticate,
  __resetAuthStore,
  __setAuthUser,
  authStore,
  isAuthenticated,
  probeAuth,
} from './auth';

beforeEach(() => {
  __resetAuthStore();
  mockGet.mockReset();
});

afterEach(() => {
  __resetAuthStore();
});

describe('auth store (V2 BFF cookie flow, mi-3vc4)', () => {
  it('starts empty and unauthenticated', () => {
    expect(get(authStore)).toEqual({ user: null, loaded: false });
    expect(get(isAuthenticated)).toBe(false);
  });

  it('isAuthenticated flips to true when a user is set', () => {
    __authenticate({ display_name: 'Ada' });
    expect(get(isAuthenticated)).toBe(true);
    expect(get(authStore).user?.display_name).toBe('Ada');
  });

  it('probeAuth populates the store from a 200 /api/v1/profile response', async () => {
    const body = {
      id: 'u-1',
      display_name: 'Ada Lovelace',
      email: 'ada@example.com',
      pending: false,
      field_defaults: null,
    };
    mockGet.mockResolvedValue({
      data: body,
      response: new Response(null, { status: 200 }),
    });
    const user = await probeAuth();
    expect(user?.display_name).toBe('Ada Lovelace');
    expect(get(authStore)).toEqual({ user: body, loaded: true });
    expect(get(isAuthenticated)).toBe(true);
  });

  it('probeAuth clears the user on 401 (anonymous browser)', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'unauthorized' } },
      response: new Response(null, { status: 401 }),
    });
    const user = await probeAuth();
    expect(user).toBeNull();
    expect(get(authStore)).toEqual({ user: null, loaded: true });
    expect(get(isAuthenticated)).toBe(false);
  });

  it('probeAuth suppresses the global toast (anonymous boot is not an error)', async () => {
    mockGet.mockImplementation(
      async (_path: string, opts?: { headers?: Record<string, string> }) => {
        expect(opts?.headers?.['x-suppress-toast']).toBe('1');
        return {
          data: undefined,
          error: { error: { code: 'unauthorized' } },
          response: new Response(null, { status: 401 }),
        };
      },
    );
    await probeAuth();
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it('probeAuth shares one in-flight call across concurrent callers', async () => {
    let resolveFn!: (v: unknown) => void;
    mockGet.mockReturnValue(
      new Promise((resolve) => {
        resolveFn = resolve;
      }),
    );
    const p1 = probeAuth();
    const p2 = probeAuth();
    resolveFn({
      data: {
        id: 'u-1',
        display_name: 'A',
        email: 'a@a',
        pending: false,
        field_defaults: null,
      },
      response: new Response(null, { status: 200 }),
    });
    await Promise.all([p1, p2]);
    expect(mockGet).toHaveBeenCalledTimes(1);
  });

  it('__setAuthUser and __resetAuthStore round-trip the store', () => {
    __setAuthUser({
      id: 'u',
      display_name: 'X',
      email: 'x@x',
      pending: false,
      field_defaults: null as unknown as never,
    });
    expect(get(isAuthenticated)).toBe(true);
    __resetAuthStore();
    expect(get(isAuthenticated)).toBe(false);
    expect(get(authStore)).toEqual({ user: null, loaded: false });
  });
});
