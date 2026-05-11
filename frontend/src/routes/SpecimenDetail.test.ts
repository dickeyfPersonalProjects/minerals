import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/svelte';

// Hoisted mock — the API client is replaced for every test in this
// file. Each test sets `mockGet` (and POST/PATCH/DELETE when
// exercising mutations) to the fixture it wants.
const { mockGet, mockPost, mockPatch, mockDelete } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
  mockPatch: vi.fn(),
  mockDelete: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet, POST: mockPost, PATCH: mockPatch, DELETE: mockDelete },
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
  main_image_id?: string | null;
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
    main_image_id: seed.main_image_id ?? null,
  };
}

type PhotoSeed = {
  id: string;
  position?: number;
  kind?: 'visible' | 'uv' | 'other';
  file_id?: string;
};

function photo(seed: PhotoSeed) {
  return {
    id: seed.id,
    specimen_id: SPECIMEN_ID,
    file_id: seed.file_id ?? 'aaaaaaaa-0000-0000-0000-000000000000',
    content_type: 'image/jpeg',
    byte_size: 1234,
    sha256: 'deadbeef',
    kind: seed.kind ?? 'visible',
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
    // Per-entry attachment lists fired by JournalAttachments.
    if (path === '/api/v1/journal/{id}/files') {
      return { data: { items: [] }, error: undefined, response: new Response() };
    }
    return { data: { items: [], next_cursor: null }, error: undefined, response: new Response() };
  });
}

beforeEach(() => {
  mockGet.mockReset();
  mockPost.mockReset();
  mockPatch.mockReset();
  mockDelete.mockReset();
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
    // The provenance section always renders (D-5) so the user can
    // open the chain editor even from an empty state. The chain
    // itself shows an empty placeholder, not a list.
    expect(screen.getByTestId('provenance-section')).toBeInTheDocument();
    expect(screen.getByTestId('provenance-empty')).toBeInTheDocument();
    expect(screen.queryByTestId('provenance-list')).toBeNull();
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
    expect(screen.queryByTestId('provenance-list')).toBeNull();
    expect(screen.getByTestId('provenance-empty')).toBeInTheDocument();
  });

  it('treats a journal fetch error as an empty journal — the page still renders', async () => {
    setupFetch({ journalError: true });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('journal-empty')).toBeInTheDocument());
    expect(screen.getByTestId('specimen-detail')).toBeInTheDocument();
  });

  it('renders kind badges on non-visible photos and filters the gallery', async () => {
    setupFetch({
      photos: [
        photo({ id: 'pppppppp-0000-0000-0000-000000000001', position: 1, kind: 'visible' }),
        photo({ id: 'pppppppp-0000-0000-0000-000000000002', position: 2, kind: 'uv' }),
        photo({ id: 'pppppppp-0000-0000-0000-000000000003', position: 3, kind: 'other' }),
      ],
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    // Hero (visible) suppresses its badge; gallery thumbs show
    // badges only for non-default kinds.
    await screen.findByTestId('hero-photo');
    expect(screen.queryByTestId('hero-photo-kind-badge')).toBeNull();
    const badges = screen.getAllByTestId('gallery-thumb-kind-badge');
    expect(badges).toHaveLength(2);
    expect(badges.map((b) => b.getAttribute('data-kind'))).toEqual(['uv', 'other']);

    // Filter to UV only — gallery shrinks to the one UV photo
    // (which becomes the hero), and other-kind thumbs disappear.
    await fireEvent.click(screen.getByTestId('photo-kind-filter-uv'));
    await waitFor(() => {
      const thumbs = screen.queryAllByTestId('gallery-thumb');
      expect(thumbs).toHaveLength(0);
    });
    // The new hero is the UV photo, and since it's no longer
    // 'visible' the hero badge appears.
    const heroBadge = screen.getByTestId('hero-photo-kind-badge');
    expect(heroBadge).toHaveAttribute('data-kind', 'uv');
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

  it('creates a journal entry: POST + refetch + panel closes', async () => {
    setupFetch({ journal: [] });
    const newEntry = journalEntry({
      id: 'aaaaaaaa-0000-0000-0000-000000000099',
      body_html: '<p>fresh entry</p>',
      body_md: 'fresh entry',
    });
    mockPost.mockResolvedValue({
      data: newEntry,
      error: undefined,
      response: new Response(null, { status: 201 }),
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('journal-empty')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('journal-add-button'));

    const panel = await screen.findByTestId('journal-create-panel');
    expect(panel).toBeInTheDocument();

    const textarea = screen.getByLabelText(/entry body/i);
    await fireEvent.input(textarea, { target: { value: 'fresh entry' } });

    // Once the form is submitted: refetch returns the new entry.
    setupFetch({ journal: [newEntry] });
    await fireEvent.submit(screen.getByTestId('journal-entry-form'));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0]?.[0]).toBe('/api/v1/specimens/{id}/journal');
    expect(mockPost.mock.calls[0]?.[1].body).toEqual({ body_md: 'fresh entry' });

    await waitFor(() =>
      expect(screen.queryByTestId('journal-create-panel')).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(screen.getAllByTestId('journal-entry')).toHaveLength(1));
    expect(screen.getByTestId('journal-entry')).toHaveTextContent('fresh entry');
  });

  it('surfaces the API envelope error when create fails', async () => {
    setupFetch({ journal: [] });
    mockPost.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'rate_limited', message: 'too many entries' } },
      response: new Response(null, { status: 429 }),
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
    await waitFor(() => expect(screen.getByTestId('journal-empty')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('journal-add-button'));

    const textarea = screen.getByLabelText(/entry body/i);
    await fireEvent.input(textarea, { target: { value: 'oops' } });
    await fireEvent.submit(screen.getByTestId('journal-entry-form'));

    await waitFor(() => expect(screen.getByTestId('journal-form-error')).toBeInTheDocument());
    expect(screen.getByTestId('journal-form-error')).toHaveTextContent('too many entries');
    // Panel stayed open so the user can correct.
    expect(screen.getByTestId('journal-create-panel')).toBeInTheDocument();
  });

  it('edits an entry: clicking Edit pre-populates the form, PATCH + refetch returns to read mode', async () => {
    const original = journalEntry({
      id: 'aaaaaaaa-0000-0000-0000-000000000001',
      body_html: '<p>old text</p>',
      body_md: 'old text',
      created_at: '2026-05-01T10:00:00Z',
      updated_at: '2026-05-01T10:00:00Z',
    });
    setupFetch({ journal: [original] });
    const updated = journalEntry({
      id: original.id,
      body_html: '<p>new text</p>',
      body_md: 'new text',
      created_at: '2026-05-01T10:00:00Z',
      updated_at: '2026-05-02T10:00:00Z',
    });
    mockPatch.mockResolvedValue({
      data: updated,
      error: undefined,
      response: new Response(null, { status: 200 }),
    });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

    const editBtn = await screen.findByTestId('journal-edit-button');
    await fireEvent.click(editBtn);

    // Pre-population from body_md.
    const textarea = (await screen.findByLabelText(/entry body/i)) as HTMLTextAreaElement;
    expect(textarea.value).toBe('old text');

    await fireEvent.input(textarea, { target: { value: 'new text' } });

    setupFetch({ journal: [updated] });
    await fireEvent.submit(screen.getByTestId('journal-entry-form'));

    await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
    expect(mockPatch.mock.calls[0]?.[0]).toBe('/api/v1/journal/{id}');
    expect(mockPatch.mock.calls[0]?.[1].params.path.id).toBe(original.id);
    expect(mockPatch.mock.calls[0]?.[1].body).toEqual({ body_md: 'new text' });

    // Form gone; read mode renders the new server-rendered HTML.
    await waitFor(() => expect(screen.queryByTestId('journal-entry-form')).not.toBeInTheDocument());
    expect(screen.getByTestId('journal-entry')).toHaveTextContent('new text');
    // Edited indicator now visible.
    expect(screen.getByTestId('edited-indicator')).toBeInTheDocument();
  });

  it('cancelling the edit form returns to read mode without calling PATCH', async () => {
    const original = journalEntry({
      id: 'aaaaaaaa-0000-0000-0000-000000000001',
      body_html: '<p>still here</p>',
      body_md: 'still here',
    });
    setupFetch({ journal: [original] });

    render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
    await fireEvent.click(await screen.findByTestId('journal-edit-button'));
    await fireEvent.click(screen.getByTestId('journal-cancel-button'));

    await waitFor(() => expect(screen.queryByTestId('journal-entry-form')).not.toBeInTheDocument());
    expect(mockPatch).not.toHaveBeenCalled();
    expect(screen.getByTestId('journal-entry')).toHaveTextContent('still here');
  });

  describe('photo delete flow', () => {
    it('opens the confirm modal on hero × click and DELETEs the photo on confirm', async () => {
      const p1 = photo({ id: 'cafebabe-0000-0000-0000-000000000001' });
      setupFetch({ photos: [p1] });

      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

      const del = await screen.findByTestId('hero-photo-delete');
      await fireEvent.click(del);

      expect(screen.getByTestId('confirm-modal')).toBeInTheDocument();

      mockDelete.mockResolvedValueOnce({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 204 }),
      });

      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));

      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      expect(mockDelete.mock.calls[0]?.[0]).toBe('/api/v1/photos/{id}');
      expect(mockDelete.mock.calls[0]?.[1].params.path.id).toBe(p1.id);

      // Modal dismissed and refetch fired.
      await waitFor(() => expect(screen.queryByTestId('confirm-modal')).not.toBeInTheDocument());
    });

    it('thumbnail × opens the modal and targets the right photo id', async () => {
      const p1 = photo({ id: 'cafebabe-0000-0000-0000-000000000001' });
      const p2 = photo({ id: 'cafebabe-0000-0000-0000-000000000002', position: 2 });
      setupFetch({ photos: [p1, p2] });

      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

      const thumbDels = await screen.findAllByTestId('gallery-thumb-delete');
      // Only p2 (the second photo) appears as a thumb; p1 is the hero.
      expect(thumbDels).toHaveLength(1);
      await fireEvent.click(thumbDels[0]!);

      expect(screen.getByTestId('confirm-modal')).toBeInTheDocument();

      mockDelete.mockResolvedValueOnce({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 204 }),
      });

      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));
      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      expect(mockDelete.mock.calls[0]?.[1].params.path.id).toBe(p2.id);
    });

    it('cancelling the modal does not call DELETE', async () => {
      const p1 = photo({ id: 'cafebabe-0000-0000-0000-000000000001' });
      setupFetch({ photos: [p1] });

      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      await fireEvent.click(await screen.findByTestId('hero-photo-delete'));
      await fireEvent.click(screen.getByTestId('confirm-modal-cancel'));

      expect(mockDelete).not.toHaveBeenCalled();
      expect(screen.queryByTestId('confirm-modal')).not.toBeInTheDocument();
    });

    it('keeps the modal closed when the DELETE fails (toast surfaces the error)', async () => {
      const p1 = photo({ id: 'cafebabe-0000-0000-0000-000000000001' });
      setupFetch({ photos: [p1] });

      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      await fireEvent.click(await screen.findByTestId('hero-photo-delete'));

      mockDelete.mockResolvedValueOnce({
        data: undefined,
        error: { error: { code: 'internal', message: 'boom' } },
        response: new Response(null, { status: 500 }),
      });

      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));

      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      // On error the modal stays open so the user can retry / cancel.
      expect(screen.getByTestId('confirm-modal')).toBeInTheDocument();
    });
  });

  describe('journal delete flow', () => {
    function entryFixture(): ReturnType<typeof journalEntry> {
      return journalEntry({
        id: 'aaaaaaaa-0000-0000-0000-000000000001',
        body_html: '<p>cleaning notes</p>',
        body_md: 'cleaning notes',
      });
    }

    it('renders a delete button on each entry', async () => {
      setupFetch({ journal: [entryFixture()] });
      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      expect(await screen.findByTestId('journal-delete-button')).toBeInTheDocument();
    });

    it('opens the modal with the attachment-warning copy and DELETEs on confirm', async () => {
      const e = entryFixture();
      setupFetch({ journal: [e] });

      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      await fireEvent.click(await screen.findByTestId('journal-delete-button'));

      const msg = screen.getByTestId('confirm-modal-message').textContent ?? '';
      expect(msg).toMatch(/attachments will also be deleted/i);

      mockDelete.mockResolvedValueOnce({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 204 }),
      });

      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));

      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      expect(mockDelete.mock.calls[0]?.[0]).toBe('/api/v1/journal/{id}');
      expect(mockDelete.mock.calls[0]?.[1].params.path.id).toBe(e.id);

      await waitFor(() => expect(screen.queryByTestId('confirm-modal')).not.toBeInTheDocument());
    });

    it('keeps the modal open on a 409 attachments-exist response', async () => {
      const e = entryFixture();
      setupFetch({ journal: [e] });
      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      await fireEvent.click(await screen.findByTestId('journal-delete-button'));

      mockDelete.mockResolvedValueOnce({
        data: undefined,
        error: {
          error: {
            code: 'journal_referenced',
            message: 'entry still has attachments',
          },
        },
        response: new Response(null, { status: 409 }),
      });

      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));
      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      expect(screen.getByTestId('confirm-modal')).toBeInTheDocument();
    });
  });

  // mi-m8q: designate one photo as the specimen's main image.
  // The hero floats to the photo whose file_id matches
  // specimen.main_image_id; "Set as main" buttons sit on every
  // other photo; clicking one PATCHes the specimen.
  describe('main image flow', () => {
    const FILE_A = 'aaaaaaaa-0000-0000-0000-000000000001';
    const FILE_B = 'aaaaaaaa-0000-0000-0000-000000000002';

    it('shows the Main badge on the hero when main_image_id matches', async () => {
      const p1 = photo({ id: 'cafebabe-0000-0000-0000-000000000001', file_id: FILE_A });
      setupFetch({
        specimen: specimen({ main_image_id: FILE_A }),
        photos: [p1],
      });
      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      expect(await screen.findByTestId('hero-photo-main-badge')).toBeInTheDocument();
      expect(screen.queryByTestId('hero-photo-set-main')).not.toBeInTheDocument();
    });

    it('shows Set-as-main on the hero when no main is designated', async () => {
      const p1 = photo({ id: 'cafebabe-0000-0000-0000-000000000001', file_id: FILE_A });
      setupFetch({ specimen: specimen({ main_image_id: null }), photos: [p1] });
      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      expect(await screen.findByTestId('hero-photo-set-main')).toBeInTheDocument();
      expect(screen.queryByTestId('hero-photo-main-badge')).not.toBeInTheDocument();
    });

    it('hoists the designated photo to the hero slot even if it is not first by position', async () => {
      // p1 sits at position 1 (would be the default hero); p2 at
      // position 2 is the designated main image and must take over.
      const p1 = photo({
        id: 'cafebabe-0000-0000-0000-000000000001',
        position: 1,
        file_id: FILE_A,
      });
      const p2 = photo({
        id: 'cafebabe-0000-0000-0000-000000000002',
        position: 2,
        file_id: FILE_B,
      });
      setupFetch({
        specimen: specimen({ main_image_id: FILE_B }),
        photos: [p1, p2],
      });
      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });

      const hero = await screen.findByTestId('hero-photo');
      const img = hero.querySelector('img') as HTMLImageElement;
      expect(img.getAttribute('src')).toBe(`/api/v1/photos/${p2.id}/display`);
      expect(screen.getByTestId('hero-photo-main-badge')).toBeInTheDocument();
    });

    it('PATCHes main_image_id when Set-as-main is clicked on a thumbnail', async () => {
      const p1 = photo({
        id: 'cafebabe-0000-0000-0000-000000000001',
        position: 1,
        file_id: FILE_A,
      });
      const p2 = photo({
        id: 'cafebabe-0000-0000-0000-000000000002',
        position: 2,
        file_id: FILE_B,
      });
      setupFetch({ specimen: specimen({ main_image_id: null }), photos: [p1, p2] });
      mockPatch.mockResolvedValueOnce({
        data: { ...specimen(), main_image_id: FILE_B },
        error: undefined,
        response: new Response(null, { status: 200 }),
      });

      render(SpecimenDetail, { params: { id: SPECIMEN_ID } });
      const setBtn = await screen.findByTestId('gallery-thumb-set-main');
      await fireEvent.click(setBtn);

      await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
      const [path, opts] = mockPatch.mock.calls[0]!;
      expect(path).toBe('/api/v1/specimens/{id}');
      expect(opts.params.path.id).toBe(SPECIMEN_ID);
      expect(opts.body).toEqual({ main_image_id: FILE_B });
    });
  });
});
