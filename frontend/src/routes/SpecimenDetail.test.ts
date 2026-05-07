import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/svelte';

// Hoisted mock — the API client is replaced for every test in this
// file. Each test sets `mockGet` to the fixture it wants.
const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet },
}));

import SpecimenDetail from './SpecimenDetail.svelte';

const SPECIMEN_ID = '11111111-1111-1111-1111-111111111111';

type SpecimenSeed = {
  id?: string;
  name?: string;
  type?: 'mineral' | 'rock' | 'meteorite';
  visibility?: 'private' | 'unlisted' | 'public';
  description?: string;
  locality_text?: string | null;
  catalog_number?: string | null;
  mass_g?: number | null;
  type_data?: Record<string, unknown>;
  locality?: Record<string, unknown>;
  dimensions?: Record<string, unknown>;
};

function specimen(seed: SpecimenSeed = {}) {
  return {
    id: seed.id ?? SPECIMEN_ID,
    name: seed.name ?? 'Smoky quartz',
    type: seed.type ?? 'mineral',
    visibility: seed.visibility ?? 'private',
    description: seed.description ?? '',
    locality_text: seed.locality_text ?? null,
    locality: seed.locality ?? {},
    dimensions: seed.dimensions ?? {},
    type_data: seed.type_data ?? {},
    catalog_number: seed.catalog_number ?? null,
    acquired_at: null,
    acquired_from: null,
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
    mass_g: seed.mass_g ?? null,
    price_cents: null,
    source_notes: null,
  };
}

type PhotoSeed = { id: string; position?: number };

function photo(seed: PhotoSeed) {
  return {
    id: seed.id,
    specimen_id: SPECIMEN_ID,
    file_id: 'aaaaaaaa-0000-0000-0000-000000000000',
    content_type: 'image/jpeg',
    byte_size: 1234,
    sha256: 'deadbeef',
    position: seed.position ?? 1,
    taken_at: null,
    created_at: '2026-05-01T12:00:00Z',
  };
}

type JournalSeed = {
  id: string;
  body_html?: string;
  body_md?: string;
  created_at?: string;
  updated_at?: string;
};

function journalEntry(seed: JournalSeed) {
  return {
    id: seed.id,
    specimen_id: SPECIMEN_ID,
    author_id: '00000000-0000-0000-0000-000000000001',
    body_md: seed.body_md ?? '',
    body_html: seed.body_html ?? '',
    created_at: seed.created_at ?? '2026-05-01T12:00:00Z',
    updated_at: seed.updated_at ?? '2026-05-01T12:00:00Z',
  };
}

type CollectorLink = {
  position: number;
  collector: { id: string; name: string };
};

type Fixture = {
  specimen?: ReturnType<typeof specimen> | null;
  specimenError?: { code: string; message: string };
  photos?: ReturnType<typeof photo>[];
  journal?: ReturnType<typeof journalEntry>[];
  journalError?: boolean;
  collectors?: CollectorLink[];
  collectorsError?: boolean;
};

function setupFetch(fx: Fixture) {
  mockGet.mockImplementation(async (path: string) => {
    if (path === '/api/v1/specimens/{id}') {
      if (fx.specimenError) {
        return {
          data: undefined,
          error: { error: fx.specimenError },
          response: new Response(null, { status: 500 }),
        };
      }
      return {
        data: fx.specimen ?? specimen(),
        error: undefined,
        response: new Response(),
      };
    }
    if (path === '/api/v1/specimens/{id}/photos') {
      return {
        data: { items: fx.photos ?? [], next_cursor: null },
        error: undefined,
        response: new Response(),
      };
    }
    if (path === '/api/v1/specimens/{id}/journal') {
      if (fx.journalError) {
        return {
          data: undefined,
          error: { error: { code: 'internal', message: 'journal down' } },
          response: new Response(null, { status: 500 }),
        };
      }
      return {
        data: { items: fx.journal ?? [], next_cursor: null },
        error: undefined,
        response: new Response(),
      };
    }
    if (path === '/api/v1/specimens/{id}/collectors') {
      if (fx.collectorsError) {
        return {
          data: undefined,
          error: { error: { code: 'internal', message: 'collectors down' } },
          response: new Response(null, { status: 500 }),
        };
      }
      return {
        data: { items: fx.collectors ?? [] },
        error: undefined,
        response: new Response(),
      };
    }
    return { data: { items: [], next_cursor: null }, error: undefined, response: new Response() };
  });
}

beforeEach(() => {
  mockGet.mockReset();
  setupFetch({});
});

afterEach(() => {
  cleanup();
});

describe('SpecimenDetail route', () => {
  it('renders header, type-specific metadata, and locality from a populated response', async () => {
    setupFetch({
      specimen: specimen({
        name: 'Smoky quartz from Mont Blanc',
        type: 'mineral',
        visibility: 'public',
        description: 'A lovely cluster.',
        locality_text: 'Mont Blanc, France',
        catalog_number: 'MIN-001',
        mass_g: 42.5,
        type_data: {
          chemical_formula: 'SiO₂',
          mohs_hardness: 7,
          color: 'smoky brown',
        },
        locality: { country: 'France', region: 'Haute-Savoie' },
        dimensions: { length_mm: 80, width_mm: 50, height_mm: 30 },
      }),
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('specimen-detail')).toBeInTheDocument());

    expect(screen.getByTestId('specimen-name')).toHaveTextContent('Smoky quartz from Mont Blanc');
    expect(screen.getByTestId('type-badge')).toHaveTextContent(/mineral/i);
    expect(screen.getByTestId('visibility-chip')).toHaveTextContent(/public/i);
    expect(screen.getByTestId('catalog-number')).toHaveTextContent('MIN-001');
    expect(screen.getByTestId('description-body')).toHaveTextContent('A lovely cluster.');
    expect(screen.getByTestId('locality-text')).toHaveTextContent('Mont Blanc, France');

    const typeData = screen.getByTestId('type-data-section');
    expect(typeData).toHaveTextContent('Chemical formula');
    expect(typeData).toHaveTextContent('SiO₂');
    expect(typeData).toHaveTextContent('Hardness (Mohs)');
    expect(typeData).toHaveTextContent('7');

    const props = screen.getByTestId('properties-section');
    expect(props).toHaveTextContent(/Mass/);
    expect(props).toHaveTextContent('42.5 g');
    expect(props).toHaveTextContent('80 × 50 × 30 mm');
  });

  it('hides the visibility chip for private specimens and the catalog tag when absent', async () => {
    setupFetch({
      specimen: specimen({ visibility: 'private', catalog_number: null }),
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('specimen-detail')).toBeInTheDocument());
    expect(screen.queryByTestId('visibility-chip')).toBeNull();
    expect(screen.queryByTestId('catalog-number')).toBeNull();
  });

  it('renders journal entries newest-first using server-rendered body_html and shows an edited indicator', async () => {
    setupFetch({
      journal: [
        journalEntry({
          id: 'aaaaaaaa-0000-0000-0000-000000000001',
          body_html: '<p>Cleaned with soft brush.</p>',
          body_md: 'Cleaned with soft brush.',
          created_at: '2026-05-02T10:00:00Z',
          updated_at: '2026-05-02T10:00:00Z',
        }),
        journalEntry({
          id: 'aaaaaaaa-0000-0000-0000-000000000002',
          body_html: '<p>Initial documentation.</p>',
          body_md: 'Initial documentation.',
          created_at: '2026-05-01T10:00:00Z',
          updated_at: '2026-05-01T11:30:00Z',
        }),
      ],
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.queryByTestId('journal-empty')).not.toBeInTheDocument());

    const entries = screen.getAllByTestId('journal-entry');
    expect(entries).toHaveLength(2);
    expect(entries[0]).toHaveTextContent('Cleaned with soft brush.');
    expect(entries[1]).toHaveTextContent('Initial documentation.');

    // Edited indicator only appears when updated_at > created_at + 1s.
    const edited = screen.getAllByTestId('edited-indicator');
    expect(edited).toHaveLength(1);
    const second = entries[1];
    const editedFirst = edited[0];
    if (!second || !editedFirst) throw new Error('expected entry + indicator');
    expect(second).toContainElement(editedFirst);
  });

  it('shows an empty journal message and no gallery when there are no photos or entries', async () => {
    setupFetch({ photos: [], journal: [] });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('journal-empty')).toBeInTheDocument());
    expect(screen.queryByTestId('hero-photo')).toBeNull();
    expect(screen.queryByTestId('photo-gallery')).toBeNull();
    // The provenance section is hidden when the chain is empty.
    expect(screen.queryByTestId('provenance-section')).toBeNull();
  });

  it('renders the provenance chain in position order when collectors are present', async () => {
    setupFetch({
      collectors: [
        {
          position: 1,
          collector: { id: 'cccccccc-0000-0000-0000-000000000001', name: 'Marie Curie' },
        },
        {
          position: 2,
          collector: { id: 'cccccccc-0000-0000-0000-000000000002', name: 'Auguste Lacroix' },
        },
      ],
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    const section = await screen.findByTestId('provenance-section');
    expect(section).toHaveTextContent('Marie Curie');
    expect(section).toHaveTextContent('Auguste Lacroix');
    const entries = screen.getAllByTestId('provenance-entry');
    expect(entries).toHaveLength(2);
    expect(entries[0]).toHaveTextContent(/^1\.\s*Marie Curie$/);
    expect(entries[1]).toHaveTextContent(/^2\.\s*Auguste Lacroix$/);
  });

  it('treats a collectors fetch error as an empty chain — the page still renders', async () => {
    setupFetch({ collectorsError: true });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('specimen-detail')).toBeInTheDocument());
    expect(screen.queryByTestId('provenance-section')).toBeNull();
  });

  it('treats a journal fetch error as an empty journal — the page still renders', async () => {
    setupFetch({ journalError: true });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('journal-empty')).toBeInTheDocument());
    expect(screen.getByTestId('specimen-detail')).toBeInTheDocument();
  });

  it('opens and closes the lightbox when the hero photo is clicked', async () => {
    setupFetch({
      photos: [
        photo({ id: 'pppppppp-0000-0000-0000-000000000001', position: 1 }),
        photo({ id: 'pppppppp-0000-0000-0000-000000000002', position: 2 }),
      ],
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    const hero = await screen.findByTestId('hero-photo');
    expect(screen.queryByTestId('lightbox')).toBeNull();

    await fireEvent.click(hero);
    expect(screen.getByTestId('lightbox')).toBeInTheDocument();
    expect(screen.getByTestId('lightbox-counter')).toHaveTextContent('1 / 2');

    await fireEvent.click(screen.getByTestId('lightbox-close'));
    await waitFor(() => expect(screen.queryByTestId('lightbox')).toBeNull());
  });

  it('shows the error state when the specimen fetch fails', async () => {
    setupFetch({ specimenError: { code: 'not_found', message: 'specimen not found' } });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('error')).toBeInTheDocument());
    expect(screen.getByText(/specimen not found/)).toBeInTheDocument();
  });

  it('shows the error state when the fetch itself rejects', async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens/{id}') throw new Error('network down');
      return { data: { items: [], next_cursor: null }, error: undefined, response: new Response() };
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('error')).toBeInTheDocument());
    expect(screen.getByText(/network down/)).toBeInTheDocument();
  });
});
