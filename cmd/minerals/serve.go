package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/config"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/incidentregister"
	"github.com/dickeyfPersonalProjects/minerals/internal/keycloak"
	"github.com/dickeyfPersonalProjects/minerals/internal/mindat"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage"
	"github.com/dickeyfPersonalProjects/minerals/internal/web"
)

// newMindatClient constructs the Mindat HTTP client when an API key
// is configured. An unset key returns nil so the api package
// recognises it as DB-only mode (per the F-1 acceptance criteria —
// the system MUST work without a Mindat key).
func newMindatClient(apiKey string) api.MindatLookup {
	if apiKey == "" {
		slog.Info("mindat: no API key configured; mineral-species lookup is DB-only")
		return nil
	}
	c, err := mindat.NewClient(mindat.Options{APIKey: apiKey})
	if err != nil {
		slog.Warn("mindat: client init failed; running DB-only", "err", err)
		return nil
	}
	return c
}

// dbPinger adapts pgxpool.Pool's Ping to the api.Pinger interface.
type dbPinger struct{ pool *pgxpool.Pool }

func (d dbPinger) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// webHandler returns the embedded-SPA fallback handler, or nil when the
// deployment runs API-only (WEB_SERVE_MODE=disabled, mi-zomq). A nil
// handler tells api.New to skip the "/" catch-all so the backend serves
// API/docs/health only — the SPA is then served from a single shared
// source (MinIO/CDN) at the static/ingress layer, which removes the
// multi-replica per-pod asset skew that motivated the decoupling.
func webHandler(cfg *config.Config) http.Handler {
	if !cfg.ServeFrontend() {
		slog.Info("web: SPA serving disabled (WEB_SERVE_MODE=disabled); backend serves API/docs/health only")
		return nil
	}
	return web.Handler()
}

func runServe(_ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateForServe(); err != nil {
		return err
	}
	configureLogger(cfg.LogLevel)

	slog.Info("server starting",
		"version", version, "port", cfg.Port, "env", cfg.Env)

	rootCtx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("serve: init db pool: %w", err)
	}
	defer pool.Close()

	// Law 25 confidentiality-incident register (mi-2p6i). When
	// INCIDENT_REGISTER_DATABASE_URL is set we open a SECOND pool to that
	// separate database and bootstrap the register's own schema there
	// (CREATE TABLE IF NOT EXISTS — deliberately not a migrations/ file,
	// so the app migrate/erasure paths have no name for it). Unset =>
	// nil store, endpoints unregistered, console section stays "planned".
	incidentReg, incidentRegPool, err := buildIncidentRegister(rootCtx, cfg)
	if err != nil {
		return fmt.Errorf("serve: init incident register: %w", err)
	}
	if incidentRegPool != nil {
		defer incidentRegPool.Close()
	}

	store, err := storage.New(rootCtx, storage.Options{
		Endpoint:        cfg.S3Endpoint,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		Region:          cfg.S3Region,
		Bucket:          cfg.S3Bucket,
	})
	if err != nil {
		return fmt.Errorf("serve: init storage: %w", err)
	}

	if cfg.IsDev() {
		ensureCtx, cancel := context.WithTimeout(rootCtx, 5*time.Second)
		if err := store.EnsureBucket(ensureCtx); err != nil {
			cancel()
			slog.Warn("bucket auto-create failed", "err", err, "bucket", cfg.S3Bucket)
		} else {
			cancel()
			slog.Info("bucket ready", "bucket", cfg.S3Bucket)
		}
	}

	if cfg.IsDev() {
		// Auto-apply pending migrations on dev startup so a fresh
		// `docker compose up -d` (mi-8ky) lands a usable app on :8080
		// without requiring a separate `make migrate-up` first. In
		// prod (ENV=prod) we keep the strict mismatch behavior — the
		// schema is owned by the migrate Job per design §6.4.
		if err := autoMigrateDev(rootCtx, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("serve: auto-migrate (dev): %w", err)
		}
	}
	if err := verifySchemaVersion(rootCtx, cfg.DatabaseURL); err != nil {
		return err
	}

	expected, err := highestMigration()
	if err != nil {
		return fmt.Errorf("serve: read embedded migrations: %w", err)
	}

	// Backend-side JWT verification (mi-aw3a). Construction only
	// validates config — OIDC discovery / JWKS fetching is lazy
	// (first request), so startup does not depend on Keycloak being
	// reachable. NewVerifier errors here only on a malformed config
	// (empty issuer or client id).
	verifier, err := oidc.NewVerifier(rootCtx, oidc.Config{
		Issuer:   cfg.OIDCIssuerURL,
		ClientID: cfg.OIDCClientID,
		JWKSURL:  cfg.OIDCJWKSURL,
	})
	if err != nil {
		return fmt.Errorf("serve: init oidc verifier: %w", err)
	}
	slog.Info("oidc verifier configured",
		"issuer", cfg.OIDCIssuerURL,
		"client_id", cfg.OIDCClientID,
		"jwks_url", cfg.OIDCJWKSURL)

	// CONTRACT.md §13 v2 authorization (mi-aw3b). The §13 v2 policy
	// set is static and code-defined (authz.DefaultPolicies), so an
	// in-memory enforcer seeded at startup is the canonical store —
	// no Postgres policy adapter is needed. The shares lookup is
	// DB-backed so the `:shared` instance qualifier resolves against
	// the shares table (migration 0010).
	enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
	if err != nil {
		return fmt.Errorf("serve: init authz enforcer: %w", err)
	}
	if err := authz.SeedDefaultPolicies(enforcer); err != nil {
		return fmt.Errorf("serve: seed authz policies: %w", err)
	}
	slog.Info("authz enforcer configured", "policies", len(authz.DefaultPolicies))

	users := db.NewUserPostgres(pool)
	settings := db.NewSettingsPostgres(pool)

	// Shared Keycloak admin client (mi-nwg5 + mi-pkn2). Built once and
	// reused for GDPR IdP erasure and the registration realm sync; nil
	// when admin credentials are unconfigured.
	kcAdmin := buildKeycloakAdmin(rootCtx, cfg)

	// BFF auth handlers (mi-bm5b). The bundle is built only when
	// every required input is present — OIDC discovery URL, client
	// id + secret, HMAC key for the state cookie, and an absolute
	// redirect_uri. In dev / test deployments missing any of these,
	// the handlers stay unregistered and the SPA falls back to the
	// (deprecated) PKCE path; the same /auth/login link 404s, which
	// is the right signal that BFF auth is off in this environment.
	bffAuth, oauthClient, sessions, err := buildBFFAuth(rootCtx, cfg, pool, users, settings)
	if err != nil {
		return fmt.Errorf("serve: init bff auth: %w", err)
	}

	photoDeps := &api.PhotoServiceDeps{
		Photos:         db.NewPhotoPostgres(pool),
		Files:          db.NewFilePostgres(pool),
		Storage:        store,
		Specimens:      db.NewSpecimenPostgres(pool),
		Users:          db.NewUserPostgres(pool),
		MaxUploadBytes: cfg.MaxUploadBytes,
		RunInTx: func(ctx context.Context, fn func(tx domain.Tx) error) error {
			return db.RunInTx(ctx, pool, func(pgxTx pgx.Tx) error {
				return fn(pgxTx)
			})
		},
	}

	deps := api.Deps{
		DB:              dbPinger{pool: pool},
		Storage:         store,
		SchemaVersion:   newSchemaVersionProbe(cfg.DatabaseURL),
		ExpectedVersion: expected,
		WebHandler:      webHandler(cfg),
		Collectors:      db.NewCollectorPostgres(pool),
		Photos:          photoDeps,
		Specimens:       db.NewSpecimenPostgres(pool),
		Journal: &api.JournalServiceDeps{
			Entries: db.NewJournalEntryPostgres(pool),
		},
		SpecimenCollectors: db.NewSpecimenCollectorPostgres(pool),
		MineralSpecies: &api.MineralSpeciesServiceDeps{
			Repo:   db.NewMineralSpeciesPostgres(pool),
			Mindat: newMindatClient(cfg.MindatAPIKey),
		},
		QRSheets: db.NewQRSheetPostgres(pool),
		Account: &api.AccountServiceDeps{
			Eraser:   db.NewAccountErasePostgres(pool),
			Storage:  store,
			Sessions: bff.NewPostgresResolver(pool),
			Identity: identityDeleterFrom(kcAdmin),
		},
		Users:               users,
		Verifier:            verifier,
		Enforcer:            enforcer,
		Admin:               db.NewAdminPostgres(pool),
		IncidentRegister:    incidentReg,
		Settings:            settings,
		RegistrationSync:    registrationSyncFrom(kcAdmin),
		RegistrationDefault: cfg.RegistrationEnabled,
		BFFAuth:             bffAuth,
		SessionMW:           buildSessionMW(cfg, oauthClient, sessions, users),
		CSRFMW:              buildCSRFMW(oauthClient),
		RateLimitMW:         buildRateLimitMW(cfg),
		JournalFiles: &api.JournalFileServiceDeps{
			Entries:        db.NewJournalEntryPostgres(pool),
			Attachments:    db.NewJournalEntryFilePostgres(pool),
			Files:          db.NewFilePostgres(pool),
			Storage:        store,
			MaxUploadBytes: cfg.MaxUploadBytes,
			RunInTx: func(ctx context.Context, fn func(tx domain.Tx) error) error {
				return db.RunInTx(ctx, pool, func(pgxTx pgx.Tx) error {
					return fn(pgxTx)
				})
			},
		},
	}
	handler := api.New(deps)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Admin listener: Prometheus `/metrics`, plus the k8s probe paths.
	// Separate port keeps scrape/probe traffic off the user-facing
	// listener and out of the public Ingress (mi-2b1k / design §7.3).
	adminShutdown, adminErr := startAdminServer(":"+cfg.AdminPort, newAdminHandler(adminProbes{
		readyz: api.ReadyzHTTPHandler(deps),
	}))

	// Background goroutine: hourly auth.sessions cleanup
	// (mi-twql / docs/design/auth-bff.md §cleanup). The loop
	// stops on rootCtx cancellation; we wait on cleanerDone
	// after srv.Shutdown so the process does not exit while
	// the pool is still being read.
	sessionCleaner := bff.NewCleaner(pool)
	cleanerDone := make(chan struct{})
	go func() {
		defer close(cleanerDone)
		sessionCleaner.Run(rootCtx)
	}()

	srvErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
		close(srvErr)
	}()

	select {
	case <-rootCtx.Done():
		slog.Info("shutdown initiated")
	case err := <-srvErr:
		if err != nil {
			return fmt.Errorf("serve: listen: %w", err)
		}
	case err := <-adminErr:
		if err != nil {
			return fmt.Errorf("serve: admin listen: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("serve: shutdown: %w", err)
	}
	if err := adminShutdown(shutdownCtx); err != nil {
		// Don't fail the whole shutdown for the admin listener — the
		// API server is what matters; the admin port is operator-only.
		slog.Warn("admin listener shutdown error", "err", err)
	}
	// Wait for the session cleanup goroutine to drain. Its loop
	// returns immediately on rootCtx.Done() (already cancelled by
	// this point), so this is a fast handshake — not a stall.
	<-cleanerDone
	slog.Info("shutdown complete")
	return nil
}

// verifySchemaVersion enforces the §6 mismatch contract on serve
// startup. If migrations/ is empty (bd #1 not yet landed), the check
// is skipped.
func verifySchemaVersion(ctx context.Context, dbURL string) error {
	has, err := hasMigrations()
	if err != nil {
		return fmt.Errorf("verify schema: enumerate migrations: %w", err)
	}
	if !has {
		slog.Info("schema version check skipped: migrations/ is empty")
		return nil
	}
	expected, err := highestMigration()
	if err != nil {
		return err
	}
	cur, dirty, err := schemaVersion(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("verify schema: read current version: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema is dirty at version %d; resolve before starting", cur)
	}
	if cur != expected {
		return fmt.Errorf(
			"schema version mismatch: binary expects v%d, database is at v%d "+
				"(in development, run: make migrate-up; "+
				"in production, run the migrate Job before rolling the deployment)",
			expected, cur)
	}
	slog.Info("schema version verified", "version", cur)
	return nil
}

func configureLogger(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})))
}

// buildBFFAuth assembles the V2 cookie-auth handler bundle when the
// deployment supplies all of OIDC_CLIENT_SECRET, OAUTH_STATE_HMAC_KEY,
// and OIDC_REDIRECT_URI. Missing any one returns (nil, nil)
// — the BFF routes stay unregistered and the legacy PKCE path keeps
// working. This lets the migration roll out one environment at a
// time without forking the binary (per docs/design/auth-bff.md).
//
// The OAuth client is constructed here (and not lazily inside the
// handlers) so misconfiguration — bad issuer, unreachable Keycloak
// at boot — surfaces at process start rather than on the first user
// login. The session cleanup goroutine (mi-twql) already runs
// regardless, so cleanup is decoupled from this gate.
func buildBFFAuth(
	ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, users domain.UserRepo, settings domain.SettingsRepo,
) (*bff.Handlers, bff.OAuthClient, bff.SessionResolver, error) {
	if cfg.OIDCRedirectURIFromLegacyEnv {
		slog.Warn("config: PUBLIC_OIDC_REDIRECT_URI is deprecated — rename it to OIDC_REDIRECT_URI (backend-consumed, not SPA-facing); the legacy name will stop being read in a future release",
			"redirect_uri", cfg.OIDCRedirectURI)
	}
	if cfg.OIDCClientSecret == "" || cfg.OAuthStateHMACKey == "" || cfg.OIDCRedirectURI == "" {
		// The half-configured state (client_secret + hmac present but
		// redirect missing) is almost always an operator mistake — a
		// ConfigMap that lost OIDC_REDIRECT_URI — not an intentional
		// disable. Make it LOUD (mi-kebf).
		if cfg.OIDCClientSecret != "" && cfg.OAuthStateHMACKey != "" && cfg.OIDCRedirectURI == "" {
			slog.Error("bff auth: OIDC_CLIENT_SECRET + OAUTH_STATE_HMAC_KEY are set but OIDC_REDIRECT_URI is unset — login will NOT work. Set OIDC_REDIRECT_URI=https://<app-host>/auth/callback")
		} else {
			slog.Info("bff auth: disabled (missing OIDC_CLIENT_SECRET / OAUTH_STATE_HMAC_KEY / OIDC_REDIRECT_URI)",
				"client_secret_present", cfg.OIDCClientSecret != "",
				"hmac_key_present", cfg.OAuthStateHMACKey != "",
				"redirect_uri_present", cfg.OIDCRedirectURI != "")
		}
		return nil, nil, nil, nil
	}
	if cfg.SessionAbsoluteExpiresHours <= 0 {
		return nil, nil, nil, fmt.Errorf("SESSION_ABSOLUTE_EXPIRES_HOURS must be > 0 when BFF auth is enabled")
	}
	if cfg.SessionIdleTimeoutMinutes <= 0 {
		return nil, nil, nil, fmt.Errorf("SESSION_IDLE_TIMEOUT_MINUTES must be > 0 when BFF auth is enabled")
	}

	oauthClient, err := bff.NewKeycloakOAuthClient(ctx, bff.OAuthConfig{
		Issuer:       cfg.OIDCIssuerURL,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		// Discovery URL override (mi-8tnv) — see the sister
		// OIDC_JWKS_URL setting in serve.go above. Empty in prod;
		// set in docker-compose so discovery uses the in-network
		// `keycloak:8080` address while the canonical issuer stays
		// the host-facing `localhost:8081` URL.
		DiscoveryURL: cfg.OIDCDiscoveryURL,
		// Standard Keycloak claim-emitting scopes — `openid` is
		// required for OIDC, the others surface the email + roles
		// the resolver needs on first-login (docs/design/auth-bff.md
		// §sessions-table).
		Scopes: []string{"openid", "profile", "email", "roles"},
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("oauth client: %w", err)
	}

	sessions := bff.NewPostgresResolver(pool)

	handlers, err := bff.NewHandlers(
		bff.HandlerConfig{
			RedirectURI:           cfg.OIDCRedirectURI,
			PostLogoutRedirectURI: cfg.PostLogoutRedirectURI,
			StateHMACKey:          []byte(cfg.OAuthStateHMACKey),
			SessionAbsoluteMax:    time.Duration(cfg.SessionAbsoluteExpiresHours) * time.Hour,
			Cookie: bff.CookieConfig{
				Path:     "/",
				Secure:   cfg.CookieSecure,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   time.Duration(cfg.CookieMaxAgeSeconds) * time.Second,
			},
			StateCookieSecure:     cfg.CookieSecure,
			RegistrationEnabled:   cfg.RegistrationEnabled,
			RegistrationEnabledFn: registrationGate(settings, cfg.RegistrationEnabled),
			EnforceCSRFOnLogout:   cfg.BFFEnforceCSRFOnLogout,
			TrustForwardedFor:     cfg.TrustForwardedFor,
		},
		bff.HandlerDeps{
			OAuth:    oauthClient,
			Sessions: sessions,
			Users:    bffUserResolver(users),
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("handlers: %w", err)
	}
	slog.Info("bff auth: enabled",
		"redirect_uri", cfg.OIDCRedirectURI,
		"enforce_csrf_on_logout", cfg.BFFEnforceCSRFOnLogout,
		"registration_enabled", cfg.RegistrationEnabled,
		"session_absolute_max_hours", cfg.SessionAbsoluteExpiresHours,
		"session_idle_timeout_minutes", cfg.SessionIdleTimeoutMinutes)
	return handlers, oauthClient, sessions, nil
}

// registrationGate returns the per-request self-signup resolver the BFF
// /auth/register handler consults (mi-pkn2). It reads the DB-backed
// runtime toggle; an unset row (never flipped) or a read error falls
// back to the deploy-time default so a transient DB hiccup can't
// silently flip the policy.
func registrationGate(settings domain.SettingsRepo, def bool) func(context.Context) bool {
	return func(ctx context.Context) bool {
		enabled, found, err := settings.RegistrationEnabled(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "bff: registration toggle read failed; using configured default",
				"default", def, "err", err)
			return def
		}
		if !found {
			return def
		}
		return enabled
	}
}

// buildSessionMW wraps SessionMiddleware with the runtime config so
// api.New can apply it to the top-level mux. Returns nil when BFF
// auth is disabled — api.New then leaves the legacy bearer-token
// chain intact (mi-sap2 / mi-1d5i #8).
func buildSessionMW(
	cfg *config.Config,
	oauthClient bff.OAuthClient,
	sessions bff.SessionResolver,
	users domain.UserRepo,
) func(http.Handler) http.Handler {
	if oauthClient == nil || sessions == nil {
		return nil
	}
	return bff.SessionMiddleware(bff.MiddlewareDeps{
		Sessions: sessions,
		OAuth:    oauthClient,
		Users:    users,
		CookieConfig: bff.CookieConfig{
			Path:     "/",
			Secure:   cfg.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   time.Duration(cfg.CookieMaxAgeSeconds) * time.Second,
		},
		IdleTimeout: time.Duration(cfg.SessionIdleTimeoutMinutes) * time.Minute,
	})
}

// buildKeycloakAdmin constructs the shared Keycloak admin-REST client
// from the four KEYCLOAK_ADMIN_* credentials. It is reused by two
// privileged surfaces: GDPR account erasure (mi-nwg5, deleting the IdP
// user) and the runtime registration toggle (mi-pkn2, syncing the
// realm's `registrationAllowed` flag). Returns nil when the credentials
// are absent — callers then fall back to the no-op deleter and an
// application-only registration toggle. Construction is lazy on the
// token fetch, so a misconfigured base URL surfaces at first use, not at
// boot.
func buildKeycloakAdmin(ctx context.Context, cfg *config.Config) *keycloak.AdminClient {
	admin, err := keycloak.NewAdminClient(ctx, keycloak.AdminConfig{
		BaseURL:      cfg.KeycloakAdminBaseURL,
		Realm:        cfg.KeycloakRealm,
		ClientID:     cfg.KeycloakAdminClientID,
		ClientSecret: cfg.KeycloakAdminClientSecret,
	})
	if err != nil {
		slog.Info("keycloak admin not configured; account deletion will not remove the IdP user "+
			"and the registration toggle will be application-only",
			"reason", err)
		return nil
	}
	slog.Info("keycloak admin configured",
		"base_url", cfg.KeycloakAdminBaseURL, "realm", cfg.KeycloakRealm)
	return admin
}

// identityDeleterFrom adapts the shared admin client into the GDPR
// IdP-deleter. A nil client (unconfigured) yields the no-op deleter so
// account deletion still succeeds (the app row + sessions are gone; the
// orphaned IdP user can only re-register).
func identityDeleterFrom(admin *keycloak.AdminClient) domain.IdentityDeleter {
	if admin == nil {
		return keycloak.NoopDeleter{}
	}
	return admin
}

// registrationSyncFrom adapts the shared admin client into the
// registration realm-syncer. A nil client (unconfigured) returns an
// untyped nil so the toggle stays application-only — returning the typed
// nil pointer would wrap a non-nil interface around a nil value and
// defeat the api layer's nil check.
func registrationSyncFrom(admin *keycloak.AdminClient) api.RegistrationRealmSyncer {
	if admin == nil {
		return nil
	}
	return admin
}

// buildIncidentRegister wires the Law 25 confidentiality-incident
// register (mi-2p6i) when INCIDENT_REGISTER_DATABASE_URL is set. It
// returns the store (as the api.IncidentRegister interface) and the
// owning pool so serve can close it on shutdown. When the URL is unset
// it returns (nil, nil, nil) — the register stays unwired, which keeps a
// single-database `docker compose up` working.
//
// The URL MUST differ from DATABASE_URL: pointing both at the same
// database would defeat the Law 25 isolation guarantee (the register
// must survive the app's erasure flow), so an identical value is a hard
// config error rather than a silent foot-gun. The store bootstraps its
// own schema on the separate pool via EnsureSchema.
func buildIncidentRegister(
	ctx context.Context, cfg *config.Config,
) (api.IncidentRegister, *pgxpool.Pool, error) {
	if cfg.IncidentRegisterDatabaseURL == "" {
		slog.Info("incident register: disabled (INCIDENT_REGISTER_DATABASE_URL unset); console section stays \"planned\"")
		return nil, nil, nil
	}
	if cfg.IncidentRegisterDatabaseURL == cfg.DatabaseURL {
		return nil, nil, fmt.Errorf(
			"INCIDENT_REGISTER_DATABASE_URL must point at a DIFFERENT database than DATABASE_URL " +
				"(Law 25 requires the incident register be isolated from app data and the erasure flow)")
	}
	regPool, err := db.NewPool(ctx, cfg.IncidentRegisterDatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("incident register pool: %w", err)
	}
	store := incidentregister.NewStore(regPool)
	if err := store.EnsureSchema(ctx); err != nil {
		regPool.Close()
		return nil, nil, fmt.Errorf("incident register schema: %w", err)
	}
	slog.Info("incident register: enabled (separate database)")
	return store, regPool, nil
}

// buildCSRFMW returns the stored-synchronizer CSRF middleware only
// when BFF auth is configured (signaled by a non-nil OAuth client).
// api.New scopes the wrap to /api/v1/* so /auth/* keeps its own
// EnforceCSRFOnLogout gate without double-handling.
func buildCSRFMW(oauthClient bff.OAuthClient) func(http.Handler) http.Handler {
	if oauthClient == nil {
		return nil
	}
	return bff.CSRFMiddleware
}

// buildRateLimitMW constructs the per-tier API rate limiter (mi-tnru)
// from config. Returns nil when RATE_LIMIT_ENABLED is false, leaving
// the chain unlimited. The limiter uses the real clock (time.Now);
// tests inject their own.
func buildRateLimitMW(cfg *config.Config) func(http.Handler) http.Handler {
	if !cfg.RateLimit.Enabled {
		return nil
	}
	tier := func(t config.RateLimitTier) api.RateLimitTier {
		return api.RateLimitTier{
			Requests: t.Requests,
			Window:   time.Duration(t.WindowSeconds) * time.Second,
		}
	}
	return api.NewRateLimitMiddleware(api.RateLimitOptions{
		Auth:  tier(cfg.RateLimit.Auth),
		Read:  tier(cfg.RateLimit.Read),
		Write: tier(cfg.RateLimit.Write),
		File:  tier(cfg.RateLimit.File),
	})
}

// bffUserResolver bridges bff.UserResolver into the application's
// first-login resolver. Reusing api.ResolveOrCreateUser keeps the
// cookie path and the bearer-token path on the same users row —
// duplicating the resolve-or-create logic here would let the two
// flows drift apart.
func bffUserResolver(repo domain.UserRepo) bff.UserResolver {
	return func(ctx context.Context, sub, email string) (uuid.UUID, error) {
		row, err := api.ResolveOrCreateUser(ctx, repo, auth.User{Sub: sub, Email: email})
		if err != nil {
			return uuid.Nil, err
		}
		return row.ID, nil
	}
}

// parseCount is a small convenience for the migrate subcommand.
func parseCount(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}
