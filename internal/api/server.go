// Package api wires the HTTP server: middleware chain, public vs
// protected route groups, and the v1 placeholder handlers
// (healthz, readyz, openapi, docs). Real CRUD lands in subsequent
// feature beads (per CONTRACT.md §13 / §10).
//
// Router choice: stdlib net/http ServeMux. Go 1.22+ supports method-
// scoped patterns (e.g. "GET /healthz") and per-segment wildcards.
// That's enough for v1; chi/gorilla buy little for the routes we
// have. If a future need (sub-routers, regex captures) actually
// pushes against the stdlib limits, a swap can happen as a
// coordinated decision rather than a default-from-day-one.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
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
}

// New returns an http.Handler with the v1 routes wired up. Callers
// embed the result in their own *http.Server.
func New(deps Deps) http.Handler {
	mux := http.NewServeMux()

	publicMW := []func(http.Handler) http.Handler{
		Recovery,
		RequestID,
		SecurityHeaders,
		CSP,
		Logging,
	}
	protectedMW := append(append([]func(http.Handler) http.Handler{},
		publicMW...),
		auth.Auth, auth.RequireUser,
	)

	// Public: liveness, readiness, OpenAPI spec, docs page.
	mux.Handle("GET /healthz", Chain(http.HandlerFunc(healthzHandler), publicMW...))
	mux.Handle("GET /readyz", Chain(http.HandlerFunc(readyzHandler(deps)), publicMW...))
	mux.Handle("GET /api/v1/openapi.json", Chain(http.HandlerFunc(openapiHandler), publicMW...))
	mux.Handle("GET /docs", Chain(http.HandlerFunc(docsHandler), publicMW...))

	// Protected /api/v1/* group. Real handlers land in feature beads;
	// for now, any unmatched /api/v1/ path falls through to a 404
	// envelope after the auth chain has run.
	apiNotFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint", nil)
	})
	mux.Handle("/api/v1/", Chain(apiNotFound, protectedMW...))

	// SPA fallback (public): everything else is the embedded SPA.
	if deps.WebHandler != nil {
		mux.Handle("/", Chain(deps.WebHandler, publicMW...))
	}

	return mux
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// readyzHandler runs the §14 readiness checks: DB ping (2s timeout),
// bucket head (2s timeout), schema version match. Returns 200 if all
// pass, 503 with a per-check JSON body otherwise.
func readyzHandler(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type check struct {
			OK      bool   `json:"ok"`
			Error   string `json:"error,omitempty"`
			Version uint   `json:"version,omitempty"`
		}
		body := struct {
			Ready  bool             `json:"ready"`
			Checks map[string]check `json:"checks"`
		}{Checks: map[string]check{}}

		ready := true

		dbCheck := check{OK: true}
		if deps.DB != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := deps.DB.Ping(ctx); err != nil {
				dbCheck = check{OK: false, Error: err.Error()}
				ready = false
			}
		} else {
			dbCheck = check{OK: false, Error: "no db wired"}
			ready = false
		}
		body.Checks["database"] = dbCheck

		storageCheck := check{OK: true}
		if deps.Storage != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := deps.Storage.HeadBucket(ctx); err != nil {
				storageCheck = check{OK: false, Error: err.Error()}
				ready = false
			}
		} else {
			storageCheck = check{OK: false, Error: "no storage wired"}
			ready = false
		}
		body.Checks["storage"] = storageCheck

		schemaCheck := check{OK: true}
		if deps.SchemaVersion != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			ver, dirty, err := deps.SchemaVersion(ctx)
			switch {
			case err != nil:
				schemaCheck = check{OK: false, Error: err.Error()}
				ready = false
			case dirty:
				schemaCheck = check{OK: false, Error: "schema is dirty", Version: ver}
				ready = false
			case deps.ExpectedVersion != 0 && ver != deps.ExpectedVersion:
				schemaCheck = check{
					OK:      false,
					Error:   fmt.Sprintf("expected version %d, found %d", deps.ExpectedVersion, ver),
					Version: ver,
				}
				ready = false
			default:
				schemaCheck = check{OK: true, Version: ver}
			}
		} else {
			// No SchemaVersion fn supplied — treat as "no migrations yet";
			// readyz still requires DB and storage to be up, but doesn't
			// require schema-version evidence.
			schemaCheck = check{OK: true}
		}
		body.Checks["schema"] = schemaCheck

		body.Ready = ready

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if ready {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(body)
	}
}

// openapiHandler returns the v1 placeholder spec. The real spec is
// generated by the chosen OpenAPI framework when that bead lands
// (per design §4.6.A).
func openapiHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(
		`{"openapi":"3.0.0","info":{"title":"minerals","version":"0.0.1"},"paths":{}}`))
}

// docsHandler returns the v1 placeholder documentation page. Redoc
// lands when the OpenAPI framework is wired.
func docsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(
		"<!doctype html><html><head><title>minerals docs</title></head>" +
			"<body><h1>minerals</h1><p>Redoc will live here.</p></body></html>"))
}
