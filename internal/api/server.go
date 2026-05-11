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
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
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

	humaAPI := humago.New(mux, cfg)
	registerSystemOperations(humaAPI, deps)
	registerCollectorOperations(humaAPI, deps.Collectors)
	registerPhotoOperations(humaAPI, mux, deps.Photos)
	registerSpecimenOperations(humaAPI, deps.Specimens)
	registerJournalOperations(humaAPI, deps.Journal)
	registerSpecimenCollectorOperations(humaAPI, deps.Specimens, deps.SpecimenCollectors)
	registerJournalFileOperations(humaAPI, mux, deps.JournalFiles)
	registerMineralSpeciesOperations(humaAPI, deps.MineralSpecies)
	registerQRSheetOperations(humaAPI, deps.QRSheets)

	// Protected /api/v1/* fallback. Real handlers land in feature
	// beads; for now any unmatched /api/v1/ path falls through to a
	// 404 envelope after the auth chain has run.
	apiNotFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint", nil)
	})
	mux.Handle("/api/v1/", Chain(apiNotFound, auth.Auth, auth.RequireUser))

	// SPA fallback (public): everything else is the embedded SPA.
	if deps.WebHandler != nil {
		mux.Handle("/", deps.WebHandler)
	}

	// Apply public middleware to the entire mux. The /api/v1/*
	// chain composes with auth wrappers above, preserving the
	// historical order: Recovery → RequestID → SecHeaders → CSP →
	// Logging → [auth.Auth → auth.RequireUser →] handler.
	publicMW := []func(http.Handler) http.Handler{
		Recovery, RequestID, SecurityHeaders, CSP, Logging,
	}
	return Chain(mux, publicMW...)
}

// healthzOutput uses a body callback so the handler can write the
// "ok" plain-text body verbatim (no JSON envelope) while still
// having the operation participate in the OpenAPI spec.
type healthzOutput struct {
	Body func(huma.Context)
}

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

func makeReadyzHandler(deps Deps) func(context.Context, *struct{}) (*readyzOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*readyzOutput, error) {
		body := readyzBody{Checks: map[string]readyzCheck{}}
		ready := true

		dbCheck := readyzCheck{OK: true}
		if deps.DB != nil {
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if err := deps.DB.Ping(cctx); err != nil {
				dbCheck = readyzCheck{OK: false, Error: err.Error()}
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
				storageCheck = readyzCheck{OK: false, Error: err.Error()}
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
				schemaCheck = readyzCheck{OK: false, Error: err.Error()}
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
		out := &readyzOutput{Body: body}
		if ready {
			out.Status = http.StatusOK
		} else {
			out.Status = http.StatusServiceUnavailable
		}
		return out, nil
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
