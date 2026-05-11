import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';

const { mockToString } = vi.hoisted(() => ({ mockToString: vi.fn() }));

vi.mock('qrcode', () => ({
  default: { toString: mockToString },
  toString: mockToString,
}));

import QrCode from './QrCode.svelte';

beforeEach(() => {
  mockToString.mockReset();
});
afterEach(cleanup);

describe('QrCode', () => {
  it('passes the value to qrcode.toString and renders the returned SVG', async () => {
    mockToString.mockResolvedValue(
      '<svg width="100" height="100" viewBox="0 0 1 1"><rect data-testid="inner-rect"/></svg>',
    );
    render(QrCode, { props: { value: 'https://example.com/x', alt: 'QR' } });
    const svg = await screen.findByTestId('qr-svg');
    expect(svg.getAttribute('data-qr-value')).toBe('https://example.com/x');
    // The component strips fixed width/height in favour of 100% so
    // the SVG inherits the parent box and prints crisply at any size.
    expect(svg.innerHTML).toContain('width="100%"');
    expect(svg.innerHTML).toContain('height="100%"');
    // First arg to toString is the payload; second is options.
    expect(mockToString).toHaveBeenCalledWith(
      'https://example.com/x',
      expect.objectContaining({ type: 'svg' }),
    );
  });

  it('renders an error fallback if the QR generator throws', async () => {
    mockToString.mockRejectedValue(new Error('input too long'));
    render(QrCode, { props: { value: 'x', alt: 'QR' } });
    await waitFor(() => {
      expect(screen.getByTestId('qr-error')).toHaveTextContent('input too long');
    });
  });
});
