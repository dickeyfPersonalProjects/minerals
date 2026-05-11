import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

// cropperjs is a vanilla DOM library that wires itself onto an <img>
// after load. jsdom can't run its layout-aware internals, so we mock
// the whole module and capture the `cropend` callback so tests can
// drive it manually.
interface MockCropper {
  options: { cropend?: () => void };
  destroyed: boolean;
  rotation: number;
  destroy: () => void;
  getCroppedCanvas: () => HTMLCanvasElement;
  rotate: (deg: number) => MockCropper;
  rotateTo: (deg: number) => MockCropper;
}

let lastCropper: MockCropper | null = null;

function MockCropperCtor(_el: HTMLImageElement, options: { cropend?: () => void }): MockCropper {
  const instance: MockCropper = {
    options,
    destroyed: false,
    rotation: 0,
    destroy() {
      instance.destroyed = true;
    },
    getCroppedCanvas() {
      return {
        toBlob: (cb: (blob: Blob) => void) => cb(new Blob(['x'], { type: 'image/jpeg' })),
      } as unknown as HTMLCanvasElement;
    },
    rotate(deg: number) {
      instance.rotation += deg;
      return instance;
    },
    rotateTo(deg: number) {
      instance.rotation = deg;
      return instance;
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

  it('rotate-right button increments rotation by 90 and enables Apply', async () => {
    renderModal();
    await fireImageLoad();

    const apply = screen.getByTestId('crop-modal-apply') as HTMLButtonElement;
    expect(apply.disabled).toBe(true);

    const readout = screen.getByTestId('crop-modal-rotate-readout');
    expect(readout).toHaveTextContent('0°');

    await fireEvent.click(screen.getByTestId('crop-modal-rotate-right'));

    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-rotate-readout')).toHaveTextContent('90°');
    });
    expect(lastCropper!.rotation).toBe(90);
    expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
  });

  it('rotate-left button decrements rotation by 90', async () => {
    renderModal();
    await fireImageLoad();

    await fireEvent.click(screen.getByTestId('crop-modal-rotate-left'));

    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-rotate-readout')).toHaveTextContent('-90°');
    });
    expect(lastCropper!.rotation).toBe(-90);
  });

  it('slider updates the degree readout and rotates the cropper', async () => {
    renderModal();
    await fireImageLoad();

    const slider = screen.getByTestId('crop-modal-rotate-slider') as HTMLInputElement;
    await fireEvent.input(slider, { target: { value: '-15' } });

    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-rotate-readout')).toHaveTextContent('-15°');
    });
    expect(lastCropper!.rotation).toBe(-15);
    expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
  });

  it('clicking the degree readout resets rotation to zero', async () => {
    renderModal();
    await fireImageLoad();

    await fireEvent.click(screen.getByTestId('crop-modal-rotate-right'));
    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-rotate-readout')).toHaveTextContent('90°');
    });

    await fireEvent.click(screen.getByTestId('crop-modal-rotate-readout'));

    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-rotate-readout')).toHaveTextContent('0°');
    });
    expect(lastCropper!.rotation).toBe(0);
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
