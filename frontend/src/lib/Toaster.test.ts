import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import Toaster from './Toaster.svelte';
import { _clearToasts, toastError, toastInfo, toastSuccess, toasts } from './toasts';
import { get } from 'svelte/store';

beforeEach(() => {
  _clearToasts();
});

afterEach(() => {
  cleanup();
  _clearToasts();
});

describe('Toaster', () => {
  it('renders no toasts when the store is empty', () => {
    render(Toaster);
    expect(screen.queryAllByTestId('toast')).toHaveLength(0);
  });

  it('renders multiple toasts in store order with the right type and message', async () => {
    render(Toaster);
    // Push outside-the-test-render — same way real callers do.
    // Use sticky duration (0) so the auto-dismiss timer doesn't
    // race with the assertions.
    toastSuccess('one', 0);
    toastError('two', 0);
    toastInfo('three', 0);

    const items = await screen.findAllByTestId('toast');
    expect(items).toHaveLength(3);
    expect(items[0]?.dataset.toastType).toBe('success');
    expect(items[1]?.dataset.toastType).toBe('error');
    expect(items[2]?.dataset.toastType).toBe('info');

    const messages = screen.getAllByTestId('toast-message').map((n) => n.textContent?.trim());
    expect(messages).toEqual(['one', 'two', 'three']);
  });

  it('close button removes its toast from the store', async () => {
    render(Toaster);
    toastSuccess('close-me', 0);
    toastInfo('keep-me', 0);

    const closeButtons = await screen.findAllByTestId('toast-close');
    expect(closeButtons).toHaveLength(2);
    await fireEvent.click(closeButtons[0]!);

    const remaining = get(toasts);
    expect(remaining).toHaveLength(1);
    expect(remaining[0]?.message).toBe('keep-me');
  });

  it('errors and warnings render with role=alert; success/info with role=status', async () => {
    render(Toaster);
    toastError('err', 0);
    toastSuccess('ok', 0);

    const items = await screen.findAllByTestId('toast');
    const errEl = items.find((el) => el.dataset.toastType === 'error');
    const okEl = items.find((el) => el.dataset.toastType === 'success');
    expect(errEl?.getAttribute('role')).toBe('alert');
    expect(okEl?.getAttribute('role')).toBe('status');
  });
});
