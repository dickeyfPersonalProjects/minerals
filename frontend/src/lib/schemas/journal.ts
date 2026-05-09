// Zod schema for the journal entry create/edit form (D-3).
//
// Single-field form: `body_md` (markdown source). The server
// renders to sanitized HTML via the §17 pipeline; we never render
// markdown client-side per the bead's constraints.

import { z } from 'zod';

const trimmed = z.string().transform((s) => s.trim());

export const journalEntryFormSchema = z.object({
  body_md: trimmed.pipe(
    z
      .string()
      .min(1, 'Entry cannot be empty')
      .max(50_000, 'Entry is too long (max 50,000 characters)'),
  ),
});

export type JournalEntryFormValues = z.infer<typeof journalEntryFormSchema>;

export function emptyJournalFormValues(): JournalEntryFormValues {
  return { body_md: '' };
}
