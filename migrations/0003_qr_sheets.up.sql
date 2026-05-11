-- 0003_qr_sheets: persist QR sticker sheets per user (mi-c78.1).
--
-- A QR sheet is the user's current working set of specimens whose
-- QR-code labels they want to print together. v1 enforces ONE active
-- sheet per user via UNIQUE(user_id) — to start over the user
-- deletes the sheet and POSTs a new one. To change template, PATCH.
--
-- qr_sheet_specimens is the ordered link table; (sheet_id, position)
-- is unique so position holes after a remove must be repacked by the
-- service layer. The (sheet_id, specimen_id) pair is also unique so
-- the same specimen can't appear twice on a sheet.
--
-- Notes on conventions:
--   * v1 has no `users` table — auth is the stub user (per
--     CONTRACT.md §13). `user_id` mirrors the `author_id` pattern used
--     across the rest of the schema: a bare uuid, populated by the
--     app from the auth context, NOT FK-constrained.
--   * `id` columns are bare uuid; the app generates UUIDv7 values
--     (per CONTRACT.md §11). No DB-side gen_random_uuid().
--   * Timestamps are timestamptz (§8).

BEGIN;

CREATE TABLE qr_sheets (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL,
    template    text NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    UNIQUE (user_id)
);

CREATE TABLE qr_sheet_specimens (
    id           uuid PRIMARY KEY,
    sheet_id     uuid NOT NULL REFERENCES qr_sheets(id) ON DELETE CASCADE,
    specimen_id  uuid NOT NULL REFERENCES specimens(id) ON DELETE CASCADE,
    position     integer NOT NULL,
    added_at     timestamptz NOT NULL,
    UNIQUE (sheet_id, specimen_id),
    -- DEFERRABLE so the service-layer "repack positions after a remove"
    -- UPDATE doesn't trip the uniqueness check mid-statement (rows
    -- shifting from N → N-1 momentarily collide). The constraint stays
    -- INITIALLY IMMEDIATE; service code opens a transaction and runs
    -- `SET CONSTRAINTS qr_sheet_specimens_sheet_id_position_key DEFERRED`
    -- around the shift.
    CONSTRAINT qr_sheet_specimens_sheet_id_position_key
        UNIQUE (sheet_id, position) DEFERRABLE INITIALLY IMMEDIATE
);

CREATE INDEX qr_sheet_specimens_specimen_id_idx
    ON qr_sheet_specimens (specimen_id);

COMMIT;
