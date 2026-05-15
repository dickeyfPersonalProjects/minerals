import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockPost, mockPush } = vi.hoisted(() => ({
  mockPost: vi.fn(),
  mockPush: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { POST: mockPost },
}));

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    push: mockPush,
    link: () => ({ destroy() {} }),
  };
});

import SpecimenNew from './SpecimenNew.svelte';
import { __resetAuthStore, setAccessToken } from '../lib/oidc/auth';

beforeEach(() => {
  mockPost.mockReset();
  mockPush.mockReset();
  // Default-authed; the anonymous block at the bottom resets.
  setAccessToken('test-token', 600);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  __resetAuthStore();
});

describe('SpecimenNew route', () => {
  it('POSTs the create body and navigates to the new specimen detail page', async () => {
    mockPost.mockResolvedValue({
      data: { id: 'abc-123', name: 'Galena', type: 'mineral' },
      error: undefined,
      response: new Response(),
    });

    render(SpecimenNew);

    const name = screen.getByLabelText(/^name/i);
    await fireEvent.input(name, { target: { value: 'Galena' } });
    const catalog = screen.getByLabelText(/catalog number/i);
    await fireEvent.input(catalog, { target: { value: 'MIN-001' } });

    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    const args = mockPost.mock.calls[0]?.[1];
    expect(args.body.type).toBe('mineral');
    expect(args.body.name).toBe('Galena');
    expect(args.body.catalog_number).toBe('MIN-001');
    expect(args.body.visibility).toBe('private');

    expect(mockPush).toHaveBeenCalledWith('/specimens/abc-123');
  });

  it('shows duplicate-catalog-number error on 409', async () => {
    mockPost.mockResolvedValue({
      data: undefined,
      error: {
        error: {
          code: 'catalog_number_conflict',
          message: 'catalog number already in use',
          details: { field: 'catalog_number' },
        },
      },
      response: new Response(null, { status: 409 }),
    });

    render(SpecimenNew);

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Galena' } });
    await fireEvent.input(screen.getByLabelText(/catalog number/i), {
      target: { value: 'DUPE' },
    });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(screen.getByTestId('catalog-number-error')).toBeInTheDocument());
    expect(screen.getByTestId('catalog-number-error')).toHaveTextContent(/already exists/i);
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('shows a banner error on a non-field 4xx', async () => {
    mockPost.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'invalid', message: 'something is off' } },
      response: new Response(null, { status: 400 }),
    });

    render(SpecimenNew);

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Galena' } });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(screen.getByTestId('form-error')).toBeInTheDocument());
    expect(screen.getByTestId('form-error')).toHaveTextContent(/something is off/);
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('hides the create form when unauthenticated (mi-eec)', async () => {
    __resetAuthStore();
    render(SpecimenNew);
    expect(screen.getByTestId('auth-required')).toBeInTheDocument();
    expect(screen.queryByTestId('specimen-form')).not.toBeInTheDocument();
  });
});
