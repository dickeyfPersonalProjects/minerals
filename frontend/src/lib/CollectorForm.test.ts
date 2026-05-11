import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import CollectorForm from './CollectorForm.svelte';
import type { CollectorFormSubmitResult } from './CollectorForm.svelte';

beforeEach(() => {
  vi.restoreAllMocks();
});

afterEach(() => {
  cleanup();
});

describe('CollectorForm', () => {
  it('shows the required-name validation error and does not call onSubmit', async () => {
    const onSubmit = vi.fn();
    render(CollectorForm, { submitLabel: 'Create', onSubmit });

    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());
    expect(screen.getByTestId('name-error')).toHaveTextContent(/required/i);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('submits trimmed values when valid', async () => {
    const onSubmit = vi.fn(async (): Promise<CollectorFormSubmitResult> => ({ kind: 'ok' }));
    render(CollectorForm, { submitLabel: 'Create', onSubmit });

    await fireEvent.input(screen.getByLabelText(/^name/i), {
      target: { value: '  Marie Curie  ' },
    });
    await fireEvent.input(screen.getByLabelText(/notes/i), {
      target: { value: '  Met at show.  ' },
    });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const passed = (onSubmit.mock.calls[0] as unknown[])[0];
    expect(passed).toEqual({ name: 'Marie Curie', notes: 'Met at show.' });
  });

  it('renders the banner error when onSubmit returns kind: error', async () => {
    const onSubmit = vi.fn(
      async (): Promise<CollectorFormSubmitResult> => ({
        kind: 'error',
        message: 'Network down',
      }),
    );
    render(CollectorForm, { submitLabel: 'Create', onSubmit });

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Anyone' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(screen.getByTestId('form-error')).toBeInTheDocument());
    expect(screen.getByTestId('form-error')).toHaveTextContent('Network down');
  });

  it('shows the field-scoped nameTakenError on kind: duplicate', async () => {
    const onSubmit = vi.fn(async (): Promise<CollectorFormSubmitResult> => ({ kind: 'duplicate' }));
    render(CollectorForm, { submitLabel: 'Create', onSubmit });

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Marie Curie' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));

    await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());
    expect(screen.getByTestId('name-error')).toHaveTextContent(/Marie Curie/);
    expect(screen.getByTestId('name-error')).toHaveTextContent(/already exists/i);
    // The duplicate path scopes to the name field, not the banner.
    expect(screen.queryByTestId('form-error')).not.toBeInTheDocument();
  });

  it('clears nameTakenError as soon as the user edits the name', async () => {
    const onSubmit = vi.fn(async (): Promise<CollectorFormSubmitResult> => ({ kind: 'duplicate' }));
    render(CollectorForm, { submitLabel: 'Create', onSubmit });

    const name = screen.getByLabelText(/^name/i);
    await fireEvent.input(name, { target: { value: 'Marie Curie' } });
    await fireEvent.submit(screen.getByTestId('collector-form'));
    await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());

    await fireEvent.input(name, { target: { value: 'Marie Curie!' } });

    await waitFor(() => expect(screen.queryByTestId('name-error')).not.toBeInTheDocument());
  });

  it('calls onCancel when the cancel button is clicked', async () => {
    const onSubmit = vi.fn();
    const onCancel = vi.fn();
    render(CollectorForm, { submitLabel: 'Save', onSubmit, onCancel });

    await fireEvent.click(screen.getByRole('button', { name: /cancel/i }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('pre-populates fields from initial values', () => {
    render(CollectorForm, {
      submitLabel: 'Save',
      onSubmit: vi.fn(),
      initial: { name: 'Existing', notes: 'Some notes' },
    });

    expect((screen.getByLabelText(/^name/i) as HTMLInputElement).value).toBe('Existing');
    expect((screen.getByLabelText(/notes/i) as HTMLTextAreaElement).value).toBe('Some notes');
  });
});
