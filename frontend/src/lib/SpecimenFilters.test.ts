import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));

vi.mock('./api', () => ({
  client: { GET: mockGet },
}));

import SpecimenFilters, {
  activeFilterCount,
  type SpecimenFiltersValue,
} from './SpecimenFilters.svelte';

beforeEach(() => {
  mockGet.mockReset();
  // Default: collector lookups return empty.
  mockGet.mockImplementation(async () => ({
    data: { items: [], next_cursor: null },
    error: undefined,
    response: new Response(),
  }));
});

afterEach(() => {
  cleanup();
});

describe('activeFilterCount', () => {
  it('counts only populated fields', () => {
    expect(activeFilterCount({})).toBe(0);
    expect(activeFilterCount({ q: 'quartz' })).toBe(1);
    expect(
      activeFilterCount({
        q: 'quartz',
        type: 'mineral',
        visibility: 'public',
        has_catalog_number: 'true',
        acquired_after: '2026-01-01',
        acquired_before: '2026-12-31',
        collector_id: 'cccccccc-1111-1111-1111-111111111111',
      }),
    ).toBe(7);
  });
});

describe('SpecimenFilters component', () => {
  it('renders the search input and exposes the filter toggle', () => {
    render(SpecimenFilters, { value: {}, onChange: vi.fn() });

    expect(screen.getByTestId('filter-search-input')).toBeInTheDocument();
    expect(screen.getByTestId('filter-toggle')).toHaveTextContent(/^Filters$/);
    // No "Clear all" until a filter is active.
    expect(screen.queryByTestId('filter-clear-all')).not.toBeInTheDocument();
    // Panel is collapsed by default.
    expect(screen.queryByTestId('filter-panel')).not.toBeInTheDocument();
  });

  it('expands and collapses the panel on toggle', async () => {
    render(SpecimenFilters, { value: {}, onChange: vi.fn() });
    await fireEvent.click(screen.getByTestId('filter-toggle'));
    expect(screen.getByTestId('filter-panel')).toBeInTheDocument();
    await fireEvent.click(screen.getByTestId('filter-toggle'));
    expect(screen.queryByTestId('filter-panel')).not.toBeInTheDocument();
  });

  it('shows the active count badge when filters are applied', () => {
    render(SpecimenFilters, {
      value: { type: 'mineral', visibility: 'public' },
      onChange: vi.fn(),
    });
    expect(screen.getByTestId('filter-toggle')).toHaveTextContent('Filters (2 active)');
    expect(screen.getByTestId('filter-clear-all')).toBeInTheDocument();
  });

  it('debounces the search input and emits once after typing settles', async () => {
    vi.useFakeTimers();
    try {
      const onChange = vi.fn();
      render(SpecimenFilters, { value: {}, onChange });

      const input = screen.getByTestId('filter-search-input') as HTMLInputElement;
      // Three keystrokes within the debounce window.
      await fireEvent.input(input, { target: { value: 'q' } });
      await fireEvent.input(input, { target: { value: 'qu' } });
      await fireEvent.input(input, { target: { value: 'quartz' } });

      expect(onChange).not.toHaveBeenCalled();
      vi.advanceTimersByTime(299);
      expect(onChange).not.toHaveBeenCalled();
      vi.advanceTimersByTime(2);
      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith({ q: 'quartz' });
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows a clear-search button that resets the search immediately', async () => {
    const onChange = vi.fn();
    const { rerender } = render(SpecimenFilters, { value: { q: 'quartz' }, onChange });

    const clearBtn = await screen.findByTestId('filter-search-clear');
    await fireEvent.click(clearBtn);

    expect(onChange).toHaveBeenCalledWith({});
    // Simulate parent accepting the change (URL replace → value
    // re-derives) so the controlled input syncs to empty.
    await rerender({ value: {}, onChange });
    expect((screen.getByTestId('filter-search-input') as HTMLInputElement).value).toBe('');
    expect(screen.queryByTestId('filter-search-clear')).not.toBeInTheDocument();
  });

  it('toggles type chips and emits the new value', async () => {
    const onChange = vi.fn();
    const { rerender } = render(SpecimenFilters, { value: {}, onChange });
    await fireEvent.click(screen.getByTestId('filter-toggle'));

    await fireEvent.click(screen.getByTestId('filter-type-mineral'));
    expect(onChange).toHaveBeenLastCalledWith({ type: 'mineral' });

    // Re-render with the new value to simulate parent acceptance,
    // then click the same chip again to clear it.
    await rerender({ value: { type: 'mineral' }, onChange });
    await fireEvent.click(screen.getByTestId('filter-type-mineral'));
    expect(onChange).toHaveBeenLastCalledWith({});
  });

  it('emits the visibility value on chip click', async () => {
    const onChange = vi.fn();
    render(SpecimenFilters, { value: {}, onChange });
    await fireEvent.click(screen.getByTestId('filter-toggle'));

    await fireEvent.click(screen.getByTestId('filter-visibility-public'));
    expect(onChange).toHaveBeenLastCalledWith({ visibility: 'public' });
  });

  it('cycles the catalog-number tri-state', async () => {
    const onChange = vi.fn();
    render(SpecimenFilters, { value: {}, onChange });
    await fireEvent.click(screen.getByTestId('filter-toggle'));

    await fireEvent.click(screen.getByTestId('filter-catalog-true'));
    expect(onChange).toHaveBeenLastCalledWith({ has_catalog_number: 'true' });

    await fireEvent.click(screen.getByTestId('filter-catalog-false'));
    expect(onChange).toHaveBeenLastCalledWith({ has_catalog_number: 'false' });

    await fireEvent.click(screen.getByTestId('filter-catalog-any'));
    expect(onChange).toHaveBeenLastCalledWith({});
  });

  it('emits acquired_after / acquired_before on date input', async () => {
    const onChange = vi.fn();
    render(SpecimenFilters, { value: {}, onChange });
    await fireEvent.click(screen.getByTestId('filter-toggle'));

    await fireEvent.input(screen.getByTestId('filter-acquired-after'), {
      target: { value: '2026-01-15' },
    });
    expect(onChange).toHaveBeenLastCalledWith({ acquired_after: '2026-01-15' });

    await fireEvent.input(screen.getByTestId('filter-acquired-before'), {
      target: { value: '2026-12-31' },
    });
    expect(onChange).toHaveBeenLastCalledWith({ acquired_before: '2026-12-31' });
  });

  it('clears all filters when "Clear all" is pressed', async () => {
    const onChange = vi.fn();
    const value: SpecimenFiltersValue = {
      q: 'quartz',
      type: 'mineral',
      visibility: 'public',
      has_catalog_number: 'true',
    };
    render(SpecimenFilters, { value, onChange });

    await fireEvent.click(screen.getByTestId('filter-clear-all'));
    expect(onChange).toHaveBeenCalledWith({});
  });

  it('debounces the collector typeahead and shows suggestions', async () => {
    vi.useFakeTimers();
    try {
      mockGet.mockImplementation(async (path: string) => {
        if (path === '/api/v1/collectors') {
          return {
            data: {
              items: [
                {
                  id: 'cccccccc-0000-0000-0000-000000000001',
                  name: 'Marie Curie',
                  notes: null,
                  author_id: '00000000-0000-0000-0000-000000000001',
                  created_at: '2026-05-01T12:00:00Z',
                  updated_at: '2026-05-01T12:00:00Z',
                },
              ],
              next_cursor: null,
            },
            error: undefined,
            response: new Response(),
          };
        }
        return { data: { items: [], next_cursor: null }, error: undefined };
      });

      const onChange = vi.fn();
      render(SpecimenFilters, { value: {}, onChange });
      await fireEvent.click(screen.getByTestId('filter-toggle'));

      const input = screen.getByTestId('filter-collector-input') as HTMLInputElement;
      await fireEvent.input(input, { target: { value: 'mar' } });

      // Before debounce settles, no API call.
      expect(mockGet).not.toHaveBeenCalledWith(
        '/api/v1/collectors',
        expect.objectContaining({ params: { query: { q: 'mar', limit: 10 } } }),
      );

      vi.advanceTimersByTime(310);
      // Allow microtasks to flush so the suggestion list renders.
      await vi.runAllTimersAsync();
      vi.useRealTimers();

      const suggestion = await screen.findByTestId('filter-collector-suggestion');
      expect(suggestion).toHaveTextContent('Marie Curie');

      await fireEvent.click(suggestion);
      expect(onChange).toHaveBeenLastCalledWith({
        collector_id: 'cccccccc-0000-0000-0000-000000000001',
      });
    } finally {
      vi.useRealTimers();
    }
  });

  it('shows a chip with the collector name when collector_id is set', async () => {
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/collectors/{id}') {
        return {
          data: {
            id: 'cccccccc-0000-0000-0000-000000000001',
            name: 'Marie Curie',
            notes: null,
            author_id: '00000000-0000-0000-0000-000000000001',
            created_at: '2026-05-01T12:00:00Z',
            updated_at: '2026-05-01T12:00:00Z',
          },
          error: undefined,
          response: new Response(),
        };
      }
      return { data: { items: [], next_cursor: null }, error: undefined };
    });

    const onChange = vi.fn();
    render(SpecimenFilters, {
      value: { collector_id: 'cccccccc-0000-0000-0000-000000000001' },
      onChange,
    });
    await fireEvent.click(screen.getByTestId('filter-toggle'));

    await waitFor(() =>
      expect(screen.getByTestId('filter-collector-chip')).toHaveTextContent('Marie Curie'),
    );

    await fireEvent.click(screen.getByTestId('filter-collector-clear'));
    expect(onChange).toHaveBeenLastCalledWith({});
  });
});
