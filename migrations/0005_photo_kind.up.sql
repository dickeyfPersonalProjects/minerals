-- 0005_photo_kind: tag each photo with the lighting condition it was
-- taken under (mi-5b6). Discriminates a visible-light snapshot from a
-- UV-fluorescence shot from anything else — feeds gallery badges and
-- the "Show UV only" filter on the specimen page.
--
-- Existing rows default to 'visible' (the v1 status quo), so no data
-- backfill is needed beyond the column add.
--
-- The enum vocabulary is intentionally narrow:
--   * visible  — ordinary daylight or studio lighting
--   * uv       — under UV-A excitation (fluorescence)
--   * other    — escape hatch for anything else (IR, polarised, etc.)
--
-- Mirrors specimen_type / specimen_visibility — Postgres enums for the
-- closed vocabulary, kept consistent with §11.

BEGIN;

CREATE TYPE photo_kind AS ENUM ('visible', 'uv', 'other');

ALTER TABLE photos
    ADD COLUMN kind photo_kind NOT NULL DEFAULT 'visible';

COMMIT;
