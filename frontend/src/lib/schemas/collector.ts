// Zod schema for the collector create/edit form (D-4) and the
// inline "Add new collector" mini-form on the chain editor (D-5).

import { z } from 'zod';

export const collectorFormSchema = z.object({
  name: z.string().trim().min(1, 'Name is required').max(200, 'Name is too long'),
  notes: z.string().max(10_000, 'Notes are too long'),
});

export type CollectorFormValues = z.infer<typeof collectorFormSchema>;
