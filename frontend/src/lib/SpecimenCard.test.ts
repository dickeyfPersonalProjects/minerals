import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));

vi.mock('./api', () => ({
  client: { GET: mockGet },
}));

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    link: () => ({ destroy() {} }),
  };
});

import SpecimenCard from './SpecimenCard.svelte';

function specimen(
  overrides: Partial<{
    id: string;
    name: string;
    type: 'mineral' | 'rock' | 'meteorite';
    visibility: 'private' | 'unlisted' | 'public';
    locality_text: string | null;
  }> = {},
) {
  return {
    id: overrides.id ?? '11111111-1111-1111-1111-111111111111',
    name: overrides.name ?? 'Quartz',
    type: overrides.type ?? 'mineral',
    visibility: overrides.visibility ?? 'private',
    locality_text: overrides.locality_text ?? null,
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

function listOk(items: { id: string }[]) {
  return { data: { items, next_cursor: null }, error: undefined, response: new Response() };
}

beforeEach(() => {
  mockGet.mockReset();
});

afterEach(() => {
  cleanup();
});

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
});
