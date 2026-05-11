import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

// Hoisted mocks — replace the API client and the QR generator for
// every test in this file. Using the hoisted helper keeps the
// vi.mock factories in scope while letting tests reset call state.
const { mockGet, mockPost, mockDelete, mockPatch, mockToString } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
  mockDelete: vi.fn(),
  mockPatch: vi.fn(),
  mockToString: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGet, POST: mockPost, DELETE: mockDelete, PATCH: mockPatch },
}));

vi.mock('qrcode', () => ({
  default: { toString: mockToString },
  toString: mockToString,
}));

// svelte-spa-router pulls window.location.hash on import for the
// initial route resolution. We feed it a stable starting hash so
// `router.querystring` is deterministic across tests.
function setHash(hash: string): void {
  window.location.hash = hash;
}

// Stub out window.print so the print button has a no-op target.
const printSpy = vi.fn();
Object.defineProperty(window, 'print', { value: printSpy, writable: true });

import QRPreview from './QRPreview.svelte';

const SPECIMEN_ID = '11111111-1111-1111-1111-111111111111';
const SPECIMEN_ID_2 = '22222222-2222-2222-2222-222222222222';

function specimen(over: Partial<Record<string, unknown>> = {}) {
  return {
    id: SPECIMEN_ID,
    name: 'Smoky quartz',
    type: 'mineral',
    visibility: 'private',
    description: '',
    locality_text: null,
    locality: {},
    dimensions: {},
    type_data: {},
    catalog_number: null,
    acquired_at: null,
    acquired_from: null,
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
    mass_g: null,
    price_cents: null,
    source_notes: null,
    ...over,
  };
}

function sheetView(over: Partial<Record<string, unknown>> = {}) {
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
  mockDelete.mockReset();
  mockPatch.mockReset();
  mockToString.mockReset();
  printSpy.mockReset();
  // Default QR renderer returns a tiny stub SVG so {@html} has
  // something to insert. Tests that care about the encoded value
  // assert against `data-qr-value` on the wrapper, not the SVG body.
  mockToString.mockResolvedValue('<svg viewBox="0 0 1 1"><rect/></svg>');
  // Default sheet probe — 404 (no sheet). Individual tests override.
  mockGet.mockImplementation(async (path: string) => {
    if (path === '/api/v1/qr-sheet') {
      return {
        data: undefined,
        error: { error: { code: 'not_found', message: 'no sheet' } },
        response: new Response(null, { status: 404 }),
      };
    }
    return { data: undefined, error: undefined, response: new Response() };
  });
});

afterEach(() => {
  cleanup();
  document.body.removeAttribute('data-qr-print');
  setHash('');
});

describe('QRPreview route — single mode', () => {
  function singleFixture(sp = specimen()) {
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens/{id}') {
        return { data: sp, error: undefined, response: new Response() };
      }
      if (path === '/api/v1/qr-sheet') {
        return {
          data: undefined,
          error: { error: { code: 'not_found', message: 'no sheet' } },
          response: new Response(null, { status: 404 }),
        };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
  }

  it('renders the QR encoded with the specimen URL and the specimen name', async () => {
    setHash(`#/specimens/qr?specimen=${SPECIMEN_ID}`);
    singleFixture(specimen({ name: 'Pyrite cube', catalog_number: 'MIN-007' }));

    render(QRPreview);

    await waitFor(() => {
      expect(screen.getByTestId('qr-name')).toHaveTextContent('Pyrite cube');
    });
    expect(screen.getByTestId('qr-catalog')).toHaveTextContent('MIN-007');
    const qr = await screen.findByTestId('qr-svg');
    // The QR component encodes the in-app specimen URL (hash routing).
    expect(qr.getAttribute('data-qr-value')).toContain(`/#/specimens/${SPECIMEN_ID}`);
  });

  it('Print button calls window.print()', async () => {
    setHash(`#/specimens/qr?specimen=${SPECIMEN_ID}`);
    singleFixture();
    render(QRPreview);
    const btn = await screen.findByTestId('qr-print-button');
    await fireEvent.click(btn);
    expect(printSpy).toHaveBeenCalledTimes(1);
  });

  it('toggles the body print marker for the lifetime of the route', async () => {
    setHash(`#/specimens/qr?specimen=${SPECIMEN_ID}`);
    singleFixture();
    const { unmount } = render(QRPreview);
    await screen.findByTestId('qr-name');
    expect(document.body.getAttribute('data-qr-print')).toBe('true');
    unmount();
    expect(document.body.getAttribute('data-qr-print')).toBeNull();
  });

  it('shows error state when the specimen fetch fails', async () => {
    setHash(`#/specimens/qr?specimen=${SPECIMEN_ID}`);
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens/{id}') {
        return {
          data: undefined,
          error: { error: { code: 'not_found', message: 'no such specimen' } },
          response: new Response(null, { status: 404 }),
        };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
    render(QRPreview);
    const err = await screen.findByTestId('qr-error');
    expect(err).toHaveTextContent('no such specimen');
  });

  it('"Add to sheet" appends to an existing sheet', async () => {
    setHash(`#/specimens/qr?specimen=${SPECIMEN_ID}`);
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens/{id}') {
        return { data: specimen(), error: undefined, response: new Response() };
      }
      if (path === '/api/v1/qr-sheet') {
        return { data: sheetView(), error: undefined, response: new Response() };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
    mockPost.mockResolvedValue({
      data: sheetView({
        specimens: [
          {
            specimen_id: SPECIMEN_ID,
            name: 'Smoky quartz',
            position: 1,
            thumbnail_url: null,
            added_at: '2026-05-10T00:00:00Z',
          },
        ],
      }),
      error: undefined,
      response: new Response(),
    });
    render(QRPreview);
    const btn = await screen.findByTestId('qr-add-to-sheet');
    expect(btn).toHaveTextContent('Add to sheet');
    await fireEvent.click(btn);
    await waitFor(() => {
      expect(mockPost).toHaveBeenCalledWith(
        '/api/v1/qr-sheet/specimens',
        expect.objectContaining({ body: { specimen_id: SPECIMEN_ID } }),
      );
    });
    // Only the append POST should fire — no create needed.
    expect(mockPost).toHaveBeenCalledTimes(1);
  });

  it('"Add to sheet" creates a sheet first when one does not exist', async () => {
    setHash(`#/specimens/qr?specimen=${SPECIMEN_ID}`);
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/specimens/{id}') {
        return { data: specimen(), error: undefined, response: new Response() };
      }
      if (path === '/api/v1/qr-sheet') {
        return {
          data: undefined,
          error: { error: { code: 'not_found', message: 'no sheet' } },
          response: new Response(null, { status: 404 }),
        };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
    // Sequence: append→404, create→201, append→200.
    mockPost
      .mockResolvedValueOnce({
        data: undefined,
        error: { error: { code: 'not_found', message: 'no sheet' } },
        response: new Response(null, { status: 404 }),
      })
      .mockResolvedValueOnce({
        data: sheetView(),
        error: undefined,
        response: new Response(null, { status: 201 }),
      })
      .mockResolvedValueOnce({
        data: sheetView(),
        error: undefined,
        response: new Response(),
      });

    render(QRPreview);
    const btn = await screen.findByTestId('qr-add-to-sheet');
    expect(btn).toHaveTextContent('Start a sheet with this specimen');
    await fireEvent.click(btn);
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(3));
    expect(mockPost.mock.calls[0]?.[0]).toBe('/api/v1/qr-sheet/specimens');
    expect(mockPost.mock.calls[1]?.[0]).toBe('/api/v1/qr-sheet');
    expect(mockPost.mock.calls[1]?.[1]).toEqual(
      expect.objectContaining({ body: { template: 'avery-5160' } }),
    );
    expect(mockPost.mock.calls[2]?.[0]).toBe('/api/v1/qr-sheet/specimens');
  });
});

describe('QRPreview route — sheet mode', () => {
  it('renders empty state when the user has no sheet', async () => {
    setHash('#/specimens/qr');
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/qr-sheet') {
        return {
          data: undefined,
          error: { error: { code: 'not_found', message: 'no sheet' } },
          response: new Response(null, { status: 404 }),
        };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
    render(QRPreview);
    await screen.findByTestId('qr-sheet-empty');
  });

  it('renders the sheet grid using template geometry and one cell per specimen', async () => {
    setHash('#/specimens/qr');
    const specimens = [
      {
        specimen_id: SPECIMEN_ID,
        name: 'Smoky quartz',
        position: 1,
        thumbnail_url: null,
        added_at: '2026-05-10T00:00:00Z',
      },
      {
        specimen_id: SPECIMEN_ID_2,
        name: 'Pyrite cube',
        position: 2,
        thumbnail_url: null,
        added_at: '2026-05-10T00:00:00Z',
      },
    ];
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/qr-sheet') {
        return {
          data: sheetView({ template: 'avery-5160', specimens, page_count: 1 }),
          error: undefined,
          response: new Response(),
        };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
    render(QRPreview);
    await screen.findByTestId('qr-sheet-pages');
    const pages = screen.getAllByTestId('qr-sheet-page');
    expect(pages).toHaveLength(1);
    // avery-5160 capacity is 30 — 2 filled cells + 28 empty placeholders.
    const cells = screen.getAllByTestId('qr-sheet-cell');
    expect(cells).toHaveLength(30);
    const filled = cells.filter((c) => c.getAttribute('data-cell-empty') === 'false');
    expect(filled).toHaveLength(2);
    const names = screen.getAllByTestId('qr-sheet-name').map((n) => n.textContent?.trim());
    expect(names).toEqual(['Smoky quartz', 'Pyrite cube']);
  });

  it('paginates when specimen count exceeds template capacity', async () => {
    setHash('#/specimens/qr');
    // avery-5163 capacity is 10 — feed 12 specimens to force a second page.
    const specimens = Array.from({ length: 12 }, (_, i) => ({
      specimen_id: `00000000-0000-0000-0000-${String(i + 1).padStart(12, '0')}`,
      name: `Specimen ${i + 1}`,
      position: i + 1,
      thumbnail_url: null,
      added_at: '2026-05-10T00:00:00Z',
    }));
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/qr-sheet') {
        return {
          data: sheetView({ template: 'avery-5163', specimens, page_count: 2 }),
          error: undefined,
          response: new Response(),
        };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
    render(QRPreview);
    await screen.findByTestId('qr-sheet-pages');
    expect(screen.getAllByTestId('qr-sheet-page')).toHaveLength(2);
    expect(screen.getByTestId('qr-sheet-summary')).toHaveTextContent('12 specimens');
    expect(screen.getByTestId('qr-sheet-summary')).toHaveTextContent('2 pages');
  });

  it('renders a no-specimens hint when the sheet exists but is empty', async () => {
    setHash('#/specimens/qr');
    mockGet.mockImplementation(async (path: string) => {
      if (path === '/api/v1/qr-sheet') {
        return {
          data: sheetView({ template: 'avery-l7160', specimens: [], page_count: 0 }),
          error: undefined,
          response: new Response(),
        };
      }
      return { data: undefined, error: undefined, response: new Response() };
    });
    render(QRPreview);
    await screen.findByTestId('qr-sheet-no-specimens');
    expect(screen.getByTestId('qr-sheet-summary')).toHaveTextContent('0 specimens');
  });
});
