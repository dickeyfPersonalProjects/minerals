# Static Security Audit — 2026-05-22 (V3 pre-launch)

**Bead:** mi-l1eg (polecat-doable static half of mi-z58x).
**Scope:** STATIC / source-level + automated-tooling review only. Source
reading, grep audits, static tooling, and missing security regression
tests. The LIVE active-scanning half (ZAP/Burp/nuclei against staging, TLS
inspection, manual IDOR probing with real requests) is operator-only and
tracked separately.

**Auditor:** polecat chrome.

## Summary

| # | Finding | Severity | Status |
|---|---------|----------|--------|
| F1 | IDOR: `GET /api/v1/files/{file_id}` served any non-attachment file (incl. private photo originals) with no visibility check | **CRITICAL** | **FIXED in this branch** + regression test |
| F2 | Journal-entry create does not authorize the parent specimen | MEDIUM | fix-bead mi-f5sm |
| F3 | `PUT /specimens/{id}/collectors` does not authorize the supplied collector_ids | MEDIUM | fix-bead mi-6863 |
| F4 | `POST /qr-sheet/specimens` does not visibility-check the added specimen | LOW–MEDIUM | fix-bead mi-os89 |
| F5 | Image decode has no pixel-dimension cap (decompression-bomb DoS) | MEDIUM | fix-bead mi-z4n6 |
| F6 | CSP `style-src 'unsafe-inline'` (global + /docs) | MEDIUM | fix-bead mi-97cl |
| F7 | `/readyz` and huma `collectDetails` echo raw dependency/framework error strings | LOW | fix-bead mi-f5v3 |
| T1 | CI: no `npm audit` (frontend SCA) | tooling gap | **ADDED in this branch** |
| T2 | CI: no `trivy` image/dependency scan | tooling gap | **ADDED in this branch** |
| T3 | CI: CodeQL not wired (bead assumed it was) | tooling gap | fix-bead mi-txj0 |

**Clean areas (verified, no findings):** SQL injection (all pgx queries
fully parameterized), markdown/HTML XSS (bluemonday applied at every render
path; the two frontend `{@html}` sinks feed sanitized/controlled data),
visibility/redaction chain (consistently applied across list, detail,
search, photo list, raw photo bytes), secret logging (no tokens / secrets /
cookies logged anywhere), and the rest of the security-header set.

---

## Authorization / IDOR audit

How authz works: a per-resource `authzGuard` (`internal/api/authz.go`) wraps
the Casbin enforcer. `checkView`/`checkViewHTTP` rewrite a 403 into a 404 so
existence is not leaked. Writes and private reads go through `authz.Enforce`
(`internal/authz/enforce.go:21`); `own` matches `obj.AuthorID == subID`.
Photos inherit owner/visibility from their parent specimen.

Every by-id handler was read. The byte-serving photo routes
(`/photos/{id}`, `/display`, `/thumb`) correctly enforce parent-specimen
view access + per-photo redaction before serving
(`internal/api/photos.go:799,807`).

### F1 — CRITICAL — IDOR in `GET /api/v1/files/{file_id}` (FIXED)

`downloadOriginal` (`internal/api/journal_files.go`) served bytes for any
`file_id`. It enforced authorization only when the file was a *journal
attachment*; for any other file it fell through and streamed the bytes with
no check. The whole enforcement block was additionally gated on
`s.authz.active()`, so even attachments served unguarded when the enforcer
was inactive.

Photo originals are stored in the **same `files` table**
(`internal/api/photos.go` writes key `files/{fileID}`), and each photo's
`file_id` is exposed in `PhotoView.FileID`. UUIDv7 ids are time-ordered and
enumerable. Any caller who learned or guessed another user's private photo
`file_id` could download the original bytes via this route, bypassing the
parent-specimen visibility gate that `/photos/{id}` enforces. The route is
also `OptionalAuth`, so even an anonymous caller could reach it.

**Fix (this branch):** the download path now resolves the journal
attachment first and serves **only** journal attachments. Any non-attachment
file (notably a photo original) returns 404 — default-deny on unknown
provenance, 404 rather than 403 to avoid leaking existence. The parent-entry
view check remains gated on the enforcer being active; the
serve-only-attachments decision is not. Photos continue to be served
exclusively through `/photos/{id}`.

**Regression test:** `TestJournalFileDownload_RejectsNonAttachmentFile`
(`internal/api/journal_files_test.go`) seeds a file with no attachment row
(a photo-original shape) and asserts the download path 404s and does not leak
the bytes. Confirmed it fails against the pre-fix code and passes after.

The only legitimate client of `/api/v1/files/{file_id}` is
`frontend/src/lib/JournalAttachments.svelte:350` (journal attachments) —
photos use `/photos/{id}` — so this change breaks no real client.

### F2 — MEDIUM — Journal create does not authorize the parent specimen

`create` (`internal/api/journal.go:193`) checks only
`journal:own:create` (`:209`) and never loads/authorizes the parent
specimen at `in.SpecimenID`. A user can attach a journal entry to another
user's specimen (FK is the only protection). Mirrors the photo-upload path,
which DOES enforce (`enforcePhoto(..., actCreate)`). **Fix:** load the parent
specimen and require `actEdit` (or `actView`) before creating the entry.

### F3 — MEDIUM — `PUT /specimens/{id}/collectors` does not authorize collector_ids

`internal/api/specimen_collectors.go:140` authorizes editing the specimen
(`:153`) but never authorizes the `collector_ids` in the body. Collectors
are owned-only. A caller can link another user's private collector to their
own specimen; `GetChain` then embeds that collector's full `CollectorView`
(name, notes) into the response, disclosing private collector data. **Fix:**
for each `collector_id`, load it and require `actView`/`actEdit` before
`ReplaceChain`.

### F4 — LOW–MEDIUM — `POST /qr-sheet/specimens` does not visibility-check the added specimen

`internal/api/qr_sheets.go:288` authorizes the sheet (own) but adds an
arbitrary `specimen_id` without checking the caller can see it. `loadView`
then surfaces the specimen's name + first-photo thumbnail URL, leaking
another user's private specimen name/thumbnail onto the caller's own sheet.
**Fix:** resolve the specimen and require `actView` before `AddSpecimen`.

---

## Visibility / redaction audit — CLEAN

The request-scoped `redactor` (`internal/api/redact.go`) drops per-field
scalars (`redactSpecimen`) and hidden photos (`filterPhotos`) using
`internal/visibility/visibility.go`. Every endpoint that emits redactable
specimen/photo data to a potentially-foreign viewer applies the same chain:

| Endpoint | Redaction |
|----------|-----------|
| `GET /specimens` (list + `?q=` search + `?scope=`) | `redactSpecimen` per row (`specimens.go:463`) |
| `GET /specimens/{id}` | `redactSpecimen` (`specimens.go:489`) |
| `PATCH /specimens/{id}` | `redactSpecimen` (`specimens.go:663`) |
| `GET /specimens/{id}/photos` | parent view-check + `filterPhotos` (`photos.go:589`) |
| `GET /photos/{id}` / `/display` / `/thumb` | `enforcePhotoHTTP` + `canSeePhotoHTTP` (`photos.go:799,807`) |
| `POST /specimens`, `POST .../photos`, `PATCH /photos/{id}` | no redactor — owner-only, auth-gated, returns caller's own resource (redactor would no-op) |
| `GET /qr-sheet*`, `POST /qr-sheet/pdf` | owner-scoped; view shape carries no redactable scalar |

Search is the `?q=` branch of the list handler and is redacted identically —
no separate leaky search path. No leak findings.

**Test-coverage note:** the redactor unit logic is well covered
(`redact_test.go`), but handler tests run with the enforcer seam inactive, so
there is no end-to-end test wiring a live enforcer + a foreign viewer through
each data-returning endpoint. Recommended (integration-tier, needs Postgres):
a per-endpoint leak test asserting a non-owner receives the row with
`price_cents`/`acquired_from`/`catalog_number` omitted and hidden photos
absent. Tracked in the F1 fix-bead's follow-up / see T-note below.

---

## Injection / upload audit

### SQL — CLEAN
All Postgres access uses `pgx` with full parameterization. The dynamic
WHERE-builders (`internal/db/specimen_postgres.go`, `collector_postgres.go`,
`photo_postgres.go`, `journal_entry_postgres.go`, `mineral_species_postgres.go`)
emit only `$N` placeholders (`$%d` from `len(args)+1`) and append values to
`args`. The `ownerScope`/`*Scope` helpers (`internal/db/shares.go`)
interpolate table/column names but every call site passes hardcoded
literals. LIKE search wraps user input with `escapeLike` and stays
parameterized. Bootstrap CLI interpolates only a compile-time constant
column slice. No dynamic SQL from user values.

### Markdown / HTML XSS — CLEAN
`body_html` is server-rendered via `Markdown.RenderString` (bluemonday
strict allowlist, `internal/markdown/markdown.go`) at every journal path
(`journal.go:202,248,276,314`). The frontend has exactly two `{@html}`
sinks: `SpecimenDetail.svelte:1132` (fed the server-sanitized `body_html`)
and `QrCode.svelte:73` (library-generated SVG from a controlled URL). Both
safe. Note: `specimen.description` is documented as markdown but stored raw
and rendered via auto-escaping text interpolation (`{specimen.description}`)
— not XSS-exploitable today, but a latent risk if a future change switches
it to `{@html}` without sanitizing. Defense-in-depth nit, no action required.

### F5 — MEDIUM — Decompression-bomb: no pixel cap before image decode
`internal/storage/imageproc/imageproc.go:75-89` (`decode`) calls
`jpeg.Decode`/`png.Decode`/`image.Decode` on the full upload with no
`DecodeConfig` pre-check. The 100 MiB byte cap bounds only the *compressed*
input; a few-KB image declaring 30000×30000 decodes to ~3.6 GB RGBA →
memory-exhaustion DoS. **Fix:** read width/height via the format's
`DecodeConfig` before decoding, reject when `width*height` exceeds a cap
(e.g. 40–100 MP), then decode.

**What's already safe in the upload path:** content-type allowlists +
415 on rejection (`photos.go:370`, `journal_files.go:251`); size caps via
`http.MaxBytesReader` + explicit 413 (`photos.go:233/381`,
`journal_files.go:187/259`); **no path traversal** — object keys are always
`"files/" + serverGeneratedUUIDv7`, user filenames never reach keys, disk
paths, or response headers; EXIF allowlist-filtered (GPS/XMP/MakerNotes
stripped).

---

## Headers / CSP / error-leak / secret-logging

Global headers (`internal/api/middleware.go:150-174`, wired
`server.go:270`): `script-src 'self'` (no `unsafe-inline`/`unsafe-eval`),
`connect-src 'self'` (BFF design — SPA never calls Keycloak directly, so
`'self'` is tighter than an issuer allowlist), `frame-ancestors 'none'` +
`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `Permissions-Policy` present, HSTS on
`X-Forwarded-Proto: https`, `base-uri`/`form-action 'self'`. The `/docs`
Redoc CSP override SRI-pins the CDN script. All required checks pass.

### F6 — MEDIUM — `style-src 'unsafe-inline'`
Present in the global CSP (`middleware.go:167`) and the `/docs` override
(`server.go:507`). Permits inline styles → widens CSS-injection surface.
script-src is clean. **Fix:** move SPA styles to hashed/nonce'd stylesheets
and drop `'unsafe-inline'` from the global `style-src`; the `/docs` override
can keep it (Redoc requires it).

### F7 — LOW — Internal error strings echoed to clients
(a) `/readyz` (`server.go:374/389/405`) puts raw dependency `err.Error()`
into the JSON response — confirm the endpoint is not reachable
unauthenticated from the public internet, else redact the `error` field for
external callers. (b) `collectDetails` (`huma_errors.go:116-131`) forwards
raw huma framework error strings into `details.errors`; safe for validation
messages but the one client-facing non-static error path — whitelist which
error types get echoed. The normal request path leaks no stack traces or
file paths: the recover middleware logs `debug.Stack()` server-side only and
returns a generic 500 (`middleware.go:126-142`).

### Secrets in source / git history / bundle — CLEAN (with a caveat)
A pattern-based scan of the full git history (`git grep` across all
commits for private-key blocks, `AKIA…` keys, and `client_secret=…`
literals, excluding test/example/placeholder values) and of the built
`frontend/dist/` bundle (API keys, OIDC client secret) found nothing —
consistent with the BFF design where the SPA holds no secrets.
**Caveat:** `gitleaks`/`trufflehog` are not installed in the polecat
workspace and may not be installed by policy, so a dedicated entropy-based
history scan was NOT run here. The added Trivy CI job runs the `secret`
scanner over the working tree on every PR; a one-time full-history
`gitleaks` pass is recommended operator-side (folded into the operator
runbook bead).

### Secret logging — CLEAN
No tokens, secrets, passwords, or cookie values are logged anywhere in
`internal/auth/`, `internal/oidc/`, or `huma_auth.go`. Verify failures log
only wrapped errors (`oidc/verifier.go` explicitly wraps so the raw token
never appears); session middleware logs `user_id`/`sub` (non-secret stable
identifiers). `ClientSecret` is never passed to a log call. Cookie is
`HttpOnly` and never logged.

---

## CSRF — protected (verified)

Stored-synchronizer CSRF (`internal/auth/bff/csrf.go`): `X-CSRF-Token`
header, constant-time compare against `sess.CSRFToken`, three documented
bypass branches (safe methods, login, the GET /csrf endpoint). Covered by
`internal/auth/bff/csrf_test.go` (10 tests). No finding.

---

## CI security tooling

| Tool | Before | Action |
|------|--------|--------|
| govulncheck (Go SCA) | wired in `pr.yml`/`main.yml` via `make vulncheck` (`govulncheck ./...`, exits non-zero on any reachable vuln) | confirmed — no change |
| npm audit (frontend SCA) | absent | **added** `npm audit --audit-level=high` to the frontend job (`pr.yml`) |
| trivy (image + dep scan) | absent | **added** a `trivy` filesystem+config scan job to `pr.yml`, `--severity HIGH,CRITICAL --exit-code 1` |
| CodeQL | **not wired** (the bead assumed it was) | fix-bead filed — adding the CodeQL workflow needs GH Advanced Security entitlement verification, out of a polecat's local-validation reach |

---

## Out of scope (operator runbook bead)

Live active scanning (ZAP/Burp/nuclei vs staging), TLS protocol/cert
inspection of the running endpoint, manual request-crafting / IDOR probing
against the live app, and anything requiring a browser-driving attack proxy
or a session against the deployment.
