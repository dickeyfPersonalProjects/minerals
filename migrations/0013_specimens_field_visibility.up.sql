-- 0013_specimens_field_visibility: add per-field visibility columns
-- to specimens (mi-y72 / parent mi-fo8 #1 — V2 per-field visibility
-- EPIC).
--
-- The resolution chain (CONTRACT.md §13 v2) walks specimen → user →
-- system default for each redactable field. These three columns are
-- the specimen-level layer. They REUSE the existing
-- specimen_visibility enum (mi-fo8 explicitly forbids introducing a
-- parallel enum, even though the name is somewhat overloaded once it
-- starts qualifying images too).
--
-- All three columns are nullable. NULL means "fall through to the
-- user default (users.field_defaults), then the system default" —
-- that nullability IS the design and stays forever (mi-y72
-- out-of-scope: "NOT NULL tightening (forever — these stay nullable,
-- that IS the design)").
--
-- No backfill: existing rows take NULL, which is the
-- fall-through-to-user-default state and matches v1 behaviour (a
-- single coarse `visibility` column governed everything). The
-- redaction logic that consumes these columns (mi-9ww / mi-fo8 #3)
-- isn't wired yet, so this is a pure schema add — handlers keep
-- compiling and keep returning the existing response shape.

BEGIN;

ALTER TABLE specimens
    ADD COLUMN visibility_price          specimen_visibility,
    ADD COLUMN visibility_acquired_from  specimen_visibility,
    ADD COLUMN visibility_images         specimen_visibility;

COMMIT;
