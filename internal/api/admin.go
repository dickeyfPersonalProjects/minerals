// Admin/devops console surface (mi-agff / mi-ilvt). This file ships
// two phases of the console:
//
//   - Foundation (mi-agff, #302): gated landing endpoint + placeholder
//     manifest that proves the auth boundary and advertises planned sections.
//
//   - Site management v1 (mi-ilvt, this PR): two read-only endpoints:
//     GET /api/v1/admin/health  — instance/readiness summary + registration status
//     GET /api/v1/admin/stats   — aggregate row counts (users, specimens, photos, journal entries)
//
// Both new endpoints are gated on the existing CONTRACT §13 v2 `devops`
// Casbin resource (devops-viewer, devops-admin, admin).  All responses
// are read-only and carry no PII.
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// adminConsoleResource is the Casbin resource the console is gated on.
// `devops:view` is held by devops-viewer, devops-admin (inherits
// viewer), and admin (superset) — see internal/authz.DefaultPolicies.
const adminConsoleResource = "devops"

// adminConsoleSection is one planned console surface. The foundation
// returns this manifest as a placeholder so the SPA shell can render
// the console's shape before any section's endpoints exist. `status`
// is "planned" until its sub-bead lands, "available" once the
// endpoint exists.
type adminConsoleSection struct {
	Key         string `json:"key" doc:"Stable identifier for the planned console surface."`
	Title       string `json:"title" doc:"Human-readable section title."`
	Status      string `json:"status" doc:"Implementation status." enum:"planned,available"`
	Description string `json:"description" doc:"What the section hosts."`
}

// adminOverviewBody is the GET /api/v1/admin/overview response. It is
// intentionally a static manifest in the foundation pass — reaching it
// at all is the meaningful signal (the caller cleared the devops gate).
type adminOverviewBody struct {
	Console  string                `json:"console" doc:"Console identifier; always \"admin\"."`
	Message  string                `json:"message" doc:"Operator-facing note that the console shell is live and surfaces are pending."`
	Sections []adminConsoleSection `json:"sections" doc:"Planned console surfaces (mi-agff decomposition). Each lands as a follow-up sub-bead."`
}

type adminOverviewOutput struct {
	Body adminOverviewBody
}

// adminConsoleSections is the manifest of console surfaces. The
// site-management section is marked "available" now that
// /admin/health and /admin/stats have landed (mi-ilvt).
var adminConsoleSections = []adminConsoleSection{
	{
		Key:    "users",
		Title:  "Users (non-personal)",
		Status: "planned",
		Description: "List/inspect all users' non-personal fields for operations. " +
			"Scoped to non-PII fields; blocked on the GDPR/PII classification sign-off.",
	},
	{
		Key:    "published-content",
		Title:  "Published content review",
		Status: "planned",
		Description: "All public/unlisted specimens, photos, and journal entries " +
			"across users, for usage-policy compliance review.",
	},
	{
		Key:    "moderation",
		Title:  "Moderation",
		Status: "planned",
		Description: "Unpublish/hide/remove policy-violating content. Hosts the " +
			"takedown actions designed in the moderation story (mi-b2q0).",
	},
	{
		Key:    "incident-register",
		Title:  "Confidentiality-incident register",
		Status: "planned",
		Description: "Law 25 incident register in a separate, append-only store " +
			"with >=5yr retention; admin-authored compliance data.",
	},
	{
		Key:    "site-management",
		Title:  "Site management",
		Status: "available",
		Description: "Operational controls: instance health summary (/admin/health), " +
			"aggregate site stats (/admin/stats). Read-only, devops:view gated.",
	},
}

// ── Health endpoint types ────────────────────────────────────────────────────

// adminHealthCheck is a single component check in the health summary.
type adminHealthCheck struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Version uint   `json:"version,omitempty"`
}

// adminHealthBody is the GET /api/v1/admin/health response.
type adminHealthBody struct {
	Ready               bool                        `json:"ready" doc:"True when all readiness checks pass."`
	RegistrationEnabled bool                        `json:"registration_enabled" doc:"Current value of the REGISTRATION_ENABLED operator switch (read-only; no toggle here)."`
	Checks              map[string]adminHealthCheck `json:"checks" doc:"Per-dependency readiness checks (database, storage, schema)."`
}

type adminHealthOutput struct {
	// Status overrides the HTTP response code: 200 when ready, 503 otherwise.
	Status int
	Body   adminHealthBody
}

// ── Stats endpoint types ─────────────────────────────────────────────────────

// adminStatsBody is the GET /api/v1/admin/stats response.
type adminStatsBody struct {
	Users          int64 `json:"users" doc:"Total rows in the users table."`
	Specimens      int64 `json:"specimens" doc:"Total rows in the specimens table."`
	Photos         int64 `json:"photos" doc:"Total rows in the photos table."`
	JournalEntries int64 `json:"journal_entries" doc:"Total rows in the journal_entries table."`
}

type adminStatsOutput struct {
	Body adminStatsBody
}

// ── Service ──────────────────────────────────────────────────────────────────

// adminService hosts the console endpoints. It carries the authz guard
// so every handler enforces the devops gate at the resource layer, the
// same seam every other write/private-read handler uses.
type adminService struct {
	guard               authzGuard
	stats               domain.AdminStatsProvider // nil → zero counts (unit-test seam)
	registrationEnabled bool
	deps                Deps // kept for evaluateReadiness reuse
}

// registerAdminOperations wires the admin/devops console endpoints.
// The console is always registered — the gate restricts access, not the
// route's presence.
func registerAdminOperations(api huma.API, mws authMiddlewares, guard authzGuard, deps Deps) {
	s := &adminService{
		guard:               guard,
		stats:               deps.AdminStats,
		registrationEnabled: deps.RegistrationEnabled,
		deps:                deps,
	}

	huma.Register(api, huma.Operation{
		OperationID: "admin-overview",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/overview",
		Summary:     "Admin/devops console landing",
		Description: "Returns the console landing manifest. Gated to the admin/devops " +
			"role via the CONTRACT §13 v2 `devops` Casbin resource: anonymous callers " +
			"receive 401, authenticated non-admin callers receive 403, and " +
			"devops-viewer/devops-admin/admin receive 200.",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.overview)

	huma.Register(api, huma.Operation{
		OperationID: "admin-health",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/health",
		Summary:     "Admin health summary",
		Description: "Returns per-dependency readiness checks (database, storage, schema) " +
			"plus the current REGISTRATION_ENABLED operator switch. " +
			"Gated on devops:view. HTTP 503 when any check fails. " +
			"Read-only; does not expose PII or internal connection strings (mi-f5v3).",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable},
		Middlewares: mws.Protected(),
	}, s.health)

	huma.Register(api, huma.Operation{
		OperationID: "admin-stats",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/stats",
		Summary:     "Admin aggregate stats",
		Description: "Returns aggregate row counts across core entities " +
			"(users, specimens, photos, journal entries). " +
			"All values are non-PII aggregates. Gated on devops:view.",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.stats_)
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (s *adminService) overview(ctx context.Context, _ *struct{}) (*adminOverviewOutput, error) {
	// The devops gate. A nil enforcer (unit-test seam) lets this pass,
	// mirroring every other guard.check call site.
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actView); err != nil {
		return nil, err
	}
	return &adminOverviewOutput{Body: adminOverviewBody{
		Console: "admin",
		Message: "Admin/devops console shell is live. Site-management surfaces are available. " +
			"Remaining data-bearing surfaces are pending (see mi-agff).",
		Sections: adminConsoleSections,
	}}, nil
}

func (s *adminService) health(ctx context.Context, _ *struct{}) (*adminHealthOutput, error) {
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actView); err != nil {
		return nil, err
	}

	body, status := evaluateReadiness(ctx, s.deps)

	// Translate readyzCheck → adminHealthCheck (same shape; re-typed to
	// keep the admin surface decoupled from the /readyz internal types).
	checks := make(map[string]adminHealthCheck, len(body.Checks))
	for k, c := range body.Checks {
		checks[k] = adminHealthCheck{OK: c.OK, Error: c.Error, Version: c.Version}
	}

	return &adminHealthOutput{
		Status: status,
		Body: adminHealthBody{
			Ready:               body.Ready,
			RegistrationEnabled: s.registrationEnabled,
			Checks:              checks,
		},
	}, nil
}

// stats_ has a trailing underscore because `stats` is already a field name on
// adminService.
func (s *adminService) stats_(ctx context.Context, _ *struct{}) (*adminStatsOutput, error) {
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actView); err != nil {
		return nil, err
	}

	var body adminStatsBody

	if s.stats != nil {
		var err error
		if body.Users, err = s.stats.CountUsers(ctx); err != nil {
			return nil, err
		}
		if body.Specimens, err = s.stats.CountSpecimens(ctx); err != nil {
			return nil, err
		}
		if body.Photos, err = s.stats.CountPhotos(ctx); err != nil {
			return nil, err
		}
		if body.JournalEntries, err = s.stats.CountJournalEntries(ctx); err != nil {
			return nil, err
		}
	}
	// If stats is nil (unit-test seam), all counts are zero — that's fine.

	return &adminStatsOutput{Body: body}, nil
}
