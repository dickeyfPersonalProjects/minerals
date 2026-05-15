-- 0010_shares: resource-sharing table for V2 RBAC (mi-1mv).
--
-- Backs CONTRACT.md §13 v2 RBAC design (the `shares` table, lines
-- 3007-3021) and the Casbin `isSharedWith` matcher function: a row
-- here grants `shared_with` access to a single resource owned by
-- `shared_by`.
--
-- Schema rules (CONTRACT.md §6 / §11):
--   - `id` is a bare `uuid` with NO database default; the application
--     generates UUIDv7 values per CONTRACT.md §11 (the discipline is
--     enforced in Go, not by a column default — same as `users` in
--     migration 0008).
--   - All timestamps are `timestamptz`.
--   - `shared_by` and `shared_with` FK to `users(id)`, which exists as
--     of migration 0008 (mi-tl2). ON DELETE CASCADE is acceptable: a
--     hard-deleted user takes their shares with them. In practice GDPR
--     erasure soft-deletes (status='deleted') so the cascade rarely
--     fires.
--
-- Orphaned shares: there is deliberately NO cascade on `resource_id`
-- (it is not even a real FK — `resource_type` is polymorphic). When a
-- shared resource is deleted, its share rows are left dangling and
-- swept by a background cleanup job tracked in a separate bead.

BEGIN;

CREATE TABLE IF NOT EXISTS shares (
    id            uuid PRIMARY KEY,
    resource_type text NOT NULL,
    resource_id   uuid NOT NULL,
    shared_by     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    shared_with   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX shares_resource_idx ON shares (resource_type, resource_id);
CREATE INDEX shares_shared_with_idx ON shares (shared_with);

COMMIT;
