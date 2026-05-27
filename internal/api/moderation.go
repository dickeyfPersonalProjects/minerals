// Moderation / abuse-handling surface (mi-b2q0). The launch moderation
// story for public user-generated content is POST-moderation: content
// publishes immediately and the operator reacts to reports. This file
// ships the two baseline pieces that story needs:
//
//	POST /api/v1/specimens/{id}/report           (public report affordance)
//	POST /api/v1/admin/specimens/{id}/takedown   (operator force-private)
//
// Reporting reaches the operator via a structured slog WARN event
// ("moderation.report") — there is no email/notification infrastructure
// yet (deferred), and a moderation queue/dashboard is intentionally out
// of scope at launch scale (the operator watches the log stream / its
// alerting). The takedown action is an explicit, audit-logged
// ("moderation.takedown") wrapper over the capability the `admin` role
// already holds via its Casbin `*:*:*` superset (an admin can edit any
// user's specimen through the normal PATCH endpoint); surfacing it as a
// named moderation action gives the operator a single, logged button.
//
// The dedicated admin-console wiring (photo/journal removal, UI) and the
// Keycloak account-disable path land as follow-up sub-beads (mi-jjzc,
// mi-3gxz); see docs/security/moderation.md for the operator runbook.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// reportReasons is the closed set of report categories the public
// affordance offers. Kept small and operator-meaningful — the free-text
// `details` field carries specifics.
var reportReasons = []string{"abuse", "illegal", "spam", "copyright", "privacy", "other"}

// moderationService hosts the report + takedown endpoints. It carries
// the specimen repo (the only content type with a public surface at
// launch) and the authz guard so the takedown enforces the same §13 v2
// gate every other write goes through.
type moderationService struct {
	specimens domain.SpecimenRepo
	guard     authzGuard
}

// registerModerationOperations wires the moderation endpoints. Like the
// other content surfaces it no-ops when the specimen repo is absent (the
// unit-test path that doesn't exercise specimens) — the routes simply
// aren't registered and fall through to the catch-all 404.
func registerModerationOperations(api huma.API, authMW authMiddlewares, guard authzGuard, specimens domain.SpecimenRepo) {
	if specimens == nil {
		return
	}
	s := &moderationService{specimens: specimens, guard: guard}

	huma.Register(api, huma.Operation{
		OperationID: "report-specimen",
		Method:      http.MethodPost,
		Path:        "/api/v1/specimens/{id}/report",
		Summary:     "Report a specimen",
		Description: "Public abuse-report affordance for a publicly visible specimen. " +
			"Anonymous callers are accepted (the page is internet-facing). The report is " +
			"delivered to the operator as a structured log event; there is no moderation " +
			"queue at launch. Returns 404 — not 403/401 — when the caller cannot see the " +
			"specimen, so a report cannot probe for private content (CONTRACT.md §13 v2). " +
			"Per-IP rate limiting (mi-tnru) blunts report spam.",
		Tags:          []string{"moderation"},
		DefaultStatus: http.StatusAccepted,
		Errors:        []int{http.StatusBadRequest, http.StatusNotFound},
		Middlewares:   authMW.Optional(),
	}, s.report)

	huma.Register(api, huma.Operation{
		OperationID: "admin-takedown-specimen",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/specimens/{id}/takedown",
		Summary:     "Force a specimen private (operator takedown)",
		Description: "Operator moderation action: forces any specimen's visibility to " +
			"`private`, removing it from public/unlisted reach. Gated on `specimens:edit` " +
			"for the target — only the `admin` role (Casbin `*:*:*` superset) can edit " +
			"content it does not own, so this is admin-only in practice. Idempotent: a " +
			"specimen that is already private returns 200 unchanged. The action is " +
			"audit-logged. Photo/journal removal and the console UI follow as sub-beads " +
			"(mi-jjzc).",
		Tags:        []string{"moderation"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
		Middlewares: authMW.Protected(),
	}, s.takedown)
}

type reportSpecimenInput struct {
	ID   string `path:"id" doc:"Specimen UUID."`
	Body reportSpecimenBody
}

type reportSpecimenBody struct {
	Reason  string `json:"reason" enum:"abuse,illegal,spam,copyright,privacy,other" doc:"Report category."`
	Details string `json:"details,omitempty" maxLength:"2000" doc:"Optional free-text context for the operator."`
}

type reportAck struct {
	ReportID string `json:"report_id" doc:"Correlation id for this report (matches the logged moderation.report event)."`
	Message  string `json:"message" doc:"Operator-facing acknowledgement."`
}

type reportSpecimenOutput struct {
	Body reportAck
}

func (s *moderationService) report(ctx context.Context, in *reportSpecimenInput) (*reportSpecimenOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	if !slices.Contains(reportReasons, in.Body.Reason) {
		return nil, newAPIError(http.StatusBadRequest, "invalid_reason",
			"reason must be one of abuse|illegal|spam|copyright|privacy|other",
			map[string]any{"field": "reason"})
	}

	sp, err := s.specimens.GetByID(ctx, id)
	if err != nil {
		return nil, mapSpecimenError(err)
	}
	// You can only report what you can see — a forbidden view is
	// rewritten to 404 so a report can't confirm a private specimen
	// exists (CONTRACT.md §13 v2).
	if err := s.guard.checkView(ctx, specimenResource(sp),
		"specimen_not_found", "no such specimen"); err != nil {
		return nil, err
	}

	reportID := domain.NewID()
	reporter := "anonymous"
	if u := auth.FromContext(ctx); u.ID != uuid.Nil {
		reporter = u.ID.String()
	}
	// The report IS the deliverable: a structured WARN the operator's
	// log alerting surfaces. details is included verbatim (bounded to
	// 2000 chars by the schema) so the operator can triage without a
	// second lookup.
	slog.WarnContext(ctx, "moderation report received",
		"event", "moderation.report",
		"report_id", reportID.String(),
		"specimen_id", sp.ID.String(),
		"author_id", sp.AuthorID.String(),
		"visibility", string(sp.Visibility),
		"reason", in.Body.Reason,
		"reporter", reporter,
		"details", in.Body.Details,
	)

	return &reportSpecimenOutput{Body: reportAck{
		ReportID: reportID.String(),
		Message:  "Thank you — this report has been sent to the site operator for review.",
	}}, nil
}

type takedownSpecimenInput struct {
	ID   string `path:"id" doc:"Specimen UUID."`
	Body takedownSpecimenBody
}

type takedownSpecimenBody struct {
	Reason string `json:"reason,omitempty" maxLength:"500" doc:"Optional operator note recorded in the takedown audit log."`
}

func (s *moderationService) takedown(ctx context.Context, in *takedownSpecimenInput) (*specimenResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	current, err := s.specimens.GetByID(ctx, id)
	if err != nil {
		return nil, mapSpecimenError(err)
	}
	// Reuse the §13 v2 edit gate: only a caller who may edit this
	// specimen may take it down. Editing content one does not own
	// requires the `admin` superset, so this is admin-only in practice
	// while still honouring the existing authz seam.
	if err := s.guard.check(ctx, specimenResource(current), actEdit); err != nil {
		return nil, err
	}

	prior := current.Visibility
	if prior != domain.VisibilityPrivate {
		current.Visibility = domain.VisibilityPrivate
		if err := s.specimens.Update(ctx, nil, current); err != nil {
			return nil, mapSpecimenError(err)
		}
	}

	actor := "unknown"
	if u := auth.FromContext(ctx); u.ID != uuid.Nil {
		actor = u.ID.String()
	}
	slog.WarnContext(ctx, "moderation takedown applied",
		"event", "moderation.takedown",
		"specimen_id", current.ID.String(),
		"author_id", current.AuthorID.String(),
		"prior_visibility", string(prior),
		"actor", actor,
		"reason", in.Body.Reason,
	)

	return &specimenResponseOutput{Body: toSpecimenView(current)}, nil
}
