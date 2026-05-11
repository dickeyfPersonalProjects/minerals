import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import Lightbox from './Lightbox.svelte';

const PHOTOS = [
  { id: 'p1', alt: 'first' },
  { id: 'p2', alt: 'second' },
  { id: 'p3', alt: 'third' },
];

beforeEach(() => {
  vi.restoreAllMocks();
});

afterEach(() => {
  cleanup();
});

describe('Lightbox', () => {
  it('renders the photo at startIndex', () => {
    render(Lightbox, { photos: PHOTOS, startIndex: 1, onClose: vi.fn() });

    const img = screen.getByTestId('lightbox-image') as HTMLImageElement;
    expect(img.getAttribute('src')).toBe('/api/v1/photos/p2/display');
    expect(img.getAttribute('alt')).toBe('second');
    expect(screen.getByTestId('lightbox-counter')).toHaveTextContent('2 / 3');
  });

  it('ArrowRight advances and wraps past the last photo', async () => {
    render(Lightbox, { photos: PHOTOS, startIndex: 2, onClose: vi.fn() });

    await fireEvent.keyDown(window, { key: 'ArrowRight' });

    await waitFor(() => {
      const img = screen.getByTestId('lightbox-image') as HTMLImageElement;
      expect(img.getAttribute('src')).toBe('/api/v1/photos/p1/display');
    });
    expect(screen.getByTestId('lightbox-counter')).toHaveTextContent('1 / 3');
  });

  it('ArrowLeft retreats and wraps before the first photo', async () => {
    render(Lightbox, { photos: PHOTOS, startIndex: 0, onClose: vi.fn() });

    await fireEvent.keyDown(window, { key: 'ArrowLeft' });

    await waitFor(() => {
      const img = screen.getByTestId('lightbox-image') as HTMLImageElement;
      expect(img.getAttribute('src')).toBe('/api/v1/photos/p3/display');
    });
    expect(screen.getByTestId('lightbox-counter')).toHaveTextContent('3 / 3');
  });

  it('Escape calls onClose', async () => {
    const onClose = vi.fn();
    render(Lightbox, { photos: PHOTOS, startIndex: 0, onClose });

    await fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('hides the figure when photos becomes empty (clamp $effect)', async () => {
    // The component clamps `index` when `photos` shrinks; with zero
    // photos the `current` derived is null and the figure unmounts.
    const { rerender } = render(Lightbox, {
      photos: PHOTOS,
      startIndex: 2,
      onClose: vi.fn(),
    });
    expect(screen.getByTestId('lightbox-image')).toBeInTheDocument();

    await rerender({ photos: [], startIndex: 2, onClose: vi.fn() });

    await waitFor(() => expect(screen.queryByTestId('lightbox-image')).not.toBeInTheDocument());
  });

  it('onDelete is called with the current photo id', async () => {
    const onDelete = vi.fn();
    render(Lightbox, {
      photos: PHOTOS,
      startIndex: 1,
      onClose: vi.fn(),
      onDelete,
    });

    await fireEvent.click(screen.getByTestId('lightbox-delete'));

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith('p2');
  });

  it('onCrop is called with the current photo id', async () => {
    const onCrop = vi.fn();
    render(Lightbox, {
      photos: PHOTOS,
      startIndex: 1,
      onClose: vi.fn(),
      onCrop,
    });

    await fireEvent.click(screen.getByTestId('lightbox-crop'));

    expect(onCrop).toHaveBeenCalledTimes(1);
    expect(onCrop).toHaveBeenCalledWith('p2');
  });

  it('hides the action bar when no action callbacks are provided', () => {
    render(Lightbox, {
      photos: PHOTOS,
      startIndex: 0,
      onClose: vi.fn(),
    });

    expect(screen.queryByTestId('lightbox-delete')).not.toBeInTheDocument();
    expect(screen.queryByTestId('lightbox-crop')).not.toBeInTheDocument();
  });

  it('renders the kind label in the lightbox metadata', async () => {
    render(Lightbox, {
      photos: [
        { id: 'p1', alt: 'first', kind: 'uv' as const },
        { id: 'p2', alt: 'second', kind: 'visible' as const },
      ],
      startIndex: 0,
      onClose: vi.fn(),
    });

    const kind = screen.getByTestId('lightbox-kind');
    expect(kind).toHaveTextContent('UV');
    expect(kind).toHaveAttribute('data-kind', 'uv');

    await fireEvent.keyDown(window, { key: 'ArrowRight' });
    await waitFor(() => {
      const next = screen.getByTestId('lightbox-kind');
      expect(next).toHaveTextContent('Visible');
      expect(next).toHaveAttribute('data-kind', 'visible');
    });
  });

  it('defaults the kind label to Visible when kind is absent', () => {
    render(Lightbox, {
      photos: [{ id: 'p1', alt: 'first' }],
      startIndex: 0,
      onClose: vi.fn(),
    });

    expect(screen.getByTestId('lightbox-kind')).toHaveTextContent('Visible');
  });

  it('hides nav arrows and counter when only one photo is shown', () => {
    render(Lightbox, {
      photos: [PHOTOS[0]!],
      startIndex: 0,
      onClose: vi.fn(),
    });

    expect(screen.queryByTestId('lightbox-prev')).not.toBeInTheDocument();
    expect(screen.queryByTestId('lightbox-next')).not.toBeInTheDocument();
    expect(screen.queryByTestId('lightbox-counter')).not.toBeInTheDocument();
  });
});
