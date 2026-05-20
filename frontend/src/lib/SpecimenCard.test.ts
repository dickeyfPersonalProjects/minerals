import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet, mockPost, mockDelete, mockPatch } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
  mockDelete: vi.fn(),
  mockPatch: vi.fn(),
}));

vi.mock('./api', () => ({
  client: { GET: mockGet, POST: mockPost, DELETE: mockDelete, PATCH: mockPatch },
}));

// V2 BFF cookie flow (mi-3vc4): cookies travel on <img> requests
// automatically, so the card renders the backend path directly on
// `src`. No blob-URL workaround, no helper to mock.

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    link: () => ({ destroy() {} }),
  };
});

import SpecimenCard from './SpecimenCard.svelte';
import { __authenticate, __resetAuthStore } from './auth';
import { __resetQrSheetStore, setNoSheet, setSheet, type QRSheetView } from './qrSheet';

function specimen(
  overrides: Partial<{
    id: string;
    name: string;
    type: 'mineral' | 'rock' | 'meteorite';
    visibility: 'private' | 'unlisted' | 'public';
    locality_text: string | null;
    author_id: string;
  }> = {},
) {
  return {
    id: overrides.id ?? '11111111-1111-1111-1111-111111111111',
    name: overrides.name ?? 'Quartz',
    type: overrides.type ?? 'mineral',
    visibility: overrides.visibility ?? 'private',
    locality_text: overrides.locality_text ?? null,
    // acquired_at, acquired_from, catalog_number, and price_cents are
    // optional in the response shape (mi-fo8 / mi-9ww / mi-z3d0 —
    // per-field visibility redaction omits the key when the viewer
    // can't see it). Tests that don't care about the value omit the key.
    // author_id defaults to the id seeded by __authenticate() so the
    // authed user owns the specimen unless a test overrides it.
    author_id: overrides.author_id ?? '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
    description: '',
    dimensions: {},
    locality: {},
    mass_g: null,
    source_notes: null,
    type_data: {},
    main_image_id: null,
  };
}

function listOk(items: { id: string }[]) {
  return { data: { items, next_cursor: null }, error: undefined, response: new Response() };
}

beforeEach(() => {
  mockGet.mockReset();
  mockPost.mockReset();
  mockDelete.mockReset();
  mockPatch.mockReset();
  __resetQrSheetStore();
  // Most tests exercise the authed path; the anonymous block at
  // the bottom of this file explicitly resets the store.
  __authenticate();
});

afterEach(() => {
  cleanup();
  __resetQrSheetStore();
  __resetAuthStore();
});

function sheetView(over: Partial<QRSheetView> = {}): QRSheetView {
  return {
    id: '99999999-9999-9999-9999-999999999999',
    template: 'avery-5160',
    page_count: 0,
    specimens: [],
    created_at: '2026-05-10T00:00:00Z',
    updated_at: '2026-05-10T00:00:00Z',
    ...over,
  };
}

const SPECIMEN_ID = '11111111-1111-1111-1111-111111111111';

describe('SpecimenCard', () => {
  it('renders the first-photo thumbnail when the API returns ≥1 photo', async () => {
    mockGet.mockResolvedValue(listOk([{ id: 'aaaa-photo-1' }]));

    render(SpecimenCard, { specimen: specimen({ name: 'Smoky quartz' }) });

    const img = (await waitFor(() =>
      screen.getByAltText('Photo of Smoky quartz'),
    )) as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/v1/photos/aaaa-photo-1/thumb');
    // Sanity: API was called for the specimen's photos.
    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(mockGet.mock.calls[0]?.[0]).toBe('/api/v1/specimens/{id}/photos');
  });

  it('falls back to the placeholder when the API returns zero photos', async () => {
    mockGet.mockResolvedValue(listOk([]));

    render(SpecimenCard, { specimen: specimen({ name: 'Pyrite' }) });

    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    // No <img> rendered — the SVG placeholder takes its place.
    expect(screen.queryByAltText('Photo of Pyrite')).not.toBeInTheDocument();
  });

  it('falls back to the placeholder when the API returns an error', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'internal', message: 'boom' } },
      response: new Response(null, { status: 500 }),
    });

    render(SpecimenCard, { specimen: specimen({ name: 'Galena' }) });

    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(screen.queryByAltText('Photo of Galena')).not.toBeInTheDocument();
  });

  describe('QR sheet affordance', () => {
    function specimenWithId() {
      const sp = specimen();
      return { ...sp, id: SPECIMEN_ID };
    }

    it('shows "+ Add to QR code sheet" when the specimen is not on the sheet', () => {
      mockGet.mockResolvedValue(listOk([]));
      setNoSheet();
      render(SpecimenCard, { specimen: specimenWithId() });
      expect(screen.getByTestId('qr-sheet-add')).toHaveTextContent('Add to QR code sheet');
      expect(screen.queryByTestId('qr-sheet-badge')).not.toBeInTheDocument();
    });

    it('shows the "On QR sheet" badge + remove button when the specimen is on the sheet', () => {
      mockGet.mockResolvedValue(listOk([]));
      setSheet(
        sheetView({
          specimens: [
            {
              specimen_id: SPECIMEN_ID,
              name: 'Quartz',
              position: 1,
              thumbnail_url: null,
              added_at: '2026-05-10T00:00:00Z',
            },
          ],
        }),
      );
      render(SpecimenCard, { specimen: specimenWithId() });
      expect(screen.getByTestId('qr-sheet-badge')).toBeInTheDocument();
      expect(screen.getByTestId('qr-sheet-remove')).toBeInTheDocument();
      expect(screen.queryByTestId('qr-sheet-add')).not.toBeInTheDocument();
    });

    it('clicking "+ Add to QR code sheet" with an existing sheet POSTs the specimen and skips the dialog', async () => {
      mockGet.mockResolvedValue(listOk([]));
      setSheet(sheetView());
      mockPost.mockResolvedValueOnce({
        data: sheetView({
          specimens: [
            {
              specimen_id: SPECIMEN_ID,
              name: 'Quartz',
              position: 1,
              thumbnail_url: null,
              added_at: '2026-05-10T00:00:00Z',
            },
          ],
        }),
        error: undefined,
        response: new Response(),
      });
      render(SpecimenCard, { specimen: specimenWithId() });
      await fireEvent.click(screen.getByTestId('qr-sheet-add'));
      await waitFor(() =>
        expect(mockPost).toHaveBeenCalledWith('/api/v1/qr-sheet/specimens', {
          body: { specimen_id: SPECIMEN_ID },
        }),
      );
      // Dialog must never appear when a sheet already exists.
      expect(screen.queryByTestId('template-selector')).not.toBeInTheDocument();
    });

    it('clicking "+ Add to QR code sheet" with no sheet opens the template selector', async () => {
      mockGet.mockResolvedValue(listOk([]));
      setNoSheet();
      render(SpecimenCard, { specimen: specimenWithId() });
      await fireEvent.click(screen.getByTestId('qr-sheet-add'));
      await screen.findByTestId('template-selector');
      // No mutation before the user picks a template.
      expect(mockPost).not.toHaveBeenCalled();
    });

    it('confirming the template creates the sheet then adds the specimen', async () => {
      mockGet.mockResolvedValue(listOk([]));
      setNoSheet();
      // POST sequence: create sheet → add specimen.
      mockPost
        .mockResolvedValueOnce({
          data: sheetView({ template: 'avery-l7160' }),
          error: undefined,
          response: new Response(null, { status: 201 }),
        })
        .mockResolvedValueOnce({
          data: sheetView({
            template: 'avery-l7160',
            specimens: [
              {
                specimen_id: SPECIMEN_ID,
                name: 'Quartz',
                position: 1,
                thumbnail_url: null,
                added_at: '2026-05-10T00:00:00Z',
              },
            ],
          }),
          error: undefined,
          response: new Response(),
        });

      render(SpecimenCard, { specimen: specimenWithId() });
      await fireEvent.click(screen.getByTestId('qr-sheet-add'));
      const target = screen
        .getAllByTestId('template-option')
        .find((o) => o.getAttribute('data-template-id') === 'avery-l7160')!;
      await fireEvent.click(target);
      await fireEvent.click(screen.getByTestId('template-selector-confirm'));

      await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(2));
      expect(mockPost.mock.calls[0]?.[0]).toBe('/api/v1/qr-sheet');
      expect(mockPost.mock.calls[0]?.[1]).toEqual(
        expect.objectContaining({ body: { template: 'avery-l7160' } }),
      );
      expect(mockPost.mock.calls[1]?.[0]).toBe('/api/v1/qr-sheet/specimens');
    });

    it('clicking remove DELETEs the specimen from the sheet', async () => {
      mockGet.mockResolvedValue(listOk([]));
      setSheet(
        sheetView({
          specimens: [
            {
              specimen_id: SPECIMEN_ID,
              name: 'Quartz',
              position: 1,
              thumbnail_url: null,
              added_at: '2026-05-10T00:00:00Z',
            },
          ],
        }),
      );
      mockDelete.mockResolvedValueOnce({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 204 }),
      });
      render(SpecimenCard, { specimen: specimenWithId() });
      await fireEvent.click(screen.getByTestId('qr-sheet-remove'));
      await waitFor(() =>
        expect(mockDelete).toHaveBeenCalledWith('/api/v1/qr-sheet/specimens/{specimen_id}', {
          params: { path: { specimen_id: SPECIMEN_ID } },
        }),
      );
    });

    it('remove failure rolls back the optimistic state', async () => {
      mockGet.mockResolvedValue(listOk([]));
      const before = sheetView({
        specimens: [
          {
            specimen_id: SPECIMEN_ID,
            name: 'Quartz',
            position: 1,
            thumbnail_url: null,
            added_at: '2026-05-10T00:00:00Z',
          },
        ],
      });
      setSheet(before);
      mockDelete.mockResolvedValueOnce({
        data: undefined,
        error: { error: { code: 'internal', message: 'boom' } },
        response: new Response(null, { status: 500 }),
      });
      render(SpecimenCard, { specimen: specimenWithId() });
      await fireEvent.click(screen.getByTestId('qr-sheet-remove'));
      // The badge should reappear after rollback (optimistic removal undone).
      await waitFor(() => {
        expect(screen.getByTestId('qr-sheet-badge')).toBeInTheDocument();
        expect(screen.queryByTestId('qr-sheet-add')).not.toBeInTheDocument();
      });
    });

    it('QR sheet buttons live outside the card link so clicking them never navigates', () => {
      mockGet.mockResolvedValue(listOk([]));
      setNoSheet();
      render(SpecimenCard, { specimen: specimenWithId() });
      const link = screen.getByTestId('specimen-card-link');
      const addBtn = screen.getByTestId('qr-sheet-add');
      // The footer (and its buttons) must not be inside the
      // anchor — nested interactive elements + bubble navigation
      // would otherwise trigger a route change every click.
      expect(link.contains(addBtn)).toBe(false);
    });
  });

  describe('inline visibility editor (mi-35hk)', () => {
    it('renders the editor on a specimen the current user owns', () => {
      mockGet.mockResolvedValue(listOk([]));
      render(SpecimenCard, { specimen: specimen({ visibility: 'unlisted' }) });
      const select = screen.getByTestId('visibility-select') as HTMLSelectElement;
      expect(select).toBeInTheDocument();
      expect(select.value).toBe('unlisted');
    });

    it('hides the editor on a specimen owned by another user', () => {
      mockGet.mockResolvedValue(listOk([]));
      render(SpecimenCard, {
        specimen: specimen({ author_id: '22222222-2222-2222-2222-222222222222' }),
      });
      expect(screen.queryByTestId('visibility-select')).not.toBeInTheDocument();
    });

    it('hides the editor for anonymous viewers', () => {
      __resetAuthStore();
      mockGet.mockResolvedValue(listOk([]));
      render(SpecimenCard, { specimen: specimen() });
      expect(screen.queryByTestId('visibility-select')).not.toBeInTheDocument();
    });

    it('changing the value PATCHes with the new visibility', async () => {
      mockGet.mockResolvedValue(listOk([]));
      mockPatch.mockResolvedValueOnce({
        data: { visibility: 'public' },
        error: undefined,
        response: new Response(),
      });
      render(SpecimenCard, { specimen: specimen({ visibility: 'private' }) });
      const select = screen.getByTestId('visibility-select') as HTMLSelectElement;
      await fireEvent.change(select, { target: { value: 'public' } });
      await waitFor(() =>
        expect(mockPatch).toHaveBeenCalledWith('/api/v1/specimens/{id}', {
          params: { path: { id: SPECIMEN_ID } },
          body: { visibility: 'public' },
        }),
      );
      // Optimistic: the chip reflects the new value immediately.
      expect(screen.getByTestId('visibility-chip')).toHaveTextContent('public');
    });

    it('reverts the optimistic value when the PATCH fails', async () => {
      mockGet.mockResolvedValue(listOk([]));
      mockPatch.mockResolvedValueOnce({
        data: undefined,
        error: { error: { code: 'internal', message: 'boom' } },
        response: new Response(null, { status: 500 }),
      });
      render(SpecimenCard, { specimen: specimen({ visibility: 'private' }) });
      const select = screen.getByTestId('visibility-select') as HTMLSelectElement;
      await fireEvent.change(select, { target: { value: 'public' } });
      await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
      // Rolled back to private → no chip (private is the hidden state).
      await waitFor(() => {
        expect(select.value).toBe('private');
        expect(screen.queryByTestId('visibility-chip')).not.toBeInTheDocument();
      });
    });

    it('notifies the parent on change and on revert', async () => {
      mockGet.mockResolvedValue(listOk([]));
      mockPatch.mockResolvedValueOnce({
        data: undefined,
        error: { error: { code: 'internal', message: 'boom' } },
        response: new Response(null, { status: 500 }),
      });
      const onVisibilityChange = vi.fn();
      render(SpecimenCard, {
        specimen: specimen({ visibility: 'private' }),
        onVisibilityChange,
      });
      await fireEvent.change(screen.getByTestId('visibility-select'), {
        target: { value: 'unlisted' },
      });
      await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
      // First the optimistic notification, then the revert.
      expect(onVisibilityChange).toHaveBeenNthCalledWith(1, SPECIMEN_ID, 'unlisted');
      expect(onVisibilityChange).toHaveBeenNthCalledWith(2, SPECIMEN_ID, 'private');
    });
  });

  it('aborts the in-flight GET on unmount', async () => {
    let capturedSignal: AbortSignal | undefined;
    mockGet.mockImplementation(async (_path: string, opts: { signal?: AbortSignal }) => {
      capturedSignal = opts.signal;
      // Never resolve — leave the request "in flight" so unmount aborts it.
      return await new Promise(() => {});
    });

    render(SpecimenCard, { specimen: specimen() });

    await waitFor(() => expect(capturedSignal).toBeDefined());
    expect(capturedSignal!.aborted).toBe(false);

    cleanup();

    expect(capturedSignal!.aborted).toBe(true);
  });

  describe('when unauthenticated', () => {
    it('hides the QR-sheet add/remove buttons (mi-eec)', async () => {
      __resetAuthStore();
      setNoSheet();
      mockGet.mockResolvedValue(listOk([]));
      render(SpecimenCard, { specimen: specimen() });
      expect(screen.queryByTestId('qr-sheet-add')).not.toBeInTheDocument();
      expect(screen.queryByTestId('qr-sheet-remove')).not.toBeInTheDocument();
    });

    it('hides the on-sheet remove control when already on a sheet', async () => {
      __resetAuthStore();
      const s = specimen();
      setSheet(
        sheetView({
          specimens: [
            {
              specimen_id: s.id,
              name: s.name,
              position: 1,
              added_at: '2026-05-10T00:00:00Z',
              thumbnail_url: null,
            },
          ],
        }),
      );
      mockGet.mockResolvedValue(listOk([]));
      render(SpecimenCard, { specimen: s });
      expect(screen.queryByTestId('qr-sheet-badge')).not.toBeInTheDocument();
      expect(screen.queryByTestId('qr-sheet-remove')).not.toBeInTheDocument();
    });
  });
});
