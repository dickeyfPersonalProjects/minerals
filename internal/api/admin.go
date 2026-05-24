// Admin/devops console surface (mi-agff). This file ships only the
// FOUNDATION per the mayor's DESIGN-FIRST directive: a single gated
// landing endpoint that proves the auth boundary and advertises the
// planned (not-yet-built) console sections. The data-bearing surfaces
// — view-all-users-non-personal, view-all-published-content,
// moderation hooks, and the Law 25 incident register — land as
// follow-up sub-beads (see the mi-agff plan comment).
//
// Gating reuses the existing CONTRACT §13 v2 `devops` Casbin resource
// (internal/authz.DefaultPolicies): the devops-viewer, devops-admin,
// and admin roles already hold `devops:*:view` (admin via the
// superset). No new role, policy, or auth code is introduced.
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
)

// adminConsoleResource is the Casbin resource the console is gated on.
// `devops:view` is held by devops-viewer, devops-admin (inherits
// viewer), and admin (superset) — see internal/authz.DefaultPolicies.
const adminConsoleResource = "devops"

// adminConsoleSection is one planned console surface. The foundation
// returns this manifest as a placeholder so the SPA shell can render
// the console's shape before any section's endpoints exist. `status`
// is "planned" for every section until its sub-bead lands.
type adminConsoleSection struct {
	Key         string `json:"key" doc:"Stable identifier for the planned console surface."`
	Title       string `json:"title" doc:"Human-readable section title."`
	Status      string `json:"status" doc:"Implementation status; \"planned\" until the section's sub-bead lands." enum:"planned,available"`
	Description string `json:"description" doc:"What the section will host once built."`
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

// adminConsoleSections is the placeholder manifest mirroring the
// mi-agff decomposition. Kept package-level so the test can assert the
// foundation advertises exactly the planned surfaces.
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
		Status: "planned",
		Description: "Operational controls (instance health, account state). " +
			"Minimal v1 scope pending operator confirmation.",
	},
}

// adminService hosts the console endpoints. It carries the authz guard
// so the handler enforces the devops gate at the resource layer, the
// same seam every other write/private-read handler uses.
type adminService struct {
	guard authzGuard
}

// registerAdminOperations wires the admin/devops console endpoints.
// The console is always registered (it depends on no optional repo) —
// the gate is what restricts access, not the route's presence.
func registerAdminOperations(api huma.API, mws authMiddlewares, guard authzGuard) {
	s := &adminService{guard: guard}

	huma.Register(api, huma.Operation{
		OperationID: "admin-overview",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/overview",
		Summary:     "Admin/devops console landing",
		Description: "Returns the console landing manifest. Gated to the admin/devops " +
			"role via the CONTRACT §13 v2 `devops` Casbin resource: anonymous callers " +
			"receive 401, authenticated non-admin callers receive 403, and " +
			"devops-viewer/devops-admin/admin receive 200. The foundation (mi-agff) " +
			"ships the gated shell + a placeholder manifest only; the data-bearing " +
			"surfaces follow as sub-beads.",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.overview)
}

func (s *adminService) overview(ctx context.Context, _ *struct{}) (*adminOverviewOutput, error) {
	// The devops gate. A nil enforcer (the unit-test seam) makes this
	// pass, mirroring every other guard.check call site; production
	// always wires a real enforcer so the gate is live in serve.
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actView); err != nil {
		return nil, err
	}
	return &adminOverviewOutput{Body: adminOverviewBody{
		Console: "admin",
		Message: "Admin/devops console shell is live. Data-bearing surfaces are pending " +
			"(see mi-agff). This landing confirms your role cleared the devops gate.",
		Sections: adminConsoleSections,
	}}, nil
}
