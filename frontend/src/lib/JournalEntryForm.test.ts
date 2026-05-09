import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import JournalEntryForm from './JournalEntryForm.svelte';
import type { JournalEntryFormSubmitResult } from './JournalEntryForm.svelte';

beforeEach(() => {
  vi.restoreAllMocks();
});

afterEach(() => {
  cleanup();
});

describe('JournalEntryForm', () => {
  it('renders an empty textarea in create mode', () => {
    const onSubmit = vi.fn();
    render(JournalEntryForm, { submitLabel: 'Add entry', onSubmit });

    const textarea = screen.getByLabelText(/entry body/i) as HTMLTextAreaElement;
    expect(textarea).toBeInTheDocument();
    expect(textarea.value).toBe('');
    expect(screen.getByTestId('journal-submit-button')).toHaveTextContent('Add entry');
  });

  it('pre-populates the textarea from initial.body_md', () => {
    const onSubmit = vi.fn();
    render(JournalEntryForm, {
      submitLabel: 'Save',
      onSubmit,
      initial: { body_md: 'Existing entry text.' },
    });

    const textarea = screen.getByLabelText(/entry body/i) as HTMLTextAreaElement;
    expect(textarea.value).toBe('Existing entry text.');
  });

  it('rejects empty body without calling onSubmit', async () => {
    const onSubmit = vi.fn();
    render(JournalEntryForm, { submitLabel: 'Add entry', onSubmit });

    await fireEvent.submit(screen.getByTestId('journal-entry-form'));
    await waitFor(() => expect(screen.getByTestId('journal-body-error')).toBeInTheDocument());
    expect(screen.getByTestId('journal-body-error')).toHaveTextContent(/cannot be empty/i);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('submits the trimmed body when valid', async () => {
    const onSubmit = vi.fn(async (): Promise<JournalEntryFormSubmitResult> => ({ kind: 'ok' }));
    render(JournalEntryForm, { submitLabel: 'Add entry', onSubmit });

    const textarea = screen.getByLabelText(/entry body/i);
    await fireEvent.input(textarea, { target: { value: '  Cleaned with brush.  ' } });
    await fireEvent.submit(screen.getByTestId('journal-entry-form'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const firstCall = onSubmit.mock.calls[0];
    expect(firstCall).toBeDefined();
    const passed = (firstCall as unknown[])[0];
    expect(passed).toEqual({ body_md: 'Cleaned with brush.' });
  });

  it('does not render an inline banner when onSubmit returns kind: error (route toasts)', async () => {
    const onSubmit = vi.fn(
      async (): Promise<JournalEntryFormSubmitResult> => ({
        kind: 'error',
        message: 'Network down',
      }),
    );
    render(JournalEntryForm, { submitLabel: 'Add entry', onSubmit });

    const textarea = screen.getByLabelText(/entry body/i);
    await fireEvent.input(textarea, { target: { value: 'Hello' } });
    await fireEvent.submit(screen.getByTestId('journal-entry-form'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    // The inline journal-form-error banner was removed in E-4 in
    // favor of a global toast surfaced by the route.
    expect(screen.queryByTestId('journal-form-error')).not.toBeInTheDocument();
  });

  it('calls onCancel when the cancel button is clicked', async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    render(JournalEntryForm, { submitLabel: 'Save', onSubmit, onCancel });

    await fireEvent.click(screen.getByTestId('journal-cancel-button'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('disables submit and cancel while a submission is pending', async () => {
    let resolveSubmit: (r: JournalEntryFormSubmitResult) => void = () => {};
    const onSubmit = vi.fn(
      () =>
        new Promise<JournalEntryFormSubmitResult>((resolve) => {
          resolveSubmit = resolve;
        }),
    );
    const onCancel = vi.fn();
    render(JournalEntryForm, { submitLabel: 'Save', onSubmit, onCancel });

    const textarea = screen.getByLabelText(/entry body/i);
    await fireEvent.input(textarea, { target: { value: 'Hello' } });
    void fireEvent.submit(screen.getByTestId('journal-entry-form'));

    await waitFor(() =>
      expect(screen.getByTestId('journal-submit-button')).toHaveTextContent(/saving…/i),
    );
    expect(screen.getByTestId('journal-submit-button')).toBeDisabled();
    expect(screen.getByTestId('journal-cancel-button')).toBeDisabled();

    resolveSubmit({ kind: 'ok' });
  });
});
