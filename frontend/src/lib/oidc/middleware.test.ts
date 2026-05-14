import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { client } from '../api';
import { installAuthHeaderMiddleware } from './middleware';
import { __resetAuthStore, setAccessToken } from './auth';

installAuthHeaderMiddleware();

const TEST_BASE = 'http://localhost/';
type GETPath = Parameters<typeof client.GET>[0];
type GETOpts = Parameters<typeof client.GET>[1];

function call(stub: typeof fetch, headers?: Record<string, string>) {
  return client.GET(
    '/healthz' as unknown as GETPath,
    {
      baseUrl: TEST_BASE,
      fetch: stub,
      ...(headers ? { headers } : {}),
    } as unknown as GETOpts,
  );
}

function capturedRequest(stub: ReturnType<typeof vi.fn>): Request {
  const calls = stub.mock.calls as unknown as unknown[][];
  const first = calls[0];
  if (!first || first.length === 0) {
    throw new Error('expected fetch to be called');
  }
  return first[0] as Request;
}

beforeEach(() => {
  __resetAuthStore();
});

afterEach(() => {
  __resetAuthStore();
  vi.restoreAllMocks();
});

describe('auth header middleware', () => {
  it('does not attach an Authorization header when no token is set', async () => {
    const fetchStub = vi.fn(
      async () =>
        new Response('{"status":"ok"}', {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
    );
    await call(fetchStub as unknown as typeof fetch);
    const req = capturedRequest(fetchStub);
    expect(req.headers.get('Authorization')).toBeNull();
  });

  it('attaches a Bearer token from the auth store', async () => {
    setAccessToken('the-token', 600);
    const fetchStub = vi.fn(
      async () =>
        new Response('{"status":"ok"}', {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
    );
    await call(fetchStub as unknown as typeof fetch);
    const req = capturedRequest(fetchStub);
    expect(req.headers.get('Authorization')).toBe('Bearer the-token');
  });

  it('does not overwrite a caller-supplied Authorization header', async () => {
    setAccessToken('store-token', 600);
    const fetchStub = vi.fn(
      async () =>
        new Response('{"status":"ok"}', {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
    );
    await call(fetchStub as unknown as typeof fetch, { Authorization: 'Bearer custom' });
    const req = capturedRequest(fetchStub);
    expect(req.headers.get('Authorization')).toBe('Bearer custom');
  });
});
