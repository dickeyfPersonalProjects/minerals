// Unit tests for the silent-renewal building blocks (mi-ct2).
//
// The end-to-end iframe + postMessage flow is exercised by the
// Playwright spec in `frontend/e2e/auth.spec.ts`. These tests cover
// the bits that ARE testable without a real browser: URL
// construction, expiry-scheduling math, and postMessage payload
// parsing.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  __resetAuthStore,
  attemptSilentRenewal,
  authStore,
  buildSilentRenewalUrl,
  getAccessToken,
  nextSilentRenewalDelay,
  parseSilentRenewalMessage,
  SILENT_TIMEOUT_MS,
  setAccessToken,
} from './auth';
import { SILENT_RENEWAL_MESSAGE } from './silent-callback-bridge';
import type { OidcConfig } from './config';
import { get } from 'svelte/store';

const config: OidcConfig = {
  issuerUrl: 'https://auth.example.com/realms/minerals',
  clientId: 'minerals-frontend',
  redirectUri: 'https://www.example.com/auth/callback',
};

beforeEach(() => {
  __resetAuthStore();
});

afterEach(() => {
  __resetAuthStore();
  // Clean up any stray iframes left over from a test that errored.
  for (const f of Array.from(document.querySelectorAll('iframe'))) f.remove();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('buildSilentRenewalUrl', () => {
  it('targets the OIDC authorize endpoint with prompt=none and the PKCE params', () => {
    const url = new URL(buildSilentRenewalUrl(config, 'state-1', 'challenge-1'));
    expect(url.origin + url.pathname).toBe(
      'https://auth.example.com/realms/minerals/protocol/openid-connect/auth',
    );
    expect(url.searchParams.get('prompt')).toBe('none');
    expect(url.searchParams.get('response_type')).toBe('code');
    expect(url.searchParams.get('client_id')).toBe('minerals-frontend');
    expect(url.searchParams.get('redirect_uri')).toBe('https://www.example.com/auth/callback');
    expect(url.searchParams.get('scope')).toBe('openid email profile');
    expect(url.searchParams.get('state')).toBe('state-1');
    expect(url.searchParams.get('code_challenge')).toBe('challenge-1');
    expect(url.searchParams.get('code_challenge_method')).toBe('S256');
  });
});

describe('nextSilentRenewalDelay', () => {
  it('returns null when the token never expires', () => {
    expect(nextSilentRenewalDelay(null, 0)).toBeNull();
  });

  it('returns null when the token is already inside the lead window', () => {
    // expiresAt 10s out, lead 30s — we should renew immediately.
    expect(nextSilentRenewalDelay(10_000, 0, 30_000)).toBeNull();
    // expiresAt is in the past — likewise.
    expect(nextSilentRenewalDelay(-1, 0, 30_000)).toBeNull();
  });

  it('schedules at expiresAt - leadMs', () => {
    expect(nextSilentRenewalDelay(60_000, 0, 30_000)).toBe(30_000);
  });

  it('clamps short delays to at least one second so we do not tight-loop', () => {
    // expiresAt 30.1s out, lead 30s → target is 100 ms; clamp to 1 s.
    expect(nextSilentRenewalDelay(30_100, 0, 30_000)).toBe(1_000);
  });
});

describe('parseSilentRenewalMessage', () => {
  it('returns null for non-objects', () => {
    expect(parseSilentRenewalMessage(null)).toBeNull();
    expect(parseSilentRenewalMessage('string')).toBeNull();
    expect(parseSilentRenewalMessage(42)).toBeNull();
  });

  it('returns null when the type marker does not match', () => {
    expect(parseSilentRenewalMessage({ type: 'something.else', params: 'a=1' })).toBeNull();
  });

  it('returns null when params is missing or non-string', () => {
    expect(parseSilentRenewalMessage({ type: SILENT_RENEWAL_MESSAGE })).toBeNull();
    expect(parseSilentRenewalMessage({ type: SILENT_RENEWAL_MESSAGE, params: 42 })).toBeNull();
  });

  it('parses a well-formed message into URLSearchParams', () => {
    const params = parseSilentRenewalMessage({
      type: SILENT_RENEWAL_MESSAGE,
      params: 'code=C-1&state=S-1',
    });
    expect(params).not.toBeNull();
    expect(params?.get('code')).toBe('C-1');
    expect(params?.get('state')).toBe('S-1');
  });

  it('tolerates an empty params string (Keycloak error with no body)', () => {
    const params = parseSilentRenewalMessage({
      type: SILENT_RENEWAL_MESSAGE,
      params: '',
    });
    expect(params).not.toBeNull();
    expect(params?.get('code')).toBeNull();
  });
});

// Build a minimal Window/Document pair backed by JSDOM (the vitest
// `environment: 'jsdom'` default) so attemptSilentRenewal can append
// the hidden iframe, receive postMessage events, and resolve cleanly
// without a real Keycloak.
function makeIframeHarness(iframeSrcCapture: { src?: string }) {
  return {
    win: window,
    doc: document,
    appendListener: () => {
      const observer = new MutationObserver((mutations) => {
        for (const m of mutations) {
          for (const node of Array.from(m.addedNodes)) {
            if (node instanceof HTMLIFrameElement) {
              iframeSrcCapture.src = node.src;
            }
          }
        }
      });
      observer.observe(document.body, { childList: true });
      return () => observer.disconnect();
    },
  };
}

function postSilent(params: string): void {
  // Synchronous dispatch — JSDOM's `window.postMessage` queues the
  // MessageEvent in a way that can starve the awaiting test under
  // vitest's microtask scheduler. A direct dispatch with the right
  // origin string is equivalent for our same-origin listener.
  window.dispatchEvent(
    new MessageEvent('message', {
      data: { type: SILENT_RENEWAL_MESSAGE, params },
      origin: window.location.origin,
    }),
  );
}

async function waitForIframe(): Promise<HTMLIFrameElement> {
  // attemptSilentRenewal awaits PKCE challenge derivation before
  // appending the iframe. Poll briefly so tests don't race the
  // async chain.
  for (let i = 0; i < 50; i++) {
    const f = document.querySelector('iframe');
    if (f) return f as HTMLIFrameElement;
    await new Promise((r) => setTimeout(r, 5));
  }
  throw new Error('iframe never appeared');
}

function tokenResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

describe('attemptSilentRenewal', () => {
  it('exchanges the code for a token, populates the store, and removes the iframe', async () => {
    const harness = makeIframeHarness({});
    const stopObserving = harness.appendListener();
    const fetchStub = vi.fn(async () =>
      tokenResponse({ access_token: 'silent-tok-1', expires_in: 600 }),
    );

    const pending = attemptSilentRenewal({
      config,
      fetch: fetchStub as unknown as typeof fetch,
      now: () => 1_000,
    });

    // Grab the state Keycloak should echo back. The iframe src is
    // the only place we can read it (the verifier is private to the
    // closure).
    const iframe = await waitForIframe();
    const state = new URL(iframe.src).searchParams.get('state')!;
    expect(state.length).toBeGreaterThan(0);

    postSilent(`code=CODE-1&state=${state}`);
    const result = await pending;
    stopObserving();

    expect(result).toEqual({ ok: true });
    expect(getAccessToken(() => 1_000)).toBe('silent-tok-1');
    expect(get(authStore).expiresAt).toBe(1_000 + 600_000);
    expect(document.querySelector('iframe')).toBeNull();

    const call = fetchStub.mock.calls[0];
    expect(call).toBeDefined();
    const [url, init] = call as unknown as [string, RequestInit];
    expect(url).toBe('https://auth.example.com/realms/minerals/protocol/openid-connect/token');
    const body = new URLSearchParams(init.body as string);
    expect(body.get('grant_type')).toBe('authorization_code');
    expect(body.get('code')).toBe('CODE-1');
  });

  it('fails fast with login-required when Keycloak says the SSO session is gone', async () => {
    const fetchStub = vi.fn();
    const pending = attemptSilentRenewal({
      config,
      fetch: fetchStub as unknown as typeof fetch,
    });
    const iframe = await waitForIframe();
    const state = new URL(iframe.src).searchParams.get('state')!;

    postSilent(`error=login_required&state=${state}`);
    const result = await pending;

    expect(result).toEqual({
      ok: false,
      reason: 'login-required',
      detail: 'login_required',
    });
    expect(getAccessToken()).toBeNull();
    expect(fetchStub).not.toHaveBeenCalled();
    expect(document.querySelector('iframe')).toBeNull();
  });

  it('rejects a message whose state does not match the issued one', async () => {
    const fetchStub = vi.fn();
    const pending = attemptSilentRenewal({
      config,
      fetch: fetchStub as unknown as typeof fetch,
    });
    await waitForIframe();

    postSilent('code=ATTACKER&state=NOT-OURS');
    const result = await pending;

    expect(result).toEqual({ ok: false, reason: 'state-mismatch' });
    expect(fetchStub).not.toHaveBeenCalled();
    expect(getAccessToken()).toBeNull();
  });

  it('times out when the iframe never resolves', async () => {
    vi.useFakeTimers();
    const fetchStub = vi.fn();
    const pending = attemptSilentRenewal({
      config,
      fetch: fetchStub as unknown as typeof fetch,
      timeoutMs: 250,
    });
    await vi.advanceTimersByTimeAsync(300);
    const result = await pending;
    expect(result).toEqual({ ok: false, reason: 'timeout' });
    expect(document.querySelector('iframe')).toBeNull();
  });

  it('returns no-config when OIDC is not wired up', async () => {
    const result = await attemptSilentRenewal({ config: null });
    expect(result).toEqual({ ok: false, reason: 'no-config' });
    // No iframe should have been appended.
    expect(document.querySelector('iframe')).toBeNull();
  });

  it('surfaces a token-error when the token endpoint rejects the code', async () => {
    const fetchStub = vi.fn(async () =>
      tokenResponse({ error: 'invalid_grant', error_description: 'bad code' }, 400),
    );
    const pending = attemptSilentRenewal({
      config,
      fetch: fetchStub as unknown as typeof fetch,
    });
    const iframe = await waitForIframe();
    const state = new URL(iframe.src).searchParams.get('state')!;
    postSilent(`code=BADCODE&state=${state}`);
    const result = await pending;
    expect(result).toEqual({ ok: false, reason: 'token-error' });
    expect(getAccessToken()).toBeNull();
  });
});

describe('silent renewal scheduling', () => {
  it('arms a timer on full-login completion', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(0);
    // Drive the schedule path via setAccessToken + a manual schedule
    // call (the same path handleAuthCallback follows).
    setAccessToken('tok', 600, () => 0);
    // Re-import to grab the freshly evaluated module's scheduler.
    const auth = await import('./auth');
    auth.scheduleSilentRenewal({ now: () => 0 });
    // Token expires in 600s; lead is 30s → timer should fire at 570s.
    // We do NOT await renewal here (that would actually mount an
    // iframe) — just assert the math by checking timer pending.
    const remaining = vi.getTimerCount();
    expect(remaining).toBeGreaterThan(0);
    auth.cancelSilentRenewal();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('SILENT_TIMEOUT_MS is a sane default', () => {
    expect(SILENT_TIMEOUT_MS).toBeGreaterThan(0);
    expect(SILENT_TIMEOUT_MS).toBeLessThanOrEqual(30_000);
  });
});
