# §1 — Scope & v1 cut line

Decided 2026-05-06 in design session.

## Summary

v1 is a single-overseer cataloging app for personal use on the local network.
It supports specimens with multiple photos, an editable markdown description,
and a placeholder for an append-only observation journal (full journal feature
arrives later). Gamma spectra, photo-kind metadata (visible/UV/etc.),
multi-user, and public-sharing UX are deferred but their schema slots are
reserved from day one to avoid painful migrations later.

## Decisions

- **Gamma spectrum data — DEFERRED.** Not in v1. No `spectra` table or related
  storage in the v1 schema. Revisit when there is a reliable capture workflow.
- **Photos — multiple per specimen in v1, with `taken_at` metadata only.**
  No photo-kind enum (visible/UV/etc.) in v1 schema; that gets added later
  with a column + UI. Multiple photos per specimen and per-photo timestamp
  are required day one.
- **Multi-user — DEFERRED, but planned.** v1 ships with a single-overseer
  stub user. Writable rows (`specimens`, `journal_entries`, `files`, etc.)
  carry an `author_id` column from day one, populated by the stub, so
  enabling real auth later is additive — no schema migration of existing
  rows.
- **Public sharing — eventual goal.** `specimens.visibility` enum
  (`private | unlisted | public`) ships in v1, defaulting to `private`. The
  UI does not need to expose visibility controls in v1, but the column
  exists so we can backfill behavior without touching every row later.
- **Notes — dual-track model (decided pre-§1, captured here for completeness):**
  - Each specimen has one editable markdown `description` field.
  - Each specimen has an "observation journal" of append-only entries
    (creation order is fixed; entry `body_md` and attached files are
    editable post-creation). Journal feature is in v1 schema; full UI may
    arrive incrementally.
- **Uploads / downloads (decided pre-§1, captured here for completeness):**
  - Uploads are Go-proxied (browser → Go → MinIO) — keeps S3 off the
    client and makes SSO integration simpler later.
  - Downloads in v1 are also Go-proxied (see "Open questions" — the
    "direct-S3 download" idea collides with private specimens, so for v1
    we keep one consistent path through Go).

## Deferred to v2 / later

- Gamma spectrum capture, storage, display
- Photo-kind metadata (visible / UV / other) and per-kind UI affordances
- Real multi-user auth (OIDC via Keycloak operator) — v1 ships with the
  middleware slot wired and an `author_id` populated by a stub user
- Public-sharing UX (the column ships; the share-this-specimen flow does
  not)
- Direct-S3 download fast path for public specimens (revisit when public
  access through Cloudflare proxy is on the table and bandwidth becomes
  visible)

## Open questions / flags

- **Download path under private specimens.** If/when we want a direct-S3
  fast path, we will need to split files by visibility (public bucket vs
  private bucket, or presigned-GET URLs that expire). For v1 this is
  resolved by routing all downloads through Go; revisit when public sharing
  ships.
