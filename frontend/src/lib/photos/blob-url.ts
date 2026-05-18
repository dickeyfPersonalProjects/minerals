// Authenticated image-byte fetcher (mi-lrqt).
//
// <img>/<link>/<source> requests issued by HTML parsing do NOT carry
// the SPA's Authorization: Bearer header — that header is attached
// only by openapi-fetch calls that go through the wrapped `client`.
// For binary photo endpoints (display / thumb / original) that means
// any non-public photo 404s when the browser renders it directly.
//
// The fix: fetch the bytes through this helper, which attaches the
// in-memory bearer token, then hand the resulting Blob to the
// browser as a `blob:` URL. Callers are responsible for revoking the
// URL on teardown via `URL.revokeObjectURL` to release the bytes.

import { getAccessToken } from '../oidc/auth';

export interface LoadOptions {
  signal?: AbortSignal;
  // Injectable for tests; defaults to globalThis.fetch.
  fetch?: typeof fetch;
}

export class AuthedImageFetchError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'AuthedImageFetchError';
  }
}

export async function loadAuthedBlobUrl(path: string, opts: LoadOptions = {}): Promise<string> {
  const f = opts.fetch ?? globalThis.fetch;
  const token = getAccessToken();
  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await f(path, { headers, signal: opts.signal });
  if (!res.ok) {
    throw new AuthedImageFetchError(res.status, `image fetch failed: HTTP ${res.status}`);
  }
  const blob = await res.blob();
  return URL.createObjectURL(blob);
}
