import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';

const { mockBeginLogout, mockPush } = vi.hoisted(() => ({
  mockBeginLogout: vi.fn(),
  mockPush: vi.fn(),
}));

vi.mock('./oidc/auth', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('./oidc/auth');
  return { ...actual, beginLogout: mockBeginLogout };
});

vi.mock('svelte-spa-router', () => ({ push: mockPush }));

import ProfileMenu from './ProfileMenu.svelte';
import { __resetAuthStore, setAccessToken } from './oidc/auth';

function makeJwt(payload: Record<string, unknown>): string {
  const b64 = (o: unknown) => Buffer.from(JSON.stringify(o)).toString('base64url');
  return `${b64({ alg: 'RS256', typ: 'JWT' })}.${b64(payload)}.signature`;
}

function authenticateAs(payload: Record<string, unknown>): void {
  setAccessToken(makeJwt(payload), 600, () => 0);
}

beforeEach(() => {
  __resetAuthStore();
  mockBeginLogout.mockReset();
  mockPush.mockReset();
  mockBeginLogout.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  __resetAuthStore();
});

describe('ProfileMenu', () => {
  it('renders initials derived from the JWT name claim', () => {
    authenticateAs({ name: 'Ada Lovelace', preferred_username: 'ada' });
    render(ProfileMenu);
    expect(screen.getByTestId('profile-menu-button')).toHaveTextContent('AL');
  });

  it('falls back to the username when no name claim is present', () => {
    authenticateAs({ preferred_username: 'grace' });
    render(ProfileMenu);
    expect(screen.getByTestId('profile-menu-button')).toHaveTextContent('GR');
  });

  it('renders a generic icon when no name or username claim is present', () => {
    authenticateAs({ sub: 'abc-123' });
    render(ProfileMenu);
    const button = screen.getByTestId('profile-menu-button');
    expect(button).toHaveTextContent('');
    expect(button.querySelector('svg')).not.toBeNull();
  });

  it('keeps the dropdown closed until the button is clicked', async () => {
    authenticateAs({ name: 'Ada Lovelace' });
    render(ProfileMenu);
    expect(screen.queryByTestId('profile-menu')).not.toBeInTheDocument();
    screen.getByTestId('profile-menu-button').click();
    expect(await screen.findByTestId('profile-menu')).toBeInTheDocument();
  });

  it('navigates to the profile page and closes the menu on Profile click', async () => {
    authenticateAs({ name: 'Ada Lovelace' });
    render(ProfileMenu);
    screen.getByTestId('profile-menu-button').click();
    (await screen.findByTestId('profile-menu-profile')).click();
    expect(mockPush).toHaveBeenCalledWith('/profile/setup');
    await vi.waitFor(() => {
      expect(screen.queryByTestId('profile-menu')).not.toBeInTheDocument();
    });
  });

  it('triggers beginLogout when Sign out is clicked', async () => {
    authenticateAs({ name: 'Ada Lovelace' });
    render(ProfileMenu);
    screen.getByTestId('profile-menu-button').click();
    (await screen.findByTestId('profile-menu-signout')).click();
    expect(mockBeginLogout).toHaveBeenCalledOnce();
  });
});
