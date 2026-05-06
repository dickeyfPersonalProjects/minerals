# §2 — Domain model

Decided 2026-05-06 in design session.

## Summary

A single `specimens` table with a `type` enum (`mineral | rock | meteorite`)
and shared common columns, plus a `type_data` JSONB column holding
type-specific fields whose canonical shape is defined by Go structs in app
code. Photos, journal entries, and journal-entry attachments are separate
tables that all reference a shared `files` table. Provenance ("ex
collection") is normalized into `collectors` + `specimen_collectors`
join, so canonical names are reusable, queryable, and editable in one
place.

The shape favors cross-type queries (one home view, one search) over
per-type purity. JSONB carries type-specific data without column explosion;
typed Go structs keep that data disciplined at the application boundary.

## Decisions

- **Single `specimens` table with `type` enum + JSONB `type_data`.** Common
  columns are shared; type-specific fields live in JSONB validated against
  Go structs (`MineralData`, `RockData`, `MeteoriteData`).
- **Catalog numbering: UUID PK + nullable human `catalog_number`.** UUIDs
  in URLs (immutable, never collide). `catalog_number` is a unique nullable
  text column the overseer fills in when desired. Specimens can be created
  without one. Auto-generation (e.g. `FD-2026-0042`) is a future
  enhancement, not v1.
- **Locality: `locality_text` (free-form, primary display) + `locality`
  JSONB (optional structured fields).** Structured shape:
  `{country, region, site, lat, lon, mindat_id}`, all optional. Search and
  filter operate on the structured side; the text side is for "show
  exactly what I wrote."
- **Provenance: tracked in v1, normalized via `collectors` table +
  `specimen_collectors` join.** Canonical collector names (e.g. "Eric
  Quinter", "Gilbert Gauthier") are entities. Editing a name updates every
  specimen at once. Order in the chain is preserved via a `position`
  column on the join row.
- **`type_data` discipline: typed Go structs per type (option 2.4a).**
  Marshalled to/from JSONB at the repo boundary. Adding a field to a type
  is a code change + zero schema migration.
- **`author_id` on every writable row from day one.** Stub user populates
  it in v1; real auth fills it in later without schema migration. Applies
  to `specimens`, `journal_entries`, `files`, and `collectors`.

## Indicative schema sketch (v1)

This is the shape we're committing to; exact column names and types may be
refined when migrations land. Authoritative artifact is the migration
files in `migrations/`, not this document.

```
specimens
  id            uuid PK
  type          enum('mineral','rock','meteorite')
  catalog_number text UNIQUE NULL
  name          text NOT NULL
  description   text NOT NULL DEFAULT ''      -- editable markdown
  visibility    enum('private','unlisted','public') NOT NULL DEFAULT 'private'
  author_id     uuid NOT NULL
  acquired_at   date NULL
  acquired_from text NULL                     -- where it was acquired (transaction)
  price_cents   bigint NULL
  source_notes  text NULL
  locality_text text NULL
  locality      jsonb NULL                    -- {country, region, site, lat, lon, mindat_id}
  mass_g        numeric NULL
  dimensions    jsonb NULL                    -- {length_mm, width_mm, height_mm}
  type_data     jsonb NOT NULL DEFAULT '{}'
  created_at    timestamptz NOT NULL
  updated_at    timestamptz NOT NULL

collectors
  id            uuid PK
  name          text NOT NULL UNIQUE          -- canonical name; edits propagate
  notes         text NULL
  created_at    timestamptz NOT NULL
  updated_at    timestamptz NOT NULL

specimen_collectors
  specimen_id   uuid FK → specimens.id
  collector_id  uuid FK → collectors.id
  position      int NOT NULL                  -- ordering in provenance chain
  created_at    timestamptz NOT NULL
  PRIMARY KEY (specimen_id, collector_id)

photos
  id            uuid PK
  specimen_id   uuid FK → specimens.id
  file_id       uuid FK → files.id
  taken_at      timestamptz NULL              -- v1: only photo metadata
  position      int NOT NULL
  created_at    timestamptz NOT NULL

journal_entries
  id            uuid PK
  specimen_id   uuid FK → specimens.id
  author_id     uuid NOT NULL
  body_md       text NOT NULL                 -- editable markdown
  created_at    timestamptz NOT NULL          -- the timestamp; immutable
  updated_at    timestamptz NOT NULL

journal_entry_files
  entry_id      uuid FK → journal_entries.id
  file_id       uuid FK → files.id
  position      int NOT NULL
  created_at    timestamptz NOT NULL
  PRIMARY KEY (entry_id, file_id)

files
  id            uuid PK
  s3_key        text NOT NULL                 -- canonical MinIO key
  content_type  text NOT NULL
  byte_size     bigint NOT NULL
  sha256        text NOT NULL                 -- content hash
  uploaded_at   timestamptz NOT NULL
  uploaded_by   uuid NOT NULL
```

## Type-specific data shapes (initial — grow organically)

These are the v1 starting shapes for `specimens.type_data`, owned by Go
structs. Fields are added by code change, not schema migration.

**MineralData** (placeholder — refine when first mineral lands)
- `chemical_formula` text?
- `mineral_species` []text?     (a specimen can be multi-species)
- `crystal_system` text?
- `mohs_hardness` numeric?
- `color` text?
- `luster` text?
- `fluorescence` text?           (free-form for v1; structured later)
- `radioactive` bool?
- `mindat_id` text?

**RockData** (placeholder)
- `rock_type` enum('igneous','sedimentary','metamorphic')?
- `composition` text?
- `formation_context` text?

**MeteoriteData** (placeholder)
- `classification` text?         (e.g., "L6", "CV3")
- `fall_or_find` enum('fall','find')?
- `fall_or_find_date` date?
- `official_name` text?
- `total_known_weight_g` numeric?
- `metbull_ref` text?

## Deferred to v2 / later

- Auto-generation of `catalog_number` from a configurable template
- Photo-kind metadata (visible / UV / other) — column added when the UI
  needs it (§1 decision)
- Strict schema validation of `type_data` at the DB level (e.g.
  `jsonb_path_exists` constraints) — for v1, app-side validation via the
  Go structs is sufficient
- Promotion of any heavily-used JSONB field into a sidecar table (e.g.
  `meteorite_details`) — a backwards-compatible refactor we can do once
  query patterns reveal pressure
- Collector merging UI (combining two near-duplicate collector entries
  into one)

## Open questions / flags

- **`type_data` on type change.** If a specimen is reclassified
  mid-collection (rare but possible — e.g. an unidentified rock turns out
  to be a meteorite), changing `type` invalidates the old `type_data`.
  v1 policy: the API rejects type changes; reclassification means
  delete + re-create. Revisit if this becomes annoying.
- **Locality structured fields are unconstrained JSONB.** Validation lives
  in the Go layer (`Locality` struct). If we ever want to enforce required
  combinations (e.g. lat without lon is invalid), enforcement is in app
  code, not DB.
- **Photo `position` and journal-entry-file `position` are app-managed.**
  Reordering is a multi-row update. Acceptable for v1 (a specimen rarely
  has more than ~20 photos), but if we ever hit hundreds of attachments
  per entry, fractional indexing might be worth the extra complexity.
