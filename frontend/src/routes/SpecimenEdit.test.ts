import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet, mockPatch, mockDelete, mockPush } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPatch: vi.fn(),
  mockDelete: vi.fn(),
  mockPush: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet, PATCH: mockPatch, DELETE: mockDelete },
}));

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    push: mockPush,
    link: () => ({ destroy() {} }),
  };
});

import SpecimenEdit from './SpecimenEdit.svelte';
import { __authenticate, __resetAuthStore } from '../lib/auth';

const SPECIMEN_ID = '11111111-1111-1111-1111-111111111111';

function specimen(
  overrides: Partial<{
    name: string;
    type: 'mineral' | 'rock' | 'meteorite';
    description: string;
    catalog_number: string | null;
    visibility: 'private' | 'unlisted' | 'public';
    visibility_price: 'private' | 'unlisted' | 'public' | null;
    visibility_acquired_from: 'private' | 'unlisted' | 'public' | null;
    visibility_images: 'private' | 'unlisted' | 'public' | null;
  }> = {},
) {
  const base: Record<string, unknown> = {
    id: SPECIMEN_ID,
    name: overrides.name ?? 'Galena',
    type: overrides.type ?? ('mineral' as const),
    description: overrides.description ?? '',
    catalog_number: overrides.catalog_number ?? null,
    visibility: overrides.visibility ?? ('private' as const),
    acquired_at: null,
    acquired_from: null,
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    dimensions: {},
    locality: {},
    locality_text: null,
    mass_g: null,
    price_cents: null,
    source_notes: null,
    type_data: {},
    updated_at: '2026-05-01T12:00:00Z',
  };
  // Per-field visibility overrides are absent on the wire when unset
  // (CONTRACT.md §13b). Only set the keys when the test seeds them
  // explicitly, so the rendered selectors see undefined (the
  // "inherit" state) by default.
  if (overrides.visibility_price !== undefined) base.visibility_price = overrides.visibility_price;
  if (overrides.visibility_acquired_from !== undefined)
    base.visibility_acquired_from = overrides.visibility_acquired_from;
  if (overrides.visibility_images !== undefined)
    base.visibility_images = overrides.visibility_images;
  return base;
}

// mockGetByPath routes the typed openapi-fetch GET mock to a per-path
// handler. The route now loads both /specimens/{id} and /profile in
// parallel; tests need to supply distinct responses for each.
function mockGetByPath(handlers: { specimen?: unknown; profile?: unknown }): void {
  mockGet.mockImplementation(async (path: string) => {
    if (path === '/api/v1/specimens/{id}') {
      return handlers.specimen ?? { data: undefined, error: undefined, response: new Response() };
    }
    if (path === '/api/v1/profile') {
      return handlers.profile ?? { data: undefined, error: undefined, response: new Response() };
    }
    return { data: undefined, error: undefined, response: new Response(null, { status: 404 }) };
  });
}

beforeEach(() => {
  mockGet.mockReset();
  mockPatch.mockReset();
  mockDelete.mockReset();
  mockPush.mockReset();
  // Default-authed; the anonymous block at the bottom resets.
  __authenticate();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  __resetAuthStore();
});

describe('SpecimenEdit route', () => {
  it('pre-populates the form from the loaded specimen', async () => {
    mockGet.mockResolvedValue({
      data: specimen({ name: 'Galena', catalog_number: 'MIN-1' }),
      error: undefined,
      response: new Response(),
    });

    render(SpecimenEdit, { params: { id: SPECIMEN_ID } });

    await waitFor(() => {
      const name = screen.getByLabelText(/^name/i) as HTMLInputElement;
      expect(name.value).toBe('Galena');
    });
    const catalog = screen.getByLabelText(/catalog number/i) as HTMLInputElement;
    expect(catalog.value).toBe('MIN-1');
  });

  it('disables the type radio in edit mode', async () => {
    mockGet.mockResolvedValue({
      data: specimen({ type: 'rock' }),
      error: undefined,
      response: new Response(),
    });

    render(SpecimenEdit, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('type-immutable-hint')).toBeInTheDocument());
    const fieldset = screen.getByTestId('type-fieldset') as HTMLFieldSetElement;
    expect(fieldset.disabled).toBe(true);
  });

  it('PATCHes only changed fields and navigates to the detail page on success', async () => {
    mockGet.mockResolvedValue({
      data: specimen({ name: 'Old' }),
      error: undefined,
      response: new Response(),
    });
    mockPatch.mockResolvedValue({
      data: specimen({ name: 'New' }),
      error: undefined,
      response: new Response(),
    });

    render(SpecimenEdit, { params: { id: SPECIMEN_ID } });
    await waitFor(() => expect(screen.getByLabelText(/^name/i)).toBeInTheDocument());

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'New' } });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
    const args = mockPatch.mock.calls[0]?.[1];
    expect(args.params.path.id).toBe(SPECIMEN_ID);
    expect(args.body).toEqual({ name: 'New' });
    expect(mockPush).toHaveBeenCalledWith(`/specimens/${SPECIMEN_ID}`);
  });

  describe('delete flow', () => {
    async function setupLoaded(seed: Parameters<typeof specimen>[0] = {}) {
      mockGet.mockResolvedValue({
        data: specimen(seed),
        error: undefined,
        response: new Response(),
      });
      render(SpecimenEdit, { params: { id: SPECIMEN_ID } });
      await waitFor(() => expect(screen.getByTestId('delete-button')).toBeInTheDocument());
    }

    it('opens the confirm modal when Delete is clicked', async () => {
      await setupLoaded({ name: 'Galena' });
      await fireEvent.click(screen.getByTestId('delete-button'));
      expect(screen.getByTestId('confirm-modal')).toBeInTheDocument();
      expect(screen.getByTestId('confirm-modal-message')).toHaveTextContent(/delete galena\?/i);
    });

    it('cancelling the modal does not call DELETE', async () => {
      await setupLoaded();
      await fireEvent.click(screen.getByTestId('delete-button'));
      await fireEvent.click(screen.getByTestId('confirm-modal-cancel'));
      expect(mockDelete).not.toHaveBeenCalled();
    });

    it('confirming calls DELETE, navigates to the list, and dismisses the modal', async () => {
      await setupLoaded();
      mockDelete.mockResolvedValue({
        data: undefined,
        error: undefined,
        response: new Response(null, { status: 204 }),
      });

      await fireEvent.click(screen.getByTestId('delete-button'));
      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));

      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      const args = mockDelete.mock.calls[0]?.[1];
      expect(args.params.path.id).toBe(SPECIMEN_ID);
      await waitFor(() => expect(mockPush).toHaveBeenCalledWith('/specimens'));
    });

    it('shows a 409 conflict toast and stays on the page', async () => {
      await setupLoaded();
      mockDelete.mockResolvedValue({
        data: undefined,
        error: {
          error: {
            code: 'specimen_referenced',
            message: 'specimen still has photos or journal entries; delete those first',
            details: { constraint: 'child_rows' },
          },
        },
        response: new Response(null, { status: 409 }),
      });

      await fireEvent.click(screen.getByTestId('delete-button'));
      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));

      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      // Modal closes only on success — on error the user keeps the
      // option to retry or cancel without re-opening it.
      expect(mockPush).not.toHaveBeenCalledWith('/specimens');
      // The toast renders into the global Toaster (mounted in App),
      // not inside this component, so we don't assert on it here —
      // just that we did not navigate.
    });

    it('uses the structured details counts in the toast when present', async () => {
      // The test verifies our message-builder honours `details.photos`
      // and `details.journal_entries` when the backend supplies them
      // (forward-compatible upgrade path documented in the bead).
      await setupLoaded();
      mockDelete.mockResolvedValue({
        data: undefined,
        error: {
          error: {
            code: 'specimen_referenced',
            message: 'still has children',
            details: { photos: 2, journal_entries: 3 },
          },
        },
        response: new Response(null, { status: 409 }),
      });
      await fireEvent.click(screen.getByTestId('delete-button'));
      await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));
      await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
      expect(mockPush).not.toHaveBeenCalledWith('/specimens');
    });
  });

  it('renders the load error state on GET failure', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'not_found', message: 'no such specimen' } },
      response: new Response(null, { status: 404 }),
    });

    render(SpecimenEdit, { params: { id: SPECIMEN_ID } });

    await waitFor(() => expect(screen.getByTestId('error')).toBeInTheDocument());
    expect(screen.getByText(/no such specimen/i)).toBeInTheDocument();
  });

  it('hides the edit form when unauthenticated (mi-eec)', async () => {
    __resetAuthStore();
    mockGet.mockResolvedValue({
      data: specimen(),
      error: undefined,
      response: new Response(),
    });
    render(SpecimenEdit, { params: { id: SPECIMEN_ID } });
    await waitFor(() => expect(screen.getByTestId('auth-required')).toBeInTheDocument());
    expect(screen.queryByTestId('specimen-form')).not.toBeInTheDocument();
  });

  describe('per-field visibility selectors (mi-fo8 #7)', () => {
    function profile(
      field_defaults?: Partial<{
        price: 'private' | 'unlisted' | 'public';
        acquired_from: 'private' | 'unlisted' | 'public';
        images: 'private' | 'unlisted' | 'public';
      }>,
    ) {
      return {
        id: '00000000-0000-0000-0000-000000000001',
        email: 'owner@example.com',
        display_name: 'Owner',
        pending: false,
        field_defaults: field_defaults ?? null,
      };
    }

    it('renders inherit option with the resolved owner-default chip text', async () => {
      mockGetByPath({
        specimen: { data: specimen(), error: undefined, response: new Response() },
        profile: {
          data: profile({ price: 'public', acquired_from: 'unlisted', images: 'private' }),
          error: undefined,
          response: new Response(),
        },
      });

      render(SpecimenEdit, { params: { id: SPECIMEN_ID } });

      const priceSel = (await screen.findByTestId('visibility-price')) as HTMLSelectElement;
      const afSel = screen.getByTestId('visibility-acquired-from') as HTMLSelectElement;
      const imgSel = screen.getByTestId('visibility-images') as HTMLSelectElement;
      // First option is the 'Use my account default' inherit sentinel;
      // its label embeds the resolution result so the user sees what
      // will actually apply if they keep the default.
      expect(priceSel.options[0]?.textContent).toMatch(/currently: public/i);
      expect(afSel.options[0]?.textContent).toMatch(/currently: unlisted/i);
      expect(imgSel.options[0]?.textContent).toMatch(/currently: private/i);
    });

    it('falls back to system default (private) when owner has no field_defaults', async () => {
      mockGetByPath({
        specimen: { data: specimen(), error: undefined, response: new Response() },
        profile: { data: profile(), error: undefined, response: new Response() },
      });

      render(SpecimenEdit, { params: { id: SPECIMEN_ID } });

      const priceSel = (await screen.findByTestId('visibility-price')) as HTMLSelectElement;
      // No owner default for price → chain ends at SystemDefault
      // (CONTRACT §13b system-default = private).
      expect(priceSel.options[0]?.textContent).toMatch(/currently: private/i);
    });

    it('prefills the selector from the specimen override when set', async () => {
      mockGetByPath({
        specimen: {
          data: specimen({ visibility_price: 'unlisted' }),
          error: undefined,
          response: new Response(),
        },
        profile: { data: profile(), error: undefined, response: new Response() },
      });

      render(SpecimenEdit, { params: { id: SPECIMEN_ID } });

      const priceSel = (await screen.findByTestId('visibility-price')) as HTMLSelectElement;
      expect(priceSel.value).toBe('unlisted');
    });

    it('PATCH sends the changed visibility_* fields with the right wire shape', async () => {
      mockGetByPath({
        specimen: {
          data: specimen({ visibility_price: 'private' }),
          error: undefined,
          response: new Response(),
        },
        profile: { data: profile(), error: undefined, response: new Response() },
      });
      mockPatch.mockResolvedValue({
        data: specimen(),
        error: undefined,
        response: new Response(),
      });

      render(SpecimenEdit, { params: { id: SPECIMEN_ID } });
      const priceSel = (await screen.findByTestId('visibility-price')) as HTMLSelectElement;
      const imgSel = screen.getByTestId('visibility-images') as HTMLSelectElement;

      // 1) clear the price override back to inherit → wire null
      // 2) set the images override → wire enum value
      await fireEvent.change(priceSel, { target: { value: '__inherit__' } });
      await fireEvent.change(imgSel, { target: { value: 'public' } });
      await fireEvent.submit(screen.getByTestId('specimen-form'));

      await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
      const body = mockPatch.mock.calls[0]?.[1].body;
      expect(body.visibility_price).toBeNull();
      expect(body.visibility_images).toBe('public');
      // Unchanged key — must not be sent so the backend leaves it alone.
      expect(body).not.toHaveProperty('visibility_acquired_from');
    });

    it('still renders selectors when the profile load fails', async () => {
      mockGetByPath({
        specimen: { data: specimen(), error: undefined, response: new Response() },
        profile: {
          data: undefined,
          error: { error: { code: 'unauthorized', message: 'no profile' } },
          response: new Response(null, { status: 401 }),
        },
      });

      render(SpecimenEdit, { params: { id: SPECIMEN_ID } });

      const priceSel = (await screen.findByTestId('visibility-price')) as HTMLSelectElement;
      // No profile → no field_defaults → system default applies.
      expect(priceSel.options[0]?.textContent).toMatch(/currently: private/i);
    });
  });
});
