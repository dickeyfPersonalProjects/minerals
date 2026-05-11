-- 0006_specimen_main_image down: drop the column. No data is
-- preserved — rolling back loses every user's main-image choice
-- and the column reverts to absent (no graceful "remember which
-- one was main" path in v1).

BEGIN;

ALTER TABLE specimens DROP COLUMN IF EXISTS main_image_id;

COMMIT;
