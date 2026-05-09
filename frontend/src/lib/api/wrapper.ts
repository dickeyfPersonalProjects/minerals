// Toast-aware wrapper around the typed API client (E-4).
//
// Pulls the duplicated `envelopeMessage` helper out of the route
// modules and adds `toastApiError`, which is the natural shape
// callers want when they don't have field-scoped UX for the
// failure: surface the error envelope's `message` (CONTRACT.md
// §10) as a toast.
//
// Callers that need to discriminate on the error envelope (e.g.
// 409 → duplicate catalog number → inline field error) keep
// using `client` directly and decide whether to toast.

import { toastError } from '../stores/toasts';

export interface ApiErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
    details?: Record<string, unknown>;
  };
}

/**
 * Render an error envelope to a single human-readable string.
 *
 * Order: `error.message` (server-rendered, the contract's
 * intended end-user copy hint) → `error.code` (snake_case stable
 * identifier) → `HTTP <status>` as a last resort. Mirrors the
 * inline helper that previously lived in every route file.
 */
export function envelopeMessage(error: ApiErrorEnvelope | undefined, status: number): string {
  return error?.error?.message || error?.error?.code || `HTTP ${status}`;
}

/**
 * Surface an API error envelope as an error toast. Use this for
 * submit-level / catch-all errors where there's no field-scoped
 * UX. Field-scoped errors stay inline next to the offending
 * input.
 */
export function toastApiError(error: ApiErrorEnvelope | undefined, status: number): void {
  toastError(envelopeMessage(error, status));
}
