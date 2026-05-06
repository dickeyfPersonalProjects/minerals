# §3 — Photo & file handling

Decided 2026-05-06 in design session.

## Summary

All uploads and downloads in v1 are proxied through the Go server — the
browser never speaks directly to MinIO. Storage is one bucket per
environment, with flat UUID-keyed object names. Image uploads
synchronously generate two derived variants (display, thumbnail);
non-image uploads store only the original. EXIF on stored images is
filtered through an allowlist that keeps photographic metadata (camera,
lens, exposure) and drops everything else (GPS, XMP, IPTC, MakerNotes).
S3 versioning and orphan-cleanup tooling are deferred to v2 — v1 relies
on transactional uploads to keep DB and storage consistent.

## Decisions

- **One MinIO bucket per environment.** Names come from an env var
  (`minerals-dev`, `minerals-prod`). A misconfigured client can't silently
  cross-write — wrong bucket = 403, not "succeeded into the wrong place."
- **Flat UUID-based object keys.** The bucket is dumb storage; the DB owns
  relationships. Concrete layout:
  ```
  files/{file_id}                 — original upload (EXIF allowlisted)
  files/{file_id}.display.jpg     — 1600px JPEG q85   (images only)
  files/{file_id}.thumb.jpg       — 400px JPEG q80    (images only)
  ```
- **`files` table holds both `id` (UUID) and `sha256`.** UUID = identity
  of this upload event (and the storage key). SHA256 = identity of the
  bytes (integrity verification, future-friendly diagnostic dedup, optional
  later migration to content-addressed storage). The same bytes uploaded
  twice produce two `files` rows with two UUIDs and the same SHA256.
- **Server-side variant generation on upload.** Synchronous in the upload
  request; adds 1–3 seconds of latency. Two derivatives: `display` (long
  edge 1600 px, JPEG q85) and `thumbnail` (long edge 400 px, JPEG q80).
- **Variants are NOT first-class DB rows.** They live at predictable keys
  derived from the original's `file_id`. If variant storage is lost, we
  regenerate on demand. Only originals are tracked in `files`.
- **Non-image uploads: original only, no variants.** PDFs, future
  spectrum files, etc. — store the original, no derivative generation.
- **EXIF: allowlist approach (option B).** On upload, before storing:
  1. Read `DateTimeOriginal` → default value for `photos.taken_at`
     (overrideable in the UI).
  2. Filter EXIF through a known-good allowlist of photographic tags
     (camera make/model, lens, ISO, shutter, aperture, focal length, white
     balance, exposure compensation, capture mode, image dimensions, color
     profile, DateTimeOriginal).
  3. Drop everything else: GPS IFD, XMP, IPTC, MakerNotes, embedded-
     thumbnail GPS.
  4. Write the filtered bytes as `original`; generate `display` and
     `thumbnail` from the filtered bytes.
- **Library choice: Go-native EXIF library** (e.g. `dsoprea/go-exif/v3`).
  No `exiftool` binary in the distroless image. If gaps surface, switch to
  shelling out to exiftool is a contained refactor.
- **Photo content-type allowlist.** Accept `image/jpeg`, `image/png`,
  `image/webp`, `image/heic`. Anything else → HTTP 415.
- **Max upload size: 100 MB per file**, configurable via env var
  (e.g. `MAX_UPLOAD_BYTES`).
- **Upload/download paths in v1 are both Go-proxied** (carried forward
  from §1). Direct-S3 download fast-path is deferred until public sharing
  ships and bandwidth becomes visible.

## Deferred to v2 / later

- Direct-S3 (or presigned-GET) download fast path for public specimens.
  Revisit once public sharing ships and we measure bandwidth.
- S3 / MinIO bucket versioning. Non-disruptive to enable later — applies
  from switch-on forward, no migration of existing objects.
- Orphan cleanup job (files in MinIO with no `files`-row reference).
  Should be near-zero in practice given transactional uploads; write a
  reconciliation script if drift is observed.
- Per-specimen / per-photo "preserve full metadata" opt-in (keeping GPS,
  XMP, MakerNotes for forensic provenance use cases).
- Journal-entry attachment content-type allowlist — designed alongside the
  journal feature itself. Same 100 MB ceiling unless discovered otherwise.
- Additional input formats (TIFF, BMP, GIF, RAW). Add when a real need
  surfaces.
- Auto-generation of `taken_at` from filesystem `mtime` when EXIF is
  absent. v1: just leave `taken_at` NULL in that case.

## Open questions / flags

- **Stored "original" is not byte-identical to upload.** EXIF allowlist
  filters out GPS/XMP/IPTC/MakerNotes — useful photographic metadata is
  preserved, but the file's SHA256 is computed over the filtered bytes,
  not the user's source. If forensic preservation of source files ever
  matters, we'll need an opt-in "preserve full metadata" mode (deferred).
- **XMP/IPTC/MakerNotes are dropped wholesale.** Intentional under the
  allowlist approach — rare for collection photos to carry useful data
  there, but worth being explicit. Easier to add a tag back to the
  allowlist later than to discover a leak.
- **HEIC handling.** HEIC support requires a decoder (HEIF library or
  shell-out). Implementation polecat will need to pick a path —
  `goheif`, `libheif` via cgo, or convert to JPEG before processing. Note
  this when implementing; not a blocker for the design.
- **Variant filename collisions are theoretically possible** if two
  uploads ever produce the same `file_id`. UUIDv4 collisions are
  cosmologically unlikely, but the storage put should fail-safe rather
  than overwrite — use conditional puts (`If-None-Match: *`) on the
  initial write.
- **Transactional upload semantics.** "Transactional" here means: write
  the file to MinIO first, then insert the `files` row; on DB-insert
  failure, delete the just-written object. Implementation polecat should
  document the exact ordering and rollback behavior in the upload handler.
