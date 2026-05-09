// Global toast notification store (E-4).
//
// Toasts are ephemeral, in-memory only; they do NOT persist across
// reloads or history (per the bead's constraints). Helpers push
// onto the store and schedule an auto-dismiss timer.
//
// Default durations: 4000ms for success/info, 6000ms for
// error/warning (errors deserve a longer read window).

import { writable, type Writable } from 'svelte/store';

export type ToastType = 'success' | 'error' | 'info' | 'warning';

export interface Toast {
  id: string;
  message: string;
  type: ToastType;
  duration: number;
}

export const DEFAULT_DURATION_MS: Record<ToastType, number> = {
  success: 4000,
  info: 4000,
  warning: 6000,
  error: 6000,
};

export const toasts: Writable<Toast[]> = writable([]);

let counter = 0;

function nextId(): string {
  counter += 1;
  // Counter + timestamp keeps ids unique even if a hot-reload
  // resets the module — old DOM ids never collide with new ones.
  return `toast-${Date.now().toString(36)}-${counter.toString(36)}`;
}

export function dismissToast(id: string): void {
  toasts.update((list) => list.filter((t) => t.id !== id));
}

function push(type: ToastType, message: string, durationMs?: number): string {
  const duration = durationMs ?? DEFAULT_DURATION_MS[type];
  const id = nextId();
  const toast: Toast = { id, message, type, duration };
  toasts.update((list) => [...list, toast]);
  // duration <= 0 disables auto-dismiss — useful for tests and for
  // any future caller that wants a sticky toast.
  if (duration > 0) {
    setTimeout(() => dismissToast(id), duration);
  }
  return id;
}

export function toastSuccess(message: string, durationMs?: number): string {
  return push('success', message, durationMs);
}

export function toastError(message: string, durationMs?: number): string {
  return push('error', message, durationMs);
}

export function toastInfo(message: string, durationMs?: number): string {
  return push('info', message, durationMs);
}

export function toastWarning(message: string, durationMs?: number): string {
  return push('warning', message, durationMs);
}

// Test-only helper: clear the store between cases. Not exported
// from a barrel; tests import directly.
export function _clearToasts(): void {
  toasts.set([]);
}
