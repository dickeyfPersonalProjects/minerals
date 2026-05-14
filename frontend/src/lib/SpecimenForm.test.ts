import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { axe } from 'vitest-axe';

const { mockGet } = vi.hoisted(() => ({ mockGet: vi.fn() }));
vi.mock('./api/index', () => ({
  client: { GET: mockGet },
}));
vi.mock('./api/wrapper', () => ({
  SUPPRESS_TOAST_HEADERS: { 'x-suppress-toast': '1' },
}));

import SpecimenForm from './SpecimenForm.svelte';
import type { SpecimenFormSubmitResult } from './SpecimenForm.svelte';
import { emptyFormValues, type SpecimenFormValues } from './schemas/specimen';

function fillRequired(values: Partial<SpecimenFormValues> = {}): SpecimenFormValues {
  return { ...emptyFormValues('mineral'), name: 'Quartz', ...values };
}

beforeEach(() => {
  vi.restoreAllMocks();
  mockGet.mockReset();
});

afterEach(() => {
  cleanup();
});

describe('SpecimenForm', () => {
  describe('renders type-specific fields', () => {
    it('mineral type shows mineral fields, hides rock/meteorite fields', () => {
      const onSubmit = vi.fn();
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: { type: 'mineral' },
      });

      expect(screen.getByTestId('mineral-fields')).toBeInTheDocument();
      expect(screen.queryByTestId('rock-fields')).not.toBeInTheDocument();
      expect(screen.queryByTestId('meteorite-fields')).not.toBeInTheDocument();
      expect(screen.getByLabelText(/chemical formula/i)).toBeInTheDocument();
    });

    it('rock type shows rock fields', () => {
      const onSubmit = vi.fn();
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: { type: 'rock' },
      });

      expect(screen.getByTestId('rock-fields')).toBeInTheDocument();
      expect(screen.queryByTestId('mineral-fields')).not.toBeInTheDocument();
      expect(screen.getByLabelText(/rock type/i)).toBeInTheDocument();
    });

    it('meteorite type shows meteorite fields', () => {
      const onSubmit = vi.fn();
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: { type: 'meteorite' },
      });

      expect(screen.getByTestId('meteorite-fields')).toBeInTheDocument();
      expect(screen.queryByTestId('mineral-fields')).not.toBeInTheDocument();
      expect(screen.getByLabelText(/^classification/i)).toBeInTheDocument();
    });

    it('toggling type radio swaps the type-data fieldset (create mode)', async () => {
      const onSubmit = vi.fn();
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: { type: 'mineral' },
      });

      expect(screen.getByTestId('mineral-fields')).toBeInTheDocument();

      const rockRadio = screen.getByRole('radio', { name: /rock/i });
      await fireEvent.click(rockRadio);

      await waitFor(() => expect(screen.getByTestId('rock-fields')).toBeInTheDocument());
      expect(screen.queryByTestId('mineral-fields')).not.toBeInTheDocument();
    });
  });

  describe('zod validation', () => {
    it('shows required-name error when submitted empty', async () => {
      const onSubmit = vi.fn();
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
      });

      await fireEvent.submit(screen.getByTestId('specimen-form'));

      await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());
      expect(screen.getByTestId('name-error')).toHaveTextContent(/required/i);
      expect(onSubmit).not.toHaveBeenCalled();
    });

    it('rejects mohs hardness above 10', async () => {
      const onSubmit = vi.fn();
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: { type: 'mineral' },
      });

      const name = screen.getByLabelText(/^name/i);
      await fireEvent.input(name, { target: { value: 'Diamond' } });
      const hardness = screen.getByLabelText(/hardness/i);
      await fireEvent.input(hardness, { target: { value: '99' } });
      await fireEvent.blur(hardness);

      await fireEvent.submit(screen.getByTestId('specimen-form'));

      await waitFor(() =>
        expect(screen.getByText(/Mohs hardness must be 0–10/i)).toBeInTheDocument(),
      );
      expect(onSubmit).not.toHaveBeenCalled();
    });
  });

  describe('submit handler', () => {
    it('submits with the typed values when valid', async () => {
      const onSubmit = vi.fn(async (): Promise<SpecimenFormSubmitResult> => ({ kind: 'ok' }));
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: { type: 'mineral' },
      });

      const name = screen.getByLabelText(/^name/i);
      await fireEvent.input(name, { target: { value: 'Galena' } });
      await fireEvent.submit(screen.getByTestId('specimen-form'));

      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
      const firstCall = onSubmit.mock.calls[0];
      expect(firstCall).toBeDefined();
      const passed = (firstCall as unknown[])[0] as SpecimenFormValues;
      expect(passed.name).toBe('Galena');
      expect(passed.type).toBe('mineral');
    });

    it('shows the catalog-number error on duplicate result', async () => {
      const onSubmit = vi.fn(
        async (): Promise<SpecimenFormSubmitResult> => ({
          kind: 'duplicate_catalog_number',
        }),
      );
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: fillRequired(),
      });

      await fireEvent.submit(screen.getByTestId('specimen-form'));

      await waitFor(() => expect(screen.getByTestId('catalog-number-error')).toBeInTheDocument());
      expect(screen.getByTestId('catalog-number-error')).toHaveTextContent(/already exists/i);
    });

    it('shows the banner error on generic API failure', async () => {
      const onSubmit = vi.fn(
        async (): Promise<SpecimenFormSubmitResult> => ({
          kind: 'error',
          message: 'Boom',
        }),
      );
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: fillRequired(),
      });

      await fireEvent.submit(screen.getByTestId('specimen-form'));

      await waitFor(() => expect(screen.getByTestId('form-error')).toBeInTheDocument());
      expect(screen.getByTestId('form-error')).toHaveTextContent(/Boom/);
    });

    it('shows a field-scoped error when API returns details.field=name', async () => {
      const onSubmit = vi.fn(
        async (): Promise<SpecimenFormSubmitResult> => ({
          kind: 'field_error',
          field: 'name',
          message: 'name is bad',
        }),
      );
      render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit,
        initial: fillRequired(),
      });

      await fireEvent.submit(screen.getByTestId('specimen-form'));

      await waitFor(() => expect(screen.getByTestId('name-error')).toBeInTheDocument());
      expect(screen.getByTestId('name-error')).toHaveTextContent(/name is bad/);
    });
  });

  describe('edit mode', () => {
    it('disables the type radios', () => {
      render(SpecimenForm, {
        mode: 'edit',
        submitLabel: 'Save',
        onSubmit: vi.fn(),
        initial: fillRequired({ type: 'rock' }),
      });

      const fieldset = screen.getByTestId('type-fieldset') as HTMLFieldSetElement;
      expect(fieldset.disabled).toBe(true);
      expect(screen.getByTestId('type-immutable-hint')).toBeInTheDocument();
    });
  });

  describe('mindat lookup', () => {
    it('Lookup button populates the visible mineral form inputs with fetched data', async () => {
      // Regression for mi-xly: the previous autocomplete updated felte's
      // store via setData but the rendered <input> values stayed empty,
      // so users thought the lookup did nothing. Switching to setFields
      // pushes the new values into the DOM as well.
      mockGet.mockResolvedValue({
        data: {
          items: [
            {
              id: 'aaaaaaaa-0000-0000-0000-000000000001',
              name: 'Quartz',
              source: 'mindat',
              mindat_id: '1234',
              attribution: 'data via Mindat (CC-BY-NC-SA 4.0)',
              author_id: '00000000-0000-0000-0000-000000000001',
              created_at: '2026-05-01T12:00:00Z',
              updated_at: '2026-05-01T12:00:00Z',
              data: {
                chemical_formula: 'SiO2',
                crystal_system: 'trigonal',
                mohs_hardness: 7,
                color: 'colorless',
                luster: 'vitreous',
                mindat_id: '1234',
                mineral_species: ['Quartz'],
              },
            },
          ],
        },
        error: null,
      });

      render(SpecimenForm, {
        mode: 'edit',
        submitLabel: 'Save',
        onSubmit: vi.fn(),
        initial: { type: 'mineral', name: 'Quartz' },
      });

      const lookupInput = screen.getByTestId('mineral-species-lookup-input') as HTMLInputElement;
      await fireEvent.input(lookupInput, { target: { value: 'quartz' } });
      await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

      const formula = document.getElementById('specimen-m-chemical-formula') as HTMLInputElement;
      const mindatId = document.getElementById('specimen-m-mindat-id') as HTMLInputElement;
      const crystal = document.getElementById('specimen-m-crystal-system') as HTMLInputElement;
      const hardness = document.getElementById('specimen-m-mohs-hardness') as HTMLInputElement;

      await waitFor(() => expect(formula.value).toBe('SiO2'));
      expect(crystal.value).toBe('trigonal');
      expect(mindatId.value).toBe('1234');
      expect(hardness.value).toBe('7');

      // Attribution from Mindat is rendered (CC-BY-NC-SA 4.0).
      expect(screen.getByTestId('mineral-attribution')).toHaveTextContent(/Mindat/);
    });

    it('shows inline error and leaves fields untouched when no match is found', async () => {
      mockGet.mockResolvedValue({ data: { items: [] }, error: null });

      render(SpecimenForm, {
        mode: 'edit',
        submitLabel: 'Save',
        onSubmit: vi.fn(),
        initial: {
          type: 'mineral',
          name: 'Unobtanium',
          m_chemical_formula: 'XYZ',
        },
      });

      const lookupInput = screen.getByTestId('mineral-species-lookup-input') as HTMLInputElement;
      await fireEvent.input(lookupInput, { target: { value: 'unobtanium' } });
      await fireEvent.click(screen.getByTestId('mineral-species-lookup-button'));

      await waitFor(() =>
        expect(screen.getByTestId('mineral-species-lookup-error')).toHaveTextContent(
          /No match found/,
        ),
      );
      // Pre-existing chemical formula is preserved on failed lookup.
      const formula = document.getElementById('specimen-m-chemical-formula') as HTMLInputElement;
      expect(formula.value).toBe('XYZ');
    });
  });

  describe('accessibility (axe)', () => {
    it('has no structural a11y violations on initial mineral render', async () => {
      const { container } = render(SpecimenForm, {
        mode: 'create',
        submitLabel: 'Create',
        onSubmit: vi.fn(),
        initial: { type: 'mineral' },
      });

      // color-contrast requires real layout (canvas) and is skipped
      // in jsdom — see bead mi-k9t constraints.
      const results = await axe(container, { rules: { 'color-contrast': { enabled: false } } });
      expect(results).toHaveNoViolations();
    });
  });
});
