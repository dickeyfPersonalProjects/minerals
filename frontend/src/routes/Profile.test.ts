import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { toasts, _clearToasts } from '../lib/toasts';

const { mockGET, mockPATCH } = vi.hoisted(() => ({
  mockGET: vi.fn(),
  mockPATCH: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGET, PATCH: mockPATCH },
}));

import Profile from './Profile.svelte';

function profileBody(field_defaults: Record<string, string> | null) {
  return {
    id: 'user-1',
    email: 'a@b.test',
    display_name: 'Ada',
    pending: false,
    field_defaults: field_defaults ?? {},
  };
}

beforeEach(() => {
  mockGET.mockReset();
  mockPATCH.mockReset();
  _clearToasts();
});

afterEach(() => {
  cleanup();
});

describe('Profile route — field defaults', () => {
  it('renders the three dropdowns showing "System default" when the API returns no defaults', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Profile);

    const price = (await screen.findByTestId('profile-default-price')) as HTMLSelectElement;
    const acq = screen.getByTestId('profile-default-acquired_from') as HTMLSelectElement;
    const img = screen.getByTestId('profile-default-images') as HTMLSelectElement;

    expect(price.value).toBe('__unset__');
    expect(acq.value).toBe('__unset__');
    expect(img.value).toBe('__unset__');

    // The sentinel option is the user-visible "System default" label.
    expect(price.selectedOptions[0]?.textContent?.trim()).toBe('System default (owner-only)');
  });

  it('reflects values from the API when defaults are populated', async () => {
    mockGET.mockResolvedValue({
      data: profileBody({ price: 'public', acquired_from: 'unlisted', images: 'private' }),
      error: undefined,
    });
    render(Profile);

    const price = (await screen.findByTestId('profile-default-price')) as HTMLSelectElement;
    expect(price.value).toBe('public');
    expect((screen.getByTestId('profile-default-acquired_from') as HTMLSelectElement).value).toBe(
      'unlisted',
    );
    expect((screen.getByTestId('profile-default-images') as HTMLSelectElement).value).toBe(
      'private',
    );
  });

  it('disables Save until a value changes, then PATCHes only the changed keys', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Profile);

    const save = (await screen.findByTestId('profile-field-defaults-save')) as HTMLButtonElement;
    expect(save.disabled).toBe(true);

    const price = screen.getByTestId('profile-default-price') as HTMLSelectElement;
    await fireEvent.change(price, { target: { value: 'public' } });
    await waitFor(() => expect(save.disabled).toBe(false));

    mockPATCH.mockResolvedValue({
      data: profileBody({ price: 'public' }),
      error: undefined,
    });
    await fireEvent.click(save);

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [path, opts] = mockPATCH.mock.calls[0] as [
      string,
      { body: { field_defaults: Record<string, unknown> } },
    ];
    expect(path).toBe('/api/v1/profile');
    expect(opts.body).toEqual({ field_defaults: { price: 'public' } });

    const list = get(toasts);
    expect(list[0]?.type).toBe('success');
    // After save, Save disables again (no dirty diff vs the new server state).
    await waitFor(() => expect(save.disabled).toBe(true));
  });

  it('sends explicit null for a key cleared back to System default', async () => {
    mockGET.mockResolvedValue({
      data: profileBody({ price: 'public', acquired_from: 'unlisted' }),
      error: undefined,
    });
    render(Profile);

    const price = (await screen.findByTestId('profile-default-price')) as HTMLSelectElement;
    expect(price.value).toBe('public');
    await fireEvent.change(price, { target: { value: '__unset__' } });

    mockPATCH.mockResolvedValue({
      data: profileBody({ acquired_from: 'unlisted' }),
      error: undefined,
    });
    await fireEvent.click(screen.getByTestId('profile-field-defaults-save'));

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [
      string,
      { body: { field_defaults: Record<string, unknown> } },
    ];
    expect(opts.body).toEqual({ field_defaults: { price: null } });
  });

  it('renders inline load-error text when GET fails', async () => {
    mockGET.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'internal_error', message: 'boom' } },
    });
    render(Profile);
    await waitFor(() =>
      expect(screen.getByTestId('profile-field-defaults-error')).toHaveTextContent('boom'),
    );
  });

  it('does not PATCH on submit when nothing is dirty', async () => {
    mockGET.mockResolvedValue({
      data: profileBody({ price: 'public' }),
      error: undefined,
    });
    render(Profile);

    const save = (await screen.findByTestId('profile-field-defaults-save')) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    // Click is a no-op while disabled, but submitting the form
    // directly is defended by the dirty-guard too.
    await fireEvent.submit(screen.getByTestId('profile-field-defaults-form'));
    expect(mockPATCH).not.toHaveBeenCalled();
  });
});
