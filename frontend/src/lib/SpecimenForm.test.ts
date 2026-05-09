import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import SpecimenForm from './SpecimenForm.svelte';
import type { SpecimenFormSubmitResult } from './SpecimenForm.svelte';
import { emptyFormValues, type SpecimenFormValues } from './schemas/specimen';

function fillRequired(values: Partial<SpecimenFormValues> = {}): SpecimenFormValues {
  return { ...emptyFormValues('mineral'), name: 'Quartz', ...values };
}

beforeEach(() => {
  vi.restoreAllMocks();
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

    it('does not render an inline banner on generic API failure (route toasts instead)', async () => {
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

      await waitFor(() => expect(onSubmit).toHaveBeenCalled());
      // Submit-level errors now surface via the global toast
      // store (E-4); the form no longer renders a banner.
      expect(screen.queryByTestId('form-error')).not.toBeInTheDocument();
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
});
