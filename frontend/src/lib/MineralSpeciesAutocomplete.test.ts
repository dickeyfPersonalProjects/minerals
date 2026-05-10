import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));

vi.mock('./api/index', () => ({
  client: { GET: mockGet },
}));
vi.mock('./api/wrapper', () => ({
  SUPPRESS_TOAST_HEADERS: { 'x-suppress-toast': '1' },
}));

import MineralSpeciesAutocomplete from './MineralSpeciesAutocomplete.svelte';

beforeEach(() => {
  vi.useFakeTimers();
  mockGet.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
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

describe('MineralSpeciesAutocomplete', () => {
  it('debounces and renders results', async () => {
    mockGet.mockResolvedValue({ data: { items: [makeSpecies()] }, error: null });

    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'qu' } });
    expect(mockGet).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(mockGet).toHaveBeenCalledWith(
      '/api/v1/mineral-species',
      expect.objectContaining({
        params: { query: { q: 'qu' } },
      }),
    );

    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-autocomplete-listbox')).toBeTruthy(),
    );
    const options = screen.getAllByTestId('mineral-species-autocomplete-option');
    expect(options).toHaveLength(1);
    expect(options[0]!.textContent).toContain('Quartz');
    expect(options[0]!.textContent).toContain('SiO2');
  });

  it('calls onSelect with the chosen record on mousedown', async () => {
    const species = makeSpecies();
    mockGet.mockResolvedValue({ data: { items: [species] }, error: null });
    const onSelect = vi.fn();

    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');
    await fireEvent.input(input, { target: { value: 'quartz' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-autocomplete-listbox')).toBeTruthy(),
    );

    const option = screen.getAllByTestId('mineral-species-autocomplete-option')[0]!;
    await fireEvent.mouseDown(option);

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(species);
  });

  it('does not query when input is empty', async () => {
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');
    await fireEvent.input(input, { target: { value: '   ' } });
    await vi.advanceTimersByTimeAsync(500);
    expect(mockGet).not.toHaveBeenCalled();
  });

  it('renders empty silently when API returns no items', async () => {
    mockGet.mockResolvedValue({ data: { items: [] }, error: null });
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');
    await fireEvent.input(input, { target: { value: 'unobtanium' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(screen.queryAllByTestId('mineral-species-autocomplete-option')).toHaveLength(0);
  });
});
