import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockPost, mockGet, mockPush } = vi.hoisted(() => ({
  mockPost: vi.fn(),
  mockGet: vi.fn(),
  mockPush: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { POST: mockPost, GET: mockGet },
}));

// Default profile response: no account default set, so the create
// form falls back to the system default ('private'). Individual tests
// override mockGet to exercise a configured default.
function mockProfile(defaultSpecimenVisibility: string | null = null) {
  mockGet.mockResolvedValue({
    data: {
      id: 'u',
      email: 'x@x',
      display_name: 'X',
      pending: false,
      field_defaults: null,
      default_specimen_visibility: defaultSpecimenVisibility,
    },
    error: undefined,
    response: new Response(),
  });
}

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    push: mockPush,
    link: () => ({ destroy() {} }),
  };
});

import SpecimenNew from './SpecimenNew.svelte';
import { __authenticate, __resetAuthStore } from '../lib/auth';

beforeEach(() => {
  mockPost.mockReset();
  mockGet.mockReset();
  mockPush.mockReset();
  mockProfile();
  // Default-authed; the anonymous block at the bottom resets.
  __authenticate();
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

    const name = await screen.findByLabelText(/^name/i);
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

  it('pre-fills the visibility field with the user account default (mi-q2d8)', async () => {
    mockProfile('public');
    mockPost.mockResolvedValue({
      data: { id: 'abc-123', name: 'Galena', type: 'mineral' },
      error: undefined,
      response: new Response(),
    });

    render(SpecimenNew);

    const visibility = (await screen.findByLabelText(/^visibility/i)) as HTMLSelectElement;
    expect(visibility.value).toBe('public');

    // Submitting without touching it carries the default through.
    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Galena' } });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0]?.[1].body.visibility).toBe('public');
  });

  it('lets the user override the pre-filled default before submit (mi-q2d8)', async () => {
    mockProfile('public');
    mockPost.mockResolvedValue({
      data: { id: 'abc-123', name: 'Galena', type: 'mineral' },
      error: undefined,
      response: new Response(),
    });

    render(SpecimenNew);

    const visibility = (await screen.findByLabelText(/^visibility/i)) as HTMLSelectElement;
    expect(visibility.value).toBe('public');
    // Override before submit — the override wins.
    await fireEvent.change(visibility, { target: { value: 'private' } });
    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Galena' } });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0]?.[1].body.visibility).toBe('private');
  });

  it('falls back to the system default when no account default is set', async () => {
    mockProfile(null);
    mockPost.mockResolvedValue({
      data: { id: 'abc-123', name: 'Galena', type: 'mineral' },
      error: undefined,
      response: new Response(),
    });

    render(SpecimenNew);

    const visibility = (await screen.findByLabelText(/^visibility/i)) as HTMLSelectElement;
    expect(visibility.value).toBe('private');
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

    await fireEvent.input(await screen.findByLabelText(/^name/i), { target: { value: 'Galena' } });
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

    await fireEvent.input(await screen.findByLabelText(/^name/i), { target: { value: 'Galena' } });
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
