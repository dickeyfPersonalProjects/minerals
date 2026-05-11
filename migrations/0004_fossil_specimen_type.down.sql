-- 0004_fossil_specimen_type down: remove the 'fossil' enum value.
--
-- Postgres does NOT support removing values from an existing enum
-- type. A "real" rollback would require recreating specimen_type
-- with the value omitted, rewriting the specimens.type column, and
-- swapping the type — which is destructive when fossil rows exist.
-- Down migrations in this project are best-effort; we leave the
-- enum unchanged and rely on application code to refuse new fossil
-- writes after a rollback (the SpecimenFossil constant disappears
-- with the application binary).

BEGIN;

-- intentional no-op; see header note.

COMMIT;
