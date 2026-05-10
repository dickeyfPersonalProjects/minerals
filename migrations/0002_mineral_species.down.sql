-- 0002_mineral_species down: drop everything 0002_mineral_species.up.sql
-- created. Clean rollback — no data is preserved.

BEGIN;

DROP TABLE IF EXISTS mineral_species;
DROP TYPE IF EXISTS mineral_species_source;

COMMIT;
