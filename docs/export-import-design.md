# User Data Export / Import — Design Spec (v1)

> Bead: `mi-dkuu` — V3-prerequisite. Goals: user data ownership/portability, a
> user-controlled backup against data loss, and GDPR-style data-access compliance.
>
> This document is the **design pass** the bead requested. It defines the portable
> archive format (versioned), the export/import endpoint contracts, the safety and
> conflict policies, and the sub-bead split for implementation. It is intentionally
> implementation-agnostic about exact column lists: the canonical Go models in
> `internal/models/*.go` are the source of truth for per-entity fields, and the
> serializers must round-trip **every** persisted, user-owned field (including
> per-field visibility settings).

## 1. Background & architecture context

The backend is a Go service (`github.com/francoisdickey/minerals`):

- **Router**: chi, `internal/api/router.go`. All data endpoints live under `/api/v1`
  inside an authenticated group guarded by `middleware.RequireUser`. The caller's
  identity (Keycloak `sub`) is read with `middleware.UserID(ctx)`.
- **Persistence**: per-entity stores in `internal/store/*.go` over `database/sql`
  (Dolt/MySQL), raw SQL, transactions via `db.BeginTx`.
- **Object storage**: `internal/storage` exposes an `ObjectStore` interface (MinIO).
  Image binaries live in the primary bucket; a `WebBucket` mirror exists (mi-a3pt).
- **Owned entities** (all rows carry `author_id`): `specimens`, `collectors`,
  `journal_entries`, `qrsheets` + `qrsheet_specimens` (join), `specimen_images`.
- **Closest precedent**: GDPR erasure — `internal/api/handlers/account.go` +
  `internal/store/account_store.go`. Its `AccountStore` already (a) enumerates a
  user's image object keys and (b) deletes all of a user's rows across every table
  in one transaction in FK-safe order. **Export is the read-only inverse of that
  enumeration; import is the re-homing inverse of the writes.** Reuse the same
  table set and ordering knowledge.

## 2. Archive format

### 2.1 Container

A **ZIP** archive (`application/zip`), streamed (never fully buffered in memory).
Layout:

```
minerals-export.zip
├── manifest.json                 # archive-level metadata + schema version
├── data/
│   ├── collectors.jsonl          # one JSON object per line, per entity type
│   ├── specimens.jsonl
│   ├── journal_entries.jsonl
│   └── qrsheets.jsonl            # includes embedded specimen-id membership
└── images/
    └── <specimenId>/<imageId>.<ext>   # binary image objects, grouped by specimen
```

JSONL (newline-delimited JSON) is used for entity collections so both export and
import can **stream** row-by-row without holding the whole dataset in memory —
important for large collections.

### 2.2 `manifest.json`

```json
{
  "schemaVersion": 1,
  "application": "minerals",
  "exportedAt": "2026-05-29T00:00:00Z",
  "exportedBy": "<keycloak-sub-of-exporter>",
  "counts": { "collectors": 0, "specimens": 0, "journalEntries": 0,
              "qrsheets": 0, "images": 0 },
  "images": [
    { "imageId": "...", "specimenId": "...", "path": "images/<sid>/<iid>.jpg",
      "contentHash": "sha256:...", "contentType": "image/jpeg",
      "width": 0, "height": 0, "byteSize": 0 }
  ]
}
```

- `schemaVersion` gates import compatibility. Bump on any breaking change; import
  validates it before writing anything and refuses unknown/newer majors with a
  clear error.
- `images[]` is the authoritative file↔specimen mapping and integrity record
  (content hash) so import can verify each binary and re-associate it.

### 2.3 Entity records (`data/*.jsonl`)

Each line is the full JSON serialization of one entity **including all persisted
fields and per-field visibility settings**, serialized from the canonical
`internal/models` structs. IDs are preserved in the archive (used only for
intra-archive references such as `journal_entries.specimenId`,
`qrsheet_specimens`, and `images[].specimenId`). On import, IDs are **remapped**
(see §4.3) — they are not authoritative across import.

`author_id` is **omitted** from per-entity records (or ignored on import): export
is always the caller's own data, and import always re-homes to the importer.

## 3. Export endpoint

`GET /api/v1/export` (authenticated).

- Scope: strictly `author_id == caller`. Never another user's data.
- Streams a ZIP to the response with
  `Content-Disposition: attachment; filename="minerals-export-<date>.zip"`.
- Pipeline: write `manifest.json` header → stream each `data/*.jsonl` from a
  keyset-paginated `ListByAuthor` query → stream each image by `GetObject` from
  MinIO straight into the zip entry. No full-collection buffering.
- v1 is **synchronous, all-or-nothing** (full collection only; no subset).
- **Open risk**: very large collections / many images may exceed request
  timeouts. v1 ships sync; if it proves insufficient, the async-job sub-bead
  (§6.5) adds a job + status-poll + signed download link. Add a configurable
  soft size/count cap that, when exceeded, returns `413` with guidance to use
  the async path.

## 4. Import endpoint

`POST /api/v1/import` (authenticated), `multipart/form-data` (or raw zip body).

### 4.1 Two-phase: validate → commit

1. **Dry-run / validate** (`?dryRun=true` or always run first internally):
   parse `manifest.json`, check `schemaVersion`, structural integrity, that every
   `images[].path` exists in the zip and hashes match, and that referential
   integrity holds within the archive. Produce a **report** (counts, conflicts,
   warnings) and write nothing. Mirrors the bootstrap-claim-orphans ergonomics
   (mi-c1y): dry-run + report, then commit.
2. **Commit**: only after validation passes.

### 4.2 Transactionality

- DB writes for one import run happen in a single transaction (or per-specimen
  sub-transactions with a clear per-item report) so a mid-import failure does not
  leave a half-imported collection. Recommend **all-or-nothing per archive** for
  v1 (simplest correct behavior); revisit per-specimen partial commit later.
- Image re-uploads to MinIO happen **after** the DB commit (best-effort, like
  erasure's MinIO cleanup ordering), with the import report flagging any object
  that failed to upload so it can be retried.

### 4.3 Re-homing & ID remapping

- Every created row gets `author_id = caller` regardless of `exportedBy`.
- All entity IDs are regenerated on import; an in-memory old→new ID map rewrites
  cross-references (journal→specimen, qrsheet membership, image→specimen, image
  object keys under the importer's key namespace).

### 4.4 Conflict & dedup policy (v1)

- **catalog_number collisions** within the importer's existing data: do **not**
  silently overwrite. Default = import as a new specimen and record the collision
  in the report (optionally suffix/annotate). Never clobber existing rows.
- **Idempotency / re-import**: v1 does not dedup across runs — re-importing the
  same archive creates a second copy, and the dry-run report makes this visible
  before commit. (A content-hash-based skip is a later enhancement.)
- Import must **never** modify or delete the importer's pre-existing data.

## 5. Acceptance (from the bead) → how this design meets it

- Export full collection + images, downloadable → §3.
- Re-import recreates the collection owned by the importer → §4.3.
- Round-trip fidelity (specimens, fields, visibility, images, journal) →
  §2.3 serializes all persisted fields incl. visibility; §2.2 hashes images.
- Validates & refuses malformed/incompatible archives with a clear error → §4.1.
- Scoped to caller on export, re-homed on import → §3, §4.3.

## 6. Sub-bead split (for implementation)

1. **(this doc)** Export format schema + versioned spec.
2. Backend export endpoint — streaming ZIP (data + images), author-scoped.
3. Backend import endpoint — validate (dry-run + report), transactional commit,
   re-home, ID remap, conflict policy.
4. Frontend Export/Import UI (Settings or a dedicated Data page).
5. Async-job mechanism (job + status poll + signed download link) — only if the
   sync path proves insufficient for large collections.
6. E2E round-trip test (export → import as same and different user → assert
   fidelity).

Dependency shape: 2 and 3 depend on 1; 4 depends on 2 and 3; 6 depends on 2 and 3;
5 depends on 2 (and is conditional).

## 7. Open questions to resolve during implementation

- Exact soft cap thresholds for sync vs async (count + total bytes).
- Whether to embed `qrsheet_specimens` membership inside `qrsheets.jsonl`
  (recommended) or as a separate file.
- Per-specimen partial-commit reporting vs strict all-or-nothing (v1 = strict).
- Content-hash-based dedup on re-import (deferred past v1).
