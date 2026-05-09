import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import Toaster from './Toaster.svelte';
import {
  clearAllToasts,
  toastError,
  toastInfo,
  toastSuccess,
  toastWarning,
  toasts,
} from '../stores/toasts';
import { get } from 'svelte/store';

beforeEach(() => {
  clearAllToasts();
});

afterEach(() => {
  cleanup();
  clearAllToasts();
});

describe('Toaster', () => {
  it('starts with no rendered toasts', () => {
    render(Toaster);
    expect(screen.queryAllByTestId('toast')).toHaveLength(0);
  });

  it('renders one card per entry in the store', async () => {
    render(Toaster);
    toastSuccess('saved');
    toastError('boom');
    toastInfo('fyi');
    toastWarning('careful');

    const cards = await screen.findAllByTestId('toast');
    expect(cards).toHaveLength(4);
    const types = cards.map((c) => c.getAttribute('data-toast-type'));
    expect(types).toEqual(['success', 'error', 'info', 'warning']);
  });

  it('marks error and warning toasts with role=alert and others with role=status', async () => {
    render(Toaster);
    toastSuccess('ok');
    toastError('boom');
    toastWarning('careful');
    toastInfo('fyi');

    const cards = await screen.findAllByTestId('toast');
    const roles = cards.map((c) => c.getAttribute('role'));
    expect(roles).toEqual(['status', 'alert', 'alert', 'status']);
  });

  it('clicking the close button removes that specific toast', async () => {
    render(Toaster);
    const idA = toastSuccess('first');
    const idB = toastError('second');
    expect(get(toasts)).toHaveLength(2);

    const cards = await screen.findAllByTestId('toast');
    const second = cards.find((c) => c.getAttribute('data-toast-id') === idB);
    expect(second).toBeDefined();
    const closeButton = second!.querySelector('[data-testid="toast-close"]') as HTMLButtonElement;
    await fireEvent.click(closeButton);

    const remaining = get(toasts);
    expect(remaining).toHaveLength(1);
    expect(remaining[0]?.id).toBe(idA);
  });
});
