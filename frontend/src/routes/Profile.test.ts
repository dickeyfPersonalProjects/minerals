import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { toasts, _clearToasts } from '../lib/toasts';

const { mockPATCH } = vi.hoisted(() => ({
  mockPATCH: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { PATCH: mockPATCH },
}));

import Profile from './Profile.svelte';
import { __authenticate, __resetAuthStore, authStore } from '../lib/auth';

function profileBody(over: { display_name?: string; email?: string } = {}) {
  return {
    id: 'user-1',
    email: over.email ?? 'ada@example.com',
    display_name: over.display_name ?? 'Ada Lovelace',
    pending: false,
    field_defaults: {},
  };
}

beforeEach(() => {
  mockPATCH.mockReset();
  _clearToasts();
  __resetAuthStore();
});

afterEach(() => {
  cleanup();
  __resetAuthStore();
});

describe('Profile route', () => {
  it('renders heading, editable Name pre-filled from the auth store, and read-only Email', () => {
    __authenticate({ display_name: 'Ada Lovelace', email: 'ada@example.com' });
    render(Profile);

    expect(screen.getByRole('heading', { name: 'Profile' })).toBeInTheDocument();

    const name = screen.getByTestId('profile-display-name') as HTMLInputElement;
    expect(name.value).toBe('Ada Lovelace');
    expect(name.readOnly).toBe(false);

    const email = screen.getByTestId('profile-email') as HTMLInputElement;
    expect(email.value).toBe('ada@example.com');
    // Email is non-editable — both readonly and disabled are set so
    // the field is visibly inert and cannot be focused/typed into.
    expect(email.readOnly).toBe(true);
    expect(email.disabled).toBe(true);
  });

  it('shows the future-capability note next to the read-only email', () => {
    __authenticate();
    render(Profile);
    expect(screen.getByText(/email changes coming soon/i)).toBeInTheDocument();
  });

  it('does not render the field-defaults section (moved to /settings in mi-1ygd)', () => {
    __authenticate();
    render(Profile);
    expect(screen.queryByTestId('profile-field-defaults-form')).not.toBeInTheDocument();
    expect(screen.queryByTestId('profile-default-price')).not.toBeInTheDocument();
    expect(screen.queryByText('Field defaults')).not.toBeInTheDocument();
  });

  it('disables Save until the Name changes, then PATCHes and refreshes auth store on success', async () => {
    __authenticate({ display_name: 'Ada', email: 'ada@example.com' });
    render(Profile);

    const save = screen.getByTestId('profile-save') as HTMLButtonElement;
    expect(save.disabled).toBe(true);

    const name = screen.getByTestId('profile-display-name') as HTMLInputElement;
    await fireEvent.input(name, { target: { value: 'Ada Lovelace' } });
    await waitFor(() => expect(save.disabled).toBe(false));

    mockPATCH.mockResolvedValue({
      data: profileBody({ display_name: 'Ada Lovelace' }),
      error: undefined,
    });
    await fireEvent.click(save);

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [path, opts] = mockPATCH.mock.calls[0] as [
      string,
      { body: { display_name: string }; headers: Record<string, string> },
    ];
    expect(path).toBe('/api/v1/profile');
    expect(opts.body).toEqual({ display_name: 'Ada Lovelace' });
    // Inline error path uses suppress-toast — the global toast
    // middleware would otherwise double-surface validation errors.
    expect(opts.headers['x-suppress-toast']).toBe('1');

    // Success toast, store updated, save disables again.
    await waitFor(() => {
      const list = get(toasts);
      expect(list[0]?.type).toBe('success');
    });
    expect(get(authStore).user?.display_name).toBe('Ada Lovelace');
    await waitFor(() => expect(save.disabled).toBe(true));
  });

  it('trims whitespace before sending the PATCH', async () => {
    __authenticate({ display_name: 'Ada' });
    render(Profile);

    const name = screen.getByTestId('profile-display-name') as HTMLInputElement;
    await fireEvent.input(name, { target: { value: '  Bob  ' } });

    mockPATCH.mockResolvedValue({
      data: profileBody({ display_name: 'Bob' }),
      error: undefined,
    });
    await fireEvent.click(screen.getByTestId('profile-save'));

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [string, { body: { display_name: string } }];
    expect(opts.body.display_name).toBe('Bob');
  });

  it('keeps Save disabled when the field is blank or whitespace-only', async () => {
    __authenticate({ display_name: 'Ada' });
    render(Profile);

    const name = screen.getByTestId('profile-display-name') as HTMLInputElement;
    const save = screen.getByTestId('profile-save') as HTMLButtonElement;

    await fireEvent.input(name, { target: { value: '' } });
    await waitFor(() => expect(save.disabled).toBe(true));

    await fireEvent.input(name, { target: { value: '   ' } });
    await waitFor(() => expect(save.disabled).toBe(true));

    expect(mockPATCH).not.toHaveBeenCalled();
  });

  it('surfaces invalid_display_name as an inline error and preserves the field', async () => {
    __authenticate({ display_name: 'Ada' });
    render(Profile);

    const name = screen.getByTestId('profile-display-name') as HTMLInputElement;
    await fireEvent.input(name, { target: { value: 'Newname' } });

    mockPATCH.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'invalid_display_name', message: 'display_name is required' } },
    });
    await fireEvent.click(screen.getByTestId('profile-save'));

    await waitFor(() =>
      expect(screen.getByTestId('profile-error')).toHaveTextContent('display_name is required'),
    );
    // The unsaved input stays so the user can correct and retry.
    expect((screen.getByTestId('profile-display-name') as HTMLInputElement).value).toBe('Newname');
  });
});
