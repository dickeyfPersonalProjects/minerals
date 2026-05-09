<script lang="ts">
  import { createForm } from 'felte';
  import { validator } from '@felte/validator-zod';
  import { untrack } from 'svelte';
  import { collectorFormSchema, type CollectorFormValues } from './schemas/collector';

  export type CollectorFormSubmitResult =
    | { kind: 'ok' }
    | { kind: 'duplicate' }
    | { kind: 'error'; message: string };

  interface Props {
    initial?: Partial<CollectorFormValues>;
    submitLabel: string;
    onSubmit: (values: CollectorFormValues) => Promise<CollectorFormSubmitResult>;
    onCancel?: () => void;
    cancelLabel?: string;
  }

  const { initial, submitLabel, onSubmit, onCancel, cancelLabel = 'Cancel' }: Props = $props();

  // Capture initial values once at mount; the form owns its own state
  // after that. Re-rendering the parent doesn't reset the form.
  const initialName = untrack(() => initial?.name ?? '');
  const initialNotes = untrack(() => initial?.notes ?? '');

  let bannerError: string | null = $state(null);
  let nameTakenError: string | null = $state(null);

  const { form, errors, isSubmitting, data } = createForm<CollectorFormValues>({
    initialValues: {
      name: initialName,
      notes: initialNotes,
    },
    extend: validator({ schema: collectorFormSchema }),
    onSubmit: async (values) => {
      bannerError = null;
      nameTakenError = null;
      const trimmed: CollectorFormValues = {
        name: values.name.trim(),
        notes: values.notes.trim(),
      };
      const result = await onSubmit(trimmed);
      if (result.kind === 'duplicate') {
        nameTakenError = `A collector named "${trimmed.name}" already exists.`;
        return;
      }
      if (result.kind === 'error') {
        bannerError = result.message;
        return;
      }
    },
  });

  // Clear the duplicate-name error as soon as the user edits the name.
  let lastName = $state(initialName);
  $effect(() => {
    if ($data.name !== lastName) {
      lastName = $data.name;
      if (nameTakenError) nameTakenError = null;
    }
  });
</script>

<form use:form data-testid="collector-form" class="space-y-4" novalidate>
  {#if bannerError}
    <div
      role="alert"
      data-testid="form-error"
      class="rounded-md border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300"
    >
      {bannerError}
    </div>
  {/if}

  <div>
    <label for="collector-name" class="mb-1 block text-sm font-medium text-[var(--color-text)]">
      Name <span class="text-red-500" aria-hidden="true">*</span>
    </label>
    <input
      id="collector-name"
      name="name"
      type="text"
      autocomplete="off"
      class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      aria-invalid={Boolean($errors.name?.[0]) || Boolean(nameTakenError)}
      aria-describedby="collector-name-error"
    />
    {#if $errors.name?.[0]}
      <p id="collector-name-error" data-testid="name-error" class="mt-1 text-xs text-red-500">
        {$errors.name[0]}
      </p>
    {:else if nameTakenError}
      <p
        id="collector-name-error"
        data-testid="name-error"
        class="mt-1 text-xs text-red-500"
        role="alert"
      >
        {nameTakenError}
      </p>
    {/if}
  </div>

  <div>
    <label for="collector-notes" class="mb-1 block text-sm font-medium text-[var(--color-text)]">
      Notes
    </label>
    <textarea
      id="collector-notes"
      name="notes"
      rows="3"
      class="w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm text-[var(--color-text)] focus:border-[var(--color-accent)] focus:outline-none"
      aria-invalid={Boolean($errors.notes?.[0])}
      aria-describedby="collector-notes-error"
    ></textarea>
    {#if $errors.notes?.[0]}
      <p id="collector-notes-error" class="mt-1 text-xs text-red-500">
        {$errors.notes[0]}
      </p>
    {/if}
  </div>

  <div class="flex items-center gap-2">
    <button
      type="submit"
      disabled={$isSubmitting}
      data-testid="submit-button"
      class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-[var(--color-accent-fg)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
    >
      {$isSubmitting ? 'Saving…' : submitLabel}
    </button>
    {#if onCancel}
      <button
        type="button"
        onclick={onCancel}
        disabled={$isSubmitting}
        class="rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-sm text-[var(--color-text)] hover:bg-[var(--color-surface-2)] disabled:opacity-60"
      >
        {cancelLabel}
      </button>
    {/if}
  </div>
</form>
