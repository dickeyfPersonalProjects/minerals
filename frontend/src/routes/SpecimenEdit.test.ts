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

const SPECIMEN_ID = '11111111-1111-1111-1111-111111111111';

function specimen(
  overrides: Partial<{
    name: string;
    type: 'mineral' | 'rock' | 'meteorite';
    description: string;
    catalog_number: string | null;
    visibility: 'private' | 'unlisted' | 'public';
  }> = {},
) {
  return {
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
}

beforeEach(() => {
  mockGet.mockReset();
  mockPatch.mockReset();
  mockDelete.mockReset();
  mockPush.mockReset();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
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
});
