import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet, mockPatch, mockPush } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPatch: vi.fn(),
  mockPush: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet, PATCH: mockPatch },
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
