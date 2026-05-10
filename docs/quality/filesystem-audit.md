# Backend filesystem usage audit (H-1)

> Scope: every Go source file under `cmd/` and `internal/`, audited
> against CONTRACT.md §17 ("Filesystem usage"). The rule: the
> production server runs with `readOnlyRootFilesystem: true` and
> the only writable path is `/tmp` (emptyDir). Every write to disk
> must (a) target `/tmp` and (b) clean up after itself.
>
> Date: 2026-05-10. Bead: `mi-kiz`. Cross-refs: PR #47 (the
> manifest fix that exposed the latent multipart-spillover bug),
> PR #16 (photo pipeline).

## TL;DR

The production server (`minerals serve`) writes to disk in **two
places**, both via huma's `MultipartFormFiles` → stdlib
`(*http.Request).ParseMultipartForm(8 * 1024)` spillover path:
the photo upload and journal-file upload handlers. Both now have
`defer in.RawBody.Form.RemoveAll()` + `defer form.File.Close()`
covering early returns, so multipart tempfiles are unlinked
before the handler returns. **No `os.CreateTemp`, no
`ioutil.*`, no other tempfile creation, no writes outside `/tmp`
are present in the server's runtime path.** The dev-only
`minerals migrate create` subcommand writes scaffolds to
`migrations/` on the developer workstation; it never runs in the
production container, so §17 does not apply.

| Surface | File:lines | Writes | Where | §17? |
|---|---|---|---|---|
| Photo upload | `internal/api/photos.go:219–239` | multipart spillover (>8 KiB body) | `$TMPDIR` (`/tmp` in prod) | ✅ compliant |
| Journal-file upload | `internal/api/journal_files.go:204–222` | multipart spillover (>8 KiB body) | `$TMPDIR` (`/tmp` in prod) | ✅ compliant |
| Image variant generation | `internal/storage/imageproc/imageproc.go` | none — pure `bytes.Buffer` | n/a | ✅ N/A |
| Migration scaffold (dev) | `cmd/minerals/migrate.go:131–154` | scaffold `.up.sql` / `.down.sql` | `migrations/` (workstation) | ✅ N/A — dev tool |
| Stdout/stderr logging | `cmd/minerals/main.go`, `serve.go`, `openapi.go` | none — `os.Stdout` writes | n/a | ✅ N/A |

---

## 1. Method

The audit enumerated every potential filesystem-write site by
greping the non-vendored, non-test Go tree for the following
patterns (per the bead's checklist):

```bash
grep -rn -E "os\.(Create|OpenFile|WriteFile|MkdirAll|Mkdir|CreateTemp|TempFile|TempDir|Remove)" \
  --include="*.go" .
grep -rn -E "(ioutil\.|MultipartReader|ParseMultipartForm|RawBody)" \
  --include="*.go" .
```

Hits were classified into:

1. **Runtime writes** — code reachable from `minerals serve`
   (the production-container entrypoint).
2. **Dev-only writes** — code only reachable from CLI subcommands
   that don't run in the production container.
3. **Non-writes** — `os.Stdout`/`os.Stderr` (not filesystem),
   `os.Args`, `os.Exit`, type names containing `Create` (e.g.
   `domain.PhotoRepo.Create`).

Only category (1) is in scope for §17.

---

## 2. Runtime writes (`minerals serve` path)

### 2.1 Multipart upload spillover — photo handler

**File:** `internal/api/photos.go:219–239`

```go
func (s *PhotoService) upload(ctx context.Context, in *uploadPhotoInput) (*uploadPhotoOutput, error) {
    // CONTRACT.md §17: huma decodes the multipart form before this
    // handler runs, so by the time we get here any body larger than
    // huma's MultipartMaxMemory (8 KiB default) has already spilled to
    // /tmp. Schedule cleanup first thing so every early return — UUID
    // parse, content-type rejection, oversize, downstream errors —
    // unlinks the tempfile. ...
    form := in.RawBody.Data()
    defer func() {
        if in.RawBody.Form != nil {
            _ = in.RawBody.Form.RemoveAll()
        }
    }()
    defer func() { _ = form.File.Close() }()

    specimenID, err := parseUUID(in.SpecimenID, "id")
    if err != nil {
        return nil, err
    }
    ...
}
```

**What's written:** when a request body exceeds huma's
`MultipartMaxMemory` (`8 * 1024` in
`adapters/humago/humago.go:19`, the adapter wired up in
`internal/api/server.go:118`), the stdlib's
`(*http.Request).ParseMultipartForm` calls
`os.CreateTemp("", "multipart-*")` to spill the part body to
disk. Photo uploads are JPEG/PNG/WebP up to
`MaxUploadBytes` (per CONTRACT §12 — currently 50 MiB by
config) so spillover is the **expected** path, not the edge
case. **Crucially, huma performs this parse during input
decoding, before the handler is invoked** — so the tempfile is
already on disk by the time `upload()` runs, and any return path
that happens before deferred cleanup leaks it.

**Cleanup:** `defer in.RawBody.Form.RemoveAll()` removes the
on-disk tempfile (and any sibling parts) before the handler
returns; `defer form.File.Close()` releases the
`*multipart.File` (an `*os.File` handle on Linux). Both defers
are registered as the **first work the handler does**, before
even parsing the path UUID — so every error branch (400 invalid
UUID, 415 unsupported media type, 413 oversized payload, 400
read error, transaction failures, downstream storage failures,
etc.) cleans up.

**§17 status:** ✅ compliant.

### 2.2 Multipart upload spillover — journal-file handler

**File:** `internal/api/journal_files.go:204–222`

Identical structure to §2.1 — same huma adapter, same
`MultipartFormFiles[T]` shape, same top-of-handler defer
pattern. Allowed content types differ (image + PDF + plain text
per `isAllowedJournalContentType`) but the spillover semantics
are the same. The journal handler additionally does an entry
existence check after the cleanup defers; placing the defers
above that check ensures a 404 on a stale entry id still
unlinks the spilled tempfile.

**§17 status:** ✅ compliant.

### 2.3 Image variant generation

**File:** `internal/storage/imageproc/imageproc.go`

`Generate(data []byte, contentType string) (Variants, error)`
decodes via stdlib `image/jpeg`, `image/png`, and
`golang.org/x/image/webp`, resizes via `golang.org/x/image/draw`,
and re-encodes via `image/jpeg.Encode(w io.Writer, ...)` into a
`bytes.Buffer`. The whole pipeline is in-memory; no `os.*` or
`ioutil.*` calls. The package's only allocation is heap memory,
which the runtime garbage-collects.

**§17 status:** ✅ compliant by construction (no filesystem access).

### 2.4 Logging

**Files:** `cmd/minerals/main.go:19`, `cmd/minerals/serve.go:210`,
`internal/api/middleware.go` (logging middleware).

All structured logging routes through `slog` to `os.Stdout`. The
production manifest captures stdout/stderr via the container
runtime; the application never opens a log file on disk.

**§17 status:** ✅ compliant (not a filesystem write).

---

## 3. Dev-only writes (out of §17 scope)

### 3.1 Migration scaffold

**File:** `cmd/minerals/migrate.go:108–158` (`migrateCreate`).

```go
dir := "migrations"
if err := os.MkdirAll(dir, 0o755); err != nil { ... }
...
if err := os.WriteFile(upPath, []byte(""), 0o644); err != nil { ... }
if err := os.WriteFile(downPath, []byte(""), 0o644); err != nil { ... }
```

**Reachability:** `migrateCreate` is dispatched from
`runMigrate(args)` only when `args[0] == "create"`. The
production container's command in
`kustomize/base/deployment.yaml` invokes `minerals serve` (and,
separately, `minerals migrate up` as a Job for forward
migrations). `minerals migrate create` is a developer-only
scaffold step (it generates new `NNNN_name.up.sql` /
`*.down.sql` pairs in the on-disk `migrations/` directory of the
working checkout). It is never run inside a pod, and the rule in
§17 is scoped to "the application" with
`readOnlyRootFilesystem: true`.

**§17 status:** N/A — dev tool, not the runtime container. The
function header comment in `migrate.go:108` already says so:
*"writes new files to the on-disk migrations/ directory and does
NOT touch the database."*

### 3.2 OpenAPI dump

**File:** `cmd/minerals/openapi.go:210–213`

```go
if _, err := os.Stdout.Write(rec.Body.Bytes()); err != nil { ... }
if _, err := os.Stdout.Write([]byte("\n")); err != nil { ... }
```

Writes the OpenAPI 3.1 spec to **stdout** (consumer redirects to a
file as needed). Not a filesystem write.

---

## 4. Patterns explicitly checked and not present

The following pattern classes were searched and **no occurrences**
were found in the runtime path:

| Pattern | Result |
|---|---|
| `os.CreateTemp(...)` | none |
| `os.TempDir()` | none |
| `ioutil.TempFile`, `ioutil.TempDir`, `ioutil.WriteFile` | none (`ioutil` not imported anywhere) |
| `os.Create(...)` | none in runtime code (one false-positive in `domain.PhotoRepo.Create`) |
| `os.OpenFile(..., os.O_WRONLY|...)` | none |
| `os.Mkdir`, `os.MkdirAll` | only `migrate create` (dev-only, §3.1) |
| `r.MultipartReader()` (manual streaming) | none — only the huma `MultipartFormFiles[T]` path |
| Writes outside `/tmp` (e.g. `/var`, `/data`, `/cache`, `/app`) | none |

Because no `os.CreateTemp` callsites exist, the bead's
`defer os.Remove(f.Name())` + `defer f.Close()` audit item is
**vacuously satisfied**. If a future polecat introduces one, the
prefix convention is `os.CreateTemp("", "minerals-*")` (per
CONTRACT §17 line 3658) — call out explicitly in code review.

---

## 5. Hardening applied in this bead

The pre-§17-codification version of the upload handlers (PR #16)
called `defer form.File.Close()` only **after** `io.ReadAll` had
finished — meaning early returns from the content-type
allowlist (415) and oversize check returned without closing the
file or removing the multipart spillover. This bead lifts the
defers to the **first lines** of each handler, before the path
UUID parse (and, in the journal case, before the entry-exists
check):

| Handler | Before | After |
|---|---|---|
| `photos.go:upload` | `defer form.File.Close()` after `io.ReadAll`; no `Form.RemoveAll` | `defer Form.RemoveAll()` + `defer File.Close()` as first statements; UUID parse moves below |
| `journal_files.go:upload` | `defer form.File.Close()` after `io.ReadAll`; no `Form.RemoveAll`; defers below entry-exists check | same as photos.go; defers above UUID parse and entry check |

Both handlers now have a `// CONTRACT.md §17` comment block
referencing the rule, so future polecats touching upload code
see the constraint inline.

---

## 6. Test coverage

`internal/api/photos_test.go::TestPhotoUpload_CleansMultipartTempfiles`
forces the spillover path by setting `$TMPDIR` to a
`t.TempDir()`, posts a JPEG larger than the 8 KiB
`MultipartMaxMemory` threshold, and asserts the temp directory
is empty after the handler returns. This is the regression
check for §17 compliance on the photo path; the journal-file
handler is structurally identical and shares the bug surface.

---

## 7. Findings summary

- **Compliant:** photo upload, journal-file upload, image
  processing, logging.
- **Out of scope (dev tooling):** `migrate create`.
- **No bead spawn-offs filed:** the audit found no
  non-trivial writes outside `/tmp` and no leaked tempfiles
  beyond the multipart spillover that this bead hardens. The
  earlier "writable `/tmp` mount" gap is already closed by
  PR #47.
