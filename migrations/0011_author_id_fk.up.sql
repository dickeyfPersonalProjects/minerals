-- 0011_author_id_fk: promote author/owner columns to real FK
-- constraints against users(id) (mi-aw3a / CONTRACT.md §13).
--
-- Background: specimens, collectors, journal_entries, files,
-- mineral_species, and qr_sheets each carry a bare `uuid NOT NULL`
-- column identifying the creating user (author_id / uploaded_by /
-- user_id). Until the users table existed (0008) these could not be
-- FK-constrained, so they were plain uuids populated by the app from
-- the auth context.
--
-- mi-aw3a flips on real Keycloak authentication. Migration 0008
-- already seeded the overseer row with the exact UUID the v1 stub
-- auth path used as author_id
-- (00000000-0000-0000-0000-000000000001), so every existing row's
-- author column already points at a valid users.id — the "backfill"
-- the original §13 plan called for is a no-op by construction. What
-- remains is to make that integrity explicit: add the FK constraints
-- so author columns provably reference a real user from here on.
--
-- No ON DELETE action (RESTRICT is the default): users are never
-- hard-deleted — GDPR erasure soft-deletes by transitioning
-- status='deleted' and scrubbing PII while keeping the row (0008),
-- so author columns never orphan and never need cascade behaviour.

BEGIN;

ALTER TABLE specimens
    ADD CONSTRAINT specimens_author_id_fkey
    FOREIGN KEY (author_id) REFERENCES users (id);

ALTER TABLE collectors
    ADD CONSTRAINT collectors_author_id_fkey
    FOREIGN KEY (author_id) REFERENCES users (id);

ALTER TABLE journal_entries
    ADD CONSTRAINT journal_entries_author_id_fkey
    FOREIGN KEY (author_id) REFERENCES users (id);

ALTER TABLE files
    ADD CONSTRAINT files_uploaded_by_fkey
    FOREIGN KEY (uploaded_by) REFERENCES users (id);

ALTER TABLE mineral_species
    ADD CONSTRAINT mineral_species_author_id_fkey
    FOREIGN KEY (author_id) REFERENCES users (id);

ALTER TABLE qr_sheets
    ADD CONSTRAINT qr_sheets_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id);

COMMIT;
