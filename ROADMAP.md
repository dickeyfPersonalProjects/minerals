# Minerals — Roadmap

This is the source of truth for the project roadmap.

**Rules:**
- When a bead is filed for a roadmap item, link it here as `(mi-xxx)`.
- When a polecat completes a bead, update the item: change `[ ]` → `[x]` and note the PR.
- New roadmap items go through the mayor before being filed as beads.

---

## V1 — Core Cataloging

*Scope: single-overseer, personal use, local network. No real auth, no public sharing.*

### Specimen management
- [x] Specimen CRUD — mineral, rock, meteorite types with `type_data` JSONB (#56)
- [x] UUIDv7 primary keys across all tables
- [x] Catalog number — nullable human-assigned field (schema only; auto-gen is v2)
- [x] Locality — free-form text + optional structured JSONB (country, region, site, lat/lon, mindat_id)
- [x] Collector/provenance tracking — normalized `collectors` + `specimen_collectors` join (#56)
- [x] Mindat-backed mineral species lookup/autocomplete (#56)
- [x] Editable markdown description per specimen
- [x] **Fossil specimen type** `(mi-6o8)`
- [x] **Photo-kind metadata** (visible / UV / other) `(mi-5b6)`
- [x] **Per-photo UV wavelength** (split 'uv' into UV SW / MW / LW; "Edit type" button on hero photo) `(hq-6lrd)`
- [x] **Structured UV fluorescence** (SW/MW/LW per-wavelength color selectors; validated 15-color enum from Henkel/FMS) `(mi-qas)` (#109)
- [x] **Magnetic + Reacts to Acid boolean properties** (tri-state null/true/false, same pattern as Radioactive) `(mi-sag)` (#108)

### Photos
- [x] Multiple photos per specimen with position ordering
- [x] **Designate main specimen image** (`main_image_id` nullable FK on `specimens`; NULL = first by position) `(mi-m8q)` (#107)
- [x] Go-proxied upload (browser → Go → MinIO) — S3 never exposed to client
- [x] EXIF filtering allowlist (keeps photographic metadata, strips GPS/XMP/IPTC)
- [x] Synchronous display (1600px) + thumbnail (400px) variant generation on upload
- [x] Image crop editor — destructive, replaces original, irreversibility warning (#69)
- [x] Image rotate controls (+90°/−90° buttons + free-form slider) `(mi-uov)` (#73)
- [x] **Rename "Crop" button to "Crop / Rotate"** (dialog does both since mi-uov) `(mi-lg3)` (#106)
- [x] **Specimen detail — adaptive image aspect ratio** (no cropping; container matches photo's natural ratio) `(mi-bg6)` (#104)
- [x] **Specimen list grid — letterbox/pillarbox in square card** (object-fit: contain + black fill) `(mi-467)` (#105)

### Observation journal
- [x] Append-only journal entries per specimen (body markdown, editable post-creation)
- [x] Journal entry file attachments

### Search & navigation
- [x] Full-text search via Postgres `tsvector`
- [x] Cursor-based pagination on list endpoints

### QR code & label printing
- [x] **QR preview page + single-specimen print** `(mi-c78.3)` (#93)
- [x] **QR sheet backend API** (sheet persistence, add/remove specimens) `(mi-c78.1)`
- [x] **QR sheet PDF generation** (server-side, all 5 Avery templates) `(mi-c78.2)` (#89)
- [x] **QR sheet builder UI** (specimen grid add/remove, navbar indicator, template switcher) `(mi-c78.4)` (#99)

### Infrastructure & deployment
- [x] Kubernetes deployment via Flux (k3s cluster)
- [x] MinIO object storage (one bucket per environment)
- [x] Postgres 16 with migrations
- [x] Docker Compose for local development
- [x] `/healthz` + `/readyz` endpoints
- [x] OpenAPI 3 docs served at `/docs` (Redoc)

### Auth (stub)
- [x] Auth middleware slot — no-op stub populates single overseer user
- [x] `author_id` on all writable rows from day one
- [x] Routes pre-grouped into public vs protected buckets

### Quality & CI
- [x] `gofmt`, `go vet`, `golangci-lint` (standard set) in CI
- [x] Unit tests + integration tests (with Postgres + MinIO services)
- [x] `go test -race -shuffle=on` + `gotestsum` JUnit output (#60)
- [x] `make test-cover` + coverage artifact upload (#60)
- [x] Frontend: prettier, eslint, svelte-check, vitest in CI (#58)
- [x] Frontend coverage reporting (#58)
- [x] `govulncheck` CI step `(mi-xql)`
- [x] `gosec`, `errorlint`, `bodyclose`, `noctx` linters `(mi-h01)`
- [x] `go-licenses` gate for §16 allowlist (#68)
- [x] `depguard` import constraint rules `(mi-3xm)`
- [x] `goimports` + `sloglint` linters `(mi-4wm)`
- [x] `gocritic`, `revive`, `misspell`, `prealloc` linters `(mi-aqa)`
- [x] Fuzz harnesses for markdown sanitizer + EXIF parser `(mi-h8j)`
- [x] a11y tests (vitest-axe) on largest forms `(mi-k9t)`
- [x] Property-based tests for specimen schema marshalling (#80)
- [x] **`lefthook` pre-commit/pre-push hooks** `(mi-cyb)`
- [ ] **Test coverage audit + gap analysis** `(mi-5si)` *(blocked: waiting on quality wave)*

---

## V2 — Multi-user ✅ COMPLETE (shipped + live in prod)

*Real authentication + multi-user-ready authorization. The V2 milestone — BFF auth, per-row/per-field visibility, V1→V2 data cut — is **done and live in production** under `mi-1d5i`.*

### Auth
- [x] Real OIDC authentication via Keycloak operator (cluster already has it) `(mi-7xo)` (#154)
- [x] Replace stub middleware — handlers, context keys, route groupings stay identical `(mi-7xo)` (#154)
- [x] One-time migration: backfill stub `author_id` to real overseer UUID `(mi-tl2 + mi-7xo)` (#147, #154)
- [x] Per-row authorization (visibility-based reads, ownership-based writes) via Casbin `(mi-bqe)` (#157)
- [x] **BFF auth migration** — Go backend is the OAuth client; browser holds only an opaque HttpOnly `minerals_session` cookie; refresh-token rotation handled server-side. PKCE/bearer stub retired. See CONTRACT §13 + `docs/design/auth-bff.md` `(mi-1d5i)`
- [x] CSRF defense — stored-synchronizer token middleware on every cookie-authenticated write (`internal/auth/bff/csrf.go`) `(mi-1d5i)`

### Public sharing
- [x] Visibility UX — `private | unlisted | public` control in specimen UI `(mi-35hk)`
- [x] Public / anonymous specimen reads (no auth required, visibility-gated)
- [x] **Per-field visibility** — independent visibility on `price`, `acquired_from`, and `images` with user profile defaults + per-specimen / per-image overrides. See CONTRACT §13b. `(mi-fo8)` (10 sub-beads merged)

---

## V3 — Public launch 🚀

*Theme: **cut the app public.** It currently runs privately over a private IP for personal use. V3 makes it safe + ready for outside users — the visibility/discovery/sharing UX plus the hardening that must land before the public cut.*

**V3 status:** the launch-blocking criticals are **DONE** — auth/IDOR (cross-tenant authz fixed + live), API rate limiting, DB backups, public DNS/TLS, and origin lockdown. Remaining before the public cut: live pen test `(mi-sx35)`, user data export/import `(mi-dkuu)`, production monitoring/alerting `(mi-vp0w)`, legal (ToS/privacy) `(mi-97kr)`, abuse/moderation `(mi-b2q0)`, and the design-first friends/sharing feature `(mi-qtq3)`.

### Features — visibility / discovery / sharing UX
- [x] Inline visibility editor on the specimens list — owner-only quick public/unlisted/private toggle `(mi-35hk)`
- [x] 'New specimens' default visibility setting in Settings; create-form pre-fills from it `(mi-q2d8)`
- [x] 'Browse all specimens' + 'Browse my collection' — two scoped list views `(mi-xue7)` (merged via mi-6yjp / mi-bd81)
- [ ] Friends / sharing — targeted per-user grants on private specimens. **DESIGN-FIRST; not yet specced** `(mi-qtq3)`

### Hardening prerequisites (must land before the public cut)
- [x] DB backup to Backblaze B2 (external, off-cluster) — example manifests + restore docs `(mi-lhsu)`. The MinIO image-mirror was split out to `(mi-a3pt)` (still open).
- [ ] MinIO image mirror to a second local bucket (replication, delete-marker off) — cost-efficient image copy, split out of `(mi-lhsu)` `(mi-a3pt)`
- [ ] MinIO bucket versioning on the primary image bucket — data-safety; distinct from the backup-bucket versioning in `(mi-lhsu)` *(unbeaded)*
- [ ] User data Export / Import — collection + images; portability + user-controlled backup + GDPR data-access `(mi-dkuu)`
- [x] GDPR right-to-erasure — Settings → Delete account: cascades the DB (specimens/photos/journal/attachments/collectors/files/qr-sheets), purges MinIO objects, revokes sessions, removes the Keycloak identity (when admin creds configured; no-op otherwise), reassigns the mineral-species catalog to the system account, and audit-logs without retaining PII `(mi-nwg5)`
- [x] API rate limiting — tiered limits to prevent abuse once publicly reachable `(mi-tnru)` (merged via mi-y3wp; account-bucket + per-IP CF-Connecting-IP + 429; in-memory per-replica accepted)
- [x] Static security audit — source review + tooling + regression tests; all 7 findings resolved (F2/F3 cross-tenant authz fixed + live in prod, F4 qr-sheet, F5 decompression cap, F6 CSP tightened, F7 error strings; T3 CodeQL was a false finding, closed) `(mi-l1eg)`, parent `(mi-z58x)`
- [ ] LIVE penetration test — operator-driven runbook; OWASP ZAP/Burp + manual IDOR/visibility/CSRF probing against staging `(mi-sx35)`
- [ ] CI: re-add Trivy scan with valid action versions + soft-fail (decoupled from the IDOR fix) `(mi-craf)`

### Implied infra (likely needed for public — candidates, not yet beaded)
*Bead these out as V3 planning matures.*
- [x] Public DNS + TLS — Cloudflare DNS + cluster Ingress + Let's Encrypt TLS + external-dns all configured. DNS currently resolves to a private IP (Cloudflare unproxied); flipping to public is a one-annotation change (Cloudflare proxy mode → public IPv6 ingress).
- [x] Edge protection foundation — Cloudflare in place (DNS now; flip to proxy mode for DDoS/CDN/edge-rate-limiting at launch). Origin locked down: pfSense drops all non-Cloudflare IPs on 80/443, so the origin is unreachable except via Cloudflare and CF-Connecting-IP is non-spoofable (was bead mi-1d7q, closed). Remaining: enable proxy mode + any edge rules at launch.
- [ ] Production monitoring / alerting — Prometheus metrics already exist `(mi-2b1k)`; need dashboards + alerts (PrometheusRule + Grafana + alert routing) `(mi-vp0w)`
- [ ] Terms of service / privacy policy (legal, for public users + GDPR: consent + deletion + data-rights) `(mi-97kr)`
- [ ] Abuse handling / moderation story (for publicly visible user-generated content; post-moderation + admin takedown + reporting) `(mi-b2q0)`
- [x] Multi-replica decisions — prod runs 2 replicas. Session lookup: no cache (mi-1d5i decision, SessionResolver interface ready if needed). Rate-limit store: in-memory per-replica accepted (a user hitting both replicas can get up to 2x quota — tolerated; shared store is the upgrade if exact global limits ever matter).

---

## V4 — Next wave of capabilities

*Post-launch feature expansion. Research and planning needed on several items before scoping beads.*

### Catalog numbering
- [ ] Auto-generation with customizable ID scheme (e.g. `FD-2026-0042`, user-defined template)

### Specimen data
- [ ] Gamma spectrum capture, storage, and display
- [ ] Advanced journal UX (research and design phase before filing)

### Photo metadata
- [ ] Per-specimen / per-photo "preserve full EXIF" opt-in (GPS, XMP, MakerNotes for provenance)

### Collectors
- [ ] Collector merging UI (combine near-duplicate collector entries)

### Storage housekeeping
- [ ] Orphan cleanup job (files in MinIO with no `files`-row reference)

### Storage locations
User-defined physical storage locations, hierarchical (e.g. House → Basement → Furnace Room → Drawer 1). Each specimen can be tagged with a location.
- [ ] Storage location entity with parent/child nesting (no cycles)
- [ ] Storage location manager UI (create, rename, reorder, nest)
- [ ] Specimen ↔ location assignment (tag specimen from specimen page or from location view)
- [ ] Location browser — view all specimens at a given location (and descendants)

### Locality map view
- [ ] Map view for localities using the structured `locality` JSONB (lat/lon)
- [ ] Quick-fill from known localities (searchable library of named collecting sites)

### Sub-collections
User can define multiple sub-collections as named subsets of their collection. Sub-collections can be nested (child of another sub-collection), forming a DAG — no loops.
- [ ] Sub-collection entity (name, description, parent)
- [ ] Sub-collection manager UI (create, nest, rename)
- [ ] Specimen ↔ sub-collection assignment (a specimen can belong to multiple sub-collections)
- [ ] Sub-collection view — filtered specimen grid for a given sub-collection

### Field-collecting trips
Separate from the collection. Tracks field trips the user has logged. Each trip can be associated with specimens collected during it.
- [ ] Trip entity (name, date range, location, notes) — detail design deferred
- [ ] Trip ↔ specimen association
- [ ] Trip log view

### Search & discovery
- [ ] Faceted search / aggregation endpoints ("count by type", "count by collector")
- [ ] Advanced query syntax (`field:value`, AND/OR/NOT)
- [ ] Fuzzy / trigram matching for typos (`pg_trgm`)
- [ ] Search across journal entries

### Mobile
- [ ] Mobile-optimized view / PWA

### Admin / devops console
A dedicated admin-facing console for operating the site. Gated to the admin/devops role (Keycloak role + Casbin policy already exist). Distinct from a normal user's view — this is the operator's window into the whole instance.
- [ ] Admin login + console shell (admin/devops role only)
- [ ] Site management surface (operational controls for running the instance)
- [ ] View the **non-personal** information of all users (no PII beyond what's needed to operate; respects the GDPR/PII boundary)
- [ ] View **all published data** across all users (specimens, photos, journal entries that are public/unlisted) for **usage-policy compliance review**
- [ ] Hooks into the moderation/takedown actions (see V3-prereq moderation story) — unpublish/hide/remove policy-violating content from the console

---

## V5 — Import & migration

*Unlocks migration from existing tools and spreadsheets.*

### Import adapters
- [ ] CSV / XLSX import with interactive column mapping UI (unlocks migration from Excel)
- [ ] MineralDB import adapter
- [ ] Mineral Desk Curator import adapter
- [ ] Mindat catalog import adapter

---

## Licensing

**PolyForm Noncommercial 1.0.0** — free for noncommercial use; all commercial rights reserved.
See `LICENSE` at the repo root. Required Notice: Copyright (c) 2026 Francois Dickey — https://github.com/dickeyfPersonalProjects/minerals

---

## Deferred / out of scope (recorded decisions)

- **CodeQL** — license is GitHub's own terms (not OSI); deferred until license posture is resolved (§3.13)
- **Custom QR label templates** — deferred until real use cases are understood
- **Mutation testing** (`gremlins`, `go-mutesting`) — too noisy at this stage; revisit post-coverage audit
- **SBOM generation** — not needed until distribution requirements change
- **Strict `type_data` DB constraints** — app-side validation via Go structs is sufficient for v1
- **Type reclassification** — API rejects type changes in v1; delete + re-create is the policy
