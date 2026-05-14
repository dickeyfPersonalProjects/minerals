import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';

const { mockHandleAuthCallback, mockReplace } = vi.hoisted(() => ({
  mockHandleAuthCallback: vi.fn(),
  mockReplace: vi.fn(),
}));

vi.mock('../lib/oidc/auth', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('../lib/oidc/auth');
  return { ...actual, handleAuthCallback: mockHandleAuthCallback };
});

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return { ...actual, replace: mockReplace };
});

import AuthCallback from './AuthCallback.svelte';

beforeEach(() => {
  mockHandleAuthCallback.mockReset();
  mockReplace.mockReset();
  window.location.hash = '#/auth/callback?code=C&state=S';
});

afterEach(() => {
  cleanup();
  window.location.hash = '';
});

describe('AuthCallback', () => {
  it('shows the busy state while the exchange is in flight', () => {
    mockHandleAuthCallback.mockImplementation(() => new Promise(() => {}));
    render(AuthCallback);
    expect(screen.getByTestId('auth-callback')).toHaveTextContent(/signing you in/i);
  });

  it('parses the query from the hash and calls handleAuthCallback', async () => {
    mockHandleAuthCallback.mockResolvedValue({ returnTo: '#/specimens' });
    render(AuthCallback);
    await waitFor(() => expect(mockHandleAuthCallback).toHaveBeenCalled());
    const callArgs = mockHandleAuthCallback.mock.calls[0];
    if (!callArgs) throw new Error('expected handleAuthCallback to be called');
    const query = callArgs[0] as URLSearchParams;
    expect(query.get('code')).toBe('C');
    expect(query.get('state')).toBe('S');
    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith('/specimens'));
  });

  it('renders the error message and a home link when the exchange fails', async () => {
    mockHandleAuthCallback.mockRejectedValue(new Error('Invalid state parameter'));
    render(AuthCallback);
    await waitFor(() => {
      expect(screen.getByTestId('auth-callback-error')).toHaveTextContent(
        'Invalid state parameter',
      );
    });
    expect(screen.getByTestId('auth-callback-home')).toHaveAttribute('href', '#/');
  });

  it('treats an empty returnTo as the home route', async () => {
    mockHandleAuthCallback.mockResolvedValue({ returnTo: '' });
    render(AuthCallback);
    await waitFor(() => expect(mockReplace).toHaveBeenCalledWith('/'));
  });
});
