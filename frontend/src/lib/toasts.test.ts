import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import {
  DEFAULT_DURATION_MS,
  _clearToasts,
  dismissToast,
  toastError,
  toastInfo,
  toastSuccess,
  toastWarning,
  toasts,
} from './toasts';

beforeEach(() => {
  _clearToasts();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  _clearToasts();
});

describe('toast helpers', () => {
  it('toastSuccess pushes a success toast with the default duration', () => {
    const id = toastSuccess('saved!');
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]?.id).toBe(id);
    expect(list[0]?.type).toBe('success');
    expect(list[0]?.message).toBe('saved!');
    expect(list[0]?.duration).toBe(DEFAULT_DURATION_MS.success);
  });

  it('toastError uses the longer error/warning default duration', () => {
    toastError('boom');
    const list = get(toasts);
    expect(list[0]?.type).toBe('error');
    expect(list[0]?.duration).toBe(DEFAULT_DURATION_MS.error);
    expect(DEFAULT_DURATION_MS.error).toBeGreaterThan(DEFAULT_DURATION_MS.success);
  });

  it('toastInfo and toastWarning create the right types', () => {
    toastInfo('fyi');
    toastWarning('careful');
    const list = get(toasts);
    expect(list.map((t) => t.type)).toEqual(['info', 'warning']);
  });

  it('assigns unique ids across many toasts', () => {
    const ids = new Set<string>();
    for (let i = 0; i < 50; i++) ids.add(toastSuccess(`m${i}`));
    expect(ids.size).toBe(50);
  });

  it('respects an explicit durationMs override', () => {
    toastInfo('quick', 100);
    expect(get(toasts)[0]?.duration).toBe(100);
  });
});

describe('auto-dismiss', () => {
  it('removes the toast from the store after its duration elapses', () => {
    toastSuccess('saved');
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(DEFAULT_DURATION_MS.success - 1);
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(get(toasts)).toHaveLength(0);
  });

  it('keeps the toast when duration is 0 (sticky)', () => {
    toastInfo('sticky', 0);
    vi.advanceTimersByTime(60_000);
    expect(get(toasts)).toHaveLength(1);
  });

  it('dismissToast removes a specific toast by id', () => {
    const a = toastInfo('a');
    const b = toastInfo('b');
    dismissToast(a);
    const remaining = get(toasts);
    expect(remaining).toHaveLength(1);
    expect(remaining[0]?.id).toBe(b);
  });
});
