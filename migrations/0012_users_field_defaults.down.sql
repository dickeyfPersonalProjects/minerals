-- Roll back 0012: drop the users.field_defaults column.
--
-- Data loss: any stored per-user defaults are discarded. The
-- redaction logic that consumes this column does not exist yet
-- (mi-fo8 #3+) so there's nothing to break.

BEGIN;

ALTER TABLE users
    DROP COLUMN IF EXISTS field_defaults;

COMMIT;
