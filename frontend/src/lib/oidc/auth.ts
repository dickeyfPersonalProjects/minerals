// OIDC Authorization Code + PKCE login flow. The browser drives the
// whole dance — it generates the verifier, redirects to Keycloak,
// then POSTs back to the token endpoint directly. No backend
// intermediary; the backend only validates the resulting bearer
// token via Keycloak's JWKS endpoint.
//
// Tokens live in memory only. `sessionStorage` holds the PKCE
// verifier, the CSRF `state` value, and the post-login return path,
// because those need to survive the Keycloak redirect round-trip.
// In-memory tokens disappear on page reload, but silent renewal
// (mi-ct2, `attemptSilentRenewal` below) re-mints one without UI
// from Keycloak's still-live SSO session, so the user does not get
// logged out on refresh.

import { derived, writable, get, type Readable, type Writable } from 'svelte/store';
import { deriveCodeChallenge, generateCodeVerifier, generateState } from './pkce';
import { loadOidcConfig, type OidcConfig } from './config';
import { SILENT_RENEWAL_MESSAGE } from './silent-callback-bridge';

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
  cancelSilentRenewal();
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

  clearAuth();

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
  // Arm the silent-renewal timer so the user is not logged out when
  // this freshly minted token approaches expiry (mi-ct2).
  scheduleSilentRenewal({ now: deps.now });
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

// ── Silent renewal (mi-ct2) ────────────────────────────────────────
//
// Page reload drops the in-memory token. Rather than punt the user
// back to the Login button, we mount a hidden iframe at Keycloak's
// authorize endpoint with `prompt=none`. Keycloak sees its own SSO
// session cookie on its origin, mints a fresh code without prompting
// the user, and 302s the iframe back to our `redirect_uri`. The
// silent-callback-bridge module then postMessages the query string
// back to this window; we exchange the code for a token here.
//
// On any failure (login_required, interaction_required, network,
// timeout) we resolve `{ ok: false }` — the user falls back to the
// Login button. We never throw an error toast for an SSO-expired
// session; that is the expected fallback path.
//
// We schedule a follow-up renewal a few seconds before token expiry
// so the user does not get logged out mid-session.

export const SILENT_TIMEOUT_MS = 10_000;
const SILENT_RENEW_LEAD_MS = 30_000;

export type SilentRenewalReason =
  | 'no-config'
  | 'window-unavailable'
  | 'login-required'
  | 'state-mismatch'
  | 'timeout'
  | 'token-error'
  | 'oauth-error';

export interface SilentRenewalSuccess {
  readonly ok: true;
}

export interface SilentRenewalFailure {
  readonly ok: false;
  readonly reason: SilentRenewalReason;
  readonly detail?: string;
}

export type SilentRenewalResult = SilentRenewalSuccess | SilentRenewalFailure;

/**
 * Build the URL of the `prompt=none` authorize request mounted in the
 * hidden iframe. Exposed for unit testing — production callers go
 * through `attemptSilentRenewal`.
 */
export function buildSilentRenewalUrl(
  config: OidcConfig,
  state: string,
  codeChallenge: string,
): string {
  const params = new URLSearchParams({
    response_type: 'code',
    client_id: config.clientId,
    redirect_uri: config.redirectUri,
    scope: 'openid email profile',
    state,
    code_challenge: codeChallenge,
    code_challenge_method: 'S256',
    prompt: 'none',
  });
  return `${config.issuerUrl}/protocol/openid-connect/auth?${params.toString()}`;
}

/**
 * Compute the delay (ms) before the next silent renewal should fire.
 * Renews `SILENT_RENEW_LEAD_MS` before `expiresAt`, but never sooner
 * than 1s from now (so a clock skew or very short token does not
 * cause a tight loop). Returns null when the token never expires or
 * is already past its lead window, in which case the caller should
 * renew immediately rather than schedule.
 */
export function nextSilentRenewalDelay(
  expiresAt: number | null,
  now: number = Date.now(),
  leadMs: number = SILENT_RENEW_LEAD_MS,
): number | null {
  if (expiresAt === null) return null;
  const target = expiresAt - leadMs;
  if (target <= now) return null;
  const delay = target - now;
  return Math.max(delay, 1_000);
}

/**
 * Parse the params object the silent-callback-bridge posts up from
 * the hidden iframe. Accepts the raw `params` string (the query
 * portion of `/auth/callback?...` without the leading `?`) and
 * returns a URLSearchParams view, or null when the payload is
 * structurally invalid.
 */
export function parseSilentRenewalMessage(data: unknown): URLSearchParams | null {
  if (!data || typeof data !== 'object') return null;
  const obj = data as { type?: unknown; params?: unknown };
  if (obj.type !== SILENT_RENEWAL_MESSAGE) return null;
  if (typeof obj.params !== 'string') return null;
  return new URLSearchParams(obj.params);
}

export interface SilentRenewalDeps {
  config?: OidcConfig | null;
  document?: Document;
  window?: Window;
  fetch?: typeof fetch;
  timeoutMs?: number;
  now?: () => number;
}

/**
 * Run the full silent renewal: hidden iframe → postMessage → token
 * exchange. Resolves to `{ ok: true }` on a freshly minted token in
 * the authStore, or `{ ok: false, reason }` on any failure path.
 * The hidden iframe is always removed before this resolves.
 */
export async function attemptSilentRenewal(
  deps: SilentRenewalDeps = {},
): Promise<SilentRenewalResult> {
  const win = deps.window ?? (typeof window !== 'undefined' ? window : undefined);
  const doc = deps.document ?? win?.document;
  if (!win || !doc) {
    return { ok: false, reason: 'window-unavailable' };
  }
  const resolved = deps.config ?? (await loadOidcConfig());
  if (!resolved) {
    return { ok: false, reason: 'no-config' };
  }
  const config: OidcConfig = resolved;
  const fetchFn = deps.fetch ?? globalThis.fetch.bind(globalThis);
  const timeoutMs = deps.timeoutMs ?? SILENT_TIMEOUT_MS;

  const verifier = generateCodeVerifier();
  const challenge = await deriveCodeChallenge(verifier);
  const state = generateState();
  const url = buildSilentRenewalUrl(config, state, challenge);

  const iframe = doc.createElement('iframe');
  iframe.setAttribute('aria-hidden', 'true');
  iframe.style.position = 'absolute';
  iframe.style.width = '0';
  iframe.style.height = '0';
  iframe.style.border = '0';
  iframe.style.visibility = 'hidden';
  iframe.src = url;

  return new Promise<SilentRenewalResult>((resolve) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const expectedOrigin = win.location.origin;

    const cleanup = (): void => {
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
      win.removeEventListener('message', onMessage);
      if (iframe.parentNode) iframe.parentNode.removeChild(iframe);
    };

    const finish = (result: SilentRenewalResult): void => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve(result);
    };

    function onMessage(event: MessageEvent): void {
      // Same-origin only — the bridge module posts with our exact
      // origin and Keycloak (cross-origin) cannot impersonate it.
      if (event.origin !== expectedOrigin) return;
      const params = parseSilentRenewalMessage(event.data);
      if (!params) return;

      const oauthError = params.get('error');
      if (oauthError) {
        const reason: SilentRenewalReason =
          oauthError === 'login_required' || oauthError === 'interaction_required'
            ? 'login-required'
            : 'oauth-error';
        finish({ ok: false, reason, detail: oauthError });
        return;
      }

      const code = params.get('code');
      const echoedState = params.get('state');
      if (!code) {
        finish({ ok: false, reason: 'oauth-error', detail: 'missing code' });
        return;
      }
      if (echoedState !== state) {
        finish({ ok: false, reason: 'state-mismatch' });
        return;
      }

      void exchangeSilentCode(config, code, verifier, fetchFn, deps.now).then(
        (ok) => {
          if (ok) scheduleSilentRenewal(deps);
          finish(ok ? { ok: true } : { ok: false, reason: 'token-error' });
        },
        () => finish({ ok: false, reason: 'token-error' }),
      );
    }

    win.addEventListener('message', onMessage);
    timer = setTimeout(() => {
      finish({ ok: false, reason: 'timeout' });
    }, timeoutMs);

    doc.body.appendChild(iframe);
  });
}

async function exchangeSilentCode(
  config: OidcConfig,
  code: string,
  verifier: string,
  fetchFn: typeof fetch,
  now?: () => number,
): Promise<boolean> {
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
  if (!response.ok) return false;
  const json = (await response.json()) as {
    access_token?: unknown;
    expires_in?: unknown;
  };
  if (typeof json.access_token !== 'string' || typeof json.expires_in !== 'number') {
    return false;
  }
  setAccessToken(json.access_token, json.expires_in, now);
  return true;
}

// Scheduling state — module-singleton because there is exactly one
// auth store and one renewal timer per browser tab.
let renewalTimer: ReturnType<typeof setTimeout> | null = null;

/**
 * (Re)arm the periodic silent renewal so a fresh token is minted
 * `SILENT_RENEW_LEAD_MS` before the current one expires. Calling
 * this with no active token cancels any pending renewal. Safe to
 * call repeatedly; the previous timer is always cleared first.
 */
export function scheduleSilentRenewal(deps: SilentRenewalDeps = {}): void {
  if (renewalTimer !== null) {
    clearTimeout(renewalTimer);
    renewalTimer = null;
  }
  const win = deps.window ?? (typeof window !== 'undefined' ? window : undefined);
  if (!win) return;
  const nowFn = deps.now ?? Date.now;
  const { expiresAt } = get(store);
  if (expiresAt === null) return;
  const delay = nextSilentRenewalDelay(expiresAt, nowFn());
  if (delay === null) return;
  renewalTimer = setTimeout(() => {
    renewalTimer = null;
    void attemptSilentRenewal(deps).then((result) => {
      if (result.ok) scheduleSilentRenewal(deps);
    });
  }, delay);
}

/** Cancel any pending scheduled silent renewal. Used on logout. */
export function cancelSilentRenewal(): void {
  if (renewalTimer !== null) {
    clearTimeout(renewalTimer);
    renewalTimer = null;
  }
}

// Test-only helper to reset the in-memory store between cases.
export function __resetAuthStore(): void {
  store.set(initial);
  cancelSilentRenewal();
}
