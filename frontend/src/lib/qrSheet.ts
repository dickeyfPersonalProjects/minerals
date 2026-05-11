// QR sheet client-side store + mutator helpers (mi-c78.4).
//
// The app has at most one active QR sticker sheet per user. This
// module owns the in-memory cache of that sheet so the navbar,
// specimen cards, and the /specimens/qr route stay in sync without
// each one re-fetching after every mutation.
//
// State machine:
//   unknown → refreshQrSheet() → none | loaded
//   loaded  → mutators (add/remove/patch) → loaded
//   loaded  → deleteSheet()                → none
//   none    → createSheet()                → loaded
//
// All mutators go through the shared API client, so the global
// toast middleware (lib/api/wrapper) surfaces HTTP errors. Callers
// receive the resulting SheetView (or null on failure) and can
// decide how to react — typical use is "ignore null, the toast
// already explained the failure".

import { writable, type Readable, type Writable } from 'svelte/store';
import { client } from './api';
import { SUPPRESS_TOAST_HEADERS } from './api/wrapper';
import type { components } from './api/schema';

export type QRSheetView = components['schemas']['QRSheetView'];
export type QRSheetSpecimenView = components['schemas']['QRSheetSpecimenView'];

export type QRSheetState =
  | { status: 'unknown' }
  | { status: 'none' }
  | { status: 'loaded'; sheet: QRSheetView };

const internal: Writable<QRSheetState> = writable({ status: 'unknown' });

export const qrSheetState: Readable<QRSheetState> = { subscribe: internal.subscribe };

export function setSheet(sheet: QRSheetView): void {
  internal.set({ status: 'loaded', sheet });
}

export function setNoSheet(): void {
  internal.set({ status: 'none' });
}

// Synchronous one-shot read. `store.subscribe(cb)()` returns the
// unsubscribe — invoking it inline gives the current value
// without leaving a dangling listener.
function snapshot(): QRSheetState {
  let s: QRSheetState = { status: 'unknown' };
  internal.subscribe((v) => (s = v))();
  return s;
}

export function getQrSheetState(): QRSheetState {
  return snapshot();
}

export async function refreshQrSheet(): Promise<QRSheetState> {
  const { data, response } = await client.GET('/api/v1/qr-sheet', {
    headers: SUPPRESS_TOAST_HEADERS,
  });
  if (response.status === 404 || !data) {
    setNoSheet();
    return snapshot();
  }
  setSheet(data);
  return snapshot();
}

export async function createSheet(template: string): Promise<QRSheetView | null> {
  const { data, error } = await client.POST('/api/v1/qr-sheet', {
    body: { template },
  });
  if (error || !data) return null;
  setSheet(data);
  return data;
}

export async function patchSheetTemplate(template: string): Promise<QRSheetView | null> {
  const { data, error } = await client.PATCH('/api/v1/qr-sheet', {
    body: { template },
  });
  if (error || !data) return null;
  setSheet(data);
  return data;
}

export async function deleteSheet(): Promise<boolean> {
  const { error } = await client.DELETE('/api/v1/qr-sheet', {});
  if (error) return false;
  setNoSheet();
  return true;
}

export async function addSpecimenToSheet(specimenId: string): Promise<QRSheetView | null> {
  const { data, error } = await client.POST('/api/v1/qr-sheet/specimens', {
    body: { specimen_id: specimenId },
  });
  if (error || !data) return null;
  setSheet(data);
  return data;
}

export async function removeSpecimenFromSheet(specimenId: string): Promise<boolean> {
  const { error } = await client.DELETE('/api/v1/qr-sheet/specimens/{specimen_id}', {
    params: { path: { specimen_id: specimenId } },
  });
  if (error) return false;
  // Drop the row from cached state so listeners (cards, sheet
  // page) react without a follow-up GET. The server's view is
  // re-fetched whenever a higher-level refresh runs.
  const cur = snapshot();
  if (cur.status === 'loaded') {
    const filtered = (cur.sheet.specimens ?? []).filter((s) => s.specimen_id !== specimenId);
    setSheet({ ...cur.sheet, specimens: filtered });
  }
  return true;
}

export function specimenOnSheet(state: QRSheetState, specimenId: string): boolean {
  if (state.status !== 'loaded') return false;
  return (state.sheet.specimens ?? []).some((s) => s.specimen_id === specimenId);
}

// Test-only — drop the cache between cases. Not exported through
// any public barrel.
export function __resetQrSheetStore(): void {
  internal.set({ status: 'unknown' });
}
