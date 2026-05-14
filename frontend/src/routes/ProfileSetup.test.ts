import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { toasts, _clearToasts } from '../lib/toasts';

const { mockPush } = vi.hoisted(() => ({
  mockPush: vi.fn(),
}));

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return { ...actual, push: mockPush };
});

// Mock the API client *before* importing ProfileSetup so the
// component's `import { client }` picks up the spy.
const { mockPOST } = vi.hoisted(() => ({ mockPOST: vi.fn() }));
vi.mock('../lib/api', () => ({
  client: { POST: mockPOST },
}));

import ProfileSetup from './ProfileSetup.svelte';

beforeEach(() => {
  mockPush.mockReset();
  mockPOST.mockReset();
  _clearToasts();
  window.sessionStorage.clear();
});

afterEach(() => {
  cleanup();
});

function typeAndSubmit(value: string): void {
  const input = screen.getByTestId('profile-display-name') as HTMLInputElement;
  fireEvent.input(input, { target: { value } });
  const submit = screen.getByTestId('profile-setup-submit') as HTMLButtonElement;
  fireEvent.click(submit);
}

describe('ProfileSetup route', () => {
  it('disables the submit button when the display name is blank', () => {
    render(ProfileSetup);
    const submit = screen.getByTestId('profile-setup-submit') as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
  });

  it('disables the submit button on a whitespace-only input', () => {
    render(ProfileSetup);
    const input = screen.getByTestId('profile-display-name') as HTMLInputElement;
    fireEvent.input(input, { target: { value: '   ' } });
    const submit = screen.getByTestId('profile-setup-submit') as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
  });

  it('POSTs to /api/v1/profile and navigates home on success when no return-to is stashed', async () => {
    mockPOST.mockResolvedValue({ response: { status: 200 } });
    render(ProfileSetup);
    typeAndSubmit('Sally Stone');

    await waitFor(() => expect(mockPOST).toHaveBeenCalled());
    const [path, opts] = mockPOST.mock.calls[0] as [string, { body: { display_name: string } }];
    expect(path).toBe('/api/v1/profile');
    expect(opts.body).toEqual({ display_name: 'Sally Stone' });

    await waitFor(() => expect(mockPush).toHaveBeenCalledWith('/'));
    const list = get(toasts);
    expect(list[0]?.type).toBe('success');
  });

  it('navigates back to the stashed return-to after a successful save', async () => {
    window.sessionStorage.setItem('minerals.profile.return_to', '#/specimens/abc');
    mockPOST.mockResolvedValue({ response: { status: 200 } });
    render(ProfileSetup);
    typeAndSubmit('Sally Stone');

    await waitFor(() => expect(mockPush).toHaveBeenCalledWith('/specimens/abc'));
    // The stash is consumed.
    expect(window.sessionStorage.getItem('minerals.profile.return_to')).toBeNull();
  });

  it('renders an inline error when the server rejects display_name', async () => {
    mockPOST.mockResolvedValue({
      error: { error: { code: 'invalid_display_name', message: 'display_name is required' } },
      response: { status: 400 },
    });
    render(ProfileSetup);
    typeAndSubmit('Whatever');

    await waitFor(() => {
      expect(screen.getByTestId('profile-setup-error')).toHaveTextContent(
        'display_name is required',
      );
    });
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('trims trailing whitespace before submitting', async () => {
    mockPOST.mockResolvedValue({ response: { status: 200 } });
    render(ProfileSetup);
    typeAndSubmit('  Sally  ');
    await waitFor(() => expect(mockPOST).toHaveBeenCalled());
    const [, opts] = mockPOST.mock.calls[0] as [string, { body: { display_name: string } }];
    expect(opts.body.display_name).toBe('Sally');
  });
});
