-- 0008_users down: drop the users table introduced by mi-tl2.
--
-- Data loss: the seeded stub-overseer row and any user rows created
-- by callers after the up migration are dropped. This is acceptable
-- because no FK constraints from other tables point at users.id yet
-- (mi-aw3 is what introduces those constraints); rolling back here
-- only erases the rows in this table, not anything that references
-- them.

BEGIN;

DROP INDEX IF EXISTS users_keycloak_sub_idx;
DROP TABLE IF EXISTS users;

COMMIT;
