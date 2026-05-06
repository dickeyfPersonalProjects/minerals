-- 0001_init: foundational schema for minerals v1.
-- Matches docs/design/02-domain-model.md §2 and the schema rules in
-- CONTRACT.md §6 (migrations), §11 (data layer), §13 (auth / author_id).
--
-- All `id` columns are bare `uuid`; the application generates UUIDv7
-- values (CONTRACT.md §11). All timestamps are `timestamptz` (§8).
-- `author_id` columns carry NO database default — the application
-- populates them from auth context (§13).

BEGIN;

CREATE TYPE specimen_type AS ENUM ('mineral', 'rock', 'meteorite');
CREATE TYPE specimen_visibility AS ENUM ('private', 'unlisted', 'public');

CREATE TABLE specimens (
    id              uuid PRIMARY KEY,
    type            specimen_type NOT NULL,
    catalog_number  text UNIQUE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    visibility      specimen_visibility NOT NULL DEFAULT 'private',
    author_id       uuid NOT NULL,
    acquired_at     date,
    acquired_from   text,
    price_cents     bigint,
    source_notes    text,
    locality_text   text,
    locality        jsonb,
    mass_g          numeric,
    dimensions      jsonb,
    type_data       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    -- Generated tsvector for full-text search (design §4.4). Sources:
    -- name, description, locality_text, source_notes, plus selected
    -- stringy fields from type_data.
    search_tsv      tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(locality_text, '')), 'C') ||
        setweight(to_tsvector('english', coalesce(source_notes, '')), 'D') ||
        setweight(to_tsvector('english', coalesce(type_data->>'chemical_formula', '')), 'D') ||
        setweight(to_tsvector('english', coalesce(type_data->>'classification', '')), 'D')
    ) STORED
);

CREATE INDEX specimens_search_tsv_idx ON specimens USING GIN (search_tsv);

CREATE TABLE collectors (
    id          uuid PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    notes       text,
    author_id   uuid NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);

CREATE TABLE files (
    id           uuid PRIMARY KEY,
    s3_key       text NOT NULL UNIQUE,
    content_type text NOT NULL,
    byte_size    bigint NOT NULL,
    sha256       text NOT NULL,
    uploaded_by  uuid NOT NULL,
    uploaded_at  timestamptz NOT NULL
);

CREATE TABLE specimen_collectors (
    specimen_id  uuid NOT NULL REFERENCES specimens(id) ON DELETE CASCADE,
    collector_id uuid NOT NULL REFERENCES collectors(id) ON DELETE RESTRICT,
    position     integer NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (specimen_id, collector_id)
);

-- specimen_id is covered by the PK leftmost; collector_id needs its own
-- index for reverse lookups (per CONTRACT.md §11 indexing discipline).
CREATE INDEX specimen_collectors_collector_id_idx
    ON specimen_collectors (collector_id);

CREATE TABLE photos (
    id           uuid PRIMARY KEY,
    specimen_id  uuid NOT NULL REFERENCES specimens(id) ON DELETE CASCADE,
    file_id      uuid NOT NULL REFERENCES files(id) ON DELETE RESTRICT,
    taken_at     timestamptz,
    position     integer NOT NULL,
    created_at   timestamptz NOT NULL
);

CREATE INDEX photos_specimen_id_idx ON photos (specimen_id);
CREATE INDEX photos_file_id_idx ON photos (file_id);

CREATE TABLE journal_entries (
    id           uuid PRIMARY KEY,
    specimen_id  uuid NOT NULL REFERENCES specimens(id) ON DELETE CASCADE,
    author_id    uuid NOT NULL,
    body_md      text NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);

CREATE INDEX journal_entries_specimen_id_idx ON journal_entries (specimen_id);

CREATE TABLE journal_entry_files (
    entry_id   uuid NOT NULL REFERENCES journal_entries(id) ON DELETE CASCADE,
    file_id    uuid NOT NULL REFERENCES files(id) ON DELETE RESTRICT,
    position   integer NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (entry_id, file_id)
);

CREATE INDEX journal_entry_files_file_id_idx
    ON journal_entry_files (file_id);

COMMIT;
