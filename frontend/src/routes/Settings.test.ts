import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { toasts, _clearToasts } from '../lib/toasts';

const { mockGET, mockPATCH, mockDELETE } = vi.hoisted(() => ({
  mockGET: vi.fn(),
  mockPATCH: vi.fn(),
  mockDELETE: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGET, PATCH: mockPATCH, DELETE: mockDELETE },
}));

import Settings from './Settings.svelte';

function profileBody(
  field_defaults: Record<string, string> | null,
  default_specimen_visibility: string | null = null,
) {
  return {
    id: 'user-1',
    email: 'a@b.test',
    display_name: 'Ada',
    pending: false,
    field_defaults: field_defaults ?? {},
    default_specimen_visibility,
  };
}

beforeEach(() => {
  mockGET.mockReset();
  mockPATCH.mockReset();
  mockDELETE.mockReset();
  _clearToasts();
});

afterEach(() => {
  cleanup();
});

describe('Settings route — field defaults', () => {
  it('renders all five dropdowns showing "System default" when the API returns no defaults', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Settings);

    const price = (await screen.findByTestId('settings-default-price')) as HTMLSelectElement;
    const acqAt = screen.getByTestId('settings-default-acquired_at') as HTMLSelectElement;
    const acqFrom = screen.getByTestId('settings-default-acquired_from') as HTMLSelectElement;
    const cat = screen.getByTestId('settings-default-catalog_number') as HTMLSelectElement;
    const img = screen.getByTestId('settings-default-images') as HTMLSelectElement;

    for (const sel of [price, acqAt, acqFrom, cat, img]) {
      expect(sel.value).toBe('__unset__');
      expect(sel.selectedOptions[0]?.textContent?.trim()).toBe('System default (owner-only)');
    }
  });

  it('renders rows in vertical layout (one row per field) in the documented order', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Settings);

    const list = await screen.findByTestId('settings-field-defaults-list');
    const rows = Array.from(
      list.querySelectorAll('[data-testid^="settings-field-defaults-row-"]'),
    ) as HTMLElement[];
    expect(rows.map((r) => r.dataset.testid)).toEqual([
      'settings-field-defaults-row-price',
      'settings-field-defaults-row-acquired_at',
      'settings-field-defaults-row-acquired_from',
      'settings-field-defaults-row-catalog_number',
      'settings-field-defaults-row-images',
    ]);
  });

  it('shows accurate helper text describing the all-specimens scope (mi-z3d0)', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Settings);

    // Wait for the form to render. The lede is the explanatory copy
    // immediately below the legend.
    await screen.findByTestId('settings-field-defaults-form');
    const body = document.body.textContent ?? '';
    expect(body).toContain('apply to all your specimens');
    expect(body).toContain("doesn't have its own per-field setting");
    // The old misleading copy must be gone — defaults DO affect
    // existing specimens (those without per-field overrides), so the
    // "never make existing data more visible" line was wrong.
    expect(body).not.toContain('never make existing data more visible');
    expect(body).not.toContain('apply to new specimens you create');
  });

  it('reflects values from the API when defaults are populated', async () => {
    mockGET.mockResolvedValue({
      data: profileBody({
        price: 'public',
        acquired_at: 'private',
        acquired_from: 'unlisted',
        catalog_number: 'public',
        images: 'private',
      }),
      error: undefined,
    });
    render(Settings);

    const price = (await screen.findByTestId('settings-default-price')) as HTMLSelectElement;
    expect(price.value).toBe('public');
    expect((screen.getByTestId('settings-default-acquired_at') as HTMLSelectElement).value).toBe(
      'private',
    );
    expect((screen.getByTestId('settings-default-acquired_from') as HTMLSelectElement).value).toBe(
      'unlisted',
    );
    expect((screen.getByTestId('settings-default-catalog_number') as HTMLSelectElement).value).toBe(
      'public',
    );
    expect((screen.getByTestId('settings-default-images') as HTMLSelectElement).value).toBe(
      'private',
    );
  });

  it('round-trips a new acquired_at default via PATCH (mi-z3d0 acceptance)', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Settings);

    const acqAt = (await screen.findByTestId('settings-default-acquired_at')) as HTMLSelectElement;
    await fireEvent.change(acqAt, { target: { value: 'private' } });

    const save = screen.getByTestId('settings-field-defaults-save') as HTMLButtonElement;
    await waitFor(() => expect(save.disabled).toBe(false));

    mockPATCH.mockResolvedValue({
      data: profileBody({ acquired_at: 'private' }),
      error: undefined,
    });
    await fireEvent.click(save);

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [
      string,
      { body: { field_defaults: Record<string, unknown> } },
    ];
    expect(opts.body).toEqual({ field_defaults: { acquired_at: 'private' } });
  });

  it('PATCHes only the catalog_number key when only that one changed', async () => {
    mockGET.mockResolvedValue({
      data: profileBody({ price: 'public' }),
      error: undefined,
    });
    render(Settings);

    const cat = (await screen.findByTestId('settings-default-catalog_number')) as HTMLSelectElement;
    await fireEvent.change(cat, { target: { value: 'private' } });

    mockPATCH.mockResolvedValue({
      data: profileBody({ price: 'public', catalog_number: 'private' }),
      error: undefined,
    });
    await fireEvent.click(screen.getByTestId('settings-field-defaults-save'));

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [
      string,
      { body: { field_defaults: Record<string, unknown> } },
    ];
    expect(opts.body).toEqual({ field_defaults: { catalog_number: 'private' } });
  });

  it('disables Save until a value changes, then PATCHes only the changed keys', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Settings);

    const save = (await screen.findByTestId('settings-field-defaults-save')) as HTMLButtonElement;
    expect(save.disabled).toBe(true);

    const price = screen.getByTestId('settings-default-price') as HTMLSelectElement;
    await fireEvent.change(price, { target: { value: 'public' } });
    await waitFor(() => expect(save.disabled).toBe(false));

    mockPATCH.mockResolvedValue({
      data: profileBody({ price: 'public' }),
      error: undefined,
    });
    await fireEvent.click(save);

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [path, opts] = mockPATCH.mock.calls[0] as [
      string,
      { body: { field_defaults: Record<string, unknown> } },
    ];
    expect(path).toBe('/api/v1/profile');
    expect(opts.body).toEqual({ field_defaults: { price: 'public' } });

    const list = get(toasts);
    expect(list[0]?.type).toBe('success');
    // After save, Save disables again (no dirty diff vs the new server state).
    await waitFor(() => expect(save.disabled).toBe(true));
  });

  it('sends explicit null for a key cleared back to System default', async () => {
    mockGET.mockResolvedValue({
      data: profileBody({ price: 'public', acquired_from: 'unlisted' }),
      error: undefined,
    });
    render(Settings);

    const price = (await screen.findByTestId('settings-default-price')) as HTMLSelectElement;
    expect(price.value).toBe('public');
    await fireEvent.change(price, { target: { value: '__unset__' } });

    mockPATCH.mockResolvedValue({
      data: profileBody({ acquired_from: 'unlisted' }),
      error: undefined,
    });
    await fireEvent.click(screen.getByTestId('settings-field-defaults-save'));

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [
      string,
      { body: { field_defaults: Record<string, unknown> } },
    ];
    expect(opts.body).toEqual({ field_defaults: { price: null } });
  });

  it('renders inline load-error text when GET fails', async () => {
    mockGET.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'internal_error', message: 'boom' } },
    });
    render(Settings);
    await waitFor(() =>
      expect(screen.getByTestId('settings-field-defaults-error')).toHaveTextContent('boom'),
    );
  });

  it('does not PATCH on submit when nothing is dirty', async () => {
    mockGET.mockResolvedValue({
      data: profileBody({ price: 'public' }),
      error: undefined,
    });
    render(Settings);

    const save = (await screen.findByTestId('settings-field-defaults-save')) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    // Click is a no-op while disabled, but submitting the form
    // directly is defended by the dirty-guard too.
    await fireEvent.submit(screen.getByTestId('settings-field-defaults-form'));
    expect(mockPATCH).not.toHaveBeenCalled();
  });
});

describe('Settings route — new specimens default visibility (mi-q2d8)', () => {
  it('shows "System default (private)" when the API returns no preference', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null, null), error: undefined });
    render(Settings);

    const sel = (await screen.findByTestId(
      'settings-default-specimen-visibility',
    )) as HTMLSelectElement;
    expect(sel.value).toBe('__unset__');
    expect(sel.selectedOptions[0]?.textContent?.trim()).toBe('System default (private)');
  });

  it('reflects the stored default_specimen_visibility from the API', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null, 'public'), error: undefined });
    render(Settings);

    const sel = (await screen.findByTestId(
      'settings-default-specimen-visibility',
    )) as HTMLSelectElement;
    expect(sel.value).toBe('public');
  });

  it('PATCHes default_specimen_visibility (only that key) when it changes', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null, null), error: undefined });
    render(Settings);

    const sel = (await screen.findByTestId(
      'settings-default-specimen-visibility',
    )) as HTMLSelectElement;
    await fireEvent.change(sel, { target: { value: 'public' } });

    mockPATCH.mockResolvedValue({ data: profileBody(null, 'public'), error: undefined });
    await fireEvent.click(screen.getByTestId('settings-field-defaults-save'));

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [string, { body: Record<string, unknown> }];
    expect(opts.body).toEqual({ default_specimen_visibility: 'public' });
  });

  it('sends explicit null when cleared back to System default', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null, 'public'), error: undefined });
    render(Settings);

    const sel = (await screen.findByTestId(
      'settings-default-specimen-visibility',
    )) as HTMLSelectElement;
    expect(sel.value).toBe('public');
    await fireEvent.change(sel, { target: { value: '__unset__' } });

    mockPATCH.mockResolvedValue({ data: profileBody(null, null), error: undefined });
    await fireEvent.click(screen.getByTestId('settings-field-defaults-save'));

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [string, { body: Record<string, unknown> }];
    expect(opts.body).toEqual({ default_specimen_visibility: null });
  });

  it('sends both field_defaults and default_specimen_visibility when both change', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null, null), error: undefined });
    render(Settings);

    const price = (await screen.findByTestId('settings-default-price')) as HTMLSelectElement;
    await fireEvent.change(price, { target: { value: 'unlisted' } });
    const sel = screen.getByTestId('settings-default-specimen-visibility') as HTMLSelectElement;
    await fireEvent.change(sel, { target: { value: 'public' } });

    mockPATCH.mockResolvedValue({
      data: profileBody({ price: 'unlisted' }, 'public'),
      error: undefined,
    });
    await fireEvent.click(screen.getByTestId('settings-field-defaults-save'));

    await waitFor(() => expect(mockPATCH).toHaveBeenCalled());
    const [, opts] = mockPATCH.mock.calls[0] as [string, { body: Record<string, unknown> }];
    expect(opts.body).toEqual({
      field_defaults: { price: 'unlisted' },
      default_specimen_visibility: 'public',
    });
  });
});

describe('Settings route — delete account (GDPR erasure, mi-nwg5)', () => {
  it('disables the delete button until the confirmation phrase is typed exactly', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    render(Settings);

    const btn = (await screen.findByTestId('settings-delete-account')) as HTMLButtonElement;
    const input = screen.getByTestId('settings-delete-confirm') as HTMLInputElement;
    expect(btn.disabled).toBe(true);

    await fireEvent.input(input, { target: { value: 'delete' } });
    expect(btn.disabled).toBe(true); // case-sensitive

    await fireEvent.input(input, { target: { value: 'DELETE' } });
    await waitFor(() => expect(btn.disabled).toBe(false));
  });

  it('DELETEs /api/v1/account with the confirm phrase and navigates home on success', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    const { assign, restore } = mockLocationAssign();
    try {
      render(Settings);

      const input = (await screen.findByTestId('settings-delete-confirm')) as HTMLInputElement;
      await fireEvent.input(input, { target: { value: 'DELETE' } });

      mockDELETE.mockResolvedValue({ data: undefined, response: { ok: true }, error: undefined });
      await fireEvent.click(screen.getByTestId('settings-delete-account'));

      await waitFor(() => expect(mockDELETE).toHaveBeenCalled());
      const [path, opts] = mockDELETE.mock.calls[0] as [string, { body: { confirm: string } }];
      expect(path).toBe('/api/v1/account');
      expect(opts.body).toEqual({ confirm: 'DELETE' });
      await waitFor(() => expect(assign).toHaveBeenCalledWith('/'));
    } finally {
      restore();
    }
  });

  it('does not navigate when the DELETE fails', async () => {
    mockGET.mockResolvedValue({ data: profileBody(null), error: undefined });
    const { assign, restore } = mockLocationAssign();
    try {
      render(Settings);

      const input = (await screen.findByTestId('settings-delete-confirm')) as HTMLInputElement;
      await fireEvent.input(input, { target: { value: 'DELETE' } });

      mockDELETE.mockResolvedValue({
        data: undefined,
        response: { ok: false },
        error: { error: { code: 'internal_error', message: 'boom' } },
      });
      await fireEvent.click(screen.getByTestId('settings-delete-account'));

      await waitFor(() => expect(mockDELETE).toHaveBeenCalled());
      expect(assign).not.toHaveBeenCalled();
      // Button re-enabled so the user can retry.
      const btn = screen.getByTestId('settings-delete-account') as HTMLButtonElement;
      await waitFor(() => expect(btn.disabled).toBe(false));
    } finally {
      restore();
    }
  });
});

// mockLocationAssign swaps window.location for a stub exposing a mock
// `assign`. jsdom's window.location.assign is non-configurable, so a
// vi.spyOn fails; redefining the whole property is the supported route.
function mockLocationAssign(): { assign: ReturnType<typeof vi.fn>; restore: () => void } {
  const original = window.location;
  const assign = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...original, assign },
  });
  return {
    assign,
    restore: () => {
      Object.defineProperty(window, 'location', { configurable: true, value: original });
    },
  };
}
