import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import ConfirmModal from './ConfirmModal.svelte';

afterEach(cleanup);

function renderWith(
  overrides: Partial<{
    title: string;
    message: string;
    confirmLabel: string;
    cancelLabel: string;
    destructive: boolean;
    busy: boolean;
  }> = {},
) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  const utils = render(ConfirmModal, {
    title: overrides.title ?? 'Delete?',
    message: overrides.message ?? 'Are you sure?',
    confirmLabel: overrides.confirmLabel ?? 'Delete',
    cancelLabel: overrides.cancelLabel ?? 'Cancel',
    destructive: overrides.destructive ?? true,
    busy: overrides.busy ?? false,
    onConfirm,
    onCancel,
  });
  return { onConfirm, onCancel, ...utils };
}

describe('ConfirmModal', () => {
  it('renders the title, message, and labels', () => {
    renderWith({
      title: 'Delete photo?',
      message: 'No undo.',
      confirmLabel: 'Yep',
      cancelLabel: 'Nope',
    });
    expect(screen.getByTestId('confirm-modal-title')).toHaveTextContent('Delete photo?');
    expect(screen.getByTestId('confirm-modal-message')).toHaveTextContent('No undo.');
    expect(screen.getByTestId('confirm-modal-confirm')).toHaveTextContent('Yep');
    expect(screen.getByTestId('confirm-modal-cancel')).toHaveTextContent('Nope');
  });

  it('focuses Cancel by default so Enter does not trigger Delete', async () => {
    renderWith();
    // Wait a tick for onMount → tick().then(focus) to settle.
    await Promise.resolve();
    await Promise.resolve();
    expect(document.activeElement).toBe(screen.getByTestId('confirm-modal-cancel'));
  });

  it('clicking Confirm fires onConfirm', async () => {
    const { onConfirm } = renderWith();
    await fireEvent.click(screen.getByTestId('confirm-modal-confirm'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('clicking Cancel fires onCancel', async () => {
    const { onCancel } = renderWith();
    await fireEvent.click(screen.getByTestId('confirm-modal-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('Escape fires onCancel', async () => {
    const { onCancel, onConfirm } = renderWith();
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('clicking the backdrop fires onCancel', async () => {
    const { onCancel } = renderWith();
    await fireEvent.click(screen.getByTestId('confirm-modal-backdrop'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('disables both buttons and ignores Escape when busy', async () => {
    const { onCancel, onConfirm } = renderWith({ busy: true });
    const confirm = screen.getByTestId('confirm-modal-confirm') as HTMLButtonElement;
    const cancel = screen.getByTestId('confirm-modal-cancel') as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    expect(cancel.disabled).toBe(true);
    expect(confirm).toHaveTextContent(/working/i);

    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onCancel).not.toHaveBeenCalled();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('Tab on the last focusable wraps back to the first (focus trap)', async () => {
    renderWith();
    await Promise.resolve();
    await Promise.resolve();
    const cancel = screen.getByTestId('confirm-modal-cancel');
    const confirm = screen.getByTestId('confirm-modal-confirm');
    confirm.focus();
    expect(document.activeElement).toBe(confirm);
    await fireEvent.keyDown(window, { key: 'Tab' });
    expect(document.activeElement).toBe(cancel);
  });

  it('Shift+Tab on the first focusable wraps to the last', async () => {
    renderWith();
    await Promise.resolve();
    await Promise.resolve();
    const cancel = screen.getByTestId('confirm-modal-cancel');
    const confirm = screen.getByTestId('confirm-modal-confirm');
    cancel.focus();
    await fireEvent.keyDown(window, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(confirm);
  });
});
