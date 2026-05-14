// OIDC client config sourced from the backend's runtime-config
// endpoint (`/api/v1/runtime-config`, mi-5ew). The backend reads the
// PUBLIC_OIDC_* env vars from its ConfigMap and serves the SPA-safe
// subset through this endpoint, so config changes don't require a
// frontend rebuild (per CONFIG.md trust-boundary note).
//
// The fetch is performed once at startup; the resolved value is
// cached in a Svelte store so the rest of the app (Login button,
// AuthCallback route) can read it synchronously after the initial
// resolution.

import { writable, get, type Readable, type Writable } from 'svelte/store';
import { client } from '../api';

export interface OidcConfig {
  readonly issuerUrl: string;
  readonly clientId: string;
  readonly redirectUri: string;
}

export type OidcConfigState =
  | { kind: 'unloaded' }
  | { kind: 'loading' }
  | { kind: 'ready'; config: OidcConfig | null }
  | { kind: 'error'; message: string };

const store: Writable<OidcConfigState> = writable({ kind: 'unloaded' });
export const oidcConfigStore: Readable<OidcConfigState> = { subscribe: store.subscribe };

let inflight: Promise<OidcConfig | null> | null = null;

/**
 * Fetch the runtime config once, cache the result, and return the
 * OIDC sub-config (or null when the backend has no OIDC values
 * configured). Concurrent callers share a single in-flight request.
 */
export async function loadOidcConfig(): Promise<OidcConfig | null> {
  const current = get(store);
  if (current.kind === 'ready') return current.config;
  if (inflight) return inflight;

  store.set({ kind: 'loading' });
  inflight = (async () => {
    try {
      const { data, error, response } = await client.GET('/api/v1/runtime-config');
      if (error || !data) {
        const msg =
          error?.error?.message || error?.error?.code || `HTTP ${response?.status ?? 'unknown'}`;
        store.set({ kind: 'error', message: msg });
        return null;
      }
      const oidc = data.oidc;
      const config: OidcConfig | null = oidc
        ? {
            issuerUrl: stripTrailingSlash(oidc.issuer_url),
            clientId: oidc.client_id,
            redirectUri: oidc.redirect_uri,
          }
        : null;
      store.set({ kind: 'ready', config });
      return config;
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      store.set({ kind: 'error', message: msg });
      return null;
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

/**
 * Return the cached OIDC config synchronously, or null if not yet
 * resolved or not configured by the backend. Callers that need to
 * guarantee resolution should await `loadOidcConfig()` first.
 */
export function getOidcConfig(): OidcConfig | null {
  const s = get(store);
  return s.kind === 'ready' ? s.config : null;
}

function stripTrailingSlash(s: string): string {
  return s.endsWith('/') ? s.slice(0, -1) : s;
}

// Test-only reset of the cached state.
export function __resetOidcConfig(): void {
  store.set({ kind: 'unloaded' });
  inflight = null;
}
