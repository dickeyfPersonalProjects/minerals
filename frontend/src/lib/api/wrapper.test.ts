import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const { mockPush } = vi.hoisted(() => ({ mockPush: vi.fn() }));
vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return { ...actual, push: mockPush };
});

import { client } from './index';
import {
  CSRFHeaderName,
  envelopeMessage,
  installToastMiddleware,
  isProfileSetupRedirect,
  readPostSetupReturn,
  SUPPRESS_TOAST_HEADERS,
} from './wrapper';
import { _clearToasts, toasts } from '../toasts';
import { __resetCsrf, __setCsrf, getCsrfToken } from '../csrf';

// The middleware install is module-scoped (idempotent) — every
// test in this file shares the same client. The wrapper's
// `installed` flag prevents double-registration.
installToastMiddleware();

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

// openapi-fetch resolves URLs via `new URL(path, baseUrl)` and
// captures `fetch` at client-creation time, so we override both
// per-call rather than mutating the prod client. The middleware
// runs identically regardless of the underlying fetch.
const TEST_BASE = 'http://localhost/';
type GETPath = Parameters<typeof client.GET>[0];
type GETOpts = Parameters<typeof client.GET>[1];

function withFetch(stub: typeof fetch) {
  return async function call(path: string, extra: Record<string, unknown> = {}) {
    return client.GET(
      path as unknown as GETPath,
      {
        baseUrl: TEST_BASE,
        fetch: stub,
        ...extra,
      } as unknown as GETOpts,
    );
  };
}

beforeEach(() => {
  _clearToasts();
  mockPush.mockReset();
  __resetCsrf();
  window.sessionStorage.clear();
  window.location.hash = '';
});

afterEach(() => {
  vi.restoreAllMocks();
  _clearToasts();
  __resetCsrf();
});

describe('isProfileSetupRedirect', () => {
  it('returns true only when the envelope code is profile_setup_required', () => {
    expect(isProfileSetupRedirect({ error: { code: 'profile_setup_required' } })).toBe(true);
    expect(isProfileSetupRedirect({ error: { code: 'forbidden' } })).toBe(false);
    expect(isProfileSetupRedirect({})).toBe(false);
    expect(isProfileSetupRedirect(undefined)).toBe(false);
  });
});

describe('envelopeMessage', () => {
  it('prefers message, falls back to code, then status', () => {
    expect(envelopeMessage({ error: { message: 'm', code: 'c' } }, 500)).toBe('m');
    expect(envelopeMessage({ error: { code: 'c' } }, 500)).toBe('c');
    expect(envelopeMessage({}, 503)).toBe('HTTP 503');
    expect(envelopeMessage(undefined, 503)).toBe('HTTP 503');
  });
});

describe('auto-toast middleware', () => {
  it('toasts the envelope message when the response is non-2xx', async () => {
    const fetchStub = vi.fn(async () =>
      jsonResponse(400, { error: { code: 'bad', message: 'something is off' } }),
    );
    const result = await withFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(result.error).toBeTruthy();

    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]?.type).toBe('error');
    expect(list[0]?.message).toBe('something is off');
  });

  it('does NOT toast when the suppress header is set', async () => {
    const fetchStub = vi.fn(async () =>
      jsonResponse(409, { error: { code: 'conflict', message: 'no' } }),
    );
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz', {
      headers: SUPPRESS_TOAST_HEADERS,
    });
    expect(get(toasts)).toHaveLength(0);
  });

  it('does NOT toast on a 2xx response', async () => {
    const fetchStub = vi.fn(async () => jsonResponse(200, { ok: true }));
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(get(toasts)).toHaveLength(0);
  });

  it('falls back to status code when the body is not JSON', async () => {
    const fetchStub = vi.fn(
      async () =>
        new Response('plain text oops', {
          status: 502,
          headers: { 'content-type': 'text/plain' },
        }),
    );
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz');

    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]?.message).toBe('HTTP 502');
  });

  it('redirects on 403 with details.redirect and does NOT toast', async () => {
    const fetchStub = vi.fn(async () =>
      jsonResponse(403, {
        error: {
          code: 'profile_setup_required',
          message: 'profile setup required',
          details: { redirect: '/profile/setup' },
        },
      }),
    );
    window.location.hash = '#/specimens/abc';
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz');

    expect(mockPush).toHaveBeenCalledWith('/profile/setup');
    expect(get(toasts)).toHaveLength(0);
    // The current hash got stashed so ProfileSetup can return to it.
    expect(window.sessionStorage.getItem('minerals.profile.return_to')).toBe('#/specimens/abc');
  });

  it('honors the redirect even when toast is suppressed', async () => {
    const fetchStub = vi.fn(async () =>
      jsonResponse(403, {
        error: { code: 'profile_setup_required', details: { redirect: '/profile/setup' } },
      }),
    );
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz', {
      headers: SUPPRESS_TOAST_HEADERS,
    });
    expect(mockPush).toHaveBeenCalledWith('/profile/setup');
    expect(get(toasts)).toHaveLength(0);
  });

  it('does NOT stash the home route or the setup route as return-to', async () => {
    const fetchStub = vi.fn(async () =>
      jsonResponse(403, {
        error: { code: 'profile_setup_required', details: { redirect: '/profile/setup' } },
      }),
    );
    window.location.hash = '#/';
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(window.sessionStorage.getItem('minerals.profile.return_to')).toBeNull();
  });

  it('does not overwrite an existing stashed return-to on subsequent 403s', async () => {
    window.sessionStorage.setItem('minerals.profile.return_to', '#/specimens/first');
    const fetchStub = vi.fn(async () =>
      jsonResponse(403, {
        error: { code: 'profile_setup_required', details: { redirect: '/profile/setup' } },
      }),
    );
    window.location.hash = '#/specimens/second';
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(window.sessionStorage.getItem('minerals.profile.return_to')).toBe('#/specimens/first');
  });

  it('falls back to the default toast path for 403s without a redirect hint', async () => {
    const fetchStub = vi.fn(async () =>
      jsonResponse(403, { error: { code: 'forbidden', message: 'nope' } }),
    );
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(mockPush).not.toHaveBeenCalled();
    expect(get(toasts).at(0)?.message).toBe('nope');
  });

  it('readPostSetupReturn consumes the stash exactly once', () => {
    window.sessionStorage.setItem('minerals.profile.return_to', '#/x');
    expect(readPostSetupReturn()).toBe('#/x');
    expect(readPostSetupReturn()).toBeNull();
  });

  it('toasts on a network error (onError path)', async () => {
    const fetchStub = vi.fn(async () => {
      throw new Error('network down');
    });

    await expect(withFetch(fetchStub as unknown as typeof fetch)('/healthz')).rejects.toThrow(
      'network down',
    );

    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]?.type).toBe('error');
    expect(list[0]?.message).toBe('network down');
  });
});

// CSRF middleware (V2 BFF cookie flow, mi-3vc4). Exercises the
// X-CSRF-Token attachment on non-safe methods and the 403
// csrf_mismatch refresh-and-retry-once path.
type POSTPath = Parameters<typeof client.POST>[0];
type POSTOpts = Parameters<typeof client.POST>[1];

function postWithFetch(stub: typeof fetch) {
  return async function call(path: string, extra: Record<string, unknown> = {}) {
    return client.POST(
      path as unknown as POSTPath,
      {
        baseUrl: TEST_BASE,
        fetch: stub,
        ...extra,
      } as unknown as POSTOpts,
    );
  };
}

describe('CSRF middleware (V2 BFF cookie flow, mi-3vc4)', () => {
  it('attaches X-CSRF-Token on POST when the store has a value', async () => {
    __setCsrf('csrf-abc');
    const fetchStub = vi.fn(async (input: RequestInfo | URL) => {
      const req = input as Request;
      expect(req.headers.get(CSRFHeaderName)).toBe('csrf-abc');
      return jsonResponse(200, { ok: true });
    });
    await postWithFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(fetchStub).toHaveBeenCalledTimes(1);
  });

  it('does NOT attach X-CSRF-Token on GET', async () => {
    __setCsrf('csrf-abc');
    const fetchStub = vi.fn(async (input: RequestInfo | URL) => {
      const req = input as Request;
      expect(req.headers.has(CSRFHeaderName)).toBe(false);
      return jsonResponse(200, { ok: true });
    });
    await withFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(fetchStub).toHaveBeenCalledTimes(1);
  });

  it('omits the header when the store is empty', async () => {
    __resetCsrf();
    const fetchStub = vi.fn(async (input: RequestInfo | URL) => {
      const req = input as Request;
      expect(req.headers.has(CSRFHeaderName)).toBe(false);
      return jsonResponse(200, { ok: true });
    });
    await postWithFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(fetchStub).toHaveBeenCalledTimes(1);
  });

  // Extract the X-CSRF-Token from a fetch call's input — openapi-fetch
  // passes a Request, the wrapper's retry path passes (url, init).
  function csrfOf(input: RequestInfo | URL, init?: RequestInit): string | null {
    if (init?.headers) {
      const h = new Headers(init.headers as HeadersInit);
      return h.get(CSRFHeaderName);
    }
    if (input instanceof Request) return input.headers.get(CSRFHeaderName);
    return null;
  }
  function retryFlagOf(input: RequestInfo | URL, init?: RequestInit): string | null {
    if (init?.headers) {
      const h = new Headers(init.headers as HeadersInit);
      return h.get('x-csrf-retried');
    }
    if (input instanceof Request) return input.headers.get('x-csrf-retried');
    return null;
  }
  function urlOf(input: RequestInfo | URL): string {
    if (typeof input === 'string') return input;
    if (input instanceof URL) return input.toString();
    return (input as Request).url;
  }

  it('on 403 csrf_mismatch: refetches the token and retries once with the fresh value', async () => {
    __setCsrf('stale');
    let postCalls = 0;
    const fetchStub = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = urlOf(input);
      if (url.endsWith('/api/v1/csrf')) {
        return jsonResponse(200, { token: 'fresh' });
      }
      postCalls += 1;
      if (postCalls === 1) {
        // Original POST — return csrf_mismatch.
        return jsonResponse(403, { error: { code: 'csrf_mismatch', message: 'stale' } });
      }
      // Retry — assert it carries the fresh token and the retry marker.
      expect(csrfOf(input, init)).toBe('fresh');
      expect(retryFlagOf(input, init)).toBe('1');
      return jsonResponse(200, { ok: true });
    });
    // Wrapper-internal retry uses globalThis.fetch, not openapi-fetch's
    // captured one — so the retry call still hits our stub.
    globalThis.fetch = fetchStub as unknown as typeof fetch;
    await postWithFetch(fetchStub as unknown as typeof fetch)('/healthz');
    expect(postCalls).toBe(2);
    expect(getCsrfToken()).toBe('fresh');
  });

  it('does NOT retry a second time on repeated csrf_mismatch', async () => {
    __setCsrf('stale');
    let postCalls = 0;
    const fetchStub = vi.fn(async (input: RequestInfo | URL) => {
      const url = urlOf(input);
      if (url.endsWith('/api/v1/csrf')) {
        return jsonResponse(200, { token: 'still-stale' });
      }
      postCalls += 1;
      return jsonResponse(403, { error: { code: 'csrf_mismatch', message: 'nope' } });
    });
    globalThis.fetch = fetchStub as unknown as typeof fetch;
    await postWithFetch(fetchStub as unknown as typeof fetch)('/healthz');
    // First POST + one retry = 2 attempts; the second 403 does NOT
    // recurse — the retry marker breaks the loop.
    expect(postCalls).toBe(2);
  });
});
