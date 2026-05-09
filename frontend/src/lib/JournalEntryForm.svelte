<script lang="ts" module>
  // Result of submitting the form. The caller handles the API call
  // and reports back via these variants — same pattern as
  // SpecimenForm.
  export type JournalEntryFormSubmitResult = { kind: 'ok' } | { kind: 'error'; message: string };
</script>

<script lang="ts">
  import { createForm } from 'felte';
  import { validator } from '@felte/validator-zod';
  import { untrack } from 'svelte';
  import {
    emptyJournalFormValues,
    journalEntryFormSchema,
    type JournalEntryFormValues,
  } from './schemas/journal';

  interface Props {
    initial?: Partial<JournalEntryFormValues>;
    submitLabel: string;
    onSubmit: (values: JournalEntryFormValues) => Promise<JournalEntryFormSubmitResult>;
    onCancel?: () => void;
    cancelLabel?: string;
    autofocus?: boolean;
  }

  const {
    initial,
    submitLabel,
    onSubmit,
    onCancel,
    cancelLabel = 'Cancel',
    autofocus = false,
  }: Props = $props();

  const initialValues: JournalEntryFormValues = untrack(() => ({
    ...emptyJournalFormValues(),
    ...(initial ?? {}),
  }));

  let bannerError: string | null = $state(null);
  let textarea: HTMLTextAreaElement | undefined = $state();

  const { form, errors, isSubmitting } = createForm<JournalEntryFormValues>({
    initialValues,
    extend: validator({ schema: journalEntryFormSchema }),
    onSubmit: async (values) => {
      bannerError = null;
      // The zod schema's `.transform(trim)` runs during validation
      // but felte forwards the raw form values to onSubmit, so we
      // trim here before handing off — callers can rely on a
      // normalized body_md.
      const result = await onSubmit({ body_md: values.body_md.trim() });
      if (result.kind === 'error') {
        bannerError = result.message;
      }
    },
  });

  function showError(): string | null {
    const e = $errors.body_md;
    if (Array.isArray(e) && e.length > 0) return e[0]!;
    return null;
  }

  $effect(() => {
    if (autofocus && textarea) textarea.focus();
  });
</script>

<form use:form data-testid="journal-entry-form" class="space-y-3" novalidate>
  {#if bannerError}
    <div
      role="alert"
      data-testid="journal-form-error"
      class="rounded-md border border-red-500/40 bg-red-500/10 p-2 text-xs text-red-700 dark:text-red-300"
    >
      {bannerError}
    </div>
  {/if}

  <div>
    <label for="journal-body-md" class="sr-only">Entry body (markdown)</label>
    <textarea
      bind:this={textarea}
      id="journal-body-md"
      name="body_md"
      rows="5"
      placeholder="Write a markdown entry…"
      class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 font-mono text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      aria-invalid={Boolean(showError())}
      aria-describedby="journal-body-md-error"
    ></textarea>
    <p class="mt-1 text-xs text-[var(--color-text-muted)]">
      Markdown is rendered server-side after save.
    </p>
    {#if showError()}
      <p
        id="journal-body-md-error"
        data-testid="journal-body-error"
        class="mt-1 text-xs text-red-500"
      >
        {showError()}
      </p>
    {/if}
  </div>

  <div class="flex items-center gap-2">
    <button
      type="submit"
      disabled={$isSubmitting}
      data-testid="journal-submit-button"
      class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
    >
      {$isSubmitting ? 'Saving…' : submitLabel}
    </button>
    {#if onCancel}
      <button
        type="button"
        onclick={onCancel}
        disabled={$isSubmitting}
        data-testid="journal-cancel-button"
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-60"
      >
        {cancelLabel}
      </button>
    {/if}
  </div>
</form>
