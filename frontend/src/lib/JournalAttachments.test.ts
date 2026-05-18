import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockGet, mockPost, mockDelete } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
  mockDelete: vi.fn(),
}));

vi.mock('./api', () => ({
  client: { GET: mockGet, POST: mockPost, DELETE: mockDelete },
}));

import JournalAttachments, { MAX_ATTACHMENT_BYTES } from './JournalAttachments.svelte';
import { __authenticate, __resetAuthStore } from './auth';

const ENTRY_ID = '11111111-1111-1111-1111-111111111111';

function pdf(name = 'doc.pdf', bytes = 1024): File {
  const blob = new Blob([new Uint8Array(bytes)], { type: 'application/pdf' });
  return new File([blob], name, { type: 'application/pdf' });
}

function file(name: string, type: string, bytes: number): File {
  const blob = new Blob([new Uint8Array(bytes)], { type });
  return new File([blob], name, { type });
}

function attachment(seed: { file_id?: string; content_type?: string; byte_size?: number } = {}) {
  return {
    file_id: seed.file_id ?? 'ffffffff-0000-0000-0000-000000000001',
    entry_id: ENTRY_ID,
    content_type: seed.content_type ?? 'application/pdf',
    byte_size: seed.byte_size ?? 4096,
    sha256: 'deadbeef',
    position: 1,
    created_at: '2026-05-01T12:00:00Z',
  };
}

function listOk(items: ReturnType<typeof attachment>[]) {
  return { data: { items }, error: undefined, response: new Response() };
}

function postOk(att: ReturnType<typeof attachment>) {
  return { data: att, error: undefined, response: new Response(null, { status: 201 }) };
}

function envelopeError(status: number, code: string, message: string) {
  return {
    data: undefined,
    error: { error: { code, message } },
    response: new Response(null, { status }),
  };
}

function dataTransfer(files: File[]): DataTransfer {
  return {
    files: files as unknown as FileList,
    items: files.map((f) => ({ kind: 'file', type: f.type })) as unknown as DataTransferItemList,
    types: ['Files'],
    dropEffect: 'copy',
  } as unknown as DataTransfer;
}

beforeEach(() => {
  mockGet.mockReset();
  mockPost.mockReset();
  mockDelete.mockReset();
  __authenticate();
});

afterEach(() => {
  cleanup();
  __resetAuthStore();
});

describe('JournalAttachments', () => {
  it('fetches and renders the initial list when no `initial` prop is supplied', async () => {
    mockGet.mockResolvedValue(listOk([attachment()]));

    render(JournalAttachments, { entryId: ENTRY_ID });

    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));
    expect(mockGet.mock.calls[0]?.[0]).toBe('/api/v1/journal/{id}/files');
    expect(mockGet.mock.calls[0]?.[1].params.path.id).toBe(ENTRY_ID);

    await waitFor(() => expect(screen.getByTestId('journal-attachment-list')).toBeInTheDocument());
    const link = screen.getByTestId('journal-attachment-download') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/api/v1/files/ffffffff-0000-0000-0000-000000000001');
    // Native browser download for cross-tab compatibility (not in OpenAPI).
    expect(link.hasAttribute('download')).toBe(true);
  });

  it('uses an initial list and skips the GET when `initial` is provided', () => {
    render(JournalAttachments, { entryId: ENTRY_ID, initial: [attachment()] });

    expect(mockGet).not.toHaveBeenCalled();
    expect(screen.getByTestId('journal-attachment-list')).toBeInTheDocument();
  });

  it('uploads a picked file via POST and refetches on success', async () => {
    mockGet
      .mockResolvedValueOnce(listOk([])) // initial fetch
      .mockResolvedValueOnce(listOk([attachment()])); // post-upload refetch
    mockPost.mockResolvedValue(postOk(attachment()));

    render(JournalAttachments, { entryId: ENTRY_ID });
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));

    const input = screen.getByTestId('journal-attachment-file-input') as HTMLInputElement;
    await fireEvent.change(input, { target: { files: [pdf('a.pdf', 2048)] } });

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    const [path, opts] = mockPost.mock.calls[0]!;
    expect(path).toBe('/api/v1/journal/{id}/files');
    expect(opts.params.path.id).toBe(ENTRY_ID);
    const fd = opts.bodySerializer(opts.body) as FormData;
    expect(fd).toBeInstanceOf(FormData);
    expect(fd.get('file')).toBeInstanceOf(File);
    expect((fd.get('file') as File).name).toBe('a.pdf');

    await waitFor(() =>
      expect(screen.getByTestId('journal-attachment-upload-item')).toHaveAttribute(
        'data-status',
        'success',
      ),
    );
    // Refetch once after the batch.
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));
  });

  it('rejects disallowed types client-side without calling POST', async () => {
    mockGet.mockResolvedValue(listOk([]));
    render(JournalAttachments, { entryId: ENTRY_ID });
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));

    const input = screen.getByTestId('journal-attachment-file-input') as HTMLInputElement;
    await fireEvent.change(input, {
      target: { files: [file('exe.bin', 'application/octet-stream', 64)] },
    });

    const item = await screen.findByTestId('journal-attachment-upload-item');
    expect(item).toHaveAttribute('data-status', 'error');
    expect(screen.getByTestId('journal-attachment-upload-error')).toHaveTextContent(
      /Unsupported type/,
    );
    expect(mockPost).not.toHaveBeenCalled();
  });

  it('rejects oversize files client-side without calling POST', async () => {
    mockGet.mockResolvedValue(listOk([]));
    render(JournalAttachments, { entryId: ENTRY_ID });
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));

    const big = pdf('huge.pdf', 1);
    Object.defineProperty(big, 'size', { value: MAX_ATTACHMENT_BYTES + 1 });

    const input = screen.getByTestId('journal-attachment-file-input') as HTMLInputElement;
    await fireEvent.change(input, { target: { files: [big] } });

    const item = await screen.findByTestId('journal-attachment-upload-item');
    expect(item).toHaveAttribute('data-status', 'error');
    expect(screen.getByTestId('journal-attachment-upload-error')).toHaveTextContent(/max is/);
    expect(mockPost).not.toHaveBeenCalled();
  });

  it('surfaces the server error envelope message on a 415 response', async () => {
    mockGet.mockResolvedValueOnce(listOk([])).mockResolvedValueOnce(listOk([]));
    mockPost.mockResolvedValue(
      envelopeError(415, 'unsupported_media_type', 'application/zip is not allowed'),
    );

    render(JournalAttachments, { entryId: ENTRY_ID });
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));

    const input = screen.getByTestId('journal-attachment-file-input') as HTMLInputElement;
    await fireEvent.change(input, { target: { files: [pdf('looks-ok.pdf', 1024)] } });

    await waitFor(() =>
      expect(screen.getByTestId('journal-attachment-upload-item')).toHaveAttribute(
        'data-status',
        'error',
      ),
    );
    expect(screen.getByTestId('journal-attachment-upload-error')).toHaveTextContent(
      /application\/zip is not allowed/,
    );
  });

  it('uploads multiple dropped files and refetches once per batch', async () => {
    mockGet.mockResolvedValueOnce(listOk([])).mockResolvedValueOnce(listOk([]));
    mockPost.mockResolvedValue(postOk(attachment()));

    render(JournalAttachments, { entryId: ENTRY_ID });
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(1));

    const zone = screen.getByTestId('journal-attachments');
    const files = [pdf('a.pdf'), pdf('b.pdf'), pdf('c.pdf')];
    await fireEvent.drop(zone, { dataTransfer: dataTransfer(files) });

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(3));
    // Initial fetch + one post-batch refetch = 2 GETs.
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));
    expect(zone).toHaveAttribute('data-dragging', 'false');
  });

  it('confirms before deleting and DELETEs + refetches on accept', async () => {
    mockGet.mockResolvedValueOnce(listOk([attachment()])).mockResolvedValueOnce(listOk([])); // post-delete
    mockDelete.mockResolvedValue({
      data: undefined,
      error: undefined,
      response: new Response(null, { status: 204 }),
    });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);

    render(JournalAttachments, { entryId: ENTRY_ID });
    await waitFor(() => expect(screen.getByTestId('journal-attachment-list')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('journal-attachment-delete'));

    await waitFor(() => expect(mockDelete).toHaveBeenCalledTimes(1));
    expect(mockDelete.mock.calls[0]?.[0]).toBe('/api/v1/journal-files/{file_id}');
    expect(mockDelete.mock.calls[0]?.[1].params.path.file_id).toBe(
      'ffffffff-0000-0000-0000-000000000001',
    );
    expect(confirmSpy).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));
  });

  it('does not DELETE when the confirm dialog is cancelled', async () => {
    mockGet.mockResolvedValue(listOk([attachment()]));
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    render(JournalAttachments, { entryId: ENTRY_ID });
    await waitFor(() => expect(screen.getByTestId('journal-attachment-list')).toBeInTheDocument());

    await fireEvent.click(screen.getByTestId('journal-attachment-delete'));

    expect(mockDelete).not.toHaveBeenCalled();
  });
});
