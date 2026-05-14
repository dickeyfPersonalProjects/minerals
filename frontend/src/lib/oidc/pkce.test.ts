import { describe, expect, it } from 'vitest';
import { deriveCodeChallenge, generateCodeVerifier, generateState } from './pkce';

// Fake crypto producing predictable bytes for verifier/state tests.
function fakeCrypto(byte: number): Crypto {
  return {
    getRandomValues: <T extends ArrayBufferView | null>(buf: T): T => {
      if (buf && 'length' in buf) {
        const view = buf as unknown as Uint8Array;
        view.fill(byte);
      }
      return buf;
    },
  } as unknown as Crypto;
}

describe('generateCodeVerifier', () => {
  it('produces a base64url-encoded string between 43 and 128 chars (RFC 7636)', () => {
    const verifier = generateCodeVerifier();
    expect(verifier.length).toBeGreaterThanOrEqual(43);
    expect(verifier.length).toBeLessThanOrEqual(128);
    // base64url alphabet only, no padding.
    expect(verifier).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it('is deterministic given fixed entropy', () => {
    const a = generateCodeVerifier(fakeCrypto(0));
    const b = generateCodeVerifier(fakeCrypto(0));
    expect(a).toBe(b);
  });

  it('varies with the underlying entropy', () => {
    expect(generateCodeVerifier(fakeCrypto(0))).not.toBe(generateCodeVerifier(fakeCrypto(255)));
  });
});

describe('deriveCodeChallenge', () => {
  // RFC 7636 §4.4 test vector: SHA256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
  // base64url-encoded → "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
  it('matches the RFC 7636 test vector', async () => {
    const challenge = await deriveCodeChallenge('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk');
    expect(challenge).toBe('E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM');
  });
});

describe('generateState', () => {
  it('produces a base64url string', () => {
    const state = generateState();
    expect(state).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(state.length).toBeGreaterThan(0);
  });
});
