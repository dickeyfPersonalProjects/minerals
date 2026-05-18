-- 0015_auth_sessions: auth.sessions schema + table for the V2
-- backend-for-frontend (BFF) migration (mi-twql / parent mi-1d5i #1).
--
-- Canonical design: docs/design/auth-bff.md (mi-bv66). The BFF
-- mediates OAuth server-side and persists access/refresh/id tokens
-- here, keyed by an opaque 32-byte session id that the browser holds
-- in an HttpOnly cookie. No application code reads or writes this
-- table yet — the SessionResolver interface and Postgres impl land
-- in mi-1d5i #2 (mi-ruyc). This migration lands the storage and the
-- cleanup loop only.
--
-- Why a dedicated `auth` schema (not the default schema): the most
-- important decision for future microservice extraction is the
-- table boundary. Domain tables live in the default schema; auth.*
-- stays separate so an eventual auth-service can be peeled out
-- without dragging anything else with it.
--
-- No FK on user_id → users.id deliberately: sessions outlive
-- user-row schema changes, and the eventual auth microservice will
-- not have access to the users table. Application-level integrity
-- is sufficient.
--
-- Idempotent forms (IF NOT EXISTS) are used throughout because the
-- `auth` schema is global (schema names ignore search_path) while
-- the integration-test base in internal/db creates a fresh per-test
-- search_path schema for every test and re-applies the full
-- migration chain inside it. Without IF NOT EXISTS, the second test
-- would race on CREATE TABLE auth.sessions and fail. The contract
-- §6 "belt and suspenders" rule sanctions this.

BEGIN;

-- `CREATE SCHEMA IF NOT EXISTS` carries a documented TOCTOU race
-- against `pg_namespace_nspname_index` when two backends apply the
-- same migration concurrently — surfaces in integration tests where
-- each per-test schema runs its own migrate-up against the same DB.
-- Wrapping the create in a DO block with a `duplicate_schema`
-- exception handler is the standard pattern.
DO $$
BEGIN
    CREATE SCHEMA auth;
EXCEPTION WHEN duplicate_schema THEN
    NULL;
END
$$;

CREATE TABLE IF NOT EXISTS auth.sessions (
    id                       BYTEA       PRIMARY KEY,
    user_sub                 TEXT        NOT NULL,
    user_id                  UUID        NOT NULL,
    access_token             TEXT        NOT NULL,
    refresh_token            TEXT        NOT NULL,
    id_token                 TEXT        NOT NULL,
    access_token_expires_at  TIMESTAMPTZ NOT NULL,
    refresh_token_expires_at TIMESTAMPTZ NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    absolute_expires_at      TIMESTAMPTZ NOT NULL,
    csrf_token               BYTEA       NOT NULL,
    ip                       INET,
    user_agent               TEXT,
    revoked_at               TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx
    ON auth.sessions (user_id);

-- Partial index supports the hot "alive session by absolute expiry"
-- check the cleanup loop and the middleware liveness check both
-- need; filtering revoked rows out keeps the index small.
CREATE INDEX IF NOT EXISTS sessions_absolute_expires_at_idx
    ON auth.sessions (absolute_expires_at)
    WHERE revoked_at IS NULL;

COMMIT;
