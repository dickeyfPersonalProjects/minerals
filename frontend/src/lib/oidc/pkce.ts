// PKCE primitives for the OAuth 2.0 Authorization Code + PKCE flow
// (RFC 7636). The frontend exchanges the code directly with Keycloak,
// so the code_verifier never leaves the browser.

const VERIFIER_BYTES = 32;
const STATE_BYTES = 16;

function base64UrlEncode(bytes: Uint8Array): string {
  let s = '';
  for (let i = 0; i < bytes.length; i++) {
    s += String.fromCharCode(bytes[i] as number);
  }
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function generateCodeVerifier(cryptoImpl: Crypto = globalThis.crypto): string {
  const bytes = new Uint8Array(VERIFIER_BYTES);
  cryptoImpl.getRandomValues(bytes);
  return base64UrlEncode(bytes);
}

export async function deriveCodeChallenge(
  verifier: string,
  subtle: SubtleCrypto = globalThis.crypto.subtle,
): Promise<string> {
  const data = new TextEncoder().encode(verifier);
  const digest = await subtle.digest('SHA-256', data);
  return base64UrlEncode(new Uint8Array(digest));
}

export function generateState(cryptoImpl: Crypto = globalThis.crypto): string {
  const bytes = new Uint8Array(STATE_BYTES);
  cryptoImpl.getRandomValues(bytes);
  return base64UrlEncode(bytes);
}
