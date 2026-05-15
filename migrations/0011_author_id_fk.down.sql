-- Roll back 0011: drop the author/owner FK constraints, returning the
-- columns to bare uuids. The column data is untouched.

BEGIN;

ALTER TABLE qr_sheets       DROP CONSTRAINT qr_sheets_user_id_fkey;
ALTER TABLE mineral_species DROP CONSTRAINT mineral_species_author_id_fkey;
ALTER TABLE files           DROP CONSTRAINT files_uploaded_by_fkey;
ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_author_id_fkey;
ALTER TABLE collectors      DROP CONSTRAINT collectors_author_id_fkey;
ALTER TABLE specimens       DROP CONSTRAINT specimens_author_id_fkey;

COMMIT;
