-- 0005_photo_kind down: clean rollback. Drops the column then the
-- enum type. No data is preserved.

BEGIN;

ALTER TABLE photos DROP COLUMN IF EXISTS kind;

DROP TYPE IF EXISTS photo_kind;

COMMIT;
