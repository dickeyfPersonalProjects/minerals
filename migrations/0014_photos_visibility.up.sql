-- 0014_photos_visibility: add per-photo visibility column (mi-y72 /
-- parent mi-fo8 #1 — V2 per-field visibility EPIC).
--
-- The minerals schema names its images table `photos` (per
-- 0001_init: photos(id, specimen_id, file_id, ...)) — the bead's
-- "verify the images table name" check resolves to `photos` here.
--
-- The resolution chain (CONTRACT.md §13 v2) walks photo →
-- specimen.visibility_images → user default → system default. This
-- column is the photo-level layer. REUSES the existing
-- specimen_visibility enum (per mi-fo8: no parallel enum).
--
-- Nullable. NULL means "fall through to the parent specimen's
-- visibility_images" — that nullability IS the design and stays
-- forever. Today every photo inherits its parent specimen's
-- visibility wholesale (CONTRACT.md §13 "sub-resources inherit
-- their parent's visibility; they do not have their own per-row
-- visibility column"); this migration introduces the column so the
-- redaction work (mi-9ww / mi-fo8 #3) can override per-photo. Until
-- that lands, handlers ignore the column and the inheritance rule
-- still holds at the API layer.

BEGIN;

ALTER TABLE photos
    ADD COLUMN visibility specimen_visibility;

COMMIT;
