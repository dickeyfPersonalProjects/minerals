import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';

const { mockLoad } = vi.hoisted(() => ({ mockLoad: vi.fn<typeof loadAuthedBlobUrl>() }));
import type { loadAuthedBlobUrl } from './blob-url';

vi.mock('./blob-url', () => ({
  loadAuthedBlobUrl: mockLoad,
  AuthedImageFetchError: class AuthedImageFetchError extends Error {},
}));

import AuthedImage from './AuthedImage.svelte';

beforeEach(() => {
  mockLoad.mockReset();
  if (typeof URL.revokeObjectURL !== 'function') {
    (URL as unknown as { revokeObjectURL: (u: string) => void }).revokeObjectURL = vi.fn();
  } else {
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  }
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('AuthedImage', () => {
  it('renders nothing while the fetch is in flight', () => {
    mockLoad.mockReturnValueOnce(new Promise(() => {}));
    render(AuthedImage, {
      src: '/api/v1/photos/p1/display',
      alt: 'a',
      'data-testid': 'img',
    });
    expect(screen.queryByTestId('img')).toBeNull();
  });

  it('renders the img with the blob URL when the fetch resolves', async () => {
    mockLoad.mockResolvedValueOnce('blob:fake/abcd');
    render(AuthedImage, {
      src: '/api/v1/photos/p1/display',
      alt: 'photo of quartz',
      class: 'h-full',
      'data-testid': 'img',
    });

    const img = (await waitFor(() => screen.getByTestId('img'))) as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('blob:fake/abcd');
    expect(img.getAttribute('data-src')).toBe('/api/v1/photos/p1/display');
    expect(img.getAttribute('alt')).toBe('photo of quartz');
    expect(img.className).toBe('h-full');
  });

  it('invokes onerror when the fetch rejects', async () => {
    mockLoad.mockRejectedValueOnce(new Error('boom'));
    const onerror = vi.fn();
    render(AuthedImage, {
      src: '/api/v1/photos/p1/display',
      alt: 'a',
      onerror,
    });

    await waitFor(() => expect(onerror).toHaveBeenCalledTimes(1));
    const arg = onerror.mock.calls[0]?.[0] as Event;
    expect(arg).toBeInstanceOf(Event);
    expect(arg.type).toBe('error');
  });

  it('revokes the previous blob URL when src changes', async () => {
    const revoke = vi.spyOn(URL, 'revokeObjectURL');
    mockLoad.mockResolvedValueOnce('blob:fake/one');
    const { rerender } = render(AuthedImage, {
      src: '/api/v1/photos/p1/display',
      alt: 'a',
      'data-testid': 'img',
    });
    await waitFor(() => {
      expect((screen.getByTestId('img') as HTMLImageElement).src).toBe('blob:fake/one');
    });

    mockLoad.mockResolvedValueOnce('blob:fake/two');
    await rerender({ src: '/api/v1/photos/p2/display', alt: 'a', 'data-testid': 'img' });

    await waitFor(() => {
      expect((screen.getByTestId('img') as HTMLImageElement).src).toBe('blob:fake/two');
    });
    expect(revoke).toHaveBeenCalledWith('blob:fake/one');
  });

  it('revokes the blob URL on unmount', async () => {
    const revoke = vi.spyOn(URL, 'revokeObjectURL');
    mockLoad.mockResolvedValueOnce('blob:fake/unmount');
    const { unmount } = render(AuthedImage, {
      src: '/api/v1/photos/p1/display',
      alt: 'a',
      'data-testid': 'img',
    });
    await waitFor(() => {
      expect((screen.getByTestId('img') as HTMLImageElement).src).toBe('blob:fake/unmount');
    });

    unmount();
    expect(revoke).toHaveBeenCalledWith('blob:fake/unmount');
  });

  it('does not call onerror after unmount even if the fetch rejects late', async () => {
    let reject!: (err: Error) => void;
    mockLoad.mockReturnValueOnce(
      new Promise((_resolve, rej) => {
        reject = rej;
      }),
    );
    const onerror = vi.fn();
    const { unmount } = render(AuthedImage, {
      src: '/api/v1/photos/p1/display',
      alt: 'a',
      onerror,
    });

    unmount();
    reject(new Error('late'));

    // Allow the microtask queue to drain.
    await Promise.resolve();
    await Promise.resolve();
    expect(onerror).not.toHaveBeenCalled();
  });
});
