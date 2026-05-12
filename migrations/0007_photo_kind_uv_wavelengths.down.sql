-- 0007_photo_kind_uv_wavelengths down: collapse the three UV
-- wavelength variants back into a single 'uv' value. Information is
-- lost (which wavelength) but the resulting state is the pre-0007
-- vocabulary exactly.

BEGIN;

ALTER TABLE photos ALTER COLUMN kind DROP DEFAULT;

ALTER TYPE photo_kind RENAME TO photo_kind_old;

CREATE TYPE photo_kind AS ENUM ('visible', 'uv', 'other');

ALTER TABLE photos
    ALTER COLUMN kind TYPE photo_kind
    USING (
        CASE kind::text
            WHEN 'uv_sw' THEN 'uv'
            WHEN 'uv_mw' THEN 'uv'
            WHEN 'uv_lw' THEN 'uv'
            ELSE kind::text
        END
    )::photo_kind;

ALTER TABLE photos ALTER COLUMN kind SET DEFAULT 'visible';

DROP TYPE photo_kind_old;

COMMIT;
