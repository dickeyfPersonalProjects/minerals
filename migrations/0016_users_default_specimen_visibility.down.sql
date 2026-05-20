-- Roll back 0016: drop the users.default_specimen_visibility column.
--
-- Data loss: any stored per-user create-form default is discarded.
-- Existing specimens are unaffected — the column only ever seeded
-- the create form's initial visibility selection.

BEGIN;

ALTER TABLE users
    DROP COLUMN IF EXISTS default_specimen_visibility;

COMMIT;
