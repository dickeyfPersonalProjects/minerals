package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// accountDeleteConfirmation is the exact phrase the caller must send in
// the request body to authorize the irreversible erasure. It mirrors
// the typed-confirmation step in the Settings UI — a second,
// machine-checked guard against an accidental DELETE.
const accountDeleteConfirmation = "DELETE"

// accountObjectStore is the slice of the storage client the deletion
// flow needs: removing one object by key. *storage.Client satisfies it.
type accountObjectStore interface {
	Delete(ctx context.Context, key string) error
}

// accountSessionRevoker force-logs-out every live session for a user.
// *bff.PostgresResolver satisfies it via RevokeAllForUser.
type accountSessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// AccountServiceDeps wires the DELETE /api/v1/account handler. Eraser
// is required; the rest are best-effort cleanup collaborators and may
// be nil in deployments that don't wire them (e.g. tests, or no admin
// Keycloak credentials).
type AccountServiceDeps struct {
	// Eraser runs the transactional DB cascade. Required — a nil Eraser
	// leaves the endpoint unregistered.
	Eraser domain.AccountEraser
	// Storage purges the user's object-store files post-commit.
	Storage accountObjectStore
	// Sessions revokes the user's live sessions so the deleted account
	// can't keep acting through a cookie that outlived the row.
	Sessions accountSessionRevoker
	// Identity removes the Keycloak user. Wired with keycloak.NoopDeleter
	// when admin credentials are absent.
	Identity domain.IdentityDeleter
}

// accountService backs the account-erasure endpoint.
type accountService struct {
	deps AccountServiceDeps
}

// accountDeleteInput is the DELETE /api/v1/account request body. The
// confirm phrase must equal accountDeleteConfirmation.
type accountDeleteInput struct {
	Body struct {
		Confirm string `json:"confirm" doc:"Must be the literal string \"DELETE\" to authorize the irreversible erasure."`
	}
}

// accountDeleteOutput carries no body — the response is 204 No Content.
// Returning a payload here would risk echoing the just-deleted PII; the
// audit trail lives in the server logs instead.
type accountDeleteOutput struct{}

// registerAccountOperations registers DELETE /api/v1/account when an
// AccountEraser is wired. The endpoint uses the Protected() chain:
// only a fully set-up (active) account can self-erase, which is also
// the only state the Settings UI is reachable from.
func registerAccountOperations(api huma.API, mws authMiddlewares, deps *AccountServiceDeps) {
	if deps == nil || deps.Eraser == nil {
		return
	}
	s := &accountService{deps: *deps}

	huma.Register(api, huma.Operation{
		OperationID: "delete-account",
		Method:      http.MethodDelete,
		Path:        "/api/v1/account",
		Summary:     "Delete the caller's account and all personal data (GDPR erasure)",
		Description: "Irreversibly erases the caller's account: every specimen, photo, " +
			"journal entry, journal attachment, collector, uploaded file, and QR sheet " +
			"is hard-deleted from the database, the backing object-store files are purged, " +
			"all sessions are revoked, and the Keycloak identity is removed (when an admin " +
			"client is configured). Mineral-species catalog entries the user authored are " +
			"reassigned to the system account rather than deleted (shared reference data). " +
			"The request body MUST carry `{\"confirm\":\"DELETE\"}`. Returns 204 on success. " +
			"This action cannot be undone.",
		Tags:          []string{"account"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
		Middlewares:   mws.Protected(),
	}, s.delete)
}

func (s *accountService) delete(ctx context.Context, in *accountDeleteInput) (*accountDeleteOutput, error) {
	u := auth.FromContext(ctx)
	if u.Sub == "" || u.ID == uuid.Nil {
		// Defensive — the Protected() chain should already have
		// surfaced this as 401 before reaching the handler.
		return nil, newAPIError(http.StatusUnauthorized,
			"unauthorized", "authentication required", nil)
	}

	if in.Body.Confirm != accountDeleteConfirmation {
		return nil, newAPIError(http.StatusBadRequest,
			"invalid_confirmation",
			`account deletion requires {"confirm":"DELETE"}`,
			map[string]any{"field": "confirm"})
	}

	// 1. The transactional DB cascade. Everything after this is
	//    best-effort cleanup of state outside the Postgres transaction.
	res, err := s.deps.Eraser.Erase(ctx, u.ID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUserNotFound):
			return nil, newAPIError(http.StatusNotFound,
				"user_not_found", "user record disappeared", nil)
		case errors.Is(err, domain.ErrStubUserUndeletable):
			return nil, newAPIError(http.StatusForbidden,
				"account_undeletable", "this account cannot be deleted", nil)
		default:
			slog.ErrorContext(ctx, "account erase: db cascade failed",
				"user_id", u.ID, "err", err)
			return nil, newAPIError(http.StatusInternalServerError,
				"internal_error", "account deletion failed", nil)
		}
	}

	// 2. Purge object-store files. Best-effort + idempotent: the DB rows
	//    are already gone, so a failure here can only leave an orphaned
	//    object (logged for an operator/sweeper to reclaim), never block
	//    the erasure or corrupt state.
	purgeFailures := 0
	if s.deps.Storage != nil {
		for _, key := range res.FreedObjectKeys {
			if err := s.deps.Storage.Delete(ctx, key); err != nil {
				purgeFailures++
				slog.ErrorContext(ctx, "account erase: object purge failed",
					"user_id", u.ID, "s3_key", key, "err", err)
			}
		}
	}

	// 3. Revoke live sessions so a cookie that outlived the row can't
	//    keep acting. Best-effort: the session middleware also fails
	//    closed when it can no longer resolve the user.
	if s.deps.Sessions != nil {
		if err := s.deps.Sessions.RevokeAllForUser(ctx, u.ID); err != nil {
			slog.ErrorContext(ctx, "account erase: session revoke failed",
				"user_id", u.ID, "err", err)
		}
	}

	// 4. Remove the Keycloak identity. Best-effort: a failure leaves an
	//    orphaned IdP user that can only log back in to receive a fresh
	//    pending row (re-registration), never to reach the deleted data.
	if s.deps.Identity != nil {
		if err := s.deps.Identity.DeleteIdentity(ctx, u.Sub); err != nil {
			slog.ErrorContext(ctx, "account erase: identity delete failed",
				"user_id", u.ID, "err", err)
		}
	}

	// 5. Audit log — who (row UUID, NOT email/display_name) and what,
	//    so the deletion is provable without retaining the erased PII
	//    (mi-nwg5 acceptance / GDPR).
	slog.InfoContext(ctx, "account erased",
		"user_id", u.ID,
		"specimens", res.Specimens,
		"photos", res.Photos,
		"journal_entries", res.JournalEntries,
		"collectors", res.Collectors,
		"files", res.Files,
		"qr_sheets", res.QRSheets,
		"reassigned_species", res.ReassignedSpecies,
		"objects_freed", len(res.FreedObjectKeys),
		"object_purge_failures", purgeFailures,
	)

	return &accountDeleteOutput{}, nil
}
