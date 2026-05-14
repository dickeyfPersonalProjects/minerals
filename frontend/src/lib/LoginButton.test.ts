import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { _clearToasts, toasts } from './toasts';
import { get } from 'svelte/store';

const { mockBeginLogin } = vi.hoisted(() => ({ mockBeginLogin: vi.fn() }));

vi.mock('./oidc/auth', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('./oidc/auth');
  return { ...actual, beginLogin: mockBeginLogin };
});

import LoginButton from './LoginButton.svelte';

beforeEach(() => {
  mockBeginLogin.mockReset();
  _clearToasts();
});

afterEach(() => {
  cleanup();
  _clearToasts();
});

describe('LoginButton', () => {
  it('renders with accessible label and triggers beginLogin on click', async () => {
    mockBeginLogin.mockResolvedValue(undefined);
    render(LoginButton);
    const btn = screen.getByTestId('login-button');
    expect(btn).toHaveTextContent(/log in/i);
    btn.click();
    expect(mockBeginLogin).toHaveBeenCalledOnce();
  });

  it('surfaces a toast when beginLogin throws', async () => {
    mockBeginLogin.mockRejectedValue(new Error('OIDC is not configured'));
    render(LoginButton);
    screen.getByTestId('login-button').click();
    await vi.waitFor(() => {
      expect(get(toasts)).toHaveLength(1);
    });
    expect(get(toasts)[0]?.message).toBe('OIDC is not configured');
  });

  it('ignores repeat clicks while a login is in flight', async () => {
    let resolveFn: (() => void) | null = null;
    mockBeginLogin.mockImplementation(
      () =>
        new Promise<void>((res) => {
          resolveFn = res;
        }),
    );
    render(LoginButton);
    const btn = screen.getByTestId('login-button');
    btn.click();
    btn.click();
    btn.click();
    expect(mockBeginLogin).toHaveBeenCalledTimes(1);
    if (resolveFn) (resolveFn as () => void)();
  });
});
