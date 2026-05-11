import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

// cropperjs is a vanilla DOM library that wires itself onto an <img>
// after load. jsdom can't run its layout-aware internals, so we mock
// the whole module and capture the `cropend` callback so tests can
// drive it manually.
interface MockCropper {
  options: { cropend?: () => void };
  destroyed: boolean;
  destroy: () => void;
  getCroppedCanvas: () => HTMLCanvasElement;
}

let lastCropper: MockCropper | null = null;

function MockCropperCtor(_el: HTMLImageElement, options: { cropend?: () => void }): MockCropper {
  const instance: MockCropper = {
    options,
    destroyed: false,
    destroy() {
      instance.destroyed = true;
    },
    getCroppedCanvas() {
      return {
        toBlob: (cb: (blob: Blob) => void) => cb(new Blob(['x'], { type: 'image/jpeg' })),
      } as unknown as HTMLCanvasElement;
    },
  };
  lastCropper = instance;
  return instance;
}
vi.mock('cropperjs', () => ({ default: MockCropperCtor }));
// cropperjs ships a CSS file we import in the component; jsdom can't
// parse it but the vite plugin handles the resolution. No-op stub is
// fine for tests since we don't render the real cropper UI.
vi.mock('cropperjs/dist/cropper.css', () => ({}));

import ImageCropModal from './ImageCropModal.svelte';

beforeEach(() => {
  lastCropper = null;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

interface ModalOverrides {
  onClose?: () => void;
  onApplied?: () => void | Promise<void>;
}

function renderModal(overrides: ModalOverrides = {}) {
  return render(ImageCropModal, {
    specimenId: 'sp-1',
    photoId: 'p-1',
    position: 1,
    takenAt: null,
    onClose: vi.fn(),
    onApplied: vi.fn(),
    ...overrides,
  });
}

async function fireImageLoad() {
  const img = screen.getByTestId('crop-modal-image');
  await fireEvent.load(img);
}

describe('ImageCropModal', () => {
  it('disables Apply until the user moves or resizes the crop box', async () => {
    renderModal();
    await fireImageLoad();

    const apply = screen.getByTestId('crop-modal-apply') as HTMLButtonElement;
    expect(apply.disabled).toBe(true);
    expect(lastCropper).not.toBeNull();

    // Simulate cropperjs firing its cropend event after the user
    // drags a handle — the only path that flips the dirty flag.
    lastCropper!.options.cropend?.();

    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });
  });

  it('Escape triggers onClose', async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    await fireImageLoad();

    await fireEvent.keyDown(window, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('renders the destructive warning callout', async () => {
    renderModal();
    const warning = screen.getByTestId('crop-modal-warning');
    expect(warning).toHaveTextContent(/permanently replace/i);
    expect(warning).toHaveTextContent(/cannot be undone/i);
  });

  it('falls back to an error message when the image fails to load', async () => {
    renderModal();
    const img = screen.getByTestId('crop-modal-image');
    await fireEvent.error(img);

    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-image-error')).toBeInTheDocument();
    });
    // Apply stays disabled when there's nothing to crop.
    expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(true);
  });
});
