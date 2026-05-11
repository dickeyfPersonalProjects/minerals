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

  it('keyboard navigation: ArrowDown/Up cycles, Enter selects, Escape closes', async () => {
    const a = makeSpecies({ id: 'aaaaaaaa-0000-0000-0000-00000000000a', name: 'Albite' });
    const b = makeSpecies({ id: 'aaaaaaaa-0000-0000-0000-00000000000b', name: 'Beryl' });
    const c = makeSpecies({ id: 'aaaaaaaa-0000-0000-0000-00000000000c', name: 'Calcite' });
    mockGet.mockResolvedValue({ data: { items: [a, b, c] }, error: null });

    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input') as HTMLInputElement;

    await fireEvent.input(input, { target: { value: 'm' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-autocomplete-listbox')).toBeTruthy(),
    );

    // Keys before any active selection do nothing on Enter.
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).not.toHaveBeenCalled();

    // ArrowDown wraps from -1 → 0 → 1; ArrowUp wraps backwards.
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    let options = screen.getAllByTestId('mineral-species-autocomplete-option');
    expect(options[1]!.getAttribute('aria-selected')).toBe('true');

    await fireEvent.keyDown(input, { key: 'ArrowUp' });
    await fireEvent.keyDown(input, { key: 'ArrowUp' });
    options = screen.getAllByTestId('mineral-species-autocomplete-option');
    expect(options[2]!.getAttribute('aria-selected')).toBe('true'); // wrapped backwards

    // aria-activedescendant should reflect the selection.
    expect(input.getAttribute('aria-activedescendant')).toBe(
      'mineral-species-autocomplete-option-2',
    );

    // Enter selects the active option.
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(c);

    // After select the listbox should be closed.
    expect(screen.queryByTestId('mineral-species-autocomplete-listbox')).toBeNull();
  });

  it('Escape closes the open listbox without selecting', async () => {
    mockGet.mockResolvedValue({ data: { items: [makeSpecies()] }, error: null });
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'q' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-autocomplete-listbox')).toBeTruthy(),
    );

    await fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByTestId('mineral-species-autocomplete-listbox')).toBeNull();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('keydown is a no-op when the listbox is closed or empty', async () => {
    mockGet.mockResolvedValue({ data: { items: [] }, error: null });
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    // Closed: no fetch yet → keydown should be a no-op (no throw).
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).not.toHaveBeenCalled();

    // Open but empty results: still a no-op.
    await fireEvent.input(input, { target: { value: 'x' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    await fireEvent.keyDown(input, { key: 'ArrowDown' });
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('renders the loading "Searching…" UI while the request is pending', async () => {
    let resolve: (v: unknown) => void = () => {};
    mockGet.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }),
    );
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'qu' } });
    await vi.advanceTimersByTimeAsync(300);

    const listbox = await screen.findByTestId('mineral-species-autocomplete-listbox');
    expect(listbox.textContent).toContain('Searching');

    // Resolve so the test cleans up cleanly.
    resolve({ data: { items: [makeSpecies()] }, error: null });
    await waitFor(() =>
      expect(screen.getAllByTestId('mineral-species-autocomplete-option').length).toBe(1),
    );
  });

  it('returns the error envelope and clears results without crashing', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'upstream_unavailable', message: 'mindat down' } },
    });
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'qu' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));

    // No options rendered on error, no crash, no selection.
    expect(screen.queryAllByTestId('mineral-species-autocomplete-option')).toHaveLength(0);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('swallows network rejections (abort/etc) without surfacing an error', async () => {
    mockGet.mockRejectedValue(new DOMException('aborted', 'AbortError'));
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'qu' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));

    expect(onSelect).not.toHaveBeenCalled();
  });

  it('debounces rapid keystrokes into a single request and aborts in-flight requests', async () => {
    // Track AbortController.abort() invocations on signals the component passes.
    const abortSpies: ReturnType<typeof vi.fn>[] = [];
    mockGet.mockImplementation((_path: string, opts: { signal?: AbortSignal }) => {
      const spy = vi.fn();
      opts.signal?.addEventListener('abort', spy);
      abortSpies.push(spy);
      return Promise.resolve({ data: { items: [makeSpecies()] }, error: null });
    });

    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    // Three rapid keystrokes within the debounce window → only one fetch.
    await fireEvent.input(input, { target: { value: 'q' } });
    await vi.advanceTimersByTimeAsync(100);
    await fireEvent.input(input, { target: { value: 'qu' } });
    await vi.advanceTimersByTimeAsync(100);
    await fireEvent.input(input, { target: { value: 'qua' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(mockGet.mock.calls[0]?.[1].params.query.q).toBe('qua');

    // A subsequent fetch should abort the prior controller.
    await fireEvent.input(input, { target: { value: 'quar' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));
    expect(abortSpies[0]).toHaveBeenCalledTimes(1);
  });

  it('clears results when the input is cleared after a successful search', async () => {
    mockGet.mockResolvedValue({ data: { items: [makeSpecies()] }, error: null });
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'qu' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-autocomplete-listbox')).toBeTruthy(),
    );

    // Clearing the input cancels the pending debounce and closes the listbox.
    await fireEvent.input(input, { target: { value: '' } });
    expect(screen.queryByTestId('mineral-species-autocomplete-listbox')).toBeNull();
  });

  it('blur closes the listbox after the 120ms grace window', async () => {
    mockGet.mockResolvedValue({ data: { items: [makeSpecies()] }, error: null });
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'qu' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-autocomplete-listbox')).toBeTruthy(),
    );

    await fireEvent.blur(input);
    // Listbox is still open inside the grace window …
    expect(screen.queryByTestId('mineral-species-autocomplete-listbox')).toBeTruthy();
    // … then closes after the timeout fires.
    await vi.advanceTimersByTimeAsync(120);
    expect(screen.queryByTestId('mineral-species-autocomplete-listbox')).toBeNull();
  });

  it('focus reopens the listbox when prior results exist', async () => {
    mockGet.mockResolvedValue({ data: { items: [makeSpecies()] }, error: null });
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect });
    const input = screen.getByTestId('mineral-species-autocomplete-input');

    await fireEvent.input(input, { target: { value: 'qu' } });
    await vi.advanceTimersByTimeAsync(300);
    await waitFor(() =>
      expect(screen.getByTestId('mineral-species-autocomplete-listbox')).toBeTruthy(),
    );

    // Close via Escape, then focus reopens it because results are still cached.
    await fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByTestId('mineral-species-autocomplete-listbox')).toBeNull();
    await fireEvent.focus(input);
    expect(screen.queryByTestId('mineral-species-autocomplete-listbox')).toBeTruthy();
  });

  it('honours initialQuery as the initial visible value', () => {
    const onSelect = vi.fn();
    render(MineralSpeciesAutocomplete, { onSelect, initialQuery: 'Pyrite' });
    const input = screen.getByTestId('mineral-species-autocomplete-input') as HTMLInputElement;
    expect(input.value).toBe('Pyrite');
    // initialQuery is read-once — no fetch is triggered at mount.
    expect(mockGet).not.toHaveBeenCalled();
  });
});
