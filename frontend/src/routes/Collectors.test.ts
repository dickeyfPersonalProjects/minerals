import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/svelte';

const { mockGet, mockPost, mockDelete } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
  mockDelete: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet, POST: mockPost, DELETE: mockDelete },
}));

import Collectors from './Collectors.svelte';
import { __resetAuthStore, setAccessToken } from '../lib/oidc/auth';

type CollectorSeed = {
  id: string;
  name: string;
  notes?: string | null;
};

function collector(seed: CollectorSeed) {
  return {
    id: seed.id,
    name: seed.name,
    notes: seed.notes ?? null,
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
  };
}

beforeEach(() => {
  mockGet.mockReset();
  mockPost.mockReset();
  mockDelete.mockReset();
  // window.confirm always says yes by default; tests can override.
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  // Default to authenticated for the existing CTA tests. The
  // unauthenticated block at the bottom resets the store.
  setAccessToken('test-token', 600);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  __resetAuthStore();
});

describe('Collectors route', () => {
  it('renders rows from the API response', async () => {
    mockGet.mockImplementation(async () => ({
      data: {
        items: [
          collector({
            id: '11111111-1111-1111-1111-111111111111',
            name: 'Marie Curie',
            notes: 'Bequeathed several specimens in 1934.',
          }),
          collector({
            id: '22222222-2222-2222-2222-222222222222',
            name: 'Charles Darwin',
          }),
        ],
        next_cursor: null,
      },
      error: undefined,
      response: new Response(),
    }));

    render(Collectors);

    await waitFor(() => expect(screen.getByTestId('collector-list')).toBeInTheDocument());
    expect(screen.getByText('Marie Curie')).toBeInTheDocument();
    expect(screen.getByText('Charles Darwin')).toBeInTheDocument();
    expect(screen.getByText(/Bequeathed several specimens/)).toBeInTheDocument();
  });

  it('shows the empty state when no collectors exist', async () => {
    mockGet.mockResolvedValue({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    });

    render(Collectors);

    await waitFor(() => expect(screen.getByTestId('empty')).toBeInTheDocument());
    expect(screen.getByText(/no collectors yet/i)).toBeInTheDocument();
  });

  it('paginates with a load-more button when next_cursor is present', async () => {
    mockGet.mockImplementationOnce(async () => ({
      data: {
        items: [collector({ id: 'a1111111-1111-1111-1111-111111111111', name: 'Alice' })],
        next_cursor: 'cursor-2',
      },
      error: undefined,
      response: new Response(),
    }));
    mockGet.mockImplementationOnce(async () => ({
      data: {
        items: [collector({ id: 'b2222222-2222-2222-2222-222222222222', name: 'Bob' })],
        next_cursor: null,
      },
      error: undefined,
      response: new Response(),
    }));

    render(Collectors);
    await waitFor(() => expect(screen.getByText('Alice')).toBeInTheDocument());

    const loadMore = await screen.findByTestId('load-more');
    await fireEvent.click(loadMore);

    await waitFor(() => expect(screen.getByText('Bob')).toBeInTheDocument());
    expect(screen.getByText('Alice')).toBeInTheDocument();
    expect(mockGet).toHaveBeenCalledTimes(2);
    const secondCall = mockGet.mock.calls[1]?.[1];
    expect(secondCall.params.query.cursor).toBe('cursor-2');
  });

  it('shows validation error when name is empty on create', async () => {
    mockGet.mockResolvedValue({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    });

    render(Collectors);
    await waitFor(() => expect(screen.getByTestId('empty')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('toggle-create'));
    expect(screen.getByTestId('create-form-wrapper')).toBeInTheDocument();

    // Submit without entering anything.
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());
    expect(screen.getByTestId('name-error')).toHaveTextContent(/required/i);
    expect(mockPost).not.toHaveBeenCalled();
  });

  it('creates a collector and refetches the list on success', async () => {
    // Initial empty list, then list with the new entry.
    mockGet.mockImplementationOnce(async () => ({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    }));
    mockPost.mockResolvedValue({
      data: collector({ id: 'c3333333-3333-3333-3333-333333333333', name: 'Curie' }),
      error: undefined,
      response: new Response(null, { status: 201 }),
    });
    mockGet.mockImplementationOnce(async () => ({
      data: {
        items: [collector({ id: 'c3333333-3333-3333-3333-333333333333', name: 'Curie' })],
        next_cursor: null,
      },
      error: undefined,
      response: new Response(),
    }));

    render(Collectors);
    await waitFor(() => expect(screen.getByTestId('empty')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('toggle-create'));
    const nameInput = screen.getByLabelText(/^name/i);
    await fireEvent.input(nameInput, { target: { value: 'Curie' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(screen.getByText('Curie')).toBeInTheDocument());
    expect(mockPost).toHaveBeenCalledTimes(1);
    const postArgs = mockPost.mock.calls[0]?.[1];
    expect(postArgs.body).toEqual({ name: 'Curie' });
    // Form collapses on success.
    expect(screen.queryByTestId('create-form-wrapper')).not.toBeInTheDocument();
  });

  it('shows an inline name error on 409 from create', async () => {
    mockGet.mockResolvedValue({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    });
    mockPost.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'conflict', message: 'name already exists' } },
      response: new Response(null, { status: 409 }),
    });

    render(Collectors);
    await waitFor(() => expect(screen.getByTestId('empty')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('toggle-create'));
    const nameInput = screen.getByLabelText(/^name/i);
    await fireEvent.input(nameInput, { target: { value: 'Curie' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());
    expect(screen.getByTestId('name-error')).toHaveTextContent(/already exists/i);
    // Still on the form, not refetched.
    expect(screen.getByTestId('create-form-wrapper')).toBeInTheDocument();
  });

  it('deletes a collector and refetches on success', async () => {
    mockGet.mockImplementationOnce(async () => ({
      data: {
        items: [collector({ id: 'd4444444-4444-4444-4444-444444444444', name: 'Doomed' })],
        next_cursor: null,
      },
      error: undefined,
      response: new Response(),
    }));
    mockDelete.mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    });
    mockGet.mockImplementationOnce(async () => ({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    }));

    render(Collectors);
    await waitFor(() => expect(screen.getByText('Doomed')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('delete-button'));

    await waitFor(() => expect(screen.getByTestId('empty')).toBeInTheDocument());
    expect(window.confirm).toHaveBeenCalled();
    expect(mockDelete).toHaveBeenCalledTimes(1);
  });

  it('skips DELETE when the user cancels the native confirm', async () => {
    mockGet.mockResolvedValue({
      data: {
        items: [collector({ id: 'e5555555-5555-5555-5555-555555555555', name: 'Spared' })],
        next_cursor: null,
      },
      error: undefined,
      response: new Response(),
    });
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    render(Collectors);
    await waitFor(() => expect(screen.getByText('Spared')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('delete-button'));

    expect(mockDelete).not.toHaveBeenCalled();
    expect(screen.getByText('Spared')).toBeInTheDocument();
  });

  it('shows the in-use error and a filtered-specimens link on delete 409', async () => {
    mockGet.mockResolvedValue({
      data: {
        items: [collector({ id: 'f6666666-6666-6666-6666-666666666666', name: 'Linked' })],
        next_cursor: null,
      },
      error: undefined,
      response: new Response(),
    });
    mockDelete.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'conflict', message: 'collector is referenced' } },
      response: new Response(null, { status: 409 }),
    });

    render(Collectors);
    await waitFor(() => expect(screen.getByText('Linked')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('delete-button'));

    await waitFor(() => expect(screen.getByTestId('delete-error')).toBeInTheDocument());
    expect(screen.getByTestId('delete-error')).toHaveTextContent(
      /referenced by one or more specimens/i,
    );
    const link = screen.getByTestId('filtered-specimens-link') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toContain(
      'collector_id=f6666666-6666-6666-6666-666666666666',
    );
    // Row still present — delete failed.
    expect(screen.getByText('Linked')).toBeInTheDocument();
  });

  it('debounces the search input and passes ?q= to the API', async () => {
    vi.useFakeTimers();
    try {
      mockGet.mockResolvedValue({
        data: { items: [], next_cursor: null },
        error: undefined,
        response: new Response(),
      });

      render(Collectors);
      // Initial load.
      await vi.runAllTimersAsync();
      expect(mockGet).toHaveBeenCalledTimes(1);

      const search = screen.getByTestId('search-input');
      await fireEvent.input(search, { target: { value: 'cu' } });
      await fireEvent.input(search, { target: { value: 'cur' } });
      // Not yet debounced — should not have fired again.
      expect(mockGet).toHaveBeenCalledTimes(1);

      // Advance past the 300ms debounce window.
      await vi.advanceTimersByTimeAsync(350);

      expect(mockGet).toHaveBeenCalledTimes(2);
      const secondCallQuery = mockGet.mock.calls[1]?.[1].params.query;
      expect(secondCallQuery.q).toBe('cur');
    } finally {
      vi.useRealTimers();
    }
  });

  describe('when unauthenticated', () => {
    it('hides write CTAs while still rendering the list (mi-eec)', async () => {
      __resetAuthStore();
      mockGet.mockResolvedValue({
        data: {
          items: [collector({ id: '11111111-1111-1111-1111-111111111111', name: 'Marie Curie' })],
          next_cursor: null,
        },
        error: undefined,
        response: new Response(),
      });

      render(Collectors);

      await waitFor(() => expect(screen.getByTestId('collector-list')).toBeInTheDocument());
      expect(screen.getByText('Marie Curie')).toBeInTheDocument();
      // No Add / Edit / Delete affordances for anonymous users.
      expect(screen.queryByTestId('toggle-create')).not.toBeInTheDocument();
      expect(screen.queryByTestId('create-form-wrapper')).not.toBeInTheDocument();
      expect(screen.queryByTestId('delete-button')).not.toBeInTheDocument();
      // The "Edit" anchor uses the same row layout — assert nothing
      // with that text is rendered for the row.
      const row = screen.getByTestId('collector-row');
      expect(row.querySelector('a[href*="/collectors/"]')).toBeTruthy();
      // The list still shows the collector name link, but the
      // edit-styled action anchor isn't present in the row's action
      // column (there are no other anchor "Edit" affordances).
      expect(row.textContent).not.toMatch(/^Edit$/m);
    });
  });
});
