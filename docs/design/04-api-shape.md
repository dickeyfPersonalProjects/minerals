# §4 — API shape

Decided 2026-05-06 in design session.

## Summary

Pragmatic JSON REST under `/api/v1/`. Resource URLs nest where the
relationship is part-of, flatten when an id is enough on its own. Lists
are cursor-paginated; filtering is by query params; full-text search
runs through a Postgres `tsvector` column. Error responses use a
consistent JSON envelope. OpenAPI 3 documentation is generated from
Go types via a type-derived framework, served as Redoc at `/docs`.

## Decisions

- **REST, not RPC.** Standard verbs (`GET`, `POST`, `PATCH`, `DELETE`),
  resources by URL, JSON in/out, status codes that mean what they say.
  Not strict REST/HATEOAS — no hypermedia link bodies, no PUT-for-replace
  ceremony.
- **`/api/v1/` URL prefix.** Free optionality for future public exposure;
  the version segment is a constant reminder that contract changes have
  consequences.
- **Resource hierarchy: nested for "part-of", flat for direct ops.**
  - Specimens: top-level CRUD at `/api/v1/specimens` (+ `/{id}`)
  - Photos: nested for create/list under specimen, flat for ops by id
  - Journal entries: same pattern as photos
  - Journal-entry attachments: POST nested under entry, DELETE flat
  - Collectors: top-level (shared entities, not owned by any specimen)
- **Cursor-based pagination on all list endpoints.**
  - `limit` defaults to 50, capped at 200
  - `cursor` is opaque base64 (encoded `{created_at, id}`) — clients
    treat as a string, server may change shape
  - Response: `{items, next_cursor}` with `next_cursor: null` at end
  - Default ordering: `created_at DESC, id DESC`
- **Filtering via query params on `GET /api/v1/specimens`** (compose with AND):
  - `type=mineral|rock|meteorite`
  - `visibility=private|unlisted|public`
  - `collector_id={uuid}` — has this collector in its provenance
  - `has_catalog_number=true|false`
  - `acquired_after=YYYY-MM-DD`, `acquired_before=YYYY-MM-DD`
  - `q={text}` — full-text search; when present, results order by
    `ts_rank` desc instead of `created_at`
- **Full-text search via Postgres `tsvector`, generated column + GIN
  index.** Indexed source: `name`, `description`, `locality_text`,
  `source_notes`, plus selected stringy fields from `type_data`
  (`chemical_formula`, `classification`, etc.). Text search config:
  `english` for v1.
- **Error response envelope** (all error responses, regardless of status):
  ```json
  {
    "error": {
      "code": "specimen_not_found",
      "message": "No specimen with id abc-123",
      "details": { "field": "catalog_number", "constraint": "unique" }
    }
  }
  ```
  - `code` is stable, machine-readable, snake_case — clients branch on
    this, never on `message`
  - `message` is human-readable for logs/dev tooling, NOT for end-user
    display — the SPA decides UX based on `code`
  - `details` is optional, error-specific structured data
  - HTTP status codes (400, 401, 403, 404, 409, 415, 422, 500) remain
    meaningful; the JSON envelope is additional structure
- **OpenAPI 3 documentation, type-derived from Go.** Framework that
  produces the spec from handler types/signatures (e.g. `huma`, `fuego`,
  `ogen`) — concrete library choice is a polecat call at implementation
  time. Code-first annotation systems (`swag`-style) are rejected because
  they drift from reality.
- **Docs served at:**
  - `GET /api/v1/openapi.json` — raw spec
  - `GET /docs` — Redoc HTML (single embedded page)
- **Docs always available in v1, both dev and prod.** Local-network
  only initially; revisit gating when public exposure through Cloudflare
  ships.

## Deferred to v2 / later

- Searching across journal entries (separate index or join — punt until
  there's a workflow that actually needs it)
- Faceted search / aggregation endpoints ("count by type", "count by
  collector")
- Advanced query syntax (`field:value`, AND/OR/NOT operators)
- Trigram / fuzzy matching for typos (`pg_trgm` extension if needed)
- Switchable text-search language config beyond `english`
- Idempotency keys for POST/PATCH (nice for retries; not required at v1
  scale)
- Rate limiting / abuse mitigation (single-user local app — not v1)
- Swagger UI alongside Redoc (add later if the try-it-out feature is
  missed)
- Hypermedia / HAL-style link relations
- API versioning beyond v1 (when a v2 is needed; v1 stays stable)
- Public exposure gating for `/docs` and `/openapi.json`

## Open questions / flags

- **Framework choice partially constrains routing.** Most type-derived
  OpenAPI frameworks wrap stdlib `net/http` with their own router.
  We're not strictly on bare stdlib once a framework is picked. Acceptable
  — reversible if we ever want to extract.
- **CORS.** In dev, the Vite proxy forwards `/api` to the Go server, so
  same-origin — no CORS needed. In prod, the SPA is embedded in the Go
  binary and served from the same origin — also no CORS needed. If we
  ever serve the API to a different origin (e.g. dedicated public API
  consumer), we'll add CORS middleware then.
- **CSRF.** Not relevant for v1 (no auth). When real auth lands, the
  approach depends on the auth model: cookie-session needs CSRF tokens;
  bearer-token in `Authorization` header doesn't. Decided alongside §5
  auth implementation, not now.
- **OpenAPI for multipart endpoints.** Photo and journal-attachment
  uploads are multipart, not JSON. The chosen framework needs to model
  this in the spec correctly. Worth a sanity check during implementation;
  not all type-derived libraries handle multipart equally well.
- **`q=` and cursor interaction.** A cursor encoded under `created_at`
  ordering becomes meaningless when `q` is supplied (results re-order by
  `ts_rank`). Implementation should either re-encode cursors per
  ordering, or document that changing `q` invalidates the cursor.
