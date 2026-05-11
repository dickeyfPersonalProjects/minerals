import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const { mockGet, mockPost, mockPatch, mockDelete } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
  mockPatch: vi.fn(),
  mockDelete: vi.fn(),
}));

vi.mock('./api', () => ({
  client: { GET: mockGet, POST: mockPost, PATCH: mockPatch, DELETE: mockDelete },
}));

import {
  __resetQrSheetStore,
  addSpecimenToSheet,
  createSheet,
  deleteSheet,
  getQrSheetState,
  patchSheetTemplate,
  qrSheetState,
  refreshQrSheet,
  removeSpecimenFromSheet,
  setNoSheet,
  setSheet,
  specimenOnSheet,
  type QRSheetView,
} from './qrSheet';

function sheet(over: Partial<QRSheetView> = {}): QRSheetView {
  return {
    id: '99999999-9999-9999-9999-999999999999',
    template: 'avery-5160',
    page_count: 1,
    specimens: [],
    created_at: '2026-05-10T00:00:00Z',
    updated_at: '2026-05-10T00:00:00Z',
    ...over,
  };
}

beforeEach(() => {
  mockGet.mockReset();
  mockPost.mockReset();
  mockPatch.mockReset();
  mockDelete.mockReset();
  __resetQrSheetStore();
});

afterEach(() => {
  __resetQrSheetStore();
});

describe('qrSheet store', () => {
  it('starts in unknown state', () => {
    expect(get(qrSheetState)).toEqual({ status: 'unknown' });
    expect(getQrSheetState()).toEqual({ status: 'unknown' });
  });

  it('setSheet / setNoSheet update state', () => {
    const s = sheet({ template: 'avery-5163' });
    setSheet(s);
    expect(get(qrSheetState)).toEqual({ status: 'loaded', sheet: s });
    setNoSheet();
    expect(get(qrSheetState)).toEqual({ status: 'none' });
  });

  it('refreshQrSheet sets none on 404', async () => {
    mockGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: { code: 'not_found', message: 'no sheet' } },
      response: new Response(null, { status: 404 }),
    });
    const result = await refreshQrSheet();
    expect(result).toEqual({ status: 'none' });
    expect(get(qrSheetState)).toEqual({ status: 'none' });
    // The probe must suppress the toast — 404 here means "no
    // sheet yet" which is a normal state, not an error.
    expect(mockGet.mock.calls[0]?.[1]).toEqual(
      expect.objectContaining({ headers: { 'x-suppress-toast': '1' } }),
    );
  });

  it('refreshQrSheet sets loaded on 200', async () => {
    const s = sheet({ template: 'avery-22806' });
    mockGet.mockResolvedValueOnce({
      data: s,
      error: undefined,
      response: new Response(null, { status: 200 }),
    });
    const result = await refreshQrSheet();
    expect(result).toEqual({ status: 'loaded', sheet: s });
  });

  it('createSheet POSTs and stores on success', async () => {
    const s = sheet({ template: 'avery-22806' });
    mockPost.mockResolvedValueOnce({
      data: s,
      error: undefined,
      response: new Response(null, { status: 201 }),
    });
    const result = await createSheet('avery-22806');
    expect(result).toEqual(s);
    expect(mockPost).toHaveBeenCalledWith('/api/v1/qr-sheet', {
      body: { template: 'avery-22806' },
    });
    expect(get(qrSheetState)).toEqual({ status: 'loaded', sheet: s });
  });

  it('createSheet returns null on error and leaves state unchanged', async () => {
    setNoSheet();
    mockPost.mockResolvedValueOnce({
      data: undefined,
      error: { error: { code: 'internal', message: 'boom' } },
      response: new Response(null, { status: 500 }),
    });
    const result = await createSheet('avery-5160');
    expect(result).toBeNull();
    expect(get(qrSheetState)).toEqual({ status: 'none' });
  });

  it('patchSheetTemplate updates state', async () => {
    setSheet(sheet());
    const updated = sheet({ template: 'avery-l7160' });
    mockPatch.mockResolvedValueOnce({
      data: updated,
      error: undefined,
      response: new Response(),
    });
    const result = await patchSheetTemplate('avery-l7160');
    expect(result).toEqual(updated);
    expect(get(qrSheetState)).toEqual({ status: 'loaded', sheet: updated });
  });

  it('deleteSheet drops state to none', async () => {
    setSheet(sheet());
    mockDelete.mockResolvedValueOnce({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    });
    const ok = await deleteSheet();
    expect(ok).toBe(true);
    expect(get(qrSheetState)).toEqual({ status: 'none' });
  });

  it('deleteSheet leaves state untouched on error', async () => {
    const s = sheet();
    setSheet(s);
    mockDelete.mockResolvedValueOnce({
      data: undefined,
      error: { error: { code: 'internal', message: 'boom' } },
      response: new Response(null, { status: 500 }),
    });
    const ok = await deleteSheet();
    expect(ok).toBe(false);
    expect(get(qrSheetState)).toEqual({ status: 'loaded', sheet: s });
  });

  it('addSpecimenToSheet POSTs and replaces sheet', async () => {
    const next = sheet({
      specimens: [
        {
          specimen_id: 'a',
          name: 'Quartz',
          position: 1,
          thumbnail_url: null,
          added_at: '2026-05-10T00:00:00Z',
        },
      ],
    });
    mockPost.mockResolvedValueOnce({
      data: next,
      error: undefined,
      response: new Response(),
    });
    const result = await addSpecimenToSheet('a');
    expect(result).toEqual(next);
    expect(mockPost).toHaveBeenCalledWith('/api/v1/qr-sheet/specimens', {
      body: { specimen_id: 'a' },
    });
    expect(get(qrSheetState)).toEqual({ status: 'loaded', sheet: next });
  });

  it('removeSpecimenFromSheet DELETEs and prunes cache on success', async () => {
    setSheet(
      sheet({
        specimens: [
          {
            specimen_id: 'a',
            name: 'A',
            position: 1,
            thumbnail_url: null,
            added_at: '2026-05-10T00:00:00Z',
          },
          {
            specimen_id: 'b',
            name: 'B',
            position: 2,
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
    const ok = await removeSpecimenFromSheet('a');
    expect(ok).toBe(true);
    expect(mockDelete).toHaveBeenCalledWith('/api/v1/qr-sheet/specimens/{specimen_id}', {
      params: { path: { specimen_id: 'a' } },
    });
    const state = get(qrSheetState);
    expect(state.status).toBe('loaded');
    if (state.status === 'loaded') {
      expect(state.sheet.specimens).toHaveLength(1);
      expect(state.sheet.specimens?.[0]?.specimen_id).toBe('b');
    }
  });

  it('removeSpecimenFromSheet leaves state untouched on error', async () => {
    const before = sheet({
      specimens: [
        {
          specimen_id: 'a',
          name: 'A',
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
    const ok = await removeSpecimenFromSheet('a');
    expect(ok).toBe(false);
    const state = get(qrSheetState);
    expect(state.status).toBe('loaded');
    if (state.status === 'loaded') {
      expect(state.sheet.specimens?.[0]?.specimen_id).toBe('a');
    }
  });

  it('specimenOnSheet returns true only when state is loaded and id is present', () => {
    expect(specimenOnSheet({ status: 'unknown' }, 'a')).toBe(false);
    expect(specimenOnSheet({ status: 'none' }, 'a')).toBe(false);
    const s = sheet({
      specimens: [
        {
          specimen_id: 'a',
          name: 'A',
          position: 1,
          thumbnail_url: null,
          added_at: '2026-05-10T00:00:00Z',
        },
      ],
    });
    expect(specimenOnSheet({ status: 'loaded', sheet: s }, 'a')).toBe(true);
    expect(specimenOnSheet({ status: 'loaded', sheet: s }, 'b')).toBe(false);
    // Null specimens list shouldn't crash.
    expect(specimenOnSheet({ status: 'loaded', sheet: sheet({ specimens: null }) }, 'a')).toBe(
      false,
    );
  });
});
