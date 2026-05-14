// API client wrapper — auto-toast on unhandled errors (E-4).
//
// Installs an openapi-fetch middleware on the existing `client`
// that fires a toast for every non-2xx response, surfacing the
// §10 error envelope's `message` (or `code` / status fallback).
//
// Suppression: pass `headers: {'x-suppress-toast': '1'}` on a
// per-call basis to opt out — used by callers that already
// surface the error via field-specific UI (e.g. catalog_number
// duplicate) and don't want a redundant global toast.
//
// Importing this module installs the middleware as a side effect.
// `main.ts` imports it once at startup so every API call is
// covered without callers needing to wire anything.

import { push } from 'svelte-spa-router';
import { client } from './index';
import { toastError } from '../toasts';

const SUPPRESS_HEADER = 'x-suppress-toast';

export interface ErrorEnvelope {
  error?: { code?: string; message?: string; details?: Record<string, unknown> };
}

// extractRedirect pulls the `redirect` hint out of a §10 error
// envelope's `details` map when present. Used by the auto-redirect
// handler on 403 responses (first-login profile gate, mi-2hf) — the
// backend signals "send the user here" without the SPA needing a
// 403-specific call site at every fetch.
function extractRedirect(envelope: ErrorEnvelope | undefined): string | undefined {
  const r = envelope?.error?.details?.redirect;
  return typeof r === 'string' ? r : undefined;
}

// ProfileSetupPath is the SPA route the backend sends pending
// users to. Shared between the redirect middleware and the
// ProfileSetup page so the two agree on the path.
export const ProfileSetupPath = '/profile/setup';

// STORAGE_POST_SETUP_RETURN is where the redirect handler stashes
// the user's current hash route before bouncing them to profile
// setup (mi-2hf). The setup page reads it on success so the user
// lands back where they were trying to go. sessionStorage (not
// memory) survives the page that triggered the redirect being
// torn down by svelte-spa-router on navigation.
const STORAGE_POST_SETUP_RETURN = 'minerals.profile.return_to';

// navigateHashRoute pushes a SPA hash route. `path` is the
// backend-supplied path (e.g. `/profile/setup`); svelte-spa-router's
// `push` is hash-based, so it converts `/x` to `#/x`. When bouncing
// to profile setup, stash the current hash so the setup page can
// restore it after the user completes the gate — but never
// overwrite an existing stashed value, so the first protected
// request the SPA makes after login owns the return target.
function navigateHashRoute(path: string): void {
  if (path === ProfileSetupPath && typeof window !== 'undefined') {
    try {
      const existing = window.sessionStorage.getItem(STORAGE_POST_SETUP_RETURN);
      if (!existing) {
        const hash = window.location.hash;
        // Skip when we're already on the setup page or have no
        // useful return target.
        if (hash && hash !== '#' + ProfileSetupPath && hash !== '#/') {
          window.sessionStorage.setItem(STORAGE_POST_SETUP_RETURN, hash);
        }
      }
    } catch {
      // Storage access can throw in private modes / SSR — the
      // navigation must still happen, so swallow.
    }
  }
  void push(path);
}

// readPostSetupReturn pops the stashed return-to value. The SPA
// reads this after a successful POST /api/v1/profile so the user
// lands back at their original destination instead of the home
// route. Returns null when nothing is stashed.
export function readPostSetupReturn(): string | null {
  if (typeof window === 'undefined') return null;
  try {
    const v = window.sessionStorage.getItem(STORAGE_POST_SETUP_RETURN);
    window.sessionStorage.removeItem(STORAGE_POST_SETUP_RETURN);
    return v;
  } catch {
    return null;
  }
}

/**
 * Convert a §10 error envelope (from openapi-fetch's `error` field)
 * into a single human-readable string suitable for toasts or
 * inline banners. Falls back to code, then status.
 */
export function envelopeMessage(error: ErrorEnvelope | undefined, status: number): string {
  return error?.error?.message || error?.error?.code || `HTTP ${status}`;
}

async function safeReadEnvelope(response: Response): Promise<ErrorEnvelope | undefined> {
  // Only attempt JSON parse for typical envelope payloads. We
  // clone so the original response body remains readable by
  // openapi-fetch's downstream consumer.
  try {
    const ct = response.headers.get('content-type') ?? '';
    if (!ct.includes('json')) return undefined;
    return (await response.clone().json()) as ErrorEnvelope;
  } catch {
    return undefined;
  }
}

let installed = false;

export function installToastMiddleware(): void {
  if (installed) return;
  installed = true;
  client.use({
    async onResponse({ request, response }) {
      if (response.ok) return;
      const suppressed = request.headers.get(SUPPRESS_HEADER) === '1';
      const envelope = await safeReadEnvelope(response);

      // First-login gate (mi-2hf): a 403 carrying `details.redirect`
      // means the backend wants the SPA to navigate. Honored even
      // when the toast is suppressed — the redirect is the action,
      // not a notification — and the toast is skipped because the
      // navigation already communicates the state.
      if (response.status === 403) {
        const redirect = extractRedirect(envelope);
        if (redirect) {
          navigateHashRoute(redirect);
          return;
        }
      }

      if (suppressed) return;
      toastError(envelopeMessage(envelope, response.status));
    },
    onError({ request, error }) {
      // Network failures / aborts surface here. Same suppression
      // contract as onResponse.
      if (request.headers.get(SUPPRESS_HEADER) === '1') return;
      const message = error instanceof Error ? error.message : String(error);
      toastError(message);
    },
  });
}

// Header constant exported so callers can reference it without
// hard-coding the literal string.
export const SUPPRESS_TOAST_HEADERS = { [SUPPRESS_HEADER]: '1' } as const;
