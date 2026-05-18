import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthedImageFetchError, loadAuthedBlobUrl } from './blob-url';
import { __resetAuthStore, setAccessToken } from '../oidc/auth';

beforeEach(() => {
  __resetAuthStore();
  // jsdom doesn't implement URL.createObjectURL; stub so the helper
  // can produce a deterministic blob: URL for assertions.
  if (typeof URL.createObjectURL !== 'function') {
    (URL as unknown as { createObjectURL: (b: Blob) => string }).createObjectURL = vi.fn(
      () => 'blob:test/00000000',
    );
  } else {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test/00000000');
  }
  if (typeof URL.revokeObjectURL !== 'function') {
    (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = vi.fn();
  }
});

afterEach(() => {
  __resetAuthStore();
  vi.restoreAllMocks();
});

describe('loadAuthedBlobUrl', () => {
  it('attaches Authorization: Bearer when a token is set', async () => {
    setAccessToken('the-token', 600);
    const fetchStub = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers ?? {});
      expect(headers.get('Authorization')).toBe('Bearer the-token');
      return new Response(new Blob(['jpg-bytes'], { type: 'image/jpeg' }), { status: 200 });
    });

    const url = await loadAuthedBlobUrl('/api/v1/photos/p1/display', {
      fetch: fetchStub as unknown as typeof fetch,
    });

    expect(fetchStub).toHaveBeenCalledTimes(1);
    expect(url).toBe('blob:test/00000000');
  });

  it('omits Authorization when no token is set', async () => {
    const fetchStub = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers ?? {});
      expect(headers.get('Authorization')).toBeNull();
      return new Response(new Blob(['x']), { status: 200 });
    });

    await loadAuthedBlobUrl('/api/v1/photos/p1/display', {
      fetch: fetchStub as unknown as typeof fetch,
    });
    expect(fetchStub).toHaveBeenCalledTimes(1);
  });

  it('throws AuthedImageFetchError on non-2xx responses', async () => {
    const fetchStub = vi.fn(
      async () => new Response('not found', { status: 404, statusText: 'Not Found' }),
    );

    await expect(
      loadAuthedBlobUrl('/api/v1/photos/p1/display', {
        fetch: fetchStub as unknown as typeof fetch,
      }),
    ).rejects.toBeInstanceOf(AuthedImageFetchError);
  });

  it('forwards the AbortSignal to fetch so callers can cancel', async () => {
    const ctrl = new AbortController();
    let seenSignal: AbortSignal | undefined;
    const fetchStub = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      seenSignal = init?.signal ?? undefined;
      return new Response(new Blob(['x']), { status: 200 });
    });

    await loadAuthedBlobUrl('/api/v1/photos/p1/display', {
      fetch: fetchStub as unknown as typeof fetch,
      signal: ctrl.signal,
    });
    expect(seenSignal).toBe(ctrl.signal);
  });
});
