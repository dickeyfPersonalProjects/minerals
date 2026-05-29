// Moderation / abuse-handling surface (mi-b2q0). The launch moderation
// story for public user-generated content is POST-moderation: content
// publishes immediately and the operator reacts to reports. This file
// ships the two baseline pieces that story needs:
//
//	POST /api/v1/specimens/{id}/report           (public report affordance)
//	POST /api/v1/admin/specimens/{id}/takedown   (operator force-private)
//	POST /api/v1/admin/photos/{id}/remove        (operator remove a photo)
//	POST /api/v1/admin/journal/{id}/remove       (operator remove a journal entry)
//
// Reporting reaches the operator via a structured slog WARN event
// ("moderation.report") — there is no email/notification infrastructure
// yet (deferred), and a moderation queue/dashboard is intentionally out
// of scope at launch scale (the operator watches the log stream / its
// alerting). The three operator actions are explicit, audit-logged
// ("moderation.takedown" / "moderation.remove_photo" /
// "moderation.remove_journal") wrappers over capabilities the `admin`
// role already holds via its Casbin `*:*:*` superset (an admin can edit
// or delete any user's content through the normal endpoints); surfacing
// them as named moderation actions gives the operator single, logged
// buttons in the admin console (mi-jjzc).
//
// Each action reuses the same §13 v2 authz seam the owner-facing
// endpoints use (actEdit for takedown, actDelete for photo/journal
// removal): editing/deleting content one does not own requires the
// admin superset, so all three are admin-only in practice. Photo
// removal reuses the photo-delete cleanup path (DB tx + MinIO objects);
// journal removal reuses the entry repo's delete (which still rejects an
// entry with attachments with 409 — the operator clears those first).
//
// The Keycloak account-disable path lands as a follow-up sub-bead
// (mi-3gxz); see docs/security/moderation.md for the operator runbook.
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

// moderationService hosts the report + takedown + removal endpoints. It
// carries the specimen repo (the content type with a public surface and
// the takedown target) and the authz guard so every action enforces the
// same §13 v2 gate every other write goes through. The photo and
// journal deps are optional: their removal endpoints register only when
// the corresponding repo is wired, mirroring the optional-repo pattern
// every other content surface follows.
type moderationService struct {
	specimens domain.SpecimenRepo
	guard     authzGuard
	// photos backs POST /api/v1/admin/photos/{id}/remove. nil leaves
	// that route unregistered (the unit-test path that doesn't exercise
	// photos).
	photos *PhotoServiceDeps
	// journal backs POST /api/v1/admin/journal/{id}/remove. nil leaves
	// that route unregistered.
	journal *JournalServiceDeps
}

// registerModerationOperations wires the moderation endpoints. Like the
// other content surfaces it no-ops when the specimen repo is absent (the
// unit-test path that doesn't exercise specimens) — the routes simply
// aren't registered and fall through to the catch-all 404. The photo and
// journal removal endpoints register only when their backing repo is
// wired.
func registerModerationOperations(api huma.API, authMW authMiddlewares, guard authzGuard, specimens domain.SpecimenRepo, photos *PhotoServiceDeps, journal *JournalServiceDeps) {
	if specimens == nil {
		return
	}
	s := &moderationService{specimens: specimens, guard: guard, photos: photos, journal: journal}

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

	if photos != nil && photos.Photos != nil && photos.Files != nil && photos.Storage != nil && photos.RunInTx != nil {
		huma.Register(api, huma.Operation{
			OperationID: "admin-remove-photo",
			Method:      http.MethodPost,
			Path:        "/api/v1/admin/photos/{id}/remove",
			Summary:     "Remove a photo (operator moderation)",
			Description: "Operator moderation action: permanently removes any photo — the " +
				"photos row, its files row, and all three MinIO objects (original + display + " +
				"thumbnail) — regardless of owner. Gated on `photos:delete` for the photo's " +
				"parent specimen; only the `admin` role (Casbin `*:*:*` superset) can delete " +
				"content it does not own, so this is admin-only in practice. The action is " +
				"audit-logged (`moderation.remove_photo`). This is a named, logged wrapper over " +
				"the capability admin already holds via the normal DELETE /api/v1/photos/{id}.",
			Tags:          []string{"moderation"},
			DefaultStatus: http.StatusOK,
			Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
			Middlewares:   authMW.Protected(),
		}, s.removePhoto)
	}

	if journal != nil && journal.Entries != nil {
		huma.Register(api, huma.Operation{
			OperationID: "admin-remove-journal",
			Method:      http.MethodPost,
			Path:        "/api/v1/admin/journal/{id}/remove",
			Summary:     "Remove a journal entry (operator moderation)",
			Description: "Operator moderation action: permanently removes any journal entry " +
				"regardless of owner. Gated on `journal:delete`; only the `admin` role (Casbin " +
				"`*:*:*` superset) can delete content it does not own, so this is admin-only in " +
				"practice. Returns 409 when the entry still has file attachments — the operator " +
				"removes those first (or uses photo removal for image attachments). The action " +
				"is audit-logged (`moderation.remove_journal`).",
			Tags:          []string{"moderation"},
			DefaultStatus: http.StatusOK,
			Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict},
			Middlewares:   authMW.Protected(),
		}, s.removeJournal)
	}
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

// moderationActionAck is the body returned by the photo/journal removal
// actions: a stable correlation id (matching the logged event) plus an
// operator-facing message. Removal has no resource left to return, so —
// unlike takedown, which returns the now-private specimen — these return
// a small acknowledgement instead.
type moderationActionAck struct {
	ActionID string `json:"action_id" doc:"Correlation id for this action (matches the logged moderation event)."`
	Message  string `json:"message" doc:"Operator-facing acknowledgement."`
}

type moderationActionOutput struct {
	Body moderationActionAck
}

type removePhotoInput struct {
	ID   string `path:"id" doc:"Photo UUID."`
	Body removeContentBody
}

type removeJournalInput struct {
	ID   string `path:"id" doc:"Journal entry UUID."`
	Body removeContentBody
}

type removeContentBody struct {
	Reason string `json:"reason,omitempty" maxLength:"500" doc:"Optional operator note recorded in the removal audit log."`
}

func (s *moderationService) removePhoto(ctx context.Context, in *removePhotoInput) (*moderationActionOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	photo, err := s.photos.Photos.GetByID(ctx, id)
	if err != nil {
		return nil, mapPhotoError(err)
	}
	// Photos carry neither owner nor visibility of their own — both are
	// inherited from the parent specimen, which is what the §13 v2 gate
	// resolves against (mirrors PhotoService.enforcePhoto). A missing
	// parent is an orphan we treat as a missing photo (404, no leak).
	parent, err := s.specimens.GetByID(ctx, photo.SpecimenID)
	if err != nil {
		return nil, newAPIError(http.StatusNotFound, "photo_not_found", "no such photo",
			map[string]any{"field": "id"})
	}
	if err := s.guard.check(ctx, photoResource(photo.ID, parent), actDelete); err != nil {
		return nil, err
	}
	if err := removePhotoBytes(ctx, *s.photos, photo); err != nil {
		return nil, mapPhotoError(err)
	}

	actionID := domain.NewID()
	slog.WarnContext(ctx, "moderation photo removed",
		"event", "moderation.remove_photo",
		"action_id", actionID.String(),
		"photo_id", photo.ID.String(),
		"specimen_id", photo.SpecimenID.String(),
		"author_id", parent.AuthorID.String(),
		"actor", moderationActor(ctx),
		"reason", in.Body.Reason,
	)
	return &moderationActionOutput{Body: moderationActionAck{
		ActionID: actionID.String(),
		Message:  "Photo removed.",
	}}, nil
}

func (s *moderationService) removeJournal(ctx context.Context, in *removeJournalInput) (*moderationActionOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	entry, err := s.journal.Entries.GetByID(ctx, id)
	if err != nil {
		return nil, mapJournalError(err)
	}
	// Reuse the §13 v2 delete gate: deleting an entry one does not own
	// requires the admin superset, so this is admin-only in practice.
	if err := s.guard.check(ctx, journalResource(entry), actDelete); err != nil {
		return nil, err
	}
	if err := s.journal.Entries.Delete(ctx, nil, id); err != nil {
		return nil, mapJournalError(err)
	}

	actionID := domain.NewID()
	slog.WarnContext(ctx, "moderation journal entry removed",
		"event", "moderation.remove_journal",
		"action_id", actionID.String(),
		"journal_id", entry.ID.String(),
		"specimen_id", entry.SpecimenID.String(),
		"author_id", entry.AuthorID.String(),
		"actor", moderationActor(ctx),
		"reason", in.Body.Reason,
	)
	return &moderationActionOutput{Body: moderationActionAck{
		ActionID: actionID.String(),
		Message:  "Journal entry removed.",
	}}, nil
}

// moderationActor returns the opaque id of the acting operator for the
// audit log, or "unknown" when no authenticated user is on the context
// (the nil-enforcer test seam).
func moderationActor(ctx context.Context) string {
	if u := auth.FromContext(ctx); u.ID != uuid.Nil {
		return u.ID.String()
	}
	return "unknown"
}
