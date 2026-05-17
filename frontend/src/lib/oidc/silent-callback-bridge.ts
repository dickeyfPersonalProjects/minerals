// Silent-renewal iframe bridge (mi-ct2).
//
// When the silent-renewal flow lands the hidden iframe back on
// `/auth/callback?...`, this module — imported first in main.ts —
// detects the iframe context, posts the query string up to the
// parent window, and aborts the SPA bootstrap. The parent (see
// `auth.ts#attemptSilentRenewal`) is listening on the same channel
// and finishes the token exchange. Without this short-circuit, the
// iframe would mount a second full copy of the SPA inside the
// hidden frame, race the parent's AuthCallback route, and burn the
// single-use PKCE verifier on the wrong side of the postMessage.
//
// MUST run BEFORE `path-to-hash.ts` and BEFORE Svelte mounts, since
// both rewrite the URL the bridge inspects (`/auth/callback`).

export const SILENT_RENEWAL_MESSAGE = 'minerals.oidc.silent-renewal';

declare global {
  interface Window {
    __mineralsSilentBridgeAborted?: boolean;
  }
}

if (
  typeof window !== 'undefined' &&
  window.parent !== window &&
  window.location.pathname === '/auth/callback'
) {
  // Same-origin parent — postMessage is unconditional and the parent
  // verifies origin + state before trusting anything.
  const params = window.location.search.replace(/^\?/, '');
  window.parent.postMessage({ type: SILENT_RENEWAL_MESSAGE, params }, window.location.origin);
  window.__mineralsSilentBridgeAborted = true;
}

export {};
