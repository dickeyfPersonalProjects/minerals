-- Roll back 0019: drop 'suspended' from the users.status CHECK set.
--
-- Any rows currently in the 'suspended' state would violate the
-- restored constraint, so we first lift them back to 'active' (the
-- state they were suspended from). This loses the suspension marker —
-- acceptable for a down-migration, and the operator can re-suspend
-- after re-applying 0019. Keycloak-side `enabled=false` is NOT touched
-- here (this migration only owns the app schema); re-enable affected
-- users in the Keycloak admin console if rolling back permanently.

BEGIN;

UPDATE users SET status = 'active' WHERE status = 'suspended';

ALTER TABLE users DROP CONSTRAINT users_status_check;

ALTER TABLE users
    ADD CONSTRAINT users_status_check
    CHECK (status IN ('pending', 'active', 'deleted'));

COMMIT;
