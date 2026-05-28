// Package api wires the HTTP server: middleware chain, public vs
// protected route groups, and the v1 endpoints (healthz, readyz,
// openapi, docs). Real CRUD lands in subsequent feature beads (per
// CONTRACT.md §13 / §10).
//
// Framework: github.com/danielgtaylor/huma/v2 with the humago
// adapter (per design §4.6.A; CONTRACT.md §16). Huma is type-derived
// — operations register handler signatures and huma generates the
// OpenAPI 3.1 spec from them. The humago adapter wraps the stdlib
// http.ServeMux so middleware and the SPA fallback continue to use
// vanilla net/http.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// Pinger reports whether the database accepts a `SELECT 1` round-trip.
type Pinger interface {
	Ping(ctx context.Context) error
}

// BucketProber reports whether the configured object-storage bucket
// is reachable (HeadBucket).
type BucketProber interface {
	HeadBucket(ctx context.Context) error
}

// SchemaVersionFn returns the current applied migration version. A
// zero/empty value with err==nil means "no migrations applied yet"
// — used in dev before migrations land.
type SchemaVersionFn func(ctx context.Context) (version uint, dirty bool, err error)

// Deps gathers the dependencies the API server needs. All are
// optional except the auth middleware, which is always present.
type Deps struct {
	DB              Pinger
	Storage         BucketProber
	SchemaVersion   SchemaVersionFn
	ExpectedVersion uint
	WebHandler      http.Handler // SPA fallback handler
	// Collectors is wired with a real repo in production. Tests that
	// don't exercise collectors leave it nil — the handler is then
	// not registered and /api/v1/collectors falls through to the
	// catch-all 404.
	Collectors domain.CollectorRepo
	// Photos is wired with the photo upload pipeline in production
	// (mi-jpu / B-3). Tests that don't exercise photos leave it nil
	// and the routes are not registered.
	Photos *PhotoServiceDeps
	// Specimens is wired with a real repo in production (mi-quf / B-2).
	// nil leaves /api/v1/specimens unregistered; the catch-all 404
	// handles requests in that case.
	Specimens domain.SpecimenRepo
	// Journal is wired with a real repo + the §17 markdown renderer
	// in production (mi-y6b / C-1). nil leaves /api/v1/journal and
	// /api/v1/specimens/{id}/journal unregistered.
	Journal *JournalServiceDeps
	// SpecimenCollectors is wired in production (mi-zv3 / C-3) to
	// expose GET/PUT /api/v1/specimens/{id}/collectors. nil leaves
	// the chain endpoints unregistered.
	SpecimenCollectors domain.SpecimenCollectorRepo
	// JournalFiles is wired with the journal-attachment upload
	// pipeline in production (mi-720 / C-2). nil leaves the
	// /api/v1/journal/{id}/files, /api/v1/journal-files/{file_id},
	// and /api/v1/files/{file_id} routes unregistered.
	JournalFiles *JournalFileServiceDeps
	// MineralSpecies is wired in production (mi-dtg / F-1) to expose
	// the /api/v1/mineral-species autocomplete + create surface.
	// The Mindat client is optional — nil mindat falls through to
	// DB-only mode.
	MineralSpecies *MineralSpeciesServiceDeps
	// QRSheets is wired in production (mi-c78.1) to expose the
	// /api/v1/qr-sheet surface backing the printable label workflow.
	QRSheets domain.QRSheetRepo
	// Account wires DELETE /api/v1/account, the GDPR right-to-erasure
	// endpoint (mi-nwg5). nil leaves the route unregistered (tests that
	// don't exercise account deletion). The Eraser field inside is the
	// only hard requirement; Storage/Sessions/Identity are best-effort
	// cleanup collaborators.
	Account *AccountServiceDeps
	// Users powers the first-login gate (mi-2hf): the auth chain
	// resolves the JWT `sub` to a row here, auto-creates a pending
	// row on first-login, and gates protected endpoints with a 403
	// + redirect until the profile is completed. nil disables the
	// resolver entirely — protected routes still run humaAuth so
	// existing handlers see the StubUser, but no DB lookup or gate
	// runs. Tests that don't exercise auth leave it nil.
	Users domain.UserRepo
	// Verifier validates Keycloak bearer tokens for the auth
	// middleware (mi-aw3a). Wired with an *oidc.Verifier in
	// production. nil selects the v1 stub-identity fallback — the
	// path for tests that don't exercise authentication.
	Verifier auth.TokenVerifier
	// Enforcer is the Casbin enforcer backing CONTRACT.md §13 v2
	// per-resource authorization (mi-aw3b). Wired in production by
	// cmd/minerals with the DB-backed shares lookup. nil disables
	// per-resource enforcement entirely — handlers still run, but no
	// authz check fires. This mirrors the nil-Verifier stub path and
	// is the seam unit tests use; serve always wires a real enforcer.
	Enforcer *casbin.Enforcer
	// BFFAuth is the GET/POST handler bundle for the V2 cookie auth
	// flow (mi-bm5b): /auth/login, /auth/callback, /auth/logout.
	// nil leaves the routes unregistered — the path tests that
	// don't configure a Keycloak client + HMAC key take. Production
	// wiring in cmd/minerals sets this whenever OIDC_CLIENT_SECRET
	// and OAUTH_STATE_HMAC_KEY are both present.
	BFFAuth *bff.Handlers
	// SessionMW wraps the mux when BFF auth is configured. The
	// middleware resolves the session cookie, refreshes the access
	// token when due, and attaches auth.User to the context — so
	// downstream huma handlers see the cookie-authenticated user the
	// same way they see a bearer-token one. nil leaves the chain on
	// the legacy bearer-token-only path (mi-sap2 / mi-1d5i #8).
	SessionMW func(http.Handler) http.Handler
	// CSRFMW enforces the stored-synchronizer CSRF check on /api/v1/*
	// writes when a session is attached. Composes UNDER SessionMW.
	// nil leaves CSRF off, which matches the bearer-token-only legacy
	// path (bearer auth in an Authorization header is not subject to
	// CSRF; cookies are).
	CSRFMW func(http.Handler) http.Handler
	// Admin is the see-all data source backing the admin/devops console's
	// users + published-content surfaces (mi-n5av / mi-gtkp). It bypasses
	// the §13 v2 per-user scoping by design — access is gated entirely on
	// the `devops` Casbin resource at the handler. nil leaves the
	// /api/v1/admin/users and /api/v1/admin/published-content routes
	// unregistered and their overview sections "planned" (the unit-test
	// path); production wiring in cmd/minerals always sets it.
	Admin domain.AdminRepo
	// IncidentRegister wires the Law 25 confidentiality-incident register
	// (mi-2p6i), backed by a store on a SEPARATE database
	// (INCIDENT_REGISTER_DATABASE_URL). nil leaves the
	// /api/v1/admin/incident-register routes unregistered and the admin
	// overview reports the section as "planned" — the dev/test path that
	// runs a single database. Production wiring in cmd/minerals sets it
	// only when the second DB URL is configured.
	IncidentRegister IncidentRegister
	// Settings is the DB-backed mutable-settings store backing the
	// runtime registration toggle (mi-pkn2): the admin console writes it
	// and the BFF /auth/register gate reads it per request. nil leaves
	// the /api/v1/admin/registration routes unregistered and the admin
	// overview's site-management section "planned" (the unit-test path);
	// production wiring in cmd/minerals always sets it.
	Settings domain.SettingsRepo
	// RegistrationSync syncs the Keycloak realm's `registrationAllowed`
	// flag when the toggle is flipped, keeping the IdP consistent with
	// the application (mi-pkn2). nil (no Keycloak admin client configured)
	// makes the toggle application-only — the realm is left untouched.
	RegistrationSync RegistrationRealmSyncer
	// RegistrationDefault is the deploy-time REGISTRATION_ENABLED value,
	// reported as the effective registration state until an operator
	// first flips the runtime toggle (mi-pkn2).
	RegistrationDefault bool
	// RateLimitMW enforces the per-tier token-bucket limits (mi-tnru):
	// strict per-IP on auth endpoints, per-account (or per-IP when
	// anonymous) on reads/writes/file-serving. Composes BETWEEN
	// SessionMW and CSRFMW so the authenticated user is already
	// attached when the limiter derives the account key. nil disables
	// rate limiting entirely — the path unit tests take (production
	// wiring in cmd/minerals sets it from the RATE_LIMIT_* config).
	RateLimitMW func(http.Handler) http.Handler
}

// New returns an http.Handler with the v1 routes wired up. Callers
// embed the result in their own *http.Server.
func New(deps Deps) http.Handler {
	installEnvelopeErrors()
	mux := http.NewServeMux()

	// Huma config: spec at /api/v1/openapi.json (per §10), docs and
	// openapi auto-mount disabled — we register them as explicit
	// huma operations so they appear in the generated spec (per
	// bead acceptance criteria).
	cfg := huma.DefaultConfig("Minerals API", "0.0.1")
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.Info.Description = "Minerals collection management API. " +
		"v1 surface: liveness, readiness, OpenAPI spec, docs."
	cfg.Servers = []*huma.Server{
		{URL: "/", Description: "Same-origin (SPA proxy in dev, embedded SPA in prod)"},
	}
	cfg.Tags = []*huma.Tag{
		{Name: "system", Description: "Operational endpoints: liveness, readiness, spec, docs."},
	}

	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "collectors", Description: "CRUD for the collectors directory (mi-yvt / B-1).",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "photos", Description: "Specimen photo upload, download, variant generation (mi-jpu / B-3).",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "specimens", Description: "CRUD for the specimens catalog (mi-quf / B-2). type_data shape per design §2.",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "journal", Description: "Per-specimen journal entries with server-rendered markdown (mi-y6b / C-1; CONTRACT.md §17 pipeline).",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "journal-files", Description: "File attachments on journal entries (mi-720 / C-2). Reuses the §12 upload pipeline; no variant generation.",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "mineral-species", Description: "Mindat-backed mineral species lookup with DB-as-canonical-store (mi-dtg / F-1).",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "qr-sheets", Description: "Per-user QR sticker sheet builder (mi-c78.1). One active sheet per user.",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "profile", Description: "First-login profile completion (mi-2hf). Pending users complete setup here before any other protected endpoint becomes reachable.",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "account", Description: "Account lifecycle: GDPR right-to-erasure self-service deletion (mi-nwg5).",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "admin", Description: "Admin/devops console (mi-agff). Gated to the admin/devops role via the §13 v2 `devops` Casbin resource. Foundation pass ships the gated shell + placeholder landing.",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "legal", Description: "Public static legal documents — privacy policy + terms of service (mi-97kr). Server-rendered via the §17 markdown pipeline; no auth.",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "moderation", Description: "Abuse handling for public user-generated content (mi-b2q0): public report affordance + operator force-private takedown. Post-moderation model; see docs/security/moderation.md.",
	})
	cfg.Tags = append(cfg.Tags, &huma.Tag{
		Name: "incident-register", Description: "Law 25 confidentiality-incident register (mi-2p6i). Admin-only, append-only/tamper-evident, stored in a SEPARATE database; >=5yr retention. Gated on the §13 v2 `devops` resource.",
	})

	humaAPI := humago.New(mux, cfg)
	authMW := newAuthMiddlewares(deps.Users, deps.Verifier)
	guard := authzGuard{enforcer: deps.Enforcer}
	registerSystemOperations(humaAPI, deps)
	registerCollectorOperations(humaAPI, authMW, guard, deps.Collectors)
	registerPhotoOperations(humaAPI, mux, authMW, guard, deps.Photos)
	registerSpecimenOperations(humaAPI, authMW, guard, deps.Specimens, deps.Users)
	registerJournalOperations(humaAPI, authMW, guard, deps.Specimens, deps.Journal)
	registerSpecimenCollectorOperations(humaAPI, authMW, guard, deps.Specimens, deps.Collectors, deps.SpecimenCollectors)
	registerJournalFileOperations(humaAPI, mux, authMW, guard, deps.JournalFiles)
	registerMineralSpeciesOperations(humaAPI, authMW, deps.MineralSpecies)
	registerQRSheetOperations(humaAPI, authMW, guard, deps.QRSheets, deps.Specimens)
	registerProfileOperations(humaAPI, authMW, deps.Users)
	registerAccountOperations(humaAPI, authMW, deps.Account)
	registerAdminOperations(humaAPI, authMW, guard, deps.IncidentRegister != nil, deps.Admin, registrationToggleWired(deps.Settings))
	registerLegalOperations(humaAPI)
	registerModerationOperations(humaAPI, authMW, guard, deps.Specimens)
	registerIncidentRegisterOperations(humaAPI, authMW, guard, deps.IncidentRegister)
	registerRegistrationOperations(humaAPI, authMW, guard, deps.Settings, deps.RegistrationSync, deps.RegistrationDefault)
	registerSpecimenRedirect(mux)

	// BFF V2 auth routes (mi-bm5b). The three routes attach to the
	// raw mux — they sit outside the /api/v1 catch-all so the
	// existing auth.Auth chain doesn't fire on them (login +
	// callback are pre-session by definition; logout reads its own
	// cookie). The public middleware (Recovery / RequestID /
	// SecurityHeaders / CSP / Logging) still wraps them via the
	// outer Chain call below.
	if deps.BFFAuth != nil {
		deps.BFFAuth.RegisterRoutes(mux)
	}

	// V2 BFF CSRF endpoint (mi-gbzs / mi-sap2 #8). Mounted only when
	// the session middleware is wired — anonymous callers receive
	// 401, and a session-less deployment has nothing to serve here.
	// The handler reads the session attached by SessionMW from the
	// request context; SessionMW is applied at the top-level wrapper
	// below, so it has already run by the time this route fires.
	if deps.SessionMW != nil {
		mux.Handle("GET /api/v1/csrf", http.HandlerFunc(bff.CSRFHandler))
	}

	// Unmatched /api/v1/* fallback: emit the §10 404 envelope. The
	// production auth chain runs at the top-level wrapper (SessionMW
	// → CSRFMW) and the huma per-operation middleware (humaAuth +
	// RequireUser); a catch-all auth chain here would be dead code
	// (mi-6tts).
	apiNotFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint", nil)
	})
	mux.Handle("/api/v1/", apiNotFound)

	// SPA fallback (public): everything else is the embedded SPA.
	if deps.WebHandler != nil {
		mux.Handle("/", deps.WebHandler)
	}

	// Wrap the mux: SessionMW (when configured) runs first so cookie
	// auth populates auth.User before huma operations evaluate their
	// per-operation middleware; CSRFMW runs on every route under it
	// (the middleware bypasses safe methods and anonymous-no-session
	// requests internally, so /api/v1/* GETs, /auth/login, /auth/callback,
	// the SPA fallback, and bearer-token-authenticated calls all pass
	// through; cookie-authenticated unsafe methods — POSTs to /api/v1/*
	// AND /auth/logout per the design doc — are enforced).
	//
	// /auth/logout going through CSRFMiddleware is belt-and-suspenders
	// with the handler's own EnforceCSRFOnLogout gate: a misconfigured
	// chain that mounts CSRFMW but not EnforceCSRFOnLogout (or vice
	// versa) still fails closed on a CSRF-less logout.
	//
	// Both default to no-ops when BFF is disabled — the legacy
	// bearer-token chain stays intact for unit tests that don't wire BFF.
	var top http.Handler = mux
	if deps.CSRFMW != nil {
		top = deps.CSRFMW(top)
	}
	// RateLimitMW sits between SessionMW (outer) and CSRFMW (inner):
	// it needs the user SessionMW attaches for account keying, and it
	// runs before CSRF so a flood of unauthenticated/forged-token
	// writes is throttled before reaching the CSRF check (mi-tnru).
	if deps.RateLimitMW != nil {
		top = deps.RateLimitMW(top)
	}
	if deps.SessionMW != nil {
		top = deps.SessionMW(top)
	}

	// Apply public middleware to the entire mux. Full chain order:
	// Recovery → RequestID → SecHeaders → CSP → Logging →
	// [SessionMW → RateLimitMW → CSRFMW →] [huma per-operation chain →] handler.
	publicMW := []func(http.Handler) http.Handler{
		Recovery, RequestID, SecurityHeaders, CSP, Logging,
	}
	return Chain(top, publicMW...)
}

// healthzOutput uses a body callback so the handler can write the
// "ok" plain-text body verbatim (no JSON envelope) while still
// having the operation participate in the OpenAPI spec.
type healthzOutput struct {
	Body func(huma.Context)
}

// readyzDBTimeout bounds the readiness DB ping. It is deliberately
// SHORTER than the other readiness checks (mi-hkh6): /readyz pings the
// SAME pgxpool that serves app traffic, so a momentarily-saturated pool
// (e.g. the SpecimenCard fan-out holding every connection for a beat)
// must not block the probe long enough to flap the pod to NotReady. A
// 1s bound fails fast and lets the probe ride out a transient burst
// rather than amplifying it by pulling the replica out of rotation.
const readyzDBTimeout = 1 * time.Second

// readyzCheck mirrors the §14 readiness probe shape.
type readyzCheck struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Version uint   `json:"version,omitempty"`
}

type readyzBody struct {
	Ready  bool                   `json:"ready"`
	Checks map[string]readyzCheck `json:"checks"`
}

type readyzOutput struct {
	// Status overrides the HTTP status — 200 if ready, 503 otherwise.
	Status int
	Body   readyzBody
}

// openapiOutput streams the generated spec verbatim.
type openapiOutput struct {
	Body func(huma.Context)
}

// docsOutput streams the Redoc HTML page.
type docsOutput struct {
	Body func(huma.Context)
}

// registerSystemOperations registers the v1 system endpoints with
// huma. After registration the operations appear in the spec served
// at /api/v1/openapi.json.
func registerSystemOperations(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "healthz",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Liveness probe",
		Description: "Returns 200 with body \"ok\" if the process is alive. Performs no dependency checks.",
		Tags:        []string{"system"},
	}, func(_ context.Context, _ *struct{}) (*healthzOutput, error) {
		return &healthzOutput{
			Body: func(c huma.Context) {
				c.SetHeader("Content-Type", "text/plain; charset=utf-8")
				_, _ = c.BodyWriter().Write([]byte("ok"))
			},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "readyz",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Readiness probe",
		Description: "Verifies database, storage, and schema version. Returns 200 if all checks pass, 503 with per-check detail otherwise.",
		Tags:        []string{"system"},
		Errors:      []int{http.StatusServiceUnavailable},
	}, makeReadyzHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "openapi",
		Method:      http.MethodGet,
		Path:        "/api/v1/openapi.json",
		Summary:     "OpenAPI 3.1 specification",
		Description: "The machine-readable contract for this API, generated from registered handler types.",
		Tags:        []string{"system"},
	}, makeOpenAPIHandler(api))

	huma.Register(api, huma.Operation{
		OperationID: "docs",
		Method:      http.MethodGet,
		Path:        "/docs",
		Summary:     "API documentation (Redoc)",
		Description: "Single-page Redoc viewer that loads the OpenAPI spec from /api/v1/openapi.json.",
		Tags:        []string{"system"},
	}, docsHandler)

}

// evaluateReadiness runs the per-dependency probes and returns the
// readyz response body plus the HTTP status to send (200 when every
// check passes, 503 otherwise). Shared between the huma operation
// served at `/readyz` on the API port and the plain http.Handler
// exposed on the admin port (mi-2b1k).
func evaluateReadiness(ctx context.Context, deps Deps) (readyzBody, int) {
	body := readyzBody{Checks: map[string]readyzCheck{}}
	ready := true

	dbCheck := readyzCheck{OK: true}
	if deps.DB != nil {
		cctx, cancel := context.WithTimeout(ctx, readyzDBTimeout)
		if err := deps.DB.Ping(cctx); err != nil {
			// /readyz is a public, unauthenticated endpoint (see
			// huma_auth.go) and is also served on the admin port.
			// The raw driver error can carry the DSN, host, or
			// internal topology, so log it server-side and return a
			// static, non-leaking string to the caller (mi-f5v3).
			slog.ErrorContext(ctx, "readyz: database probe failed", "err", err)
			dbCheck = readyzCheck{OK: false, Error: "database ping failed"}
			ready = false
		}
		cancel()
	} else {
		dbCheck = readyzCheck{OK: false, Error: "no db wired"}
		ready = false
	}
	body.Checks["database"] = dbCheck

	storageCheck := readyzCheck{OK: true}
	if deps.Storage != nil {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := deps.Storage.HeadBucket(cctx); err != nil {
			// Static string only — the S3/MinIO error can leak the
			// endpoint URL, bucket name, or credentials hint (mi-f5v3).
			slog.ErrorContext(ctx, "readyz: storage probe failed", "err", err)
			storageCheck = readyzCheck{OK: false, Error: "storage check failed"}
			ready = false
		}
		cancel()
	} else {
		storageCheck = readyzCheck{OK: false, Error: "no storage wired"}
		ready = false
	}
	body.Checks["storage"] = storageCheck

	var schemaCheck readyzCheck
	if deps.SchemaVersion != nil {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ver, dirty, err := deps.SchemaVersion(cctx)
		cancel()
		switch {
		case err != nil:
			// Static string only — the migration/query error can leak
			// SQL, schema names, or driver internals (mi-f5v3).
			slog.ErrorContext(ctx, "readyz: schema probe failed", "err", err)
			schemaCheck = readyzCheck{OK: false, Error: "schema version check failed"}
			ready = false
		case dirty:
			schemaCheck = readyzCheck{OK: false, Error: "schema is dirty", Version: ver}
			ready = false
		case deps.ExpectedVersion != 0 && ver != deps.ExpectedVersion:
			schemaCheck = readyzCheck{
				OK:      false,
				Error:   fmt.Sprintf("expected version %d, found %d", deps.ExpectedVersion, ver),
				Version: ver,
			}
			ready = false
		default:
			schemaCheck = readyzCheck{OK: true, Version: ver}
		}
	} else {
		// Treat as "no migrations yet" — readyz still requires DB
		// and storage to be up but doesn't gate on schema-version
		// evidence.
		schemaCheck = readyzCheck{OK: true}
	}
	body.Checks["schema"] = schemaCheck

	body.Ready = ready
	if ready {
		return body, http.StatusOK
	}
	return body, http.StatusServiceUnavailable
}

func makeReadyzHandler(deps Deps) func(context.Context, *struct{}) (*readyzOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*readyzOutput, error) {
		body, status := evaluateReadiness(ctx, deps)
		return &readyzOutput{Status: status, Body: body}, nil
	}
}

// ReadyzHTTPHandler returns a plain http.Handler that runs the same
// readiness checks as the API's `/readyz` huma operation and writes
// the JSON response shape documented in design §7.3. Used by the
// admin-port mux (mi-2b1k) so the k8s probe and the API see the same
// answer.
func ReadyzHTTPHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, status := evaluateReadiness(r.Context(), deps)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			// Best-effort; status header is already written.
			return
		}
	}
}

// makeOpenAPIHandler captures the live huma.API so the spec returned
// reflects every registered operation. The spec is marshalled per
// request — the surface is small and v1 doesn't merit the caching
// dance that huma's auto-handler does internally.
func makeOpenAPIHandler(api huma.API) func(context.Context, *struct{}) (*openapiOutput, error) {
	return func(_ context.Context, _ *struct{}) (*openapiOutput, error) {
		return &openapiOutput{
			Body: func(c huma.Context) {
				spec, err := json.Marshal(api.OpenAPI())
				if err != nil {
					c.SetStatus(http.StatusInternalServerError)
					_, _ = c.BodyWriter().Write([]byte(`{"error":{"code":"openapi_marshal","message":"failed to render spec"}}`))
					return
				}
				c.SetHeader("Content-Type", "application/json; charset=utf-8")
				_, _ = c.BodyWriter().Write(spec)
			},
		}, nil
	}
}

// redocHTML is a self-contained Redoc page. The Redoc bundle is
// loaded from the cdn.redoc.ly CDN with subresource integrity
// pinning. The /docs response sets a route-scoped CSP that allows
// the Redoc origin and worker — the global CSP (script-src 'self')
// would otherwise block the bundle. Polecats touching this CSP MUST
// keep the allowlist minimal and pinned (no wildcards, no
// 'unsafe-eval').
const redocHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Minerals API · Redoc</title>
<style>body{margin:0;padding:0;}</style>
</head>
<body>
<redoc spec-url="/api/v1/openapi.json"></redoc>
<script src="https://cdn.redoc.ly/redoc/v2.5.0/bundles/redoc.standalone.js" integrity="sha384-7Q+50QavCV4WWj9zV8zAmSANyAEXnlpgyo8GOq6y4hETtY5PHl7KeruvBA08fzMo" crossorigin="anonymous"></script>
</body>
</html>
`

// docsCSP is the per-route CSP for /docs. It overrides the global
// §17 CSP just for this endpoint to allow the Redoc bundle from the
// pinned CDN. Inline styles and blob workers are required by Redoc.
const docsCSP = "default-src 'self'; " +
	"script-src 'self' https://cdn.redoc.ly; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com data:; " +
	"img-src 'self' data: https:; " +
	"worker-src 'self' blob:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

func docsHandler(_ context.Context, _ *struct{}) (*docsOutput, error) {
	return &docsOutput{
		Body: func(c huma.Context) {
			c.SetHeader("Content-Type", "text/html; charset=utf-8")
			c.SetHeader("Content-Security-Policy", docsCSP)
			_, _ = c.BodyWriter().Write([]byte(redocHTML))
		},
	}, nil
}
