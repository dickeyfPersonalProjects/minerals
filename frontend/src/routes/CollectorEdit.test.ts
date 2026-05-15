import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/svelte';

const { mockGet, mockPatch, mockPush } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPatch: vi.fn(),
  mockPush: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet, PATCH: mockPatch },
}));

// Stub the router's `push` so navigation in tests is observable.
vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    push: mockPush,
    link: () => ({ destroy() {} }),
  };
});

import CollectorEdit from './CollectorEdit.svelte';
import { __resetAuthStore, setAccessToken } from '../lib/oidc/auth';

const COLLECTOR_ID = '11111111-1111-1111-1111-111111111111';

function collector(overrides: Partial<{ name: string; notes: string | null }> = {}) {
  return {
    id: COLLECTOR_ID,
    name: overrides.name ?? 'Marie Curie',
    notes: overrides.notes ?? 'original notes',
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
  };
}

beforeEach(() => {
  mockGet.mockReset();
  mockPatch.mockReset();
  mockPush.mockReset();
  setAccessToken('test-token', 600);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  __resetAuthStore();
});

describe('CollectorEdit route', () => {
  it('loads the collector and pre-populates the form', async () => {
    mockGet.mockResolvedValue({
      data: collector(),
      error: undefined,
      response: new Response(),
    });

    render(CollectorEdit, { params: { id: COLLECTOR_ID } });

    await waitFor(() => {
      const name = screen.getByLabelText(/^name/i) as HTMLInputElement;
      expect(name.value).toBe('Marie Curie');
    });
    const notes = screen.getByLabelText(/notes/i) as HTMLTextAreaElement;
    expect(notes.value).toBe('original notes');
  });

  it('renders an error state when load fails', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'not_found', message: 'no such collector' } },
      response: new Response(null, { status: 404 }),
    });

    render(CollectorEdit, { params: { id: COLLECTOR_ID } });

    await waitFor(() => expect(screen.getByTestId('error')).toBeInTheDocument());
    expect(screen.getByText(/no such collector/i)).toBeInTheDocument();
  });

  it('PATCHes only the changed field and navigates to /collectors on success', async () => {
    mockGet.mockResolvedValue({
      data: collector({ name: 'Old', notes: 'kept' }),
      error: undefined,
      response: new Response(),
    });
    mockPatch.mockResolvedValue({
      data: collector({ name: 'New', notes: 'kept' }),
      error: undefined,
      response: new Response(),
    });

    render(CollectorEdit, { params: { id: COLLECTOR_ID } });
    await waitFor(() => expect(screen.getByLabelText(/^name/i)).toBeInTheDocument());

    const name = screen.getByLabelText(/^name/i);
    await fireEvent.input(name, { target: { value: 'New' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
    const args = mockPatch.mock.calls[0]?.[1];
    expect(args.params.path.id).toBe(COLLECTOR_ID);
    expect(args.body).toEqual({ name: 'New' });
    expect(mockPush).toHaveBeenCalledWith('/collectors');
  });

  it('shows an inline name error on PATCH 409', async () => {
    mockGet.mockResolvedValue({
      data: collector({ name: 'Original' }),
      error: undefined,
      response: new Response(),
    });
    mockPatch.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'conflict', message: 'name in use' } },
      response: new Response(null, { status: 409 }),
    });

    render(CollectorEdit, { params: { id: COLLECTOR_ID } });
    await waitFor(() => expect(screen.getByLabelText(/^name/i)).toBeInTheDocument());

    const name = screen.getByLabelText(/^name/i);
    await fireEvent.input(name, { target: { value: 'Taken' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());
    expect(screen.getByTestId('name-error')).toHaveTextContent(/already exists/i);
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('navigates to /collectors when Cancel is clicked', async () => {
    mockGet.mockResolvedValue({
      data: collector(),
      error: undefined,
      response: new Response(),
    });

    render(CollectorEdit, { params: { id: COLLECTOR_ID } });
    await waitFor(() => expect(screen.getByLabelText(/^name/i)).toBeInTheDocument());

    await fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    expect(mockPush).toHaveBeenCalledWith('/collectors');
  });

  it('hides the edit form when unauthenticated (mi-eec)', async () => {
    __resetAuthStore();
    mockGet.mockResolvedValue({
      data: collector(),
      error: undefined,
      response: new Response(),
    });
    render(CollectorEdit, { params: { id: COLLECTOR_ID } });
    await waitFor(() => expect(screen.getByTestId('auth-required')).toBeInTheDocument());
    expect(screen.queryByLabelText(/^name/i)).not.toBeInTheDocument();
  });
});
