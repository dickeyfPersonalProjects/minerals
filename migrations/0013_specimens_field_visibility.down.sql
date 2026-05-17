-- Roll back 0013: drop the per-field visibility columns from
-- specimens.
--
-- Data loss: any stored per-specimen per-field visibility overrides
-- are discarded. The redaction logic that consumes these columns
-- does not exist yet (mi-fo8 #3+) so there's nothing to break.

BEGIN;

ALTER TABLE specimens
    DROP COLUMN IF EXISTS visibility_images,
    DROP COLUMN IF EXISTS visibility_acquired_from,
    DROP COLUMN IF EXISTS visibility_price;

COMMIT;
