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
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
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
	// incidentRegisterWired reports whether the Law 25 incident register
	// store is configured (INCIDENT_REGISTER_DATABASE_URL set). It flips
	// the overview's incident-register section from "planned" to
	// "available" so the SPA shell knows the endpoints exist (mi-2p6i).
	incidentRegisterWired bool
	// admin is the see-all data source backing the users +
	// published-content surfaces (mi-n5av / mi-gtkp). nil leaves those
	// two endpoints unregistered and their overview sections "planned"
	// — the unit-test path that doesn't exercise them.
	admin domain.AdminRepo
	// registrationToggleWired reports whether the runtime registration
	// toggle (mi-pkn2) is available — i.e. a settings store is wired. It
	// flips the site-management section from "planned" to "available".
	registrationToggleWired bool
	// moderationWired reports whether the console moderation surface is
	// operable (mi-jjzc): content can be listed (AdminRepo) and at least
	// the takedown action is wired (specimen repo). It flips the
	// moderation section from "planned" to "available".
	moderationWired bool
	// suspend wires the operator account-suspension action (mi-3gxz).
	// nil leaves the suspend/unsuspend endpoints unregistered (the
	// unit-test path / a deployment without a user repo).
	suspend *AdminSuspendDeps
}

// AdminSuspendDeps wires the operator account-suspension action
// (mi-3gxz): POST .../users/{id}/suspend and .../unsuspend. Users is
// required — a nil Users (or nil AdminSuspendDeps) leaves both
// endpoints unregistered. Identity and Sessions are optional
// collaborators:
//   - Identity disables/enables the Keycloak user (blocks new logins).
//     nil when no admin client is configured; the action then reports
//     identity_synced=false and relies on the app-level block alone.
//   - Sessions force-logs-out the suspended user's live sessions for
//     immediate effect. nil skips that best-effort step.
type AdminSuspendDeps struct {
	Users    domain.UserRepo
	Identity domain.IdentitySuspender
	Sessions accountSessionRevoker
}

// registerAdminOperations wires the admin/devops console endpoints.
// The overview landing is always registered (it depends on no optional
// repo) — the gate is what restricts access, not the route's presence.
// incidentRegisterWired toggles the incident-register section's status
// in the overview manifest. The users + published-content data surfaces
// (mi-n5av / mi-gtkp) register only when admin is non-nil, matching the
// optional-repo pattern every other content surface follows.
func registerAdminOperations(api huma.API, mws authMiddlewares, guard authzGuard, incidentRegisterWired bool, admin domain.AdminRepo, registrationToggleWired bool, moderationWired bool, suspend *AdminSuspendDeps) {
	s := &adminService{guard: guard, incidentRegisterWired: incidentRegisterWired, admin: admin, registrationToggleWired: registrationToggleWired, moderationWired: moderationWired, suspend: suspend}

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

	if suspend != nil && suspend.Users != nil {
		huma.Register(api, huma.Operation{
			OperationID: "admin-suspend-user",
			Method:      http.MethodPost,
			Path:        "/api/v1/admin/users/{id}/suspend",
			Summary:     "Suspend a user account (operator action)",
			Description: "Operator moderation action: suspends a user account (mi-3gxz). The " +
				"account's Keycloak identity is disabled (blocking new logins and token " +
				"issuance), its live sessions are revoked for immediate effect, and the app " +
				"status flips to `suspended` so the auth gate fail-closes every authenticated " +
				"request. Reversible via unsuspend. Only an `active` account can be suspended " +
				"(pending/deleted return 409); already-suspended is an idempotent 200. An " +
				"operator cannot suspend their own account, nor the system account. Gated on " +
				"`devops:edit` (devops-admin/admin); audit-logged. Returns 502 when the " +
				"identity provider is configured but unreachable — no change is made.",
			Tags:        []string{"admin"},
			Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusBadGateway},
			Middlewares: mws.Protected(),
		}, s.suspendUser)

		huma.Register(api, huma.Operation{
			OperationID: "admin-unsuspend-user",
			Method:      http.MethodPost,
			Path:        "/api/v1/admin/users/{id}/unsuspend",
			Summary:     "Lift a user account suspension (operator action)",
			Description: "Reverses a suspension (mi-3gxz): re-enables the Keycloak identity and " +
				"flips the app status back to `active`. An account that is not currently " +
				"suspended is an idempotent 200 (its status is returned unchanged). Gated on " +
				"`devops:edit` (devops-admin/admin); audit-logged. Returns 502 when the " +
				"identity provider is configured but unreachable — no change is made.",
			Tags:        []string{"admin"},
			Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusBadGateway},
			Middlewares: mws.Protected(),
		}, s.unsuspendUser)
	}

	if admin == nil {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-users",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/users",
		Summary:     "List all users (non-personal fields)",
		Description: "Cursor-paginated list of ALL users across the instance, exposing only " +
			"NON-PERSONAL fields (mi-n5av): opaque id, display name, content counts, account " +
			"status, and creation time. By the mayor's 2026-05-24 PII decision this view " +
			"carries NO email, NO IP, and no auth identifiers beyond the opaque id. Gated on " +
			"the CONTRACT §13 v2 `devops` resource (devops-viewer/devops-admin/admin); every " +
			"access is audit-logged.",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.listUsers)

	huma.Register(api, huma.Operation{
		OperationID: "admin-list-published-content",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/published-content",
		Summary:     "List all published content (usage-policy review)",
		Description: "Cursor-paginated, owner-attributed feed of ALL public/unlisted content " +
			"across users — specimens, their non-private photos, and their journal entries " +
			"(mi-gtkp) — for usage-policy compliance review. Owner attribution is display " +
			"name + opaque id only (NO email). Gated on the CONTRACT §13 v2 `devops` resource; " +
			"every access is audit-logged.",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.listPublishedContent)
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
		Sections: s.sections(),
	}}, nil
}

// sections returns the console manifest with section statuses reflecting
// what is actually wired. Only the incident-register section is dynamic
// today: it reads "available" when its separate-DB store is configured
// (mi-2p6i), "planned" otherwise. The rest stay "planned" until their
// sub-beads land.
func (s *adminService) sections() []adminConsoleSection {
	out := make([]adminConsoleSection, len(adminConsoleSections))
	copy(out, adminConsoleSections)
	for i := range out {
		switch out[i].Key {
		case "incident-register":
			if s.incidentRegisterWired {
				out[i].Status = "available"
			}
		case "users", "published-content":
			// Both surfaces land together off the same AdminRepo
			// (mi-n5av + mi-gtkp); flip them to "available" once it is
			// wired so the SPA shell knows the endpoints exist.
			if s.admin != nil {
				out[i].Status = "available"
			}
		case "site-management":
			// Hosts the runtime registration toggle (mi-pkn2); available
			// once the settings store is wired so the SPA renders the
			// control rather than a placeholder.
			if s.registrationToggleWired {
				out[i].Status = "available"
			}
		case "moderation":
			// Hosts the takedown/removal action hooks (mi-jjzc); available
			// once content can be listed and the takedown action is wired,
			// so the SPA renders the moderation panel rather than a
			// placeholder.
			if s.moderationWired {
				out[i].Status = "available"
			}
		}
	}
	return out
}

// adminUserView is the wire shape of the non-personal user row
// (mi-n5av). The struct has NO email/PII field by construction — the
// PII boundary is enforced here, not just in the SQL.
type adminUserView struct {
	ID            uuid.UUID `json:"id" doc:"Opaque user id (UUIDv7) — the only identifier exposed; no email or auth subject."`
	DisplayName   *string   `json:"display_name" doc:"User's chosen display name; null when never set."`
	Status        string    `json:"status" doc:"Account status." enum:"pending,active,suspended,deleted"`
	SpecimenCount int       `json:"specimen_count" doc:"Number of specimens authored by this user."`
	PhotoCount    int       `json:"photo_count" doc:"Number of photos across this user's specimens."`
	JournalCount  int       `json:"journal_count" doc:"Number of journal entries authored by this user."`
	CreatedAt     time.Time `json:"created_at" doc:"Account creation timestamp (RFC 3339)."`
}

func toAdminUserView(u domain.AdminUser) adminUserView {
	return adminUserView{
		ID:            u.ID,
		DisplayName:   u.DisplayName,
		Status:        string(u.Status),
		SpecimenCount: u.SpecimenCount,
		PhotoCount:    u.PhotoCount,
		JournalCount:  u.JournalCount,
		CreatedAt:     u.CreatedAt,
	}
}

type listAdminUsersInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"200" doc:"Page size (1-200; defaults to 50, values above 200 silently clamped)."`
	Cursor string `query:"cursor" doc:"Opaque pagination cursor from the previous page (CONTRACT.md §10.3)."`
}

type listAdminUsersOutput struct {
	Body adminUserListBody
}

type adminUserListBody struct {
	Items      []adminUserView `json:"items" doc:"Page of users in (created_at DESC, id DESC) order — non-personal fields only."`
	NextCursor *string         `json:"next_cursor" doc:"Cursor for the next page; null at end of results."`
}

func (s *adminService) listUsers(ctx context.Context, in *listAdminUsersInput) (*listAdminUsersOutput, error) {
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actView); err != nil {
		return nil, err
	}
	rows, cursor, err := s.admin.ListUsers(ctx, domain.Page{Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, mapListError(err)
	}
	items := make([]adminUserView, 0, len(rows))
	for _, r := range rows {
		items = append(items, toAdminUserView(r))
	}
	body := adminUserListBody{Items: items}
	if cursor != "" {
		c := string(cursor)
		body.NextCursor = &c
	}
	s.auditView(ctx, "users", len(items))
	return &listAdminUsersOutput{Body: body}, nil
}

// adminContentView is the wire shape of one published-content row
// (mi-gtkp). Owner attribution is display name + opaque id only.
type adminContentView struct {
	Kind             string    `json:"kind" doc:"Content type." enum:"specimen,photo,journal"`
	ID               uuid.UUID `json:"id" doc:"Id of the content row (specimen, photo, or journal entry)."`
	SpecimenID       uuid.UUID `json:"specimen_id" doc:"Id of the anchor specimen (equals id when kind=specimen)."`
	Title            string    `json:"title" doc:"Anchor specimen name."`
	Preview          string    `json:"preview" doc:"Short excerpt (journal body, first 200 chars); empty for specimens and photos."`
	Visibility       string    `json:"visibility" doc:"Effective visibility of the row." enum:"public,unlisted"`
	OwnerID          uuid.UUID `json:"owner_id" doc:"Opaque owner id — no email."`
	OwnerDisplayName *string   `json:"owner_display_name" doc:"Owner display name; null when never set."`
	CreatedAt        time.Time `json:"created_at" doc:"Row creation timestamp (RFC 3339)."`
}

func toAdminContentView(c domain.AdminContent) adminContentView {
	return adminContentView{
		Kind:             string(c.Kind),
		ID:               c.ID,
		SpecimenID:       c.SpecimenID,
		Title:            c.Title,
		Preview:          c.Preview,
		Visibility:       string(c.Visibility),
		OwnerID:          c.OwnerID,
		OwnerDisplayName: c.OwnerDisplayName,
		CreatedAt:        c.CreatedAt,
	}
}

type listPublishedContentInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"200" doc:"Page size (1-200; defaults to 50, values above 200 silently clamped)."`
	Cursor string `query:"cursor" doc:"Opaque pagination cursor from the previous page (CONTRACT.md §10.3)."`
}

type listPublishedContentOutput struct {
	Body publishedContentListBody
}

type publishedContentListBody struct {
	Items      []adminContentView `json:"items" doc:"Page of published content in (created_at DESC, id DESC) order."`
	NextCursor *string            `json:"next_cursor" doc:"Cursor for the next page; null at end of results."`
}

func (s *adminService) listPublishedContent(ctx context.Context, in *listPublishedContentInput) (*listPublishedContentOutput, error) {
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actView); err != nil {
		return nil, err
	}
	rows, cursor, err := s.admin.ListPublishedContent(ctx, domain.Page{Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, mapListError(err)
	}
	items := make([]adminContentView, 0, len(rows))
	for _, r := range rows {
		items = append(items, toAdminContentView(r))
	}
	body := publishedContentListBody{Items: items}
	if cursor != "" {
		c := string(cursor)
		body.NextCursor = &c
	}
	s.auditView(ctx, "published-content", len(items))
	return &listPublishedContentOutput{Body: body}, nil
}

// suspendUserInput is the POST .../users/{id}/suspend request. The
// optional reason is recorded verbatim in the audit log.
type suspendUserInput struct {
	ID   string `path:"id" doc:"Target user's opaque id (UUIDv7)."`
	Body struct {
		Reason string `json:"reason,omitempty" maxLength:"500" doc:"Optional operator note recorded in the suspension audit log."`
	}
}

type unsuspendUserInput struct {
	ID string `path:"id" doc:"Target user's opaque id (UUIDv7)."`
}

// accountStatusResultBody confirms the post-action account state. It
// carries no PII — only the opaque id, the resulting status, and
// whether the Keycloak identity was synced (false when no admin client
// is configured, signalling the operator to disable/enable the user in
// the Keycloak console directly).
type accountStatusResultBody struct {
	ID             uuid.UUID `json:"id" doc:"The affected user's opaque id."`
	Status         string    `json:"status" doc:"The account status after the action." enum:"pending,active,suspended,deleted"`
	IdentitySynced bool      `json:"identity_synced" doc:"Whether the Keycloak identity's enabled flag was synced (false when no admin client is configured)."`
}

type accountStatusOutput struct {
	Body accountStatusResultBody
}

// suspendUser disables an account. Ordering mirrors the registration
// toggle (registration.go): the identity provider is touched FIRST, so
// a sync failure aborts with both the IdP and the app row at their
// prior (active/enabled) state — consistent — rather than marking the
// app suspended while the IdP user stays enabled. Only after the IdP
// agrees do we persist the status flip and revoke sessions.
func (s *adminService) suspendUser(ctx context.Context, in *suspendUserInput) (*accountStatusOutput, error) {
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actEdit); err != nil {
		return nil, err
	}
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	// An operator must not lock themselves out, and the system/stub
	// account underpins legacy author_id FKs — neither is suspendable.
	if caller := auth.FromContext(ctx); caller.ID == id {
		return nil, newAPIError(http.StatusBadRequest, "cannot_suspend_self",
			"you cannot suspend your own account", nil)
	}
	if id == auth.StubUser.ID {
		return nil, newAPIError(http.StatusForbidden, "cannot_suspend_system",
			"the system account cannot be suspended", nil)
	}

	u, err := s.suspend.Users.GetByID(ctx, id)
	if err != nil {
		return nil, mapUserLookupError(err)
	}
	switch u.Status {
	case domain.UserStatusSuspended:
		// Idempotent: already suspended, nothing to do.
		return &accountStatusOutput{Body: accountStatusResultBody{
			ID: id, Status: string(u.Status), IdentitySynced: s.suspend.Identity != nil,
		}}, nil
	case domain.UserStatusDeleted:
		return nil, newAPIError(http.StatusConflict, "account_deleted",
			"cannot suspend a deleted account", nil)
	case domain.UserStatusActive:
		// proceed
	default: // pending
		return nil, newAPIError(http.StatusConflict, "account_not_active",
			"only an active account can be suspended", nil)
	}

	identitySynced, err := s.syncIdentityEnabled(ctx, u.KeycloakSub, false)
	if err != nil {
		return nil, err
	}

	if err := s.suspend.Users.SetStatus(ctx, nil, id, domain.UserStatusSuspended, time.Now().UTC()); err != nil {
		// The IdP is already disabled (when synced) — the account is
		// still blocked, the app status just didn't record it. Fail
		// loudly so the operator can retry; this is the safe direction.
		slog.ErrorContext(ctx, "account suspend: status persist failed",
			"event", "admin.account.suspend_persist_failed", "user_id", id, "err", err)
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to persist the suspension", nil)
	}

	// Best-effort immediate logout. The auth gate already fail-closes on
	// the suspended status, so a revoke failure cannot leave the account
	// usable — it only delays the cookie's own invalidation.
	revoked := true
	if s.suspend.Sessions != nil {
		if err := s.suspend.Sessions.RevokeAllForUser(ctx, id); err != nil {
			revoked = false
			slog.ErrorContext(ctx, "account suspend: session revoke failed",
				"event", "admin.account.suspend_revoke_failed", "user_id", id, "err", err)
		}
	}

	s.auditAccountAction(ctx, "admin.account.suspended", id, identitySynced, revoked, in.Body.Reason)
	return &accountStatusOutput{Body: accountStatusResultBody{
		ID: id, Status: string(domain.UserStatusSuspended), IdentitySynced: identitySynced,
	}}, nil
}

// unsuspendUser lifts a suspension. Symmetric to suspendUser: re-enable
// the IdP first (a failure aborts with both sides still suspended),
// then flip the app status back to active.
func (s *adminService) unsuspendUser(ctx context.Context, in *unsuspendUserInput) (*accountStatusOutput, error) {
	if err := s.guard.check(ctx, &authz.Resource{Type: adminConsoleResource}, actEdit); err != nil {
		return nil, err
	}
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}

	u, err := s.suspend.Users.GetByID(ctx, id)
	if err != nil {
		return nil, mapUserLookupError(err)
	}
	if u.Status != domain.UserStatusSuspended {
		// Idempotent: not suspended, nothing to lift. Return the
		// current status unchanged.
		return &accountStatusOutput{Body: accountStatusResultBody{
			ID: id, Status: string(u.Status), IdentitySynced: s.suspend.Identity != nil,
		}}, nil
	}

	identitySynced, err := s.syncIdentityEnabled(ctx, u.KeycloakSub, true)
	if err != nil {
		return nil, err
	}

	if err := s.suspend.Users.SetStatus(ctx, nil, id, domain.UserStatusActive, time.Now().UTC()); err != nil {
		slog.ErrorContext(ctx, "account unsuspend: status persist failed",
			"event", "admin.account.unsuspend_persist_failed", "user_id", id, "err", err)
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to persist the unsuspension", nil)
	}

	s.auditAccountAction(ctx, "admin.account.unsuspended", id, identitySynced, false, "")
	return &accountStatusOutput{Body: accountStatusResultBody{
		ID: id, Status: string(domain.UserStatusActive), IdentitySynced: identitySynced,
	}}, nil
}

// syncIdentityEnabled flips the Keycloak user's enabled flag when an
// admin client is configured, returning whether the sync ran. A sync
// failure is surfaced as 502 so the caller aborts before mutating app
// state, keeping the IdP and the app row consistent.
func (s *adminService) syncIdentityEnabled(ctx context.Context, sub string, enabled bool) (bool, error) {
	if s.suspend.Identity == nil {
		return false, nil
	}
	if err := s.suspend.Identity.SetIdentityEnabled(ctx, sub, enabled); err != nil {
		slog.ErrorContext(ctx, "account suspend: keycloak identity sync failed",
			"event", "admin.account.identity_sync_failed", "enabled", enabled, "err", err)
		return false, newAPIError(http.StatusBadGateway, "identity_sync_failed",
			"failed to update the account at the identity provider; no change was made", nil)
	}
	return true, nil
}

// mapUserLookupError maps a UserRepo.GetByID error to the API envelope:
// a missing row is 404, anything else is a 500.
func mapUserLookupError(err error) error {
	if errors.Is(err, domain.ErrUserNotFound) {
		return newAPIError(http.StatusNotFound, "user_not_found", "no such user", nil)
	}
	return newAPIError(http.StatusInternalServerError, "internal_error", "failed to load user", nil)
}

// auditAccountAction emits the structured audit event for a suspend or
// unsuspend. No PII: the actor and target are opaque ids, roles come
// from the JWT.
func (s *adminService) auditAccountAction(ctx context.Context, event string, target uuid.UUID, identitySynced, sessionsRevoked bool, reason string) {
	u := auth.FromContext(ctx)
	actor := "unknown"
	if u.ID != uuid.Nil {
		actor = u.ID.String()
	}
	slog.WarnContext(ctx, "admin account action",
		"event", event,
		"target_id", target.String(),
		"actor", actor,
		"roles", strings.Join(u.Roles, ","),
		"identity_synced", identitySynced,
		"sessions_revoked", sessionsRevoked,
		"reason", reason,
	)
}

// auditView emits the structured audit-trail event the bead requires:
// who viewed which admin surface and how many rows they saw. It logs no
// PII (the actor is the opaque caller id; roles come from the JWT) — the
// breadcrumb is for the operator's own access log, not a data export.
func (s *adminService) auditView(ctx context.Context, surface string, count int) {
	u := auth.FromContext(ctx)
	actor := "unknown"
	if u.ID != uuid.Nil {
		actor = u.ID.String()
	}
	slog.InfoContext(ctx, "admin console view",
		"event", "admin.view",
		"surface", surface,
		"actor", actor,
		"roles", strings.Join(u.Roles, ","),
		"count", count,
	)
}
