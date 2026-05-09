import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, cleanup } from '@testing-library/svelte';

import SpecimenForm from './SpecimenForm.svelte';
import type { components } from './api/schema';
import { toCreateBody, toPatchBody, type SpecimenFormValues } from './schemas/specimen';

type SpecimenView = components['schemas']['SpecimenView'];

beforeEach(() => {
  // Form layout uses <details>; jsdom treats them inertly. Nothing
  // to do here — the inputs inside <details> are still queryable.
});

afterEach(() => {
  cleanup();
});

function makeSpecimen(overrides: Partial<SpecimenView> = {}): SpecimenView {
  return {
    id: '11111111-1111-1111-1111-111111111111',
    name: 'Smoky quartz',
    type: 'mineral',
    visibility: 'private',
    description: '',
    locality_text: null,
    locality: {},
    dimensions: {},
    type_data: {},
    catalog_number: null,
    acquired_at: null,
    acquired_from: null,
    author_id: '00000000-0000-0000-0000-000000000001',
    created_at: '2026-05-01T12:00:00Z',
    updated_at: '2026-05-01T12:00:00Z',
    mass_g: null,
    price_cents: null,
    source_notes: null,
    ...overrides,
  };
}

// Capture submit args via a closure so we don't have to fight
// `vi.fn`'s tuple typing on `mock.calls[]`.
function captureSubmit() {
  const captured: { values?: SpecimenFormValues } = {};
  const submit = vi.fn(async (values: SpecimenFormValues) => {
    captured.values = values;
    return { ok: true as const };
  });
  return { submit, captured };
}

describe('SpecimenForm — create mode', () => {
  it('renders mineral fields by default and submits a create body', async () => {
    const { submit, captured } = captureSubmit();

    render(SpecimenForm, { mode: 'create', submit, cancelHref: '/specimens' });

    // Defaults: type=mineral, visibility=private.
    expect(screen.getByTestId('type-radio-mineral')).toBeChecked();
    expect(screen.getByTestId('type-data-mineral')).toBeInTheDocument();

    await fireEvent.input(screen.getByTestId('field-name'), { target: { value: 'Calcite' } });
    await fireEvent.input(screen.getByTestId('field-mineral-formula'), {
      target: { value: 'CaCO3' },
    });
    await fireEvent.input(screen.getByTestId('field-mineral-hardness'), {
      target: { value: '3' },
    });

    await fireEvent.click(screen.getByTestId('form-submit'));

    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    const values = captured.values;
    if (!values) throw new Error('submit captured no values');
    expect(values.type).toBe('mineral');
    expect(values.name).toBe('Calcite');

    const body = toCreateBody(values);
    expect(body.type).toBe('mineral');
    expect(body.name).toBe('Calcite');
    expect(body.visibility).toBe('private');
    expect(body.type_data).toMatchObject({ chemical_formula: 'CaCO3', mohs_hardness: 3 });
  });

  it('switches type and resets type_data, then renders rock fields', async () => {
    const { submit, captured } = captureSubmit();

    render(SpecimenForm, { mode: 'create', submit, cancelHref: '/specimens' });

    // Fill a mineral field, then switch to rock — the mineral data
    // must not bleed into the submit body.
    await fireEvent.input(screen.getByTestId('field-mineral-formula'), {
      target: { value: 'SiO2' },
    });
    await fireEvent.click(screen.getByTestId('type-radio-rock'));
    await waitFor(() => expect(screen.getByTestId('type-data-rock')).toBeInTheDocument());

    await fireEvent.input(screen.getByTestId('field-name'), { target: { value: 'Granite' } });
    await fireEvent.change(screen.getByTestId('field-rock-type'), {
      target: { value: 'igneous' },
    });
    await fireEvent.click(screen.getByTestId('form-submit'));

    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    const values = captured.values;
    if (!values) throw new Error('submit captured no values');
    expect(values.type).toBe('rock');
    const body = toCreateBody(values);
    expect(body.type_data).toEqual({ rock_type: 'igneous' });
    expect(JSON.stringify(body)).not.toContain('SiO2');
  });

  it('blocks submit and shows an inline error when required name is empty', async () => {
    const submit = vi.fn(async () => ({ ok: true as const }));

    render(SpecimenForm, { mode: 'create', submit, cancelHref: '/specimens' });

    // The name field is left blank; submit should not invoke the
    // handler.
    await fireEvent.click(screen.getByTestId('form-submit'));

    // Felte marks fields as touched on submit attempt, so the
    // inline error should now render.
    await waitFor(() => expect(screen.getByTestId('error-name')).toBeInTheDocument());
    expect(submit).not.toHaveBeenCalled();
  });

  it('rejects mohs_hardness above 10 with the schema message', async () => {
    const submit = vi.fn(async () => ({ ok: true as const }));

    render(SpecimenForm, { mode: 'create', submit, cancelHref: '/specimens' });

    await fireEvent.input(screen.getByTestId('field-name'), { target: { value: 'Spinel' } });
    await fireEvent.input(screen.getByTestId('field-mineral-hardness'), {
      target: { value: '99' },
    });
    await fireEvent.click(screen.getByTestId('form-submit'));

    await waitFor(() =>
      expect(screen.getByTestId('error-mineral-hardness')).toHaveTextContent(/0–10/),
    );
    expect(submit).not.toHaveBeenCalled();
  });

  it('surfaces a 4xx error envelope as a banner and pins field errors via details.field', async () => {
    const submit = vi.fn(async () => ({
      ok: false as const,
      error: {
        code: 'specimen_catalog_number_conflict',
        message: 'catalog_number already in use',
        details: { field: 'catalog_number' },
      },
      status: 409,
    }));

    render(SpecimenForm, { mode: 'create', submit, cancelHref: '/specimens' });

    await fireEvent.input(screen.getByTestId('field-name'), { target: { value: 'Foo' } });
    await fireEvent.input(screen.getByTestId('field-catalog-number'), {
      target: { value: 'MIN-001' },
    });
    await fireEvent.click(screen.getByTestId('form-submit'));

    await waitFor(() =>
      expect(screen.getByTestId('form-error-banner')).toHaveTextContent(
        /catalog_number already in use/,
      ),
    );
    expect(screen.getByTestId('error-catalog-number')).toHaveTextContent(
      /catalog_number already in use/,
    );
  });
});

describe('SpecimenForm — edit mode', () => {
  it('hydrates from a SpecimenView, locks the type radio, and PATCHes only changed shape', async () => {
    const { submit, captured } = captureSubmit();

    const initial = makeSpecimen({
      type: 'meteorite',
      name: 'Allende',
      catalog_number: 'MET-001',
      visibility: 'public',
      mass_g: 12.5,
      type_data: {
        classification: 'CV3',
        fall_or_find: 'fall',
        official_name: 'Allende',
      },
    });

    render(SpecimenForm, { mode: 'edit', initial, submit, cancelHref: '/specimens/x' });

    // Type radios disabled, lock notice present.
    expect(screen.getByTestId('type-radio-meteorite')).toBeDisabled();
    expect(screen.getByTestId('type-locked')).toBeInTheDocument();

    // Hydrated values bound to the inputs.
    expect(screen.getByTestId('field-name')).toHaveValue('Allende');
    expect(screen.getByTestId('field-catalog-number')).toHaveValue('MET-001');
    expect(screen.getByTestId('field-mass')).toHaveValue(12.5);
    expect(screen.getByTestId('field-meteorite-class')).toHaveValue('CV3');

    // Edit only the name, then submit.
    await fireEvent.input(screen.getByTestId('field-name'), { target: { value: 'Allende v2' } });
    await fireEvent.click(screen.getByTestId('form-submit'));

    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
    const values = captured.values;
    if (!values) throw new Error('submit captured no values');
    expect(values.type).toBe('meteorite');
    expect(values.name).toBe('Allende v2');

    // PATCH body must NOT include `type` (immutable per CONTRACT
    // §10) and must preserve the hydrated type_data fields.
    const body = toPatchBody(values);
    expect(body).not.toHaveProperty('type');
    expect(body.name).toBe('Allende v2');
    expect(body.type_data).toMatchObject({ classification: 'CV3', fall_or_find: 'fall' });
  });

  it('disables the submit button and shows a pending indicator while submit is in flight', async () => {
    let release: () => void = () => {};
    const submit = vi.fn(
      () =>
        new Promise<{ ok: true }>((resolve) => {
          release = () => resolve({ ok: true });
        }),
    );

    const initial = makeSpecimen({ type: 'mineral', name: 'Spinel' });
    render(SpecimenForm, { mode: 'edit', initial, submit, cancelHref: '/specimens/x' });

    await fireEvent.click(screen.getByTestId('form-submit'));
    await waitFor(() => expect(screen.getByTestId('form-submitting')).toBeInTheDocument());
    expect(screen.getByTestId('form-submit')).toBeDisabled();

    // Release the in-flight submit so the test process doesn't
    // leave a dangling promise.
    release();
  });
});
