-- 0004_fossil_specimen_type: add 'fossil' as a fourth specimen_type
-- enum value alongside mineral|rock|meteorite (mi-6o8).
--
-- Postgres 12+ permits ALTER TYPE ... ADD VALUE inside a transaction,
-- with the caveat that the new value cannot be referenced in the same
-- transaction. This migration only adds the value, so wrapping it in
-- BEGIN/COMMIT (matching the rest of the migrations directory) is
-- safe.

BEGIN;

ALTER TYPE specimen_type ADD VALUE 'fossil';

COMMIT;
