-- 0001_init down: drop everything 0001_init.up.sql created.
-- This is a clean rollback — no data is preserved. Drops are
-- ordered so dependent tables go before their parents.

BEGIN;

DROP TABLE IF EXISTS journal_entry_files;
DROP TABLE IF EXISTS journal_entries;
DROP TABLE IF EXISTS photos;
DROP TABLE IF EXISTS specimen_collectors;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS collectors;
DROP TABLE IF EXISTS specimens;

DROP TYPE IF EXISTS specimen_visibility;
DROP TYPE IF EXISTS specimen_type;

COMMIT;
