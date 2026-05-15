-- 0010_shares down: drop the shares table introduced by mi-1mv.
--
-- Data loss: every share row is dropped. This is acceptable — no other
-- table FKs against shares.id, so rolling back here only erases the
-- rows in this table. The cascade FKs to users(id) disappear with the
-- table.

BEGIN;

DROP INDEX IF EXISTS shares_shared_with_idx;
DROP INDEX IF EXISTS shares_resource_idx;
DROP TABLE IF EXISTS shares;

COMMIT;
