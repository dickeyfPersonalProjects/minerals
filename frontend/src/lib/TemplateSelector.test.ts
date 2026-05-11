import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import TemplateSelector from './TemplateSelector.svelte';

afterEach(() => {
  cleanup();
});

describe('TemplateSelector dialog', () => {
  it('renders one card per Avery template with name and capacity', () => {
    render(TemplateSelector, {
      onConfirm: vi.fn(),
      onCancel: vi.fn(),
    });
    const options = screen.getAllByTestId('template-option');
    // 5 v1 templates per qrTemplates.ts.
    expect(options).toHaveLength(5);
    const ids = options.map((o) => o.getAttribute('data-template-id'));
    expect(ids).toEqual(['avery-5160', 'avery-5163', 'avery-5164', 'avery-22806', 'avery-l7160']);
    // Capacity copy is rendered for the first option (3×10 = 30).
    expect(options[0]?.textContent).toContain('30 per sheet');
  });

  it('highlights the default initial template', () => {
    render(TemplateSelector, {
      onConfirm: vi.fn(),
      onCancel: vi.fn(),
    });
    const options = screen.getAllByTestId('template-option');
    expect(options[0]?.getAttribute('data-selected')).toBe('true');
    expect(options[0]?.getAttribute('aria-pressed')).toBe('true');
    expect(options[1]?.getAttribute('data-selected')).toBe('false');
  });

  it('honours the `initial` prop for the highlighted card', () => {
    render(TemplateSelector, {
      initial: 'avery-22806',
      onConfirm: vi.fn(),
      onCancel: vi.fn(),
    });
    const target = screen
      .getAllByTestId('template-option')
      .find((o) => o.getAttribute('data-template-id') === 'avery-22806');
    expect(target?.getAttribute('data-selected')).toBe('true');
  });

  it('clicking another card moves the selection', async () => {
    render(TemplateSelector, {
      onConfirm: vi.fn(),
      onCancel: vi.fn(),
    });
    const options = screen.getAllByTestId('template-option');
    const second = options[1]!;
    await fireEvent.click(second);
    expect(second.getAttribute('data-selected')).toBe('true');
    expect(options[0]?.getAttribute('data-selected')).toBe('false');
  });

  it('Confirm fires the selected template id', async () => {
    const onConfirm = vi.fn();
    render(TemplateSelector, {
      onConfirm,
      onCancel: vi.fn(),
    });
    const target = screen
      .getAllByTestId('template-option')
      .find((o) => o.getAttribute('data-template-id') === 'avery-l7160')!;
    await fireEvent.click(target);
    await fireEvent.click(screen.getByTestId('template-selector-confirm'));
    expect(onConfirm).toHaveBeenCalledWith('avery-l7160');
  });

  it('Cancel button fires onCancel without confirming', async () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(TemplateSelector, { onConfirm, onCancel });
    await fireEvent.click(screen.getByTestId('template-selector-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('Escape closes the dialog (cancel)', async () => {
    const onCancel = vi.fn();
    render(TemplateSelector, { onConfirm: vi.fn(), onCancel });
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('backdrop click dismisses the dialog', async () => {
    const onCancel = vi.fn();
    render(TemplateSelector, { onConfirm: vi.fn(), onCancel });
    await fireEvent.click(screen.getByTestId('template-selector-backdrop'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('busy disables both action buttons and prevents Escape from cancelling', async () => {
    const onCancel = vi.fn();
    const onConfirm = vi.fn();
    render(TemplateSelector, { onConfirm, onCancel, busy: true });
    expect(screen.getByTestId('template-selector-confirm')).toBeDisabled();
    expect(screen.getByTestId('template-selector-cancel')).toBeDisabled();
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(onCancel).not.toHaveBeenCalled();
  });

  it('renders the supplied title and confirm label', async () => {
    render(TemplateSelector, {
      title: 'Change label template',
      confirmLabel: 'Update template',
      onConfirm: vi.fn(),
      onCancel: vi.fn(),
    });
    expect(screen.getByText('Change label template')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId('template-selector-confirm')).toHaveTextContent('Update template');
    });
  });
});
