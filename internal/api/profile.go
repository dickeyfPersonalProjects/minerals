package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// MaxDisplayNameLen caps the display_name a user can submit during
// first-login setup. The schema column itself is `text` (unbounded);
// the cap exists at the API boundary to keep abusive inputs out of
// logs and toast messages.
const MaxDisplayNameLen = 80

// profileSetupInput is the POST /api/v1/profile request body. v1
// collects only display_name; later beads may extend this surface.
type profileSetupInput struct {
	Body struct {
		DisplayName string `json:"display_name" doc:"Public display name; required, 1–80 characters, trimmed."`
	}
}

// profileBody is the post-setup user state returned to the SPA.
// `pending` is always false after a successful setup — the
// frontend reads it back to confirm the gate has lifted before
// navigating to the original destination.
type profileBody struct {
	ID          string `json:"id" doc:"User row UUID."`
	Email       string `json:"email" doc:"Email from the JWT claim, persisted at first-login."`
	DisplayName string `json:"display_name" doc:"Display name as persisted."`
	Pending     bool   `json:"pending" doc:"Profile-setup-required flag; always false on a successful response."`
}

type profileOutput struct {
	Body profileBody
}

// profileService wires the profile setup handler against a
// UserRepo. Constructed in api.New when Users is non-nil.
type profileService struct {
	repo domain.UserRepo
}

func registerProfileOperations(api huma.API, mws authMiddlewares, repo domain.UserRepo) {
	if repo == nil {
		return
	}
	s := &profileService{repo: repo}

	huma.Register(api, huma.Operation{
		OperationID: "complete-profile",
		Method:      http.MethodPost,
		Path:        "/api/v1/profile",
		Summary:     "Complete first-login profile setup",
		Description: "Persists the caller's display_name and flips their account from " +
			"`pending` to `active`. After a successful call the first-login gate (mi-2hf) " +
			"no longer redirects this user away from protected endpoints. " +
			"This endpoint MUST be reachable while the caller is still pending — it is " +
			"the only protected route that bypasses the gate.",
		Tags:        []string{"profile"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws.SetupAllowed(),
	}, s.complete)
}

func (s *profileService) complete(
	ctx context.Context, in *profileSetupInput,
) (*profileOutput, error) {
	u := auth.FromContext(ctx)
	if u.ID == (auth.User{}.ID) {
		// Defensive — the auth middleware should already have
		// surfaced this as 401 before reaching the handler.
		return nil, newAPIError(http.StatusUnauthorized,
			"unauthorized", "authentication required", nil)
	}

	name := strings.TrimSpace(in.Body.DisplayName)
	if name == "" {
		return nil, newAPIError(http.StatusBadRequest,
			"invalid_display_name", "display_name is required",
			map[string]any{"field": "display_name"})
	}
	if len(name) > MaxDisplayNameLen {
		return nil, newAPIError(http.StatusBadRequest,
			"invalid_display_name",
			"display_name exceeds the maximum length",
			map[string]any{"field": "display_name", "max": MaxDisplayNameLen})
	}

	now := time.Now().UTC()
	if err := s.repo.MarkActive(ctx, nil, u.ID, name, now); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			return nil, newAPIError(http.StatusNotFound,
				"user_not_found", "user record disappeared", nil)
		}
		return nil, err
	}
	return &profileOutput{Body: profileBody{
		ID:          u.ID.String(),
		Email:       u.Email,
		DisplayName: name,
		Pending:     false,
	}}, nil
}
