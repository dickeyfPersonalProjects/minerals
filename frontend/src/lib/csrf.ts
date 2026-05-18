// CSRF token store for the V2 BFF cookie flow (mi-1d5i / mi-3vc4).
//
// The backend mints a per-session 32-byte secret stored in the
// auth.sessions row and exposes it as base64url at GET /api/v1/csrf.
// The SPA fetches the token once after a session is confirmed and
// attaches it as `X-CSRF-Token` on every POST / PUT / PATCH / DELETE
// (api/wrapper.ts owns the middleware that reads this store).
//
// On a 403 csrf_mismatch the wrapper refetches the token and retries
// the original request once, so this module exposes the bare
// fetch/get primitives — the retry policy belongs to the wrapper.
//
// Storage is in-memory only. The token is paired with the HttpOnly
// session cookie; an XSS that can read it from this store can do
// little more than what it could already do by calling the wrapped
// client, because the cookie is what actually authenticates.

import { writable, get, type Readable, type Writable } from 'svelte/store';

const store: Writable<string | null> = writable(null);
export const csrfStore: Readable<string | null> = { subscribe: store.subscribe };

const CSRF_ENDPOINT = '/api/v1/csrf';

// fetchCsrfTokenDeps is the seam tests use to inject a stub fetch
// without monkey-patching globalThis.
export interface FetchCsrfDeps {
  fetch?: typeof fetch;
}

/**
 * GET /api/v1/csrf and populate the in-memory store. Returns the
 * token on success, null when the call fails (401, network drop,
 * etc.) so callers can fall back to anonymous behavior. The endpoint
 * sits behind SessionMiddleware on the backend — a pre-auth call
 * resolves to 401 and we keep the store empty until a session lands.
 */
export async function fetchCsrfToken(deps: FetchCsrfDeps = {}): Promise<string | null> {
  const f = deps.fetch ?? globalThis.fetch;
  try {
    const res = await f(CSRF_ENDPOINT, {
      method: 'GET',
      credentials: 'include',
      headers: { Accept: 'application/json' },
    });
    if (!res.ok) {
      // 401/403/5xx — clear any stale value so a stale tab does not
      // replay a token after the session lapses.
      store.set(null);
      return null;
    }
    const body = (await res.json()) as { token?: unknown };
    if (typeof body.token !== 'string' || body.token.length === 0) {
      store.set(null);
      return null;
    }
    store.set(body.token);
    return body.token;
  } catch {
    store.set(null);
    return null;
  }
}

/** Synchronous read for middleware use. Returns null when unset. */
export function getCsrfToken(): string | null {
  return get(store);
}

/** Test-only helper: drop the cached token between cases. */
export function __resetCsrf(): void {
  store.set(null);
}

/** Test-only helper: seed the store without a fetch. */
export function __setCsrf(token: string | null): void {
  store.set(token);
}
