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

	// BFF auth handlers (mi-bm5b). The bundle is built only when
	// every required input is present — OIDC discovery URL, client
	// id + secret, HMAC key for the state cookie, and an absolute
	// redirect_uri. In dev / test deployments missing any of these,
	// the handlers stay unregistered and the SPA falls back to the
	// (deprecated) PKCE path; the same /auth/login link 404s, which
	// is the right signal that BFF auth is off in this environment.
	bffAuth, err := buildBFFAuth(rootCtx, cfg, pool, users)
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
		SchemaVersion:   func(ctx context.Context) (uint, bool, error) { return schemaVersion(ctx, cfg.DatabaseURL) },
		ExpectedVersion: expected,
		WebHandler:      web.Handler(),
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
		Users:    users,
		Verifier: verifier,
		Enforcer: enforcer,
		BFFAuth:  bffAuth,
		RuntimeOIDC: api.RuntimeOIDCConfig{
			IssuerURL:   cfg.PublicOIDCIssuerURL,
			ClientID:    cfg.PublicOIDCClientID,
			RedirectURI: cfg.PublicOIDCRedirectURI,
		},
		CSPIssuerOrigin: cfg.PublicOIDCIssuerOrigin,
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
// and PUBLIC_OIDC_REDIRECT_URI. Missing any one returns (nil, nil)
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
	ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, users domain.UserRepo,
) (*bff.Handlers, error) {
	if cfg.OIDCClientSecret == "" || cfg.OAuthStateHMACKey == "" || cfg.PublicOIDCRedirectURI == "" {
		slog.Info("bff auth: disabled (missing OIDC_CLIENT_SECRET / OAUTH_STATE_HMAC_KEY / PUBLIC_OIDC_REDIRECT_URI)",
			"client_secret_present", cfg.OIDCClientSecret != "",
			"hmac_key_present", cfg.OAuthStateHMACKey != "",
			"redirect_uri_present", cfg.PublicOIDCRedirectURI != "")
		return nil, nil //nolint:nilnil // explicit "feature off" signal, not an error
	}
	if cfg.SessionAbsoluteExpiresHours <= 0 {
		return nil, fmt.Errorf("SESSION_ABSOLUTE_EXPIRES_HOURS must be > 0 when BFF auth is enabled")
	}

	oauthClient, err := bff.NewKeycloakOAuthClient(ctx, bff.OAuthConfig{
		Issuer:       cfg.OIDCIssuerURL,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		// Standard Keycloak claim-emitting scopes — `openid` is
		// required for OIDC, the others surface the email + roles
		// the resolver needs on first-login (docs/design/auth-bff.md
		// §sessions-table).
		Scopes: []string{"openid", "profile", "email", "roles"},
	})
	if err != nil {
		return nil, fmt.Errorf("oauth client: %w", err)
	}

	handlers, err := bff.NewHandlers(
		bff.HandlerConfig{
			RedirectURI:           cfg.PublicOIDCRedirectURI,
			PostLogoutRedirectURI: cfg.PostLogoutRedirectURI,
			StateHMACKey:          []byte(cfg.OAuthStateHMACKey),
			SessionAbsoluteMax:    time.Duration(cfg.SessionAbsoluteExpiresHours) * time.Hour,
			Cookie: bff.CookieConfig{
				Path:     "/",
				Secure:   cfg.CookieSecure,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   time.Duration(cfg.CookieMaxAgeSeconds) * time.Second,
			},
			StateCookieSecure:   cfg.CookieSecure,
			EnforceCSRFOnLogout: cfg.BFFEnforceCSRFOnLogout,
			TrustForwardedFor:   cfg.TrustForwardedFor,
		},
		bff.HandlerDeps{
			OAuth:    oauthClient,
			Sessions: bff.NewPostgresResolver(pool),
			Users:    bffUserResolver(users),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("handlers: %w", err)
	}
	slog.Info("bff auth: enabled",
		"redirect_uri", cfg.PublicOIDCRedirectURI,
		"enforce_csrf_on_logout", cfg.BFFEnforceCSRFOnLogout,
		"session_absolute_max_hours", cfg.SessionAbsoluteExpiresHours)
	return handlers, nil
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
