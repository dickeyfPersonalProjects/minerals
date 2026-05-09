import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet, mockPost, mockPut } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
  mockPut: vi.fn(),
}));

vi.mock('./api', () => ({
  client: { GET: mockGet, POST: mockPost, PUT: mockPut },
}));

import CollectorChainEditor from './CollectorChainEditor.svelte';

const SPECIMEN_ID = '11111111-1111-1111-1111-111111111111';

function collector(id: string, name: string, notes: string | null = null) {
  return {
    id,
    name,
    notes,
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  mockGet.mockReset();
  mockPost.mockReset();
  mockPut.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
  cleanup();
});

describe('CollectorChainEditor', () => {
  it('renders the existing chain in order with disabled boundary arrows', () => {
    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [
        { id: 'cccccccc-0000-0000-0000-000000000001', name: 'Marie Curie' },
        { id: 'cccccccc-0000-0000-0000-000000000002', name: 'Auguste Lacroix' },
      ],
      onSaved: vi.fn(),
      onCancel: vi.fn(),
    });

    const rows = screen.getAllByTestId('chain-row');
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent('Marie Curie');
    expect(rows[1]).toHaveTextContent('Auguste Lacroix');

    // First row's "up" arrow is disabled; last row's "down" arrow is disabled.
    const ups = screen.getAllByTestId('move-up');
    const downs = screen.getAllByTestId('move-down');
    expect(ups[0]).toBeDisabled();
    expect(ups[1]).not.toBeDisabled();
    expect(downs[0]).not.toBeDisabled();
    expect(downs[1]).toBeDisabled();
  });

  it('adds a collector from the autocomplete and excludes it from later suggestions', async () => {
    mockGet.mockResolvedValue({
      data: {
        items: [
          collector('cccccccc-0000-0000-0000-000000000001', 'Marie Curie'),
          collector('cccccccc-0000-0000-0000-000000000002', 'Auguste Lacroix'),
        ],
        next_cursor: null,
      },
      error: undefined,
      response: new Response(),
    });

    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [],
      onSaved: vi.fn(),
      onCancel: vi.fn(),
    });

    expect(screen.getByTestId('chain-empty')).toBeInTheDocument();

    const search = screen.getByTestId('chain-search') as HTMLInputElement;
    await fireEvent.input(search, { target: { value: 'curi' } });

    // Wait for the 300ms debounce to fire then for the GET to land.
    await vi.advanceTimersByTimeAsync(310);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(mockGet.mock.calls[0]?.[0]).toBe('/api/v1/collectors');
    expect(mockGet.mock.calls[0]?.[1].params.query).toEqual({ q: 'curi', limit: 10 });

    const suggestions = await screen.findAllByTestId('chain-suggestion');
    expect(suggestions).toHaveLength(2);

    await fireEvent.click(suggestions[0]!);
    await waitFor(() => expect(screen.getAllByTestId('chain-row')).toHaveLength(1));
    expect(screen.getByTestId('chain-row')).toHaveTextContent('Marie Curie');
    // Search input cleared, suggestions hidden after pick.
    expect((screen.getByTestId('chain-search') as HTMLInputElement).value).toBe('');
    expect(screen.queryByTestId('chain-suggestions')).toBeNull();

    // Search again — already-in-chain entry must be filtered out.
    await fireEvent.input(search, { target: { value: 'au' } });
    await vi.advanceTimersByTimeAsync(310);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));
    const after = await screen.findAllByTestId('chain-suggestion');
    expect(after).toHaveLength(1);
    expect(after[0]).toHaveTextContent('Auguste Lacroix');
  });

  it('reorders with the up/down arrows', async () => {
    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [
        { id: 'cccccccc-0000-0000-0000-000000000001', name: 'A' },
        { id: 'cccccccc-0000-0000-0000-000000000002', name: 'B' },
        { id: 'cccccccc-0000-0000-0000-000000000003', name: 'C' },
      ],
      onSaved: vi.fn(),
      onCancel: vi.fn(),
    });

    // Move B (index 1) up -> [B, A, C]
    const downs = screen.getAllByTestId('move-down');
    await fireEvent.click(downs[0]!);
    let rows = screen.getAllByTestId('chain-row');
    expect(rows[0]).toHaveTextContent('B');
    expect(rows[1]).toHaveTextContent('A');
    expect(rows[2]).toHaveTextContent('C');

    // Move C up -> [B, C, A]
    const ups = screen.getAllByTestId('move-up');
    await fireEvent.click(ups[2]!);
    rows = screen.getAllByTestId('chain-row');
    expect(rows[0]).toHaveTextContent('B');
    expect(rows[1]).toHaveTextContent('C');
    expect(rows[2]).toHaveTextContent('A');
  });

  it('removes a collector from the chain locally', async () => {
    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [
        { id: 'cccccccc-0000-0000-0000-000000000001', name: 'Marie Curie' },
        { id: 'cccccccc-0000-0000-0000-000000000002', name: 'Auguste Lacroix' },
      ],
      onSaved: vi.fn(),
      onCancel: vi.fn(),
    });

    const removes = screen.getAllByTestId('remove-row');
    await fireEvent.click(removes[0]!);
    const rows = screen.getAllByTestId('chain-row');
    expect(rows).toHaveLength(1);
    expect(rows[0]).toHaveTextContent('Auguste Lacroix');
    // PUT is not auto-fired on local change.
    expect(mockPut).not.toHaveBeenCalled();
  });

  it('Save calls PUT with the ordered collector_ids and invokes onSaved', async () => {
    mockPut.mockResolvedValue({
      data: { items: [] },
      error: undefined,
      response: new Response(null, { status: 200 }),
    });
    const onSaved = vi.fn();

    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [
        { id: 'cccccccc-0000-0000-0000-000000000001', name: 'A' },
        { id: 'cccccccc-0000-0000-0000-000000000002', name: 'B' },
      ],
      onSaved,
      onCancel: vi.fn(),
    });

    // Reorder before saving so we can prove the saved order matches local state.
    await fireEvent.click(screen.getAllByTestId('move-down')[0]!);

    await fireEvent.click(screen.getByTestId('chain-save'));

    await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
    expect(mockPut.mock.calls[0]?.[0]).toBe('/api/v1/specimens/{id}/collectors');
    expect(mockPut.mock.calls[0]?.[1].params.path.id).toBe(SPECIMEN_ID);
    expect(mockPut.mock.calls[0]?.[1].body).toEqual({
      collector_ids: [
        'cccccccc-0000-0000-0000-000000000002',
        'cccccccc-0000-0000-0000-000000000001',
      ],
    });
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
  });

  it('shows the API envelope error when Save fails and does not call onSaved', async () => {
    mockPut.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'conflict', message: 'collector vanished' } },
      response: new Response(null, { status: 409 }),
    });
    const onSaved = vi.fn();

    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [{ id: 'cccccccc-0000-0000-0000-000000000001', name: 'A' }],
      onSaved,
      onCancel: vi.fn(),
    });

    await fireEvent.click(screen.getByTestId('chain-save'));

    await waitFor(() =>
      expect(screen.getByTestId('chain-editor-error')).toHaveTextContent('collector vanished'),
    );
    expect(onSaved).not.toHaveBeenCalled();
  });

  it('Cancel calls onCancel without invoking PUT (parent re-renders to discard local edits)', async () => {
    const onCancel = vi.fn();
    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [{ id: 'cccccccc-0000-0000-0000-000000000001', name: 'A' }],
      onSaved: vi.fn(),
      onCancel,
    });

    // Local edit (remove) — purely client-side, must not persist.
    await fireEvent.click(screen.getAllByTestId('remove-row')[0]!);
    await fireEvent.click(screen.getByTestId('chain-cancel'));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(mockPut).not.toHaveBeenCalled();
  });

  it('inline "Add new collector" mini-form creates and appends the new collector', async () => {
    mockPost.mockResolvedValue({
      data: collector('cccccccc-0000-0000-0000-000000000099', 'New Person'),
      error: undefined,
      response: new Response(null, { status: 201 }),
    });

    render(CollectorChainEditor, {
      specimenId: SPECIMEN_ID,
      initial: [],
      onSaved: vi.fn(),
      onCancel: vi.fn(),
    });

    await fireEvent.click(screen.getByTestId('show-new-collector'));
    expect(screen.getByTestId('new-collector-panel')).toBeInTheDocument();

    const nameInput = screen.getByLabelText(/name/i);
    await fireEvent.input(nameInput, { target: { value: 'New Person' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0]?.[0]).toBe('/api/v1/collectors');
    expect(mockPost.mock.calls[0]?.[1].body).toEqual({ name: 'New Person' });

    await waitFor(() => expect(screen.getAllByTestId('chain-row')).toHaveLength(1));
    expect(screen.getByTestId('chain-row')).toHaveTextContent('New Person');
    expect(screen.queryByTestId('new-collector-panel')).toBeNull();
  });
});
