-- Roll back 0014: drop the photos.visibility column.
--
-- Data loss: any stored per-photo visibility overrides are
-- discarded. The redaction logic that consumes this column does not
-- exist yet (mi-fo8 #3+) so there's nothing to break.

BEGIN;

ALTER TABLE photos
    DROP COLUMN IF EXISTS visibility;

COMMIT;
