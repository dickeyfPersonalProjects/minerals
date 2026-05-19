import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';

const { mockPush, mockAssign } = vi.hoisted(() => ({
  mockPush: vi.fn(),
  mockAssign: vi.fn(),
}));

vi.mock('svelte-spa-router', () => ({ push: mockPush }));

import ProfileMenu from './ProfileMenu.svelte';
import { __authenticate, __resetAuthStore } from './auth';
import { __resetCsrf, __setCsrf } from './csrf';

let originalFetch: typeof fetch;
let originalLocation: Location;

beforeEach(() => {
  __resetAuthStore();
  __resetCsrf();
  mockPush.mockReset();
  mockAssign.mockReset();
  originalFetch = globalThis.fetch;
  // jsdom's location.assign is non-configurable AND throws
  // "Not implemented" when called, so neither vi.spyOn nor
  // defineProperty(window.location, ...) work. Replace the entire
  // `window.location` object instead — keeps every other property
  // intact while routing assign() into our mock.
  originalLocation = window.location;
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: Object.assign({}, originalLocation, { assign: mockAssign }),
  });
});

afterEach(() => {
  cleanup();
  __resetAuthStore();
  __resetCsrf();
  globalThis.fetch = originalFetch;
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: originalLocation,
  });
});

describe('ProfileMenu (V2 BFF cookie flow, mi-3vc4)', () => {
  it('renders initials derived from the display_name', () => {
    __authenticate({ display_name: 'Ada Lovelace' });
    render(ProfileMenu);
    expect(screen.getByTestId('profile-menu-button')).toHaveTextContent('AL');
  });

  it('falls back to the email when no display_name is present', () => {
    __authenticate({ display_name: '', email: 'grace@example.com' });
    render(ProfileMenu);
    expect(screen.getByTestId('profile-menu-button')).toHaveTextContent('GR');
  });

  it('renders a generic icon when neither display_name nor email is present', () => {
    __authenticate({ display_name: '', email: '' });
    render(ProfileMenu);
    const button = screen.getByTestId('profile-menu-button');
    expect(button).toHaveTextContent('');
    expect(button.querySelector('svg')).not.toBeNull();
  });

  it('keeps the dropdown closed until the button is clicked', async () => {
    __authenticate({ display_name: 'Ada Lovelace' });
    render(ProfileMenu);
    expect(screen.queryByTestId('profile-menu')).not.toBeInTheDocument();
    screen.getByTestId('profile-menu-button').click();
    expect(await screen.findByTestId('profile-menu')).toBeInTheDocument();
  });

  it('navigates to the profile page and closes the menu on Profile click', async () => {
    __authenticate({ display_name: 'Ada Lovelace' });
    render(ProfileMenu);
    screen.getByTestId('profile-menu-button').click();
    (await screen.findByTestId('profile-menu-profile')).click();
    expect(mockPush).toHaveBeenCalledWith('/profile');
    await waitFor(() => {
      expect(screen.queryByTestId('profile-menu')).not.toBeInTheDocument();
    });
  });

  it('navigates to the settings page and closes the menu on Settings click (mi-1ygd)', async () => {
    __authenticate({ display_name: 'Ada Lovelace' });
    render(ProfileMenu);
    screen.getByTestId('profile-menu-button').click();
    (await screen.findByTestId('profile-menu-settings')).click();
    expect(mockPush).toHaveBeenCalledWith('/settings');
    await waitFor(() => {
      expect(screen.queryByTestId('profile-menu')).not.toBeInTheDocument();
    });
  });

  it('POSTs /auth/logout with the X-CSRF-Token header and reboots the SPA', async () => {
    __authenticate({ display_name: 'Ada Lovelace' });
    __setCsrf('token-from-csrf-store');
    const fetchStub = vi.fn(async () => new Response(null, { status: 204 }));
    globalThis.fetch = fetchStub as unknown as typeof fetch;

    render(ProfileMenu);
    screen.getByTestId('profile-menu-button').click();
    (await screen.findByTestId('profile-menu-signout')).click();

    await waitFor(() => expect(fetchStub).toHaveBeenCalledTimes(1));
    const [url, init] = (fetchStub.mock.calls[0] ?? []) as unknown as [
      string,
      RequestInit | undefined,
    ];
    expect(url).toBe('/auth/logout');
    expect(init?.method).toBe('POST');
    expect(init?.credentials).toBe('include');
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.['X-CSRF-Token']).toBe('token-from-csrf-store');

    await waitFor(() => expect(mockAssign).toHaveBeenCalledWith('/'));
  });

  it('still reboots when the CSRF store is empty (best-effort logout)', async () => {
    __authenticate({ display_name: 'Ada Lovelace' });
    const fetchStub = vi.fn(async () => new Response(null, { status: 403 }));
    globalThis.fetch = fetchStub as unknown as typeof fetch;

    render(ProfileMenu);
    screen.getByTestId('profile-menu-button').click();
    (await screen.findByTestId('profile-menu-signout')).click();

    await waitFor(() => expect(fetchStub).toHaveBeenCalledTimes(1));
    const [, init] = (fetchStub.mock.calls[0] ?? []) as unknown as [
      string,
      RequestInit | undefined,
    ];
    // No CSRF in the store → no header attached.
    expect(init?.headers).toBeUndefined();
    // Even on 403 the SPA reboots — the cookie is HttpOnly so the
    // best we can do is bounce to home and let the next probe show
    // current truth.
    await waitFor(() => expect(mockAssign).toHaveBeenCalledWith('/'));
  });
});
