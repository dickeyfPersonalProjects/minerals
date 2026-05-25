-- 0017_specimens_tagged: add the per-specimen owner-tracking flag
-- (mi-n28q). Marks that the owner has already applied a QR-code
-- label or other physical tag to this specimen.
--
-- Design decisions:
--   - NOT NULL with DEFAULT false: every existing row becomes
--     "untagged", which is the correct semantic — no historical
--     specimen had a label we know about.
--   - Plain BOOLEAN (not tri-state): this is owner metadata, not a
--     physical property. The owner either tagged it or they haven't.
--   - NOT in type_data: it's a first-class tracking column, not a
--     type-specific attribute, and applies to all specimen types.
--   - Owner-only visibility (enforced at the API layer, not in the
--     DB): the tagged flag has no value to other viewers.

BEGIN;

ALTER TABLE specimens
    ADD COLUMN tagged BOOLEAN NOT NULL DEFAULT false;

COMMIT;
