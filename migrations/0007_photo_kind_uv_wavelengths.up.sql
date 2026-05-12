-- 0007_photo_kind_uv_wavelengths: split the photo_kind 'uv' value into
-- three wavelength-specific variants — 'uv_sw' (shortwave, 254 nm),
-- 'uv_mw' (midwave, ~312 nm), 'uv_lw' (longwave, ~365 nm) — so the
-- specimen detail page can offer "Edit type" with the mineralogically
-- meaningful set (hq-6lrd).
--
-- Strategy: recreate the type. Postgres' ALTER TYPE ADD VALUE works in
-- a transaction since 12, but the new value can't be used in the same
-- transaction for an UPDATE — which we need to migrate legacy 'uv'
-- rows. The rename-and-cast pattern keeps the migration atomic and
-- reversible.
--
-- Legacy rows: any photo currently tagged 'uv' becomes 'uv_lw'. LW is
-- by far the most common UV used in mineralogical photography, so this
-- is the safest guess; operators who know better can re-tag via the
-- new "Edit type" UI.

BEGIN;

ALTER TABLE photos ALTER COLUMN kind DROP DEFAULT;

ALTER TYPE photo_kind RENAME TO photo_kind_old;

CREATE TYPE photo_kind AS ENUM ('visible', 'uv_sw', 'uv_mw', 'uv_lw', 'other');

ALTER TABLE photos
    ALTER COLUMN kind TYPE photo_kind
    USING (
        CASE kind::text
            WHEN 'uv' THEN 'uv_lw'
            ELSE kind::text
        END
    )::photo_kind;

ALTER TABLE photos ALTER COLUMN kind SET DEFAULT 'visible';

DROP TYPE photo_kind_old;

COMMIT;
