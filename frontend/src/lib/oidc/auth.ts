// OIDC Authorization Code + PKCE login flow. The browser drives the
// whole dance — it generates the verifier, redirects to Keycloak,
// then POSTs back to the token endpoint directly. No backend
// intermediary; the backend only validates the resulting bearer
// token via Keycloak's JWKS endpoint.
//
// Tokens live in memory only. `sessionStorage` holds the PKCE
// verifier, the CSRF `state` value, and the post-login return path,
// because those need to survive the Keycloak redirect round-trip.

import { derived, writable, get, type Readable, type Writable } from 'svelte/store';
import { deriveCodeChallenge, generateCodeVerifier, generateState } from './pkce';
import { loadOidcConfig, type OidcConfig } from './config';

const STORAGE_VERIFIER = 'minerals.oidc.code_verifier';
const STORAGE_STATE = 'minerals.oidc.state';
const STORAGE_RETURN_TO = 'minerals.oidc.return_to';
// sessionStorage flag set when the current Keycloak round-trip was
// kicked off by attemptSilentRenewal() instead of an interactive
// click. Tells the callback handler to treat login_required as a
// quiet 'SSO session is gone' signal rather than an error to display.
const STORAGE_SILENT_RENEWAL = 'minerals.oidc.silent_renewal';
// localStorage marker set on every successful token mint, cleared on
// logout and when silent renewal returns login_required. Its sole
// purpose is to suppress the silent-renewal redirect for browsers
// that have never logged in (no marker → don't redirect to Keycloak
// on app boot, since no SSO session can possibly exist). Holds no
// secret material — just a 1/0 boolean.
const STORAGE_HAD_SESSION = 'minerals.oidc.had_session';

export interface AuthState {
  readonly accessToken: string | null;
  readonly expiresAt: number | null;
}

const initial: AuthState = { accessToken: null, expiresAt: null };
const store: Writable<AuthState> = writable(initial);

export const authStore: Readable<AuthState> = { subscribe: store.subscribe };

// True iff a non-expired access token is held. Components gate
// write CTAs on this (mi-eec) — anonymous users see a read-only UI
// because writes require auth (CONTRACT §13). Expiry is checked
// against the same wall clock as getAccessToken so the flag flips
// to false as soon as the token lapses.
export const isAuthenticated: Readable<boolean> = derived(
  store,
  ($s) => $s.accessToken !== null && ($s.expiresAt === null || Date.now() < $s.expiresAt),
);

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

export interface TokenClaims {
  readonly name: string | null;
  readonly preferredUsername: string | null;
  readonly email: string | null;
}

function decodeBase64Url(segment: string): string {
  const b64 = segment.replace(/-/g, '+').replace(/_/g, '/');
  const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4);
  const binary = atob(padded);
  const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

/**
 * Decode the display-relevant claims from a JWT access token. This is
 * a best-effort, unverified read of the payload for UI purposes only
 * (the backend independently verifies the token via JWKS) — never
 * trust these claims for authorization decisions. Returns null for a
 * missing or malformed token.
 */
export function decodeTokenClaims(token: string | null): TokenClaims | null {
  if (!token) return null;
  const parts = token.split('.');
  const payloadSegment = parts[1];
  if (parts.length !== 3 || !payloadSegment) return null;
  try {
    const payload = JSON.parse(decodeBase64Url(payloadSegment)) as Record<string, unknown>;
    return {
      name: typeof payload.name === 'string' ? payload.name : null,
      preferredUsername:
        typeof payload.preferred_username === 'string' ? payload.preferred_username : null,
      email: typeof payload.email === 'string' ? payload.email : null,
    };
  } catch {
    return null;
  }
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

export interface BeginLogoutDeps {
  config?: OidcConfig | null;
  locationAssign?: (url: string) => void;
  appUrl?: string;
  localStorage?: Storage;
}

/**
 * Sign out. PKCE auth holds the token in the frontend only — there is
 * no backend session — so logout drops the in-memory token and then
 * redirects to Keycloak's end-session endpoint to terminate the SSO
 * session. Keycloak bounces back to the app root unauthenticated.
 *
 * `client_id` + `post_logout_redirect_uri` are used in place of an
 * `id_token_hint` because mi-5ew's authStore does not retain the id
 * token; Keycloak accepts this form when the redirect URI is
 * registered on the client. When OIDC is not configured, local state
 * is still cleared and the app navigates home.
 */
export async function beginLogout(deps: BeginLogoutDeps = {}): Promise<void> {
  const config = deps.config ?? (await loadOidcConfig());
  const assign = deps.locationAssign ?? ((url) => window.location.assign(url));
  const appUrl = deps.appUrl ?? `${window.location.origin}/`;
  const local = deps.localStorage ?? window.localStorage;

  clearAuth();
  // Clear the had-session marker so the next app boot won't try to
  // silently revive a session the user explicitly ended.
  local.removeItem(STORAGE_HAD_SESSION);

  if (!config) {
    assign(appUrl);
    return;
  }

  const params = new URLSearchParams({
    client_id: config.clientId,
    post_logout_redirect_uri: appUrl,
  });
  assign(`${config.issuerUrl}/protocol/openid-connect/logout?${params.toString()}`);
}

export interface HandleCallbackDeps {
  config?: OidcConfig | null;
  sessionStorage?: Storage;
  localStorage?: Storage;
  fetch?: typeof fetch;
  now?: () => number;
}

export interface CallbackResult {
  /** The hash route to redirect to after successful login. */
  readonly returnTo: string;
  /**
   * True when this callback completed a silent-renewal round-trip
   * (kicked off by attemptSilentRenewal, not an interactive click).
   * The UI uses this to skip the "Signing you in…" busy state and
   * to suppress the error screen on `login_required`.
   */
  readonly silentRenewal: boolean;
  /**
   * True when silent renewal returned login_required (or a sibling
   * error) — the SSO session is gone, the had-session marker has
   * been cleared, and the user is now anonymous. The UI should
   * navigate home without displaying an error.
   */
  readonly sessionEnded: boolean;
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
  const local = deps.localStorage ?? window.localStorage;
  const fetchFn = deps.fetch ?? globalThis.fetch.bind(globalThis);

  const verifier = storage.getItem(STORAGE_VERIFIER);
  const expectedState = storage.getItem(STORAGE_STATE);
  const returnTo = storage.getItem(STORAGE_RETURN_TO) || '#/';
  const silentRenewal = storage.getItem(STORAGE_SILENT_RENEWAL) === '1';

  // Single-use values — clear before doing any work so a thrown
  // error or duplicate call cannot replay them.
  storage.removeItem(STORAGE_VERIFIER);
  storage.removeItem(STORAGE_STATE);
  storage.removeItem(STORAGE_RETURN_TO);
  storage.removeItem(STORAGE_SILENT_RENEWAL);

  const oauthError = query.get('error');
  if (oauthError) {
    // Silent renewal: when Keycloak says the user must interact (no
    // SSO cookie, or the cookie is expired), we drop the had-session
    // marker and resolve quietly so the UI returns to the anonymous
    // home view without flashing an error. The user-visible Login
    // button is still reachable from the navbar.
    if (silentRenewal && isInteractionRequiredError(oauthError)) {
      local.removeItem(STORAGE_HAD_SESSION);
      return { returnTo, silentRenewal: true, sessionEnded: true };
    }
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
  // Persist the had-session marker so the next app boot can attempt
  // silent renewal instead of leaving the user logged-out after a
  // refresh. Set on both interactive and silent paths — both end up
  // here with a fresh token.
  local.setItem(STORAGE_HAD_SESSION, '1');
  return { returnTo, silentRenewal, sessionEnded: false };
}

function isInteractionRequiredError(code: string): boolean {
  return (
    code === 'login_required' || code === 'interaction_required' || code === 'consent_required'
  );
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

export interface AttemptSilentRenewalDeps {
  config?: OidcConfig | null;
  sessionStorage?: Storage;
  localStorage?: Storage;
  locationAssign?: (url: string) => void;
  currentHash?: string;
}

export type SilentRenewalSkipReason =
  | 'not-configured'
  | 'no-prior-session'
  | 'already-authenticated'
  | 'callback-in-progress'
  | 'interactive-login-in-flight';

export type SilentRenewalOutcome =
  | { kind: 'redirecting' }
  | { kind: 'skipped'; reason: SilentRenewalSkipReason };

/**
 * If the browser has held a session before AND no in-memory token is
 * present, navigate the WHOLE TAB to Keycloak's `/auth?prompt=none`
 * endpoint so a still-valid SSO cookie can mint a fresh access token
 * without user interaction (mi-wmyc, strategy 2).
 *
 * Returns immediately (without navigating) when any of the following
 * are true:
 *   - OIDC is not configured for this deployment.
 *   - The browser has never held a session (no had_session marker) —
 *     there is no SSO cookie to leverage and we'd just bounce the
 *     anonymous user through Keycloak for nothing.
 *   - An access token is already in memory (nothing to renew).
 *   - The current URL is the auth callback route — completing an
 *     in-flight interactive login takes priority; a silent attempt
 *     here would steal the verifier/state and loop.
 *   - The PKCE verifier is still in sessionStorage (an interactive
 *     login is mid-redirect from a different tab/route).
 *
 * On success the function calls `location.assign(...)` and the
 * caller's promise resolves AFTER the navigation has been queued —
 * the page is about to be replaced, so callers should treat this as
 * "we are leaving now."
 */
export async function attemptSilentRenewal(
  deps: AttemptSilentRenewalDeps = {},
): Promise<SilentRenewalOutcome> {
  const local = deps.localStorage ?? window.localStorage;
  if (local.getItem(STORAGE_HAD_SESSION) !== '1') {
    return { kind: 'skipped', reason: 'no-prior-session' };
  }
  if (get(store).accessToken !== null) {
    return { kind: 'skipped', reason: 'already-authenticated' };
  }
  const hash = deps.currentHash ?? window.location.hash;
  if (hash.startsWith('#/auth/callback')) {
    return { kind: 'skipped', reason: 'callback-in-progress' };
  }
  const session = deps.sessionStorage ?? window.sessionStorage;
  if (session.getItem(STORAGE_VERIFIER) !== null) {
    return { kind: 'skipped', reason: 'interactive-login-in-flight' };
  }

  const config = deps.config ?? (await loadOidcConfig());
  if (!config) {
    return { kind: 'skipped', reason: 'not-configured' };
  }

  const assign = deps.locationAssign ?? ((url) => window.location.assign(url));
  const verifier = generateCodeVerifier();
  const challenge = await deriveCodeChallenge(verifier);
  const state = generateState();
  const returnTo = hash || '#/';

  session.setItem(STORAGE_VERIFIER, verifier);
  session.setItem(STORAGE_STATE, state);
  session.setItem(STORAGE_RETURN_TO, returnTo);
  session.setItem(STORAGE_SILENT_RENEWAL, '1');

  const params = new URLSearchParams({
    response_type: 'code',
    client_id: config.clientId,
    redirect_uri: config.redirectUri,
    scope: 'openid email profile',
    state,
    code_challenge: challenge,
    code_challenge_method: 'S256',
    prompt: 'none',
  });
  assign(`${config.issuerUrl}/protocol/openid-connect/auth?${params.toString()}`);
  return { kind: 'redirecting' };
}

// Test-only helper to reset the in-memory store between cases.
export function __resetAuthStore(): void {
  store.set(initial);
}
