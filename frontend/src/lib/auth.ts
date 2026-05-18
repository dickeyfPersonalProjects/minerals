// SPA auth state for the V2 BFF cookie flow (mi-1d5i / mi-3vc4).
//
// The SPA no longer drives any OAuth dance: the session cookie is
// HttpOnly so the browser cannot read it. To know whether a session
// is attached the SPA probes `GET /api/v1/profile` — 200 means the
// cookie resolved to a user, 401 means anonymous. The profile body
// (`display_name`, `email`, `id`) populates the UI; there are no
// JWT claims to decode any more.
//
// LoginButton is a plain `<a href="/auth/login">` and ProfileMenu's
// sign-out is a form POST to `/auth/logout`. Neither comes through
// this module — it owns state only.

import { derived, get, writable, type Readable, type Writable } from 'svelte/store';
import { client } from './api';
import type { components } from './api/schema';

export type AuthUser = components['schemas']['ProfileBody'];

export interface AuthState {
  readonly user: AuthUser | null;
  /**
   * True once a /api/v1/profile probe has resolved (either to a
   * user or to a 401). UI surfaces gate on the resulting `user`
   * value; the loaded flag exists so consumers can defer optimistic
   * rendering when needed.
   */
  readonly loaded: boolean;
}

const initial: AuthState = { user: null, loaded: false };
const store: Writable<AuthState> = writable(initial);

export const authStore: Readable<AuthState> = { subscribe: store.subscribe };

// True iff a profile probe resolved to a logged-in user. Components
// gate write CTAs on this (mi-eec) — anonymous users see a read-only
// UI because writes require auth (CONTRACT §13).
export const isAuthenticated: Readable<boolean> = derived(store, ($s) => $s.user !== null);

let inflight: Promise<AuthUser | null> | null = null;

/**
 * Probe `GET /api/v1/profile` to determine whether the browser has
 * an attached session. Populates the store with the profile body on
 * 200, clears it on 401. Concurrent callers share one round-trip;
 * errors (network / 5xx) leave the store unchanged but resolve the
 * promise to null so callers don't hang.
 */
export function probeAuth(): Promise<AuthUser | null> {
  if (inflight) return inflight;
  inflight = (async () => {
    try {
      const { data, response } = await client.GET('/api/v1/profile', {
        // Suppress the global toast on 401 — anonymous is a normal
        // state on app boot, not an error to flash at the user.
        headers: { 'x-suppress-toast': '1' },
      });
      if (response.status === 401) {
        store.set({ user: null, loaded: true });
        return null;
      }
      if (data) {
        store.set({ user: data, loaded: true });
        return data;
      }
      // Non-401 error path: don't claim authoritative state. Leave
      // the loaded flag where it was so a retry can populate it.
      return get(store).user;
    } catch {
      return get(store).user;
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

/**
 * Clear the in-memory user. Called by tests; production code does
 * not log out client-side — sign-out is a form POST to /auth/logout
 * and the resulting redirect tears the SPA down anyway.
 */
export function clearAuth(): void {
  store.set(initial);
}

// Test-only helper to reset the store between cases.
export function __resetAuthStore(): void {
  store.set(initial);
  inflight = null;
}

// Test-only seeding. Production code populates the store through
// probeAuth(); tests use this to bypass the fetch.
export function __setAuthUser(user: AuthUser | null): void {
  store.set({ user, loaded: true });
}

// Test-only convenience: seed the store with a default authenticated
// user. Tests that only care whether `$isAuthenticated` is true reach
// for this; tests that depend on a specific display_name / email use
// __setAuthUser with their own payload.
export function __authenticate(over: Partial<AuthUser> = {}): void {
  __setAuthUser({
    id: '00000000-0000-0000-0000-000000000001',
    display_name: 'Test User',
    email: 'test@example.com',
    pending: false,
    field_defaults: null as unknown as AuthUser['field_defaults'],
    ...over,
  });
}
