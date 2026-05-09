import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockPost } = vi.hoisted(() => ({ mockPost: vi.fn() }));

vi.mock('./api', () => ({
  client: { POST: mockPost },
}));

import PhotoUploader, { MAX_UPLOAD_BYTES } from './PhotoUploader.svelte';

const SPECIMEN_ID = '11111111-1111-1111-1111-111111111111';

function jpeg(name = 'photo.jpg', bytes = 1024): File {
  const blob = new Blob([new Uint8Array(bytes)], { type: 'image/jpeg' });
  return new File([blob], name, { type: 'image/jpeg' });
}

function file(name: string, type: string, bytes: number): File {
  const blob = new Blob([new Uint8Array(bytes)], { type });
  return new File([blob], name, { type });
}

function ok() {
  return {
    data: {
      id: 'pppppppp-0000-0000-0000-000000000001',
      specimen_id: SPECIMEN_ID,
      file_id: 'aaaaaaaa-0000-0000-0000-000000000000',
      content_type: 'image/jpeg',
      byte_size: 1024,
      sha256: 'deadbeef',
      position: 1,
      taken_at: null,
      created_at: '2026-05-09T12:00:00Z',
    },
    error: undefined,
    response: new Response(null, { status: 201 }),
  };
}

function envelopeError(status: number, code: string, message: string) {
  return {
    data: undefined,
    error: { error: { code, message } },
    response: new Response(null, { status }),
  };
}

function dataTransfer(files: File[]): DataTransfer {
  // jsdom's DataTransfer doesn't support `items`/`files` directly, so
  // build a minimal stub good enough for the handlers under test.
  return {
    files: files as unknown as FileList,
    items: files.map((f) => ({ kind: 'file', type: f.type })) as unknown as DataTransferItemList,
    types: ['Files'],
    dropEffect: 'copy',
  } as unknown as DataTransfer;
}

beforeEach(() => {
  mockPost.mockReset();
});

afterEach(() => {
  cleanup();
});

describe('PhotoUploader', () => {
  it('shows the drop overlay on dragenter and hides it on dragleave', async () => {
    render(PhotoUploader, { specimenId: SPECIMEN_ID, onUploaded: vi.fn() });

    const zone = screen.getByTestId('photo-uploader');
    expect(screen.queryByTestId('photo-drop-overlay')).toBeNull();
    expect(zone).toHaveAttribute('data-dragging', 'false');

    await fireEvent.dragEnter(zone, { dataTransfer: dataTransfer([]) });
    expect(screen.getByTestId('photo-drop-overlay')).toBeInTheDocument();
    expect(zone).toHaveAttribute('data-dragging', 'true');

    await fireEvent.dragLeave(zone, { dataTransfer: dataTransfer([]) });
    await waitFor(() => expect(screen.queryByTestId('photo-drop-overlay')).toBeNull());
    expect(zone).toHaveAttribute('data-dragging', 'false');
  });

  it('uploads a picked file via the API and calls onUploaded after success', async () => {
    mockPost.mockResolvedValue(ok());
    const onUploaded = vi.fn();

    render(PhotoUploader, { specimenId: SPECIMEN_ID, onUploaded });

    const input = screen.getByTestId('photo-file-input') as HTMLInputElement;
    await fireEvent.change(input, { target: { files: [jpeg('a.jpg', 2048)] } });

    await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(1));
    expect(mockPost).toHaveBeenCalledTimes(1);
    const [path, opts] = mockPost.mock.calls[0]!;
    expect(path).toBe('/api/v1/specimens/{id}/photos');
    expect(opts.params.path.id).toBe(SPECIMEN_ID);
    // The bodySerializer must produce a FormData with the `file`
    // form field — this is the multipart contract from §12.
    const fd = opts.bodySerializer(opts.body) as FormData;
    expect(fd).toBeInstanceOf(FormData);
    expect(fd.get('file')).toBeInstanceOf(File);
    expect((fd.get('file') as File).name).toBe('a.jpg');

    await waitFor(() =>
      expect(screen.getByTestId('photo-upload-item')).toHaveAttribute('data-status', 'success'),
    );
    expect(screen.getByTestId('photo-upload-item')).toHaveTextContent('Uploaded');
  });

  it('rejects disallowed types client-side without calling the API', async () => {
    const onUploaded = vi.fn();
    render(PhotoUploader, { specimenId: SPECIMEN_ID, onUploaded });

    const input = screen.getByTestId('photo-file-input') as HTMLInputElement;
    await fireEvent.change(input, {
      target: { files: [file('notes.txt', 'text/plain', 100)] },
    });

    const item = await screen.findByTestId('photo-upload-item');
    expect(item).toHaveAttribute('data-status', 'error');
    expect(screen.getByTestId('photo-upload-error')).toHaveTextContent(/Unsupported type/);
    expect(mockPost).not.toHaveBeenCalled();
    // No successful uploads → still no refetch needed.
    expect(onUploaded).not.toHaveBeenCalled();
  });

  it('rejects oversize files client-side without calling the API', async () => {
    const onUploaded = vi.fn();
    render(PhotoUploader, { specimenId: SPECIMEN_ID, onUploaded });

    // Build a File whose declared size exceeds MAX_UPLOAD_BYTES
    // without actually allocating that many bytes.
    const big = jpeg('huge.jpg', 1);
    Object.defineProperty(big, 'size', { value: MAX_UPLOAD_BYTES + 1 });

    const input = screen.getByTestId('photo-file-input') as HTMLInputElement;
    await fireEvent.change(input, { target: { files: [big] } });

    const item = await screen.findByTestId('photo-upload-item');
    expect(item).toHaveAttribute('data-status', 'error');
    expect(screen.getByTestId('photo-upload-error')).toHaveTextContent(/max is/);
    expect(mockPost).not.toHaveBeenCalled();
    expect(onUploaded).not.toHaveBeenCalled();
  });

  it('surfaces the server error envelope message on a 415 response', async () => {
    mockPost.mockResolvedValue(
      envelopeError(415, 'unsupported_media_type', 'image/gif is not allowed'),
    );
    const onUploaded = vi.fn();

    render(PhotoUploader, { specimenId: SPECIMEN_ID, onUploaded });

    const input = screen.getByTestId('photo-file-input') as HTMLInputElement;
    await fireEvent.change(input, { target: { files: [jpeg('looks-ok.jpg', 1024)] } });

    await waitFor(() =>
      expect(screen.getByTestId('photo-upload-item')).toHaveAttribute('data-status', 'error'),
    );
    expect(screen.getByTestId('photo-upload-error')).toHaveTextContent(/image\/gif is not allowed/);
    // Refetch still runs after the batch finishes — see the
    // component's processAll comment about batches with mixed
    // outcomes. We only assert it was invoked once.
    expect(onUploaded).toHaveBeenCalledTimes(1);
  });

  it('uploads multiple dropped files and refetches once per batch', async () => {
    mockPost.mockResolvedValue(ok());
    const onUploaded = vi.fn();

    render(PhotoUploader, { specimenId: SPECIMEN_ID, onUploaded });

    const zone = screen.getByTestId('photo-uploader');
    const files = [jpeg('a.jpg'), jpeg('b.jpg'), jpeg('c.jpg')];
    await fireEvent.drop(zone, { dataTransfer: dataTransfer(files) });

    await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(1));
    expect(mockPost).toHaveBeenCalledTimes(3);
    // After the drop completes the dragging overlay is gone.
    expect(screen.queryByTestId('photo-drop-overlay')).toBeNull();
    expect(zone).toHaveAttribute('data-dragging', 'false');
  });
});
