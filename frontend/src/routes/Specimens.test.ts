import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/svelte';

// Hoisted mock — the client is replaced for every test in this
// file. Each test sets `mockGet` to the fixture it wants.
const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet },
}));

import Specimens from './Specimens.svelte';

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
  // Default: photo lookups return an empty list so cards fall
  // back to the placeholder. Individual tests can override.
  mockGet.mockImplementation(async (path: string) => {
    if (path === '/api/v1/specimens/{id}/photos') {
      return { data: { items: [], next_cursor: null }, error: undefined };
    }
    return { data: { items: [], next_cursor: null }, error: undefined };
  });
});

afterEach(() => {
  cleanup();
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
});
