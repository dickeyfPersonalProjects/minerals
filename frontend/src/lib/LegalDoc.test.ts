import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGET } = vi.hoisted(() => ({
  mockGET: vi.fn(),
}));

vi.mock('./api', () => ({
  client: { GET: mockGET },
}));

import LegalDoc from './LegalDoc.svelte';

beforeEach(() => {
  mockGET.mockReset();
});

afterEach(() => {
  cleanup();
});

describe('LegalDoc (mi-97kr)', () => {
  it('fetches the document for its slug and renders the sanitized HTML', async () => {
    mockGET.mockResolvedValue({
      data: { slug: 'privacy', title: 'Privacy Policy', html: '<h1>Privacy Policy</h1><p>hi</p>' },
      error: undefined,
      response: { status: 200 },
    });

    render(LegalDoc, { slug: 'privacy' });

    await waitFor(() => {
      expect(screen.getByTestId('legal-body')).toBeInTheDocument();
    });
    expect(screen.getByTestId('legal-body')).toContainHTML('<h1>Privacy Policy</h1>');

    // Called the typed endpoint with the slug path param.
    expect(mockGET).toHaveBeenCalledWith(
      '/api/v1/legal/{slug}',
      expect.objectContaining({ params: { path: { slug: 'privacy' } } }),
    );
  });

  it('sets the document title from the response', async () => {
    mockGET.mockResolvedValue({
      data: { slug: 'terms', title: 'Terms of Service', html: '<h1>Terms</h1>' },
      error: undefined,
      response: { status: 200 },
    });

    render(LegalDoc, { slug: 'terms' });

    await waitFor(() => {
      expect(document.title).toBe('Terms of Service · Minerals');
    });
  });

  it('shows an error state with a retry that refetches', async () => {
    mockGET.mockResolvedValueOnce({
      data: undefined,
      error: { error: { code: 'internal_error', message: 'legal document unavailable' } },
      response: { status: 500 },
    });

    render(LegalDoc, { slug: 'privacy' });

    await waitFor(() => {
      expect(screen.getByTestId('legal-error')).toBeInTheDocument();
    });
    expect(screen.getByTestId('legal-error')).toHaveTextContent('legal document unavailable');

    // Retry path: second call succeeds.
    mockGET.mockResolvedValueOnce({
      data: { slug: 'privacy', title: 'Privacy Policy', html: '<h1>Privacy Policy</h1>' },
      error: undefined,
      response: { status: 200 },
    });
    await fireEvent.click(screen.getByRole('button', { name: /retry/i }));

    await waitFor(() => {
      expect(screen.getByTestId('legal-body')).toBeInTheDocument();
    });
    expect(mockGET).toHaveBeenCalledTimes(2);
  });
});
