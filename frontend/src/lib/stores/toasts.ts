// Global toast store for the SPA (E-4).
//
// Toasts surface success/error/info/warning feedback consistently
// across forms and async operations. They are ephemeral — there
// is no persistence across reloads.

import { writable, type Writable } from 'svelte/store';

export type ToastType = 'success' | 'error' | 'info' | 'warning';

export interface Toast {
  id: string;
  message: string;
  type: ToastType;
  duration: number;
}

const DEFAULT_SUCCESS_INFO_MS = 4000;
const DEFAULT_ERROR_WARNING_MS = 6000;

export const toasts: Writable<Toast[]> = writable([]);

const timers = new Map<string, ReturnType<typeof setTimeout>>();

function genId(): string {
  // Random alphanumeric id — uniqueness within a session is the
  // bar; ULID would be overkill for ephemeral UI state.
  return `t_${Math.random().toString(36).slice(2, 11)}_${Date.now().toString(36)}`;
}

function push(type: ToastType, message: string, duration: number): string {
  const id = genId();
  const toast: Toast = { id, type, message, duration };
  toasts.update((list) => [...list, toast]);
  if (duration > 0) {
    const handle = setTimeout(() => dismissToast(id), duration);
    timers.set(id, handle);
  }
  return id;
}

export function dismissToast(id: string): void {
  const handle = timers.get(id);
  if (handle) {
    clearTimeout(handle);
    timers.delete(id);
  }
  toasts.update((list) => list.filter((t) => t.id !== id));
}

export function clearAllToasts(): void {
  for (const handle of timers.values()) clearTimeout(handle);
  timers.clear();
  toasts.set([]);
}

export function toastSuccess(message: string, durationMs?: number): string {
  return push('success', message, durationMs ?? DEFAULT_SUCCESS_INFO_MS);
}

export function toastError(message: string, durationMs?: number): string {
  return push('error', message, durationMs ?? DEFAULT_ERROR_WARNING_MS);
}

export function toastInfo(message: string, durationMs?: number): string {
  return push('info', message, durationMs ?? DEFAULT_SUCCESS_INFO_MS);
}

export function toastWarning(message: string, durationMs?: number): string {
  return push('warning', message, durationMs ?? DEFAULT_ERROR_WARNING_MS);
}
