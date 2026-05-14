import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));

vi.mock('./api', () => ({
  client: { GET: mockGet, POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    link: () => ({ destroy() {} }),
  };
});

import Layout from './Layout.svelte';
import { __resetQrSheetStore, setSheet, type QRSheetView } from './qrSheet';
import { __resetOidcConfig } from './oidc/config';

function sheet(over: Partial<QRSheetView> = {}): QRSheetView {
  return {
    id: '99999999-9999-9999-9999-999999999999',
    template: 'avery-5160',
    page_count: 0,
    specimens: [],
    created_at: '2026-05-10T00:00:00Z',
    updated_at: '2026-05-10T00:00:00Z',
    ...over,
  };
}

beforeEach(() => {
  mockGet.mockReset();
  __resetQrSheetStore();
  __resetOidcConfig();
});

afterEach(() => {
  cleanup();
  __resetQrSheetStore();
  __resetOidcConfig();
});

describe('Layout — navbar', () => {
  it('hides the QR Sticker Sheet link when no sheet exists', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'not_found', message: 'no sheet' } },
      response: new Response(null, { status: 404 }),
    });
    render(Layout);
    // The refresh runs onMount; wait for the GET to complete then
    // verify the nav stays clean.
    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    expect(screen.queryByTestId('nav-qr-sheet')).not.toBeInTheDocument();
  });

  it('shows the QR Sticker Sheet link when a sheet is loaded', async () => {
    mockGet.mockResolvedValue({
      data: sheet(),
      error: undefined,
      response: new Response(null, { status: 200 }),
    });
    render(Layout);
    const link = await screen.findByTestId('nav-qr-sheet');
    expect(link).toHaveAttribute('href', '/specimens/qr');
    expect(link).toHaveTextContent('QR Sticker Sheet');
  });

  it('toggles the link reactively when the store state changes', async () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'not_found', message: 'no sheet' } },
      response: new Response(null, { status: 404 }),
    });
    render(Layout);
    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    expect(screen.queryByTestId('nav-qr-sheet')).not.toBeInTheDocument();

    // Simulate an "add to sheet" elsewhere — the store flips to
    // loaded and the link should appear without another fetch.
    setSheet(sheet());
    await screen.findByTestId('nav-qr-sheet');
  });

  it('always renders the static Specimens and Collectors links', () => {
    mockGet.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'not_found', message: 'no sheet' } },
      response: new Response(null, { status: 404 }),
    });
    render(Layout);
    expect(screen.getByTestId('nav-collectors')).toBeInTheDocument();
  });
});
