-- 0012_users_field_defaults: add per-field visibility defaults to users
-- (mi-y72 / parent mi-fo8 #1 — V2 per-field visibility EPIC).
--
-- Schema-only step in the per-field visibility resolution chain. The
-- new column carries each user's preferred default for individually
-- redactable fields. Sparse JSON: an absent key means "no user
-- default; fall through to the system default in the resolution
-- chain" (CONTRACT.md §13).
--
-- Structure:
--   {"price": <vis>, "acquired_from": <vis>, "images": <vis>}
-- where <vis> is one of the specimen_visibility enum values
-- ('private', 'unlisted', 'public'). The column itself is the
-- top-level nullable: NULL is the all-fields-fall-through case.
--
-- No validation is enforced at the DB layer. Two reasons:
--   1. CONTRACT.md §11 stores other discriminated-union JSON
--      (specimens.type_data, dimensions, locality) without DB-level
--      CHECK constraints — the application owns validation. We
--      follow the same convention here.
--   2. The redaction work (mi-fo8 #3) is the only consumer; the
--      handler validates keys + values before persistence.
--
-- No behaviour change: backend handlers continue ignoring this
-- column until mi-tyb (PATCH /api/v1/profile) and mi-9ww
-- (resolveVisibility wiring) light it up.

BEGIN;

ALTER TABLE users
    ADD COLUMN field_defaults jsonb;

COMMIT;
