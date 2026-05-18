-- Roll back 0015: drop the auth schema and the sessions table.
--
-- Data loss: all auth.sessions rows are discarded — every
-- authenticated user is forced to re-login. Acceptable: nothing
-- depends on this table yet (mi-1d5i #2 / mi-ruyc is what
-- introduces the SessionResolver impl), so rolling back is the
-- bead's documented recovery plan ("drop the schema; cheap because
-- nothing depends on it yet").
--
-- CASCADE because dropping the schema while it still contains the
-- sessions table requires it. The schema is exclusive to this
-- migration as of mi-twql — no other migration adds objects to
-- auth.* — so CASCADE has a tight blast radius.

BEGIN;

DROP SCHEMA IF EXISTS auth CASCADE;

COMMIT;
