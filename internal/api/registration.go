// Runtime registration on/off toggle (mi-pkn2). The operator flips
// self-signup from the admin console without a redeploy: the toggle is
// persisted in the DB-backed settings store (domain.SettingsRepo) that
// the BFF /auth/register gate reads per request, and — when a Keycloak
// admin client is configured — the realm's `registrationAllowed` flag
// is kept in sync so the application and the IdP agree.
//
// Two endpoints under /api/v1/admin/registration, on the §13 v2 `devops`
// resource the rest of the console uses:
//
//	GET  .../registration   read the effective state   (devops:view)
//	PUT  .../registration   flip the toggle            (devops:edit)
//
// devops-viewer can read; devops-admin and admin can flip. The whole
// surface registers only when a settings store is wired (it always is in
// production; the unit-test path that doesn't exercise it leaves it nil).
package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// RegistrationRealmSyncer keeps the Keycloak realm's self-signup flag
// aligned with the application toggle. *keycloak.AdminClient implements
// it; the wiring passes nil when admin credentials are unconfigured, in
// which case the toggle is application-only (the realm is left as-is).
type RegistrationRealmSyncer interface {
	SetRegistrationAllowed(ctx context.Context, enabled bool) error
}

// registrationService hosts the toggle endpoints. defaultEnabled is the
// deploy-time REGISTRATION_ENABLED value, returned by GET (and reported
// as the effective state) until an operator first flips the toggle and a
// stored row exists. realmSync is nil when no Keycloak admin client is
// configured.
type registrationService struct {
	guard          authzGuard
	settings       domain.SettingsRepo
	realmSync      RegistrationRealmSyncer
	defaultEnabled bool
}

// registrationToggleWired reports whether the runtime toggle surface is
// available — i.e. a settings store is wired. Drives the admin overview's
// site-management section status.
func registrationToggleWired(settings domain.SettingsRepo) bool { return settings != nil }

// registerRegistrationOperations wires the toggle endpoints. It no-ops
// when settings is nil (the unit-test path that doesn't exercise the
// toggle) — the routes are absent and fall through to the catch-all 404.
func registerRegistrationOperations(
	api huma.API, mws authMiddlewares, guard authzGuard,
	settings domain.SettingsRepo, realmSync RegistrationRealmSyncer, defaultEnabled bool,
) {
	if settings == nil {
		return
	}
	s := &registrationService{
		guard:          guard,
		settings:       settings,
		realmSync:      realmSync,
		defaultEnabled: defaultEnabled,
	}

	huma.Register(api, huma.Operation{
		OperationID: "admin-get-registration",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/registration",
		Summary:     "Read the runtime registration toggle",
		Description: "Returns whether self-signup is currently enabled and whether that value " +
			"comes from the stored runtime toggle or the deploy-time default. Gated on the " +
			"§13 v2 `devops` resource (devops-viewer, devops-admin, admin).",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden},
		Middlewares: mws.Protected(),
	}, s.get)

	huma.Register(api, huma.Operation{
		OperationID: "admin-set-registration",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/registration",
		Summary:     "Flip the runtime registration toggle",
		Description: "Enables or disables self-signup at runtime, no redeploy required. Persists " +
			"the value to the settings store (read per request by the /auth/register gate) and, " +
			"when a Keycloak admin client is configured, syncs the realm's `registrationAllowed` " +
			"flag so the app and IdP stay consistent. Gated on `devops:edit` — devops-admin and " +
			"admin can flip; a view-only devops-viewer receives 403. The change is audit-logged.",
		Tags:        []string{"admin"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusBadGateway},
		Middlewares: mws.Protected(),
	}, s.set)
}

type registrationStateBody struct {
	Enabled bool   `json:"enabled" doc:"Whether self-signup is currently enabled."`
	Source  string `json:"source" doc:"Where the value comes from." enum:"stored,default"`
}

type getRegistrationOutput struct {
	Body registrationStateBody
}

func (s *registrationService) get(ctx context.Context, _ *struct{}) (*getRegistrationOutput, error) {
	if err := s.guard.check(ctx, devopsResource(), actView); err != nil {
		return nil, err
	}
	enabled, found, err := s.settings.RegistrationEnabled(ctx)
	if err != nil {
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to read registration setting", nil)
	}
	body := registrationStateBody{Source: "stored", Enabled: enabled}
	if !found {
		body.Source = "default"
		body.Enabled = s.defaultEnabled
	}
	return &getRegistrationOutput{Body: body}, nil
}

type setRegistrationInput struct {
	Body setRegistrationBody
}

type setRegistrationBody struct {
	Enabled bool `json:"enabled" doc:"Target state: true enables self-signup, false disables it."`
}

type setRegistrationOutput struct {
	Body setRegistrationResultBody
}

type setRegistrationResultBody struct {
	Enabled     bool `json:"enabled" doc:"The newly persisted self-signup state."`
	RealmSynced bool `json:"realm_synced" doc:"Whether the Keycloak realm flag was synced (false when no admin client is configured)."`
}

// set flips the toggle. The Keycloak realm is synced FIRST so a sync
// failure leaves both the realm and the stored toggle at their previous
// value (consistent) rather than diverging — only after the realm
// agrees do we persist the new value the app gate reads. When no admin
// client is configured the realm step is skipped and the toggle is
// application-only.
func (s *registrationService) set(ctx context.Context, in *setRegistrationInput) (*setRegistrationOutput, error) {
	if err := s.guard.check(ctx, devopsResource(), actEdit); err != nil {
		return nil, err
	}

	actor := uuid.Nil
	if u := auth.FromContext(ctx); u.ID != uuid.Nil {
		actor = u.ID
	}

	realmSynced := false
	if s.realmSync != nil {
		if err := s.realmSync.SetRegistrationAllowed(ctx, in.Body.Enabled); err != nil {
			slog.ErrorContext(ctx, "registration toggle: keycloak realm sync failed",
				"event", "admin.registration.realm_sync_failed",
				"enabled", in.Body.Enabled,
				"err", err)
			return nil, newAPIError(http.StatusBadGateway, "realm_sync_failed",
				"failed to sync the identity provider; the toggle was not changed", nil)
		}
		realmSynced = true
	}

	if err := s.settings.SetRegistrationEnabled(ctx, in.Body.Enabled, actor); err != nil {
		slog.ErrorContext(ctx, "registration toggle: persist failed",
			"event", "admin.registration.persist_failed",
			"enabled", in.Body.Enabled,
			"err", err)
		return nil, newAPIError(http.StatusInternalServerError, "internal_error",
			"failed to persist registration setting", nil)
	}

	actorStr := "unknown"
	if actor != uuid.Nil {
		actorStr = actor.String()
	}
	slog.InfoContext(ctx, "registration toggle changed",
		"event", "admin.registration.changed",
		"enabled", in.Body.Enabled,
		"realm_synced", realmSynced,
		"actor", actorStr,
	)

	return &setRegistrationOutput{Body: setRegistrationResultBody{
		Enabled:     in.Body.Enabled,
		RealmSynced: realmSynced,
	}}, nil
}
