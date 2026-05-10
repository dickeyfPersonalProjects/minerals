-- 0002_mineral_species: Mindat-backed mineral lookup with DB as the
-- canonical store (mi-dtg / F-1).
--
-- Provenance: each row is either sourced from Mindat (source='mindat',
-- mindat_id populated) or hand-entered by a user (source='user',
-- mindat_id NULL). We never delete records: once a Mindat lookup
-- lands here, it's the canonical copy — not a TTL-bound cache.
--
-- The link from `specimens.type_data.mindat_id` (jsonb) to this table
-- is logical only; it is NOT enforced at the DB level (per the F-1
-- bead acceptance criteria — the app verifies on read).

BEGIN;

CREATE TYPE mineral_species_source AS ENUM ('mindat', 'user');

CREATE TABLE mineral_species (
    id           uuid PRIMARY KEY,
    name         text NOT NULL UNIQUE,
    source       mineral_species_source NOT NULL,
    mindat_id    text UNIQUE,
    data         jsonb NOT NULL DEFAULT '{}'::jsonb,
    attribution  text,
    author_id    uuid NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);

-- ILIKE substring match on `name` is the v1 search path (per the F-1
-- bead — pg_trgm/fuzzy is a v2 polish item). A simple lower(name)
-- expression index keeps prefix-style searches snappy without pulling
-- in pg_trgm.
CREATE INDEX mineral_species_name_lower_idx
    ON mineral_species (lower(name));

COMMIT;
