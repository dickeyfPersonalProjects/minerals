-- 0003_qr_sheets down: drop everything 0003_qr_sheets.up.sql created.
-- Clean rollback — no data is preserved.

BEGIN;

DROP TABLE IF EXISTS qr_sheet_specimens;
DROP TABLE IF EXISTS qr_sheets;

COMMIT;
