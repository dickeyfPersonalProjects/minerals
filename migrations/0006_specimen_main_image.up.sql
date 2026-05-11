-- 0006_specimen_main_image: let users designate one photo as the
-- specimen's "main image" — used by the detail-page hero and the
-- specimen list card thumbnail (mi-m8q).
--
-- Design choice (per the bead): we do NOT add a flag to the photos
-- join table. A single nullable FK on specimens is enough — choosing
-- a new main is one UPDATE, with no multi-row constraint to enforce.
--
-- Semantics:
--   * NULL means "fall back to the first photo by position" — the
--     v1 status-quo, so existing rows need no backfill.
--   * The column references files(id), not photos(id): the file is
--     what survives a re-upload or crop replacement at the storage
--     layer. The API guards membership (the file must belong to a
--     photo on this specimen) at PATCH time.
--   * ON DELETE SET NULL means deleting the underlying file (which
--     the photo DELETE handler does after dropping the photo row)
--     reverts the specimen to the first-by-position fallback —
--     gracefully, no orphans.

BEGIN;

ALTER TABLE specimens
    ADD COLUMN main_image_id uuid REFERENCES files(id) ON DELETE SET NULL;

COMMIT;
