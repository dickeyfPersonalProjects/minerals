import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import {
  clearAllToasts,
  dismissToast,
  toastError,
  toastInfo,
  toastSuccess,
  toastWarning,
  toasts,
} from './toasts';

beforeEach(() => {
  clearAllToasts();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  clearAllToasts();
});

describe('toast store', () => {
  it('toastSuccess pushes a success entry with the default 4s duration', () => {
    const id = toastSuccess('saved');
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]).toMatchObject({ id, type: 'success', message: 'saved', duration: 4000 });
  });

  it('toastError defaults to a longer 6s duration so the user has time to read', () => {
    toastError('boom');
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]?.type).toBe('error');
    expect(list[0]?.duration).toBe(6000);
  });

  it('toastInfo and toastWarning use the right defaults', () => {
    toastInfo('fyi');
    toastWarning('careful');
    const list = get(toasts);
    expect(list).toHaveLength(2);
    expect(list[0]).toMatchObject({ type: 'info', duration: 4000 });
    expect(list[1]).toMatchObject({ type: 'warning', duration: 6000 });
  });

  it('honors a caller-supplied duration', () => {
    toastSuccess('quick', 250);
    expect(get(toasts)[0]?.duration).toBe(250);
  });

  it('auto-dismisses after the duration elapses', () => {
    toastSuccess('saved', 1000);
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(999);
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(get(toasts)).toHaveLength(0);
  });

  it('generates unique ids for back-to-back toasts', () => {
    const a = toastSuccess('one');
    const b = toastSuccess('two');
    expect(a).not.toEqual(b);
    const ids = get(toasts).map((t) => t.id);
    expect(new Set(ids).size).toBe(2);
  });

  it('dismissToast removes the toast immediately and cancels its auto-dismiss timer', () => {
    const id = toastSuccess('saved', 5000);
    expect(get(toasts)).toHaveLength(1);
    dismissToast(id);
    expect(get(toasts)).toHaveLength(0);
    // Even after the original timeout would have fired, no second
    // dismiss should attempt to remove a now-gone toast.
    vi.advanceTimersByTime(10_000);
    expect(get(toasts)).toHaveLength(0);
  });

  it('dismissToast on an unknown id is a no-op', () => {
    toastSuccess('saved');
    dismissToast('does-not-exist');
    expect(get(toasts)).toHaveLength(1);
  });

  it('clearAllToasts empties the store and stops pending timers', () => {
    toastSuccess('a');
    toastError('b');
    toastInfo('c');
    expect(get(toasts)).toHaveLength(3);
    clearAllToasts();
    expect(get(toasts)).toHaveLength(0);
    vi.advanceTimersByTime(10_000);
    expect(get(toasts)).toHaveLength(0);
  });
});
