-- 0009_normalize_chemical_formula down (mi-c8v).
--
-- Data loss: this migration is intentionally irreversible at the data
-- level. The up migration rewrote HTML-flavored chemical_formula
-- values into clean Unicode; reconstructing the original HTML markup
-- from the Unicode result is not possible (the mapping was lossy by
-- design — e.g. ₂ collapses both <sub>2</sub> and the literal Unicode
-- ₂ to the same output).
--
-- We deliberately leave Unicode values in place on rollback rather
-- than mangle them back to broken HTML. The schema is unchanged by
-- 0009 so there is nothing structural to undo — this down is a
-- documented no-op.

BEGIN;
-- Intentional no-op; see comment above.
COMMIT;
