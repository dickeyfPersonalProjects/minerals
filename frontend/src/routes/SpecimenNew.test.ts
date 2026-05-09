import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';

const { mockPost, mockPush } = vi.hoisted(() => ({
  mockPost: vi.fn(),
  mockPush: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { POST: mockPost },
}));

vi.mock('svelte-spa-router', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('svelte-spa-router');
  return {
    ...actual,
    push: mockPush,
    link: () => ({ destroy() {} }),
  };
});

import SpecimenNew from './SpecimenNew.svelte';
import { clearAllToasts, toasts } from '../lib/stores/toasts';
import { get } from 'svelte/store';

beforeEach(() => {
  mockPost.mockReset();
  mockPush.mockReset();
  clearAllToasts();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  clearAllToasts();
});

describe('SpecimenNew route', () => {
  it('POSTs the create body and navigates to the new specimen detail page', async () => {
    mockPost.mockResolvedValue({
      data: { id: 'abc-123', name: 'Galena', type: 'mineral' },
      error: undefined,
      response: new Response(),
    });

    render(SpecimenNew);

    const name = screen.getByLabelText(/^name/i);
    await fireEvent.input(name, { target: { value: 'Galena' } });
    const catalog = screen.getByLabelText(/catalog number/i);
    await fireEvent.input(catalog, { target: { value: 'MIN-001' } });

    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    const args = mockPost.mock.calls[0]?.[1];
    expect(args.body.type).toBe('mineral');
    expect(args.body.name).toBe('Galena');
    expect(args.body.catalog_number).toBe('MIN-001');
    expect(args.body.visibility).toBe('private');

    expect(mockPush).toHaveBeenCalledWith('/specimens/abc-123');
  });

  it('shows duplicate-catalog-number error on 409', async () => {
    mockPost.mockResolvedValue({
      data: undefined,
      error: {
        error: {
          code: 'catalog_number_conflict',
          message: 'catalog number already in use',
          details: { field: 'catalog_number' },
        },
      },
      response: new Response(null, { status: 409 }),
    });

    render(SpecimenNew);

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Galena' } });
    await fireEvent.input(screen.getByLabelText(/catalog number/i), {
      target: { value: 'DUPE' },
    });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(screen.getByTestId('catalog-number-error')).toBeInTheDocument());
    expect(screen.getByTestId('catalog-number-error')).toHaveTextContent(/already exists/i);
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('toasts on a non-field 4xx instead of rendering a banner', async () => {
    mockPost.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'invalid', message: 'something is off' } },
      response: new Response(null, { status: 400 }),
    });

    render(SpecimenNew);

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Galena' } });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(get(toasts)).toHaveLength(1));
    const t = get(toasts)[0]!;
    expect(t.type).toBe('error');
    expect(t.message).toMatch(/something is off/);
    expect(screen.queryByTestId('form-error')).not.toBeInTheDocument();
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('toasts a success message after creating', async () => {
    mockPost.mockResolvedValue({
      data: { id: 'abc-123', name: 'Galena', type: 'mineral' },
      error: undefined,
      response: new Response(),
    });

    render(SpecimenNew);

    await fireEvent.input(screen.getByLabelText(/^name/i), { target: { value: 'Galena' } });
    await fireEvent.submit(screen.getByTestId('specimen-form'));

    await waitFor(() => expect(mockPush).toHaveBeenCalled());
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]!.type).toBe('success');
    expect(list[0]!.message).toMatch(/created/i);
  });
});
