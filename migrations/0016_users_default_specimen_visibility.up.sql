-- 0016_users_default_specimen_visibility: add the per-user default
-- visibility for newly-created specimens (mi-q2d8).
--
-- Distinct from users.field_defaults (migration 0012): that column
-- carries per-FIELD defaults (price / acquired_from / images / …)
-- within a specimen. THIS column is the WHOLE-specimen default
-- visibility the create form pre-fills with. Two different axes —
-- keep them separate.
--
-- Nullable: NULL means "no user preference; the create form falls
-- back to the system default (private, CONTRACT.md §13)". A value
-- is one of the specimen_visibility enum strings ('private',
-- 'unlisted', 'public').
--
-- No DB-level CHECK constraint, matching the convention used for
-- field_defaults (0012) and the other discriminated-union JSON
-- columns (CONTRACT.md §11): the application owns validation. The
-- PATCH /api/v1/profile handler validates the value before persist.
--
-- The value is only a DEFAULT for the create form's initial state;
-- it never changes the visibility of existing specimens.

BEGIN;

ALTER TABLE users
    ADD COLUMN default_specimen_visibility text;

COMMIT;
