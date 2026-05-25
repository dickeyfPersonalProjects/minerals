-- Roll back 0017: drop the specimens.tagged column.
--
-- Data loss: the tagged status of every specimen is discarded.
-- Specimens themselves are unaffected.

BEGIN;

ALTER TABLE specimens
    DROP COLUMN IF EXISTS tagged;

COMMIT;
