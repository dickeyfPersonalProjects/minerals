import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { axe } from 'vitest-axe';

// Hoisted mocks — the client and router are replaced for every
// test. Each test sets `mockGet` to the fixture it wants and can
// inspect `mockReplace` to verify URL sync.
const { mockGet, mockReplace } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockReplace: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet },
}));

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    replace: mockReplace,
    link: () => ({ destroy() {} }),
  };
});

import Specimens from './Specimens.svelte';
import { __resetAuthStore, setAccessToken } from '../lib/oidc/auth';

type SpecimenSeed = {
  id: string;
  name: string;
  type: 'mineral' | 'rock' | 'meteorite';
  visibility?: 'private' | 'unlisted' | 'public';
  locality_text?: string | null;
};

function specimen(seed: SpecimenSeed) {
  return {
    id: seed.id,
    name: seed.name,
    type: seed.type,
    visibility: seed.visibility ?? 'private',
    locality_text: seed.locality_text ?? null,
    acquired_at: null,
    acquired_from: null,
    author_id: '00000000-0000-0000-0000-000000000001',
    catalog_number: null,
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
    description: '',
    dimensions: {},
    locality: {},
    mass_g: null,
    price_cents: null,
    source_notes: null,
    type_data: {},
  };
}

beforeEach(() => {
  mockGet.mockReset();
  mockReplace.mockReset();
  // Reset hash so each test starts at /specimens with no filters.
  window.location.hash = '#/specimens';
  // Default: photo lookups return an empty list so cards fall
  // back to the placeholder. Individual tests can override.
  mockGet.mockImplementation(async (path: string) => {
    if (path === '/api/v1/specimens/{id}/photos') {
      return { data: { items: [], next_cursor: null }, error: undefined };
    }
    return { data: { items: [], next_cursor: null }, error: undefined };
  });
  // Default-authed; the anonymous block at the bottom resets.
  setAccessToken('test-token', 600);
});

afterEach(() => {
  cleanup();
  window.location.hash = '';
  __resetAuthStore();
});

describe('Specimens route', () => {
  it('renders cards from the API response', async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens') {
        return {
          data: {
            items: [
              specimen({
                id: '11111111-1111-1111-1111-111111111111',
                name: 'Smoky quartz',
                type: 'mineral',
                visibility: 'public',
                locality_text: 'Mont Blanc, France',
              }),
              specimen({
                id: '22222222-2222-2222-2222-222222222222',
                name: 'Basalt cobble',
                type: 'rock',
                locality_text: 'Mauna Loa, Hawai‘i',
              }),
            ],
            next_cursor: null,
          },
          error: undefined,
          response: new Response(),
        };
      }
      return { data: { items: [], next_cursor: null }, error: undefined };
    });

    render(Specimens);

    await waitFor(() => expect(screen.getByTestId('specimen-grid')).toBeInTheDocument());

    expect(screen.getByText('Smoky quartz')).toBeInTheDocument();
    expect(screen.getByText('Basalt cobble')).toBeInTheDocument();
    expect(screen.getByText(/Mont Blanc/)).toBeInTheDocument();
    // Public specimen surfaces a visibility chip; private one does not.
    const chips = screen.getAllByTestId('visibility-chip');
    expect(chips).toHaveLength(1);
    expect(chips[0]).toHaveTextContent(/public/i);
    // Both type badges render.
    expect(screen.getAllByTestId('type-badge')).toHaveLength(2);
  });

  it('shows the empty state when no specimens exist', async () => {
    mockGet.mockImplementation(async () => ({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    }));

    render(Specimens);

    await waitFor(() => expect(screen.getByTestId('empty')).toBeInTheDocument());
    expect(screen.getByText(/no specimens yet/i)).toBeInTheDocument();
  });

  it('shows the error state when the API returns an envelope error', async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens') {
        return {
          data: undefined,
          error: { error: { code: 'internal', message: 'database is on fire' } },
          response: new Response(null, { status: 500 }),
        };
      }
      return { data: { items: [], next_cursor: null }, error: undefined };
    });

    render(Specimens);

    await waitFor(() => expect(screen.getByTestId('error')).toBeInTheDocument());
    expect(screen.getByText(/database is on fire/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument();
  });

  it('shows the error state when the fetch itself rejects', async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens') throw new Error('network down');
      return { data: { items: [], next_cursor: null }, error: undefined };
    });

    render(Specimens);

    await waitFor(() => expect(screen.getByTestId('error')).toBeInTheDocument());
    expect(screen.getByText(/network down/)).toBeInTheDocument();
  });

  it('parses filters from the URL hash and forwards them to the API', async () => {
    window.location.hash =
      '#/specimens?q=quartz&type=mineral&visibility=public&has_catalog_number=true&acquired_after=2026-01-01&acquired_before=2026-12-31&collector_id=cccccccc-0000-0000-0000-000000000001';
    // jsdom: ensure svelte-spa-router's listener picks up the new
    // hash before the component mounts.
    window.dispatchEvent(new Event('hashchange'));

    const calls: Array<{ path: string; opts: { params?: { query?: Record<string, string> } } }> =
      [];
    mockGet.mockImplementation(async (path: string, opts: unknown) => {
      const o = opts as { params?: { query?: Record<string, string> } };
      calls.push({ path, opts: o });
      return {
        data: { items: [], next_cursor: null },
        error: undefined,
        response: new Response(),
      };
    });

    render(Specimens);

    await waitFor(() => {
      expect(calls.find((c) => c.path === '/api/v1/specimens')).toBeDefined();
    });
    const call = calls.find((c) => c.path === '/api/v1/specimens')!;
    expect(call.opts.params?.query).toEqual({
      q: 'quartz',
      type: 'mineral',
      visibility: 'public',
      has_catalog_number: 'true',
      acquired_after: '2026-01-01',
      acquired_before: '2026-12-31',
      collector_id: 'cccccccc-0000-0000-0000-000000000001',
    });
    // Relevance hint shows when q is set.
    expect(screen.getByTestId('relevance-hint')).toBeInTheDocument();
  });

  it('calls replace() with serialised filter state when a chip is clicked', async () => {
    mockGet.mockImplementation(async () => ({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    }));

    render(Specimens);

    await waitFor(() => expect(screen.getByTestId('empty')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('filter-toggle'));
    await fireEvent.click(screen.getByTestId('filter-type-mineral'));

    expect(mockReplace).toHaveBeenCalledWith('/specimens?type=mineral');
  });

  it('has no structural a11y violations on grid render', async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens') {
        return {
          data: {
            items: [
              specimen({
                id: '11111111-1111-1111-1111-111111111111',
                name: 'Smoky quartz',
                type: 'mineral',
                visibility: 'public',
                locality_text: 'Mont Blanc, France',
              }),
            ],
            next_cursor: null,
          },
          error: undefined,
          response: new Response(),
        };
      }
      return { data: { items: [], next_cursor: null }, error: undefined };
    });

    const { container } = render(Specimens);

    await waitFor(() => expect(screen.getByTestId('specimen-grid')).toBeInTheDocument());

    // color-contrast requires real layout (canvas) and is skipped
    // in jsdom — see bead mi-k9t constraints.
    const results = await axe(container, { rules: { 'color-contrast': { enabled: false } } });
    expect(results).toHaveNoViolations();
  });

  it('shows the filtered empty state when results are empty under filters', async () => {
    window.location.hash = '#/specimens?type=meteorite';
    window.dispatchEvent(new Event('hashchange'));
    mockGet.mockImplementation(async () => ({
      data: { items: [], next_cursor: null },
      error: undefined,
      response: new Response(),
    }));

    render(Specimens);

    await waitFor(() => expect(screen.getByTestId('empty-filtered')).toBeInTheDocument());
    expect(screen.queryByTestId('empty')).not.toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('empty-clear-filters'));
    expect(mockReplace).toHaveBeenCalledWith('/specimens');
  });

  it('does NOT render the error UI on a profile_setup_required 403 (mi-4p4)', async () => {
    // Backend gate: first-login user with no profile row. The
    // wrapper middleware is responsible for the redirect; this
    // component should stay in `loading` so the user never sees an
    // "access forbidden" banner before the route navigates away.
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens') {
        return {
          data: undefined,
          error: {
            error: {
              code: 'profile_setup_required',
              message: 'profile setup required',
              details: { redirect: '/profile/setup' },
            },
          },
          response: new Response(null, { status: 403 }),
        };
      }
      return { data: { items: [], next_cursor: null }, error: undefined };
    });

    render(Specimens);

    // Let the fetch resolve and any pending microtasks settle.
    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await Promise.resolve();
    await Promise.resolve();

    // No error UI of any kind — the page stays on the skeleton so
    // the redirect lands without a visible flash of an error.
    expect(screen.queryByTestId('error')).not.toBeInTheDocument();
    expect(screen.queryByText(/access forbidden/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/profile setup required/i)).not.toBeInTheDocument();
    // The skeleton (loading) is still showing.
    expect(screen.getByTestId('loading')).toBeInTheDocument();
  });

  describe('when unauthenticated', () => {
    it('renders the grid without write buttons (mi-eec)', async () => {
      __resetAuthStore();
      mockGet.mockImplementation(async (path: string) => {
        if (path === '/api/v1/specimens') {
          return {
            data: {
              items: [
                specimen({
                  id: '11111111-1111-1111-1111-111111111111',
                  name: 'Smoky quartz',
                  type: 'mineral',
                  visibility: 'public',
                }),
              ],
              next_cursor: null,
            },
            error: undefined,
            response: new Response(),
          };
        }
        return { data: { items: [], next_cursor: null }, error: undefined };
      });

      render(Specimens);

      await waitFor(() => expect(screen.getByTestId('specimen-grid')).toBeInTheDocument());
      expect(screen.getByText('Smoky quartz')).toBeInTheDocument();
      expect(screen.queryByTestId('new-specimen')).not.toBeInTheDocument();
      // Cards must not surface the QR-sheet add CTA either.
      expect(screen.queryByTestId('qr-sheet-add')).not.toBeInTheDocument();
    });
  });
});
