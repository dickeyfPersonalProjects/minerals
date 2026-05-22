import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

// cropperjs v2 is a web-component library that mounts <cropper-*> custom
// elements into the DOM and drives them via a shadow-DOM render loop. jsdom
// can't run that, so we mock the whole module. The mock mirrors the v2 API the
// component touches: getCropperImage().$rotate (relative, degree-string),
// getCropperSelection().$toCanvas (async), and the <cropper-canvas>'s
// `actionend` event. We capture the actionend listener so tests can drive the
// "user finished dragging the crop box" path manually.
interface MockCropper {
  destroyed: boolean;
  rotation: number;
  canvasBlob: Blob | null;
  destroy: () => void;
  getCropperImage: () => { $rotate: (angle: string) => void } | null;
  getCropperSelection: () => { $toCanvas: () => Promise<HTMLCanvasElement | null> } | null;
  getCropperCanvas: () => { addEventListener: (type: string, cb: () => void) => void } | null;
  // Invokes the captured `actionend` handler — the v2 analog of v1's `cropend`.
  fireActionEnd: () => void;
}

let lastCropper: MockCropper | null = null;
let nextCanvasBlob: Blob | null = new Blob(['x'], { type: 'image/jpeg' });
let nextCanvasReturnsNull = false;

function MockCropperCtor(): MockCropper {
  let actionEndHandler: (() => void) | null = null;
  const instance: MockCropper = {
    destroyed: false,
    rotation: 0,
    canvasBlob: nextCanvasBlob,
    destroy() {
      instance.destroyed = true;
    },
    getCropperImage() {
      return {
        // The component rotates by a signed degree delta ("90deg", "-90deg").
        // Accumulate it so `rotation` tracks the absolute applied angle.
        $rotate(angle: string) {
          instance.rotation += parseFloat(angle);
        },
      };
    },
    getCropperSelection() {
      return {
        $toCanvas() {
          if (nextCanvasReturnsNull) return Promise.resolve(null);
          return Promise.resolve({
            toBlob: (cb: (blob: Blob | null) => void) => cb(instance.canvasBlob),
          } as unknown as HTMLCanvasElement);
        },
      };
    },
    getCropperCanvas() {
      return {
        addEventListener(type: string, cb: () => void) {
          if (type === 'actionend') actionEndHandler = cb;
        },
      };
    },
    fireActionEnd() {
      actionEndHandler?.();
    },
  };
  lastCropper = instance;
  return instance;
}
vi.mock('cropperjs', () => ({ default: MockCropperCtor }));

// Mock the typed API client. Each verb is a fresh vi.fn() per test so
// we can assert call args and shape responses independently.
const { mockPOST, mockPATCH, mockDELETE } = vi.hoisted(() => ({
  mockPOST: vi.fn(),
  mockPATCH: vi.fn(),
  mockDELETE: vi.fn(),
}));
vi.mock('./api', () => ({
  client: { POST: mockPOST, PATCH: mockPATCH, DELETE: mockDELETE },
}));
vi.mock('./api/wrapper', () => ({
  SUPPRESS_TOAST_HEADERS: { 'x-suppress-toast': '1' },
  envelopeMessage: (e: { error?: { message?: string; code?: string } } | undefined, s: number) =>
    e?.error?.message || e?.error?.code || `HTTP ${s}`,
}));

// V2 BFF cookie flow (mi-3vc4): cookies travel on <img> requests,
// so the modal renders the backend path directly on `src` and lets
// the browser drive the request. No blob-URL helper to mock.

const { mockToastError, mockToastSuccess } = vi.hoisted(() => ({
  mockToastError: vi.fn(),
  mockToastSuccess: vi.fn(),
}));
vi.mock('./toasts', () => ({
  toastError: mockToastError,
  toastSuccess: mockToastSuccess,
}));

import ImageCropModal from './ImageCropModal.svelte';

beforeEach(() => {
  lastCropper = null;
  nextCanvasBlob = new Blob(['x'], { type: 'image/jpeg' });
  nextCanvasReturnsNull = false;
  mockPOST.mockReset();
  mockPATCH.mockReset();
  mockDELETE.mockReset();
  mockToastError.mockReset();
  mockToastSuccess.mockReset();
});

afterEach(() => {
  cleanup();
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
  // The <img> is in the DOM immediately; we fire the browser's
  // `load` event so the component's `onload` handler initialises
  // cropperjs.
  const img = await waitFor(() => screen.getByTestId('crop-modal-image'));
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
    lastCropper!.fireActionEnd();

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

  it('falls back to an error message when the rendered <img> fires an error event', async () => {
    renderModal();
    const img = await waitFor(() => screen.getByTestId('crop-modal-image'));
    await fireEvent.error(img);

    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-image-error')).toBeInTheDocument();
    });
  });

  it('clicking Cancel calls onClose', async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    await fireImageLoad();

    await fireEvent.click(screen.getByTestId('crop-modal-cancel'));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('clicking the X close button calls onClose', async () => {
    const onClose = vi.fn();
    renderModal({ onClose });

    await fireEvent.click(screen.getByTestId('crop-modal-close'));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('clicking the backdrop calls onClose', async () => {
    const onClose = vi.fn();
    renderModal({ onClose });

    await fireEvent.click(screen.getByTestId('crop-modal-backdrop'));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('Apply uploads a JPEG blob, patches position, deletes original, and calls onApplied + onClose', async () => {
    mockPOST.mockResolvedValue({
      data: { id: 'new-photo-id' },
      response: { status: 201 },
    });
    mockPATCH.mockResolvedValue({ data: {}, response: { status: 200 } });
    mockDELETE.mockResolvedValue({ data: {}, response: { status: 204 } });

    const onClose = vi.fn();
    const onApplied = vi.fn();
    renderModal({ onClose, onApplied });
    await fireImageLoad();

    // Mark dirty via the cropend hook so Apply enables.
    lastCropper!.fireActionEnd();
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });

    await fireEvent.click(screen.getByTestId('crop-modal-apply'));

    await waitFor(() => {
      expect(mockPOST).toHaveBeenCalledTimes(1);
    });
    const postCall = mockPOST.mock.calls[0]!;
    expect(postCall[0]).toBe('/api/v1/specimens/{id}/photos');
    const postOpts = postCall[1] as {
      params: { path: { id: string } };
      bodySerializer: () => FormData;
      headers: Record<string, string>;
    };
    expect(postOpts.params.path.id).toBe('sp-1');
    expect(postOpts.headers['x-suppress-toast']).toBe('1');

    const fd = postOpts.bodySerializer();
    const fileEntry = fd.get('file');
    expect(fileEntry).toBeInstanceOf(File);
    expect((fileEntry as File).type).toBe('image/jpeg');
    expect((fileEntry as File).name).toMatch(/^cropped-.*\.jpg$/);

    await waitFor(() => {
      expect(mockPATCH).toHaveBeenCalledTimes(1);
    });
    const patchOpts = mockPATCH.mock.calls[0]![1] as {
      params: { path: { id: string } };
      body: { position: number; taken_at?: string };
    };
    expect(patchOpts.params.path.id).toBe('new-photo-id');
    expect(patchOpts.body.position).toBe(1);

    await waitFor(() => {
      expect(mockDELETE).toHaveBeenCalledTimes(1);
    });
    const deleteOpts = mockDELETE.mock.calls[0]![1] as { params: { path: { id: string } } };
    expect(deleteOpts.params.path.id).toBe('p-1');

    await waitFor(() => {
      expect(onApplied).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });
    expect(mockToastSuccess).toHaveBeenCalledWith('Photo cropped');
  });

  it('forwards taken_at on the PATCH when provided', async () => {
    mockPOST.mockResolvedValue({ data: { id: 'new-id' }, response: { status: 201 } });
    mockPATCH.mockResolvedValue({ data: {}, response: { status: 200 } });
    mockDELETE.mockResolvedValue({ data: {}, response: { status: 204 } });

    render(ImageCropModal, {
      specimenId: 'sp-1',
      photoId: 'p-1',
      position: 3,
      takenAt: '2026-01-02T03:04:05Z',
      onClose: vi.fn(),
      onApplied: vi.fn(),
    });
    await fireImageLoad();
    lastCropper!.fireActionEnd();

    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });
    await fireEvent.click(screen.getByTestId('crop-modal-apply'));

    await waitFor(() => {
      expect(mockPATCH).toHaveBeenCalledTimes(1);
    });
    const patchBody = (
      mockPATCH.mock.calls[0]![1] as { body: { position: number; taken_at?: string } }
    ).body;
    expect(patchBody.position).toBe(3);
    expect(patchBody.taken_at).toBe('2026-01-02T03:04:05Z');
  });

  it('surfaces an upload error and skips PATCH/DELETE/onApplied', async () => {
    mockPOST.mockResolvedValue({
      error: { error: { message: 'upload boom' } },
      response: { status: 500 },
    });
    const onClose = vi.fn();
    const onApplied = vi.fn();
    renderModal({ onClose, onApplied });
    await fireImageLoad();
    lastCropper!.fireActionEnd();
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });

    await fireEvent.click(screen.getByTestId('crop-modal-apply'));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('upload boom'));
    });
    expect(mockPATCH).not.toHaveBeenCalled();
    expect(mockDELETE).not.toHaveBeenCalled();
    expect(onApplied).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    // Apply re-enables after the failure so the user can retry.
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });
  });

  it('reports a missing id on upload success without an id', async () => {
    mockPOST.mockResolvedValue({ data: {}, response: { status: 201 } });
    renderModal();
    await fireImageLoad();
    lastCropper!.fireActionEnd();
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });

    await fireEvent.click(screen.getByTestId('crop-modal-apply'));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('no id'));
    });
    expect(mockPATCH).not.toHaveBeenCalled();
    expect(mockDELETE).not.toHaveBeenCalled();
  });

  it('warns when delete-original fails but still completes the crop', async () => {
    mockPOST.mockResolvedValue({ data: { id: 'new-id' }, response: { status: 201 } });
    mockPATCH.mockResolvedValue({ data: {}, response: { status: 200 } });
    mockDELETE.mockResolvedValue({
      error: { error: { message: 'delete denied' } },
      response: { status: 403 },
    });

    const onApplied = vi.fn();
    const onClose = vi.fn();
    renderModal({ onApplied, onClose });
    await fireImageLoad();
    lastCropper!.fireActionEnd();
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });

    await fireEvent.click(screen.getByTestId('crop-modal-apply'));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith(
        expect.stringMatching(/Crop saved but original not removed.*delete denied/),
      );
    });
    expect(mockToastSuccess).not.toHaveBeenCalled();
    expect(onApplied).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('toasts when the cropped canvas yields no blob', async () => {
    nextCanvasBlob = null;
    renderModal();
    await fireImageLoad();
    lastCropper!.fireActionEnd();
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });

    await fireEvent.click(screen.getByTestId('crop-modal-apply'));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith(
        expect.stringMatching(/Crop failed.*Canvas produced no blob/),
      );
    });
    expect(mockPOST).not.toHaveBeenCalled();
  });

  it('toasts when $toCanvas resolves no canvas', async () => {
    nextCanvasReturnsNull = true;
    renderModal();
    await fireImageLoad();
    lastCropper!.fireActionEnd();
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });

    await fireEvent.click(screen.getByTestId('crop-modal-apply'));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith(expect.stringContaining('no cropped canvas'));
    });
    expect(mockPOST).not.toHaveBeenCalled();
  });

  it('Escape is a no-op while busy', async () => {
    // Block POST so the component sits in the `busy` state long enough
    // to assert that Escape does not call onClose mid-flight.
    let releasePost!: (v: unknown) => void;
    mockPOST.mockReturnValue(
      new Promise((resolve) => {
        releasePost = resolve;
      }),
    );
    const onClose = vi.fn();
    renderModal({ onClose });
    await fireImageLoad();
    lastCropper!.fireActionEnd();
    await waitFor(() => {
      expect((screen.getByTestId('crop-modal-apply') as HTMLButtonElement).disabled).toBe(false);
    });

    await fireEvent.click(screen.getByTestId('crop-modal-apply'));
    await waitFor(() => {
      expect(screen.getByTestId('crop-modal-apply')).toHaveTextContent(/Applying/);
    });

    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();

    // Let the promise settle so afterEach unmount doesn't race.
    releasePost({ data: { id: 'x' }, response: { status: 201 } });
    mockPATCH.mockResolvedValue({ data: {}, response: { status: 200 } });
    mockDELETE.mockResolvedValue({ data: {}, response: { status: 204 } });
  });
});
