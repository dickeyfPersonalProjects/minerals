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

import { client } from './index';
import { toastError } from '../toasts';

const SUPPRESS_HEADER = 'x-suppress-toast';

export interface ErrorEnvelope {
  error?: { code?: string; message?: string; details?: unknown };
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
      if (request.headers.get(SUPPRESS_HEADER) === '1') return;
      const envelope = await safeReadEnvelope(response);
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
