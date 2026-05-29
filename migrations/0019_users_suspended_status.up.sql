-- 0019_users_suspended_status: add the 'suspended' account status
-- (mi-3gxz) — the app-level side of operator account suspension.
--
-- Migration 0008 deliberately modelled users.status as a TEXT + CHECK
-- pair "so follow-on beads can introduce additional statuses (e.g. a
-- 'banned' state) without the rename-and-cast dance ENUM changes
-- require" — this is that follow-on. We widen the CHECK to admit
-- 'suspended' alongside the existing pending/active/deleted set.
--
-- Semantics (see internal/api/admin.go suspend/unsuspend + the auth
-- resolver gate):
--   - 'suspended' is reached only from 'active' via an operator action
--     in the admin console; unsuspend returns it to 'active'.
--   - A suspended user's Keycloak identity is disabled (no new login /
--     token issuance) and their live sessions are revoked; the app's
--     auth gate also fail-closes every protected request while the row
--     is 'suspended', so enforcement does not depend on the IdP alone.
--   - It is distinct from 'deleted' (the GDPR-erasure tombstone):
--     suspension is reversible and preserves the user's data.
--
-- The constraint created inline by 0008 is auto-named users_status_check
-- by Postgres; we drop and re-create it with the widened value set.

BEGIN;

ALTER TABLE users DROP CONSTRAINT users_status_check;

ALTER TABLE users
    ADD CONSTRAINT users_status_check
    CHECK (status IN ('pending', 'active', 'suspended', 'deleted'));

COMMIT;
