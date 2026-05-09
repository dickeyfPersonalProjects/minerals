import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { client } from './index';
import { envelopeMessage, installToastMiddleware, SUPPRESS_TOAST_HEADERS } from './wrapper';
import { _clearToasts, toasts } from '../toasts';

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
});

afterEach(() => {
  vi.restoreAllMocks();
  _clearToasts();
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
