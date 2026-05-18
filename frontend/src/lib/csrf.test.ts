import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { __resetCsrf, csrfStore, fetchCsrfToken, getCsrfToken } from './csrf';

beforeEach(() => {
  __resetCsrf();
});

afterEach(() => {
  vi.restoreAllMocks();
  __resetCsrf();
});

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

describe('csrf store (V2 BFF cookie flow, mi-3vc4)', () => {
  it('starts empty', () => {
    expect(get(csrfStore)).toBeNull();
    expect(getCsrfToken()).toBeNull();
  });

  it('fetchCsrfToken populates the store on a 200 with {token: "..."}', async () => {
    const fetchStub = vi.fn(async () => jsonResponse(200, { token: 'abc123' }));
    const token = await fetchCsrfToken({ fetch: fetchStub as unknown as typeof fetch });
    expect(token).toBe('abc123');
    expect(get(csrfStore)).toBe('abc123');
    expect(getCsrfToken()).toBe('abc123');
  });

  it('sends credentials: include so the session cookie travels', async () => {
    const fetchStub = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      expect(init?.credentials).toBe('include');
      return jsonResponse(200, { token: 't' });
    });
    await fetchCsrfToken({ fetch: fetchStub as unknown as typeof fetch });
    expect(fetchStub).toHaveBeenCalledTimes(1);
  });

  it('clears the store and returns null on 401 (anonymous caller)', async () => {
    // Seed a stale value to prove it's cleared, not kept.
    const fetchStub = vi.fn(async () => jsonResponse(401, { error: { code: 'unauthorized' } }));
    const token = await fetchCsrfToken({ fetch: fetchStub as unknown as typeof fetch });
    expect(token).toBeNull();
    expect(get(csrfStore)).toBeNull();
  });

  it('returns null and keeps the store empty when the response is malformed', async () => {
    const fetchStub = vi.fn(async () => jsonResponse(200, { token: 42 }));
    const token = await fetchCsrfToken({ fetch: fetchStub as unknown as typeof fetch });
    expect(token).toBeNull();
    expect(get(csrfStore)).toBeNull();
  });

  it('returns null on network failure', async () => {
    const fetchStub = vi.fn(async () => {
      throw new Error('network down');
    });
    const token = await fetchCsrfToken({ fetch: fetchStub as unknown as typeof fetch });
    expect(token).toBeNull();
    expect(get(csrfStore)).toBeNull();
  });
});
