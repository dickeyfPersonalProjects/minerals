import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));

vi.mock('./api/index', () => ({
  client: { GET: mockGet },
}));
vi.mock('./api/wrapper', () => ({
  SUPPRESS_TOAST_HEADERS: { 'x-suppress-toast': '1' },
}));

import MineralSpeciesLookup from './MineralSpeciesLookup.svelte';
import { toasts, _clearToasts } from './toasts';

beforeEach(() => {
  mockGet.mockReset();
  _clearToasts();
});

afterEach(() => {
  cleanup();
});

function makeSpecies(overrides: Record<string, unknown> = {}) {
  return {
    id: 'aaaaaaaa-0000-0000-0000-000000000001',
    name: 'Quartz',
    source: 'mindat',
    mindat_id: '12345',
    attribution: 'data via Mindat (CC-BY-NC-SA 4.0)',
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
    data: { chemical_formula: 'SiO2', mohs_hardness: 7 },
    ...overrides,
  };
}

describe('MineralSpeciesLookup', () => {
  it('does not fetch on mount, even with initialQuery', () => {
    const onSelect = vi.fn();
    render(MineralSpeciesLookup, { onSelect, initialQuery: 'Pyrite' });
    const input = screen.getByTestId('mineral-species-lookup-input') as HTMLInputElement;
    expect(input.value).toBe('Pyrite');
    expect(mockGet).not.toHaveBeenCalled();
  });

  it('clicking Lookup fetches with the typed query and calls onSelect with the top match', async () => {
    const species = makeSpecies();
    mockGet.mockResolvedValue({ data: { items: [species] }, error: null });
    const onSelect = vi.fn();

    render(MineralSpeciesLookup, { onSelect });
    const input = screen.getByTestId('mineral-species-lookup-input');
    await fireEvent.input(input, { target: { value: 'quartz' } });

    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(mockGet).toHaveBeenCalledWith(
      '/api/v1/mineral-species',
      expect.objectContaining({ params: { query: { q: 'quartz' } } }),
    );
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith(species));
  });

  it('Enter inside the input triggers lookup (without submitting the parent form)', async () => {
    const species = makeSpecies();
    mockGet.mockResolvedValue({ data: { items: [species] }, error: null });
    const onSelect = vi.fn();

    render(MineralSpeciesLookup, { onSelect });
    const input = screen.getByTestId('mineral-species-lookup-input');
    await fireEvent.input(input, { target: { value: 'quartz' } });

    const ev = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    input.dispatchEvent(ev);

    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(ev.defaultPrevented).toBe(true);
  });

  it('shows a success toast naming the fetched mineral', async () => {
    mockGet.mockResolvedValue({
      data: { items: [makeSpecies({ name: 'Galena' })] },
      error: null,
    });
    render(MineralSpeciesLookup, { onSelect: vi.fn() });
    await fireEvent.input(screen.getByTestId('mineral-species-lookup-input'), {
      target: { value: 'galena' },
    });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

    await waitFor(() => {
      const list = get(toasts);
      expect(list.length).toBe(1);
      expect(list[0]!.type).toBe('success');
      expect(list[0]!.message).toBe('Fetched data for Galena');
    });
  });

  it('uses the first item when the API returns multiple matches', async () => {
    const first = makeSpecies({ name: 'Quartz', id: 'q-1' });
    const second = makeSpecies({ name: 'Quartzite', id: 'q-2' });
    mockGet.mockResolvedValue({ data: { items: [first, second] }, error: null });
    const onSelect = vi.fn();

    render(MineralSpeciesLookup, { onSelect });
    await fireEvent.input(screen.getByTestId('mineral-species-lookup-input'), {
      target: { value: 'qu' },
    });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

    await waitFor(() => expect(onSelect).toHaveBeenCalledWith(first));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it('shows an inline error and does not call onSelect when no match is found', async () => {
    mockGet.mockResolvedValue({ data: { items: [] }, error: null });
    const onSelect = vi.fn();

    render(MineralSpeciesLookup, { onSelect });
    await fireEvent.input(screen.getByTestId('mineral-species-lookup-input'), {
      target: { value: 'unobtanium' },
    });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-lookup-error')).toHaveTextContent(
        /No match found for "unobtanium"/,
      ),
    );
    expect(onSelect).not.toHaveBeenCalled();
    expect(get(toasts)).toHaveLength(0);
  });

  it('shows an inline error on upstream API error', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'upstream_unavailable', message: 'mindat down' } },
    });
    const onSelect = vi.fn();

    render(MineralSpeciesLookup, { onSelect });
    await fireEvent.input(screen.getByTestId('mineral-species-lookup-input'), {
      target: { value: 'quartz' },
    });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-lookup-error')).toHaveTextContent(/Lookup failed/),
    );
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('shows an inline error on network rejection', async () => {
    mockGet.mockRejectedValue(new Error('network'));
    const onSelect = vi.fn();

    render(MineralSpeciesLookup, { onSelect });
    await fireEvent.input(screen.getByTestId('mineral-species-lookup-input'), {
      target: { value: 'quartz' },
    });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-lookup-error')).toHaveTextContent(/Lookup failed/),
    );
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('refuses to lookup an empty / whitespace query and shows an inline hint', async () => {
    const onSelect = vi.fn();
    render(MineralSpeciesLookup, { onSelect });
    await fireEvent.input(screen.getByTestId('mineral-species-lookup-input'), {
      target: { value: '   ' },
    });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

    expect(mockGet).not.toHaveBeenCalled();
    expect(screen.getByTestId('mineral-species-lookup-error')).toHaveTextContent(
      /Enter a mineral name/i,
    );
  });

  it('clears a prior error as soon as the user edits the input', async () => {
    mockGet.mockResolvedValue({ data: { items: [] }, error: null });
    const onSelect = vi.fn();

    render(MineralSpeciesLookup, { onSelect });
    const input = screen.getByTestId('mineral-species-lookup-input');
    await fireEvent.input(input, { target: { value: 'unobtanium' } });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));
    await waitFor(() => screen.getByTestId('mineral-species-lookup-error'));

    await fireEvent.input(input, { target: { value: 'unobtaniu' } });
    expect(screen.queryByTestId('mineral-species-lookup-error')).toBeNull();
  });

  it('disables the button while a lookup is in flight', async () => {
    let resolve: (v: unknown) => void = () => {};
    mockGet.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    render(MineralSpeciesLookup, { onSelect: vi.fn() });
    await fireEvent.input(screen.getByTestId('mineral-species-lookup-input'), {
      target: { value: 'quartz' },
    });
    const button = screen.getByTestId('mineral-species-lookup-button') as HTMLButtonElement;
    await fireEvent.click(button);

    await waitFor(() => expect(button.disabled).toBe(true));
    expect(button.textContent).toMatch(/Looking up/);

    resolve({ data: { items: [makeSpecies()] }, error: null });
    await waitFor(() => expect(button.disabled).toBe(false));
  });

  it('mirrors the selected mineral name back into the input on success', async () => {
    const species = makeSpecies({ name: 'Pyrite' });
    mockGet.mockResolvedValue({ data: { items: [species] }, error: null });
    render(MineralSpeciesLookup, { onSelect: vi.fn() });
    const input = screen.getByTestId('mineral-species-lookup-input') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: 'pyr' } });
    await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));
    await waitFor(() => expect(input.value).toBe('Pyrite'));
  });
});
