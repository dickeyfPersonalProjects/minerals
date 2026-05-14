// OIDC Authorization Code + PKCE login flow. The browser drives the
// whole dance — it generates the verifier, redirects to Keycloak,
// then POSTs back to the token endpoint directly. No backend
// intermediary; the backend only validates the resulting bearer
// token via Keycloak's JWKS endpoint.
//
// Tokens live in memory only. `sessionStorage` holds the PKCE
// verifier, the CSRF `state` value, and the post-login return path,
// because those need to survive the Keycloak redirect round-trip.

import { writable, get, type Readable, type Writable } from 'svelte/store';
import { deriveCodeChallenge, generateCodeVerifier, generateState } from './pkce';
import { loadOidcConfig, type OidcConfig } from './config';

const STORAGE_VERIFIER = 'minerals.oidc.code_verifier';
const STORAGE_STATE = 'minerals.oidc.state';
const STORAGE_RETURN_TO = 'minerals.oidc.return_to';

export interface AuthState {
  readonly accessToken: string | null;
  readonly expiresAt: number | null;
}

const initial: AuthState = { accessToken: null, expiresAt: null };
const store: Writable<AuthState> = writable(initial);

export const authStore: Readable<AuthState> = { subscribe: store.subscribe };

/**
 * Return the current access token, clearing it lazily if expired.
 * Returns null when not authenticated or after expiry.
 */
export function getAccessToken(now: () => number = Date.now): string | null {
  const state = get(store);
  if (!state.accessToken) return null;
  if (state.expiresAt !== null && now() >= state.expiresAt) {
    store.set(initial);
    return null;
  }
  return state.accessToken;
}

export function setAccessToken(
  token: string,
  expiresInSeconds: number,
  now: () => number = Date.now,
): void {
  store.set({
    accessToken: token,
    expiresAt: now() + expiresInSeconds * 1000,
  });
}

export function clearAuth(): void {
  store.set(initial);
}

export interface BeginLoginDeps {
  config?: OidcConfig | null;
  sessionStorage?: Storage;
  locationAssign?: (url: string) => void;
}

/**
 * Kick off the OIDC redirect. Generates the PKCE verifier and the
 * CSRF state, stashes them in sessionStorage, then navigates to
 * Keycloak's `/auth` endpoint.
 *
 * `returnTo` is the hash route (e.g. `#/specimens/123`) to return to
 * after the callback completes. Defaults to the current hash, falling
 * back to `#/`.
 */
export async function beginLogin(returnTo?: string, deps: BeginLoginDeps = {}): Promise<void> {
  const config = deps.config ?? (await loadOidcConfig());
  if (!config) throw new Error('OIDC is not configured');
  const storage = deps.sessionStorage ?? window.sessionStorage;
  const assign = deps.locationAssign ?? ((url) => window.location.assign(url));
  const dest = returnTo ?? (window.location.hash || '#/');

  const verifier = generateCodeVerifier();
  const challenge = await deriveCodeChallenge(verifier);
  const state = generateState();

  storage.setItem(STORAGE_VERIFIER, verifier);
  storage.setItem(STORAGE_STATE, state);
  storage.setItem(STORAGE_RETURN_TO, dest);

  const params = new URLSearchParams({
    response_type: 'code',
    client_id: config.clientId,
    redirect_uri: config.redirectUri,
    scope: 'openid email profile',
    state,
    code_challenge: challenge,
    code_challenge_method: 'S256',
  });
  assign(`${config.issuerUrl}/protocol/openid-connect/auth?${params.toString()}`);
}

export interface HandleCallbackDeps {
  config?: OidcConfig | null;
  sessionStorage?: Storage;
  fetch?: typeof fetch;
  now?: () => number;
}

export interface CallbackResult {
  /** The hash route to redirect to after successful login. */
  readonly returnTo: string;
}

/**
 * Complete the OIDC flow: validate state, exchange code+verifier for
 * an access token, and stash the token in memory. The single-use
 * verifier and state are cleared from storage on every path
 * (success, error, or rejection) — they MUST NOT be reused.
 */
export async function handleAuthCallback(
  query: URLSearchParams,
  deps: HandleCallbackDeps = {},
): Promise<CallbackResult> {
  const config = deps.config ?? (await loadOidcConfig());
  if (!config) throw new Error('OIDC is not configured');
  const storage = deps.sessionStorage ?? window.sessionStorage;
  const fetchFn = deps.fetch ?? globalThis.fetch.bind(globalThis);

  const verifier = storage.getItem(STORAGE_VERIFIER);
  const expectedState = storage.getItem(STORAGE_STATE);
  const returnTo = storage.getItem(STORAGE_RETURN_TO) || '#/';

  // Single-use values — clear before doing any work so a thrown
  // error or duplicate call cannot replay them.
  storage.removeItem(STORAGE_VERIFIER);
  storage.removeItem(STORAGE_STATE);
  storage.removeItem(STORAGE_RETURN_TO);

  const oauthError = query.get('error');
  if (oauthError) {
    const desc = query.get('error_description') || oauthError;
    throw new Error(desc);
  }

  const code = query.get('code');
  const state = query.get('state');
  if (!code) throw new Error('Missing authorization code');
  if (!state || !expectedState || state !== expectedState) {
    throw new Error('Invalid state parameter');
  }
  if (!verifier) {
    throw new Error('Missing PKCE verifier; restart login');
  }

  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    code,
    redirect_uri: config.redirectUri,
    client_id: config.clientId,
    code_verifier: verifier,
  });

  const response = await fetchFn(`${config.issuerUrl}/protocol/openid-connect/token`, {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded' },
    body: body.toString(),
  });

  if (!response.ok) {
    throw new Error(await tokenErrorMessage(response));
  }
  const json = (await response.json()) as {
    access_token?: unknown;
    expires_in?: unknown;
  };
  if (typeof json.access_token !== 'string' || typeof json.expires_in !== 'number') {
    throw new Error('Malformed token response');
  }
  setAccessToken(json.access_token, json.expires_in, deps.now);
  return { returnTo };
}

async function tokenErrorMessage(response: Response): Promise<string> {
  try {
    const json = (await response.json()) as { error?: string; error_description?: string };
    if (json.error_description) return json.error_description;
    if (json.error) return json.error;
  } catch {
    // fall through
  }
  return `Token exchange failed (HTTP ${response.status})`;
}

// Test-only helper to reset the in-memory store between cases.
export function __resetAuthStore(): void {
  store.set(initial);
}
