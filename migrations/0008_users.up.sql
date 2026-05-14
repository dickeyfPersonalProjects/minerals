-- 0008_users: foundation table for V2 auth (mi-tl2).
--
-- Maps a Keycloak JWT `sub` claim to the application's user identity,
-- tracks profile-completion status for the first-login gate, and
-- provides the row that existing `author_id` columns will FK against
-- once mi-aw3 turns those references into hard constraints.
--
-- Schema rules (CONTRACT.md §6 / §11):
--   - `id` is a bare `uuid` with NO database default; the application
--     generates UUIDv7 values per CONTRACT.md §11.
--   - All timestamps are `timestamptz`.
--   - Status uses a TEXT + CHECK pair (not a Postgres ENUM type) so
--     follow-on beads can introduce additional statuses (e.g. a
--     'banned' state) without the rename-and-cast dance ENUM changes
--     require — see migration 0007 for that pattern.
--
-- Backfill: the v1 stub user identity (CONTRACT.md §13:
-- 00000000-0000-0000-0000-000000000001 / overseer@minerals.local)
-- gets a corresponding row so existing FKs from specimens,
-- journal_entries, files, and collectors remain valid once mi-aw3
-- promotes those references to NOT NULL FK constraints. The
-- placeholder `keycloak_sub` value ('stub-overseer') is replaced by
-- the real `sub` claim in the same migration that flips on real auth
-- (CONTRACT.md §13: "When real auth lands, a one-time migration
-- backfills the stub's author_id rows with the actual overseer's
-- user id").
--
-- GDPR (informational, not implemented here):
--   - PII columns: `email`, `display_name`.
--   - Right to erasure transitions `status` to 'deleted' and scrubs
--     PII columns; the soft-delete keeps the UUID stable so
--     `author_id` FKs across the system don't orphan. The erasure
--     endpoint and scrub job are tracked separately and out of scope
--     for this migration.

BEGIN;

CREATE TABLE users (
    id              uuid PRIMARY KEY,
    keycloak_sub    text NOT NULL UNIQUE,
    email           text NOT NULL,
    display_name    text,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'active', 'deleted')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX users_keycloak_sub_idx ON users (keycloak_sub);

INSERT INTO users (id, keycloak_sub, email, display_name, status)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'stub-overseer',
    'overseer@minerals.local',
    'Overseer',
    'active'
);

COMMIT;
