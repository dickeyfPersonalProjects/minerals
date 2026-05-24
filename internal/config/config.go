// Package config loads runtime configuration from environment
// variables. Per CONTRACT.md §15 this is the sole entry point for
// reading the process environment; every other package receives
// values via dependency injection.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the v1 runtime configuration. All fields correspond to
// entries in the §15 inventory.
type Config struct {
	Port string
	// AdminPort is the operator-facing HTTP listener that serves
	// `/metrics` (Prometheus exposition) and the k8s probe paths
	// (`/healthz`, `/readyz`). Separate from the user-facing Port so
	// scrape and probe traffic doesn't compete with API requests for
	// handler capacity, and so the admin surface can stay off the
	// public Ingress (see mi-2b1k / `kustomize/base/service.yaml`).
	AdminPort         string
	DatabaseURL       string
	S3Endpoint        string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3Bucket          string
	S3Region          string
	MaxUploadBytes    int64
	LogLevel          string
	Env               string
	// MindatAPIKey is the credential for the Mindat REST API used by
	// the mineral-species lookup pipeline (mi-dtg / F-1). Optional in
	// every environment — when unset, the system falls back to
	// DB-only mineral-species lookups.
	MindatAPIKey string

	// OIDCRedirectURI is the absolute URL the BFF passes to Keycloak on
	// /auth/login and reuses on /auth/callback's code exchange.
	// Backend-consumed under V2 BFF — the SPA never sees it. Read from
	// OIDC_REDIRECT_URI; for a transition window it falls back to the
	// legacy PUBLIC_OIDC_REDIRECT_URI name with a deprecation warning
	// (see CONFIG.md and mi-kebf — the misleading PUBLIC_ prefix caused
	// a prod incident when an operator deleted it as SPA-only config).
	OIDCRedirectURI string

	// OIDCRedirectURIFromLegacyEnv is true when OIDCRedirectURI was
	// sourced from the deprecated PUBLIC_OIDC_REDIRECT_URI env var
	// rather than OIDC_REDIRECT_URI. The boot path logs a deprecation
	// warning in that case (mi-kebf).
	OIDCRedirectURIFromLegacyEnv bool

	// OIDCIssuerURL and OIDCClientID configure backend-side JWT
	// verification (mi-aw3a). The backend is a pure resource server:
	// it validates bearer tokens against the Keycloak realm's JWKS
	// endpoint (discovered from the issuer URL) and checks that the
	// token's audience contains OIDCClientID. No client secret — the
	// backend never performs an auth-code exchange. Both default to
	// the local dev Keycloak so `docker compose up` wires real auth
	// without extra env; prod overlays supply the real values via the
	// `minerals-config` ConfigMap (see CONFIG.md).
	OIDCIssuerURL string
	OIDCClientID  string

	// OIDCJWKSURL, when non-empty, overrides OIDC discovery for
	// locating the realm's JWKS endpoint (mi-dau). The verifier still
	// checks the JWT's `iss` claim against OIDCIssuerURL — JWKSURL
	// only changes where keys are fetched from. Set this when the
	// canonical issuer URL (which must match the browser-issued
	// token's `iss`) is not reachable from inside the backend
	// container, as in the docker-compose dev stack where the issuer
	// is `http://localhost:8081/realms/minerals` (host-side) but the
	// in-container backend reaches Keycloak at `http://keycloak:8080`.
	// Empty in prod — OIDC discovery handles it.
	OIDCJWKSURL string

	// OIDCDiscoveryURL, when non-empty, overrides the URL the BFF
	// OAuth client uses to fetch the OIDC discovery document (mi-8tnv).
	// The canonical OIDCIssuerURL is still used to validate the
	// discovery doc's `iss` field (and the `iss` claim on issued
	// tokens) — OIDCDiscoveryURL only changes where the well-known
	// document is fetched from. Sister setting to OIDCJWKSURL: same
	// rationale, applied to the BFF OAuth client's discovery instead
	// of the verifier's JWKS lookup. Empty in prod.
	OIDCDiscoveryURL string

	// OIDCClientSecret is the Keycloak confidential-client secret
	// the BFF uses on the server-to-server code exchange (mi-bm5b).
	// Required to enable the /auth/login → /auth/callback flow; when
	// empty the BFF handlers are not registered and the SPA falls
	// back to the (deprecated) PKCE path. Sealed in the gitops
	// overlay; never logged.
	OIDCClientSecret string

	// Keycloak admin REST credentials for GDPR account erasure
	// (mi-nwg5). The backend is otherwise a pure resource server and
	// never calls the admin API; these power the single privileged
	// operation — deleting the IdP user behind a deleted app account.
	// All four are required to enable IdP-user deletion; when any is
	// empty the account-deletion flow wires keycloak.NoopDeleter and
	// leaves the Keycloak user in place (the app row + sessions are
	// gone regardless). The service account behind
	// KEYCLOAK_ADMIN_CLIENT_ID must hold the realm-management
	// `manage-users` role. Sealed in the gitops overlay; never logged.
	//
	//   KeycloakAdminBaseURL — server root, e.g. https://kc.example.com
	//                          (NO /realms/... suffix).
	//   KeycloakRealm        — realm the app's users live in (default
	//                          "minerals").
	//   KeycloakAdminClientID / KeycloakAdminClientSecret — the admin
	//                          service-account client credentials.
	KeycloakAdminBaseURL      string
	KeycloakRealm             string
	KeycloakAdminClientID     string
	KeycloakAdminClientSecret string

	// OAuthStateHMACKey signs the short-lived state cookie issued
	// by /auth/login and verified on /auth/callback (mi-bm5b). 32-
	// byte minimum, enforced when the BFF handlers boot. Treat as a
	// secret — leaking it lets an attacker forge state values.
	// Rotated by deploying a new value: in-flight logins fail with
	// 400 invalid_state and users retry, which is acceptable.
	OAuthStateHMACKey string

	// CookieSecure flips the Secure flag on the BFF cookies (session
	// + state). True in prod/staging; false in dev (docker-compose
	// serves on plain HTTP localhost). Per-environment, never
	// per-request — never inferred from X-Forwarded-Proto.
	CookieSecure bool

	// CookieMaxAgeSeconds is the Max-Age the session cookie carries
	// to the browser. Must be longer than SessionAbsoluteExpiresHours
	// so the server-side row expires first and a stale cookie
	// arriving past expiry cleanly clears (the design's invariant).
	// Default 1209600 (14 days).
	CookieMaxAgeSeconds int

	// SessionAbsoluteExpiresHours is the hard cap on a single
	// session row's lifetime, stamped into auth.sessions.
	// absolute_expires_at on Create. The session middleware
	// (mi-ken4) tears down sessions past this even when Keycloak
	// would still issue a refresh. Default 168 (7 days).
	SessionAbsoluteExpiresHours int

	// SessionIdleTimeoutMinutes is the gap since last_used_at after
	// which the session middleware revokes the session. Tracks the
	// design's idle-timeout knob (docs/design/auth-bff.md
	// §sessions-table §four-expiration-concepts). Default 1440
	// (24 hours). Must be > 0 when BFF auth is enabled.
	SessionIdleTimeoutMinutes int

	// PostLogoutRedirectURI is the absolute URL the BFF asks
	// Keycloak to bounce the browser back to after the SSO logout
	// completes. MUST be on Keycloak's post_logout_redirect_uris
	// allowlist. Empty disables the 302-to-Keycloak step (handler
	// returns 204 after revoking the local session).
	PostLogoutRedirectURI string

	// RegistrationEnabled gates the BFF's GET /auth/register route
	// and is the application-level switch for Keycloak self-signup.
	// True (the default) wires the route and lets the SPA's Register
	// link reach Keycloak's registration form; false makes the route
	// return 404 so an inadvertent click stays inside the operator's
	// no-signup policy. The Keycloak realm's `registration_allowed`
	// flag is the ultimate gate at the IdP — this knob just lets
	// the application say no without a realm change.
	RegistrationEnabled bool

	// BFFEnforceCSRFOnLogout gates the /auth/logout handler's CSRF
	// check. False until the generic CSRF middleware (mi-gbzs) and
	// the SPA wiring (mi-3vc4) ship; production flips it true once
	// both are live. The header check is constant-time.
	BFFEnforceCSRFOnLogout bool

	// TrustForwardedFor enables X-Forwarded-For-based client-IP
	// extraction for the BFF callback handler (used for the
	// auth.sessions.ip forensics column). True only when the
	// ingress strips/normalises the header so a hostile client
	// cannot spoof the value. Default false — RemoteAddr is the
	// safe fallback.
	TrustForwardedFor bool

	// RateLimit configures the per-tier token-bucket API rate limiting
	// (mi-tnru). See RateLimitConfig.
	RateLimit RateLimitConfig
}

// RateLimitConfig is the operator surface for the API rate limiter
// (mi-tnru). Each tier is a requests-per-window budget; see
// internal/api for how requests map to tiers. In-memory per-replica:
// with 2 prod replicas a caller can draw up to 2× a tier's budget —
// an accepted ops tradeoff (a shared store would be the upgrade for
// exact global limits).
type RateLimitConfig struct {
	// Enabled gates the whole limiter. When false, cmd/minerals leaves
	// Deps.RateLimitMW nil and no limiting is applied. Default true.
	Enabled bool
	// Auth/Read/Write/File are the per-tier budgets in
	// requests-per-window form.
	Auth  RateLimitTier
	Read  RateLimitTier
	Write RateLimitTier
	File  RateLimitTier
}

// RateLimitTier is one tier's budget: Requests calls per WindowSeconds
// per key.
type RateLimitTier struct {
	Requests      int
	WindowSeconds int
}

// Defaults for ENV=dev or unset. Mirrors the inventory in CONTRACT.md
// §15. Required-in-prod variables are not defaulted when ENV=prod;
// see Load.
const (
	defaultPort      = "8080"
	defaultAdminPort = "9090"
	// defaultDatabaseURL embeds the dev-only minerals/minerals credentials matching
	// docker-compose.yml. Load() rejects this URL when ENV=prod, so the embedded
	// password cannot be used in production.
	defaultDatabaseURL       = "postgres://minerals:minerals@localhost:5432/minerals?sslmode=disable" //nolint:gosec // G101: dev default; prod rejected in Load()
	defaultS3Endpoint        = "http://localhost:9000"
	defaultS3AccessKeyID     = "minioadmin"
	defaultS3SecretAccessKey = "minioadmin"
	defaultS3Bucket          = "minerals-dev"
	defaultS3Region          = "us-east-1"
	defaultMaxUploadBytes    = int64(104857600) // 100 MiB
	defaultLogLevel          = "info"
	defaultEnv               = "dev"
	// defaultOIDCIssuerURL / defaultOIDCClientID point at the local
	// dev Keycloak realm (docker-compose). Prod overlays override
	// both via the minerals-config ConfigMap.
	defaultOIDCIssuerURL = "http://localhost:8081/realms/minerals"
	defaultOIDCClientID  = "minerals-frontend"

	// defaultKeycloakRealm is the realm the app's users live in. Used
	// by the GDPR account-erasure admin client (mi-nwg5) to build the
	// admin API path; prod overlays override via KEYCLOAK_REALM.
	defaultKeycloakRealm = "minerals"

	// Rate-limit tier defaults (mi-tnru). Windows are seconds.
	// Auth is strict (brute-force defense); reads are generous (public
	// browse traffic); writes moderate; file-serving guards the
	// expensive binary/bandwidth surface.
	defaultRateLimitAuthRequests   = 10
	defaultRateLimitAuthWindowSec  = 60
	defaultRateLimitReadRequests   = 300
	defaultRateLimitReadWindowSec  = 60
	defaultRateLimitWriteRequests  = 60
	defaultRateLimitWriteWindowSec = 60
	defaultRateLimitFileRequests   = 120
	defaultRateLimitFileWindowSec  = 60
)

// Load reads the environment and produces a Config with format-level
// validation applied. Per CONTRACT.md §15, strictness checks (which
// "required in prod" variables must be present) are NOT performed
// here — subcommand dispatchers call ValidateForServe or
// ValidateForMigrate before proceeding, so a subcommand that doesn't
// touch a given subsystem doesn't need its env vars set.
//
// Load still returns an error on malformed values (bad enum, bad
// integer), and still performs exactly one os.Getenv read per
// variable.
func Load() (*Config, error) {
	return loadFrom(os.Getenv)
}

// loadFrom is Load with an injected env-lookup, used by tests.
func loadFrom(get func(string) string) (*Config, error) {
	env := strings.TrimSpace(get("ENV"))
	prod := env == "prod"
	if env == "" {
		env = defaultEnv
	}

	cfg := &Config{Env: env}

	cfg.Port = orDefault(get("PORT"), defaultPort)
	cfg.AdminPort = orDefault(get("ADMIN_PORT"), defaultAdminPort)
	cfg.LogLevel = orDefault(get("LOG_LEVEL"), defaultLogLevel)
	cfg.S3Region = orDefault(get("S3_REGION"), defaultS3Region)
	cfg.MindatAPIKey = strings.TrimSpace(get("MINDAT_API_KEY"))
	// OIDC_REDIRECT_URI is the canonical backend-only name (mi-kebf).
	// For a migration window we still accept the legacy
	// PUBLIC_OIDC_REDIRECT_URI when the new name is unset, flagging it
	// so the boot log can emit a deprecation warning. A follow-up bead
	// removes this fallback once both envs' ConfigMaps are migrated.
	if v := strings.TrimSpace(get("OIDC_REDIRECT_URI")); v != "" {
		cfg.OIDCRedirectURI = v
	} else if v := strings.TrimSpace(get("PUBLIC_OIDC_REDIRECT_URI")); v != "" {
		cfg.OIDCRedirectURI = v
		cfg.OIDCRedirectURIFromLegacyEnv = true
	}
	cfg.OIDCIssuerURL = orDefault(get("OIDC_ISSUER_URL"), defaultOIDCIssuerURL)
	cfg.OIDCClientID = orDefault(get("OIDC_CLIENT_ID"), defaultOIDCClientID)
	cfg.OIDCJWKSURL = strings.TrimSpace(get("OIDC_JWKS_URL"))
	cfg.OIDCDiscoveryURL = strings.TrimSpace(get("OIDC_DISCOVERY_URL"))
	cfg.OIDCClientSecret = strings.TrimSpace(get("OIDC_CLIENT_SECRET"))
	cfg.KeycloakAdminBaseURL = strings.TrimSpace(get("KEYCLOAK_ADMIN_BASE_URL"))
	cfg.KeycloakRealm = orDefault(get("KEYCLOAK_REALM"), defaultKeycloakRealm)
	cfg.KeycloakAdminClientID = strings.TrimSpace(get("KEYCLOAK_ADMIN_CLIENT_ID"))
	cfg.KeycloakAdminClientSecret = strings.TrimSpace(get("KEYCLOAK_ADMIN_CLIENT_SECRET"))
	cfg.OAuthStateHMACKey = strings.TrimSpace(get("OAUTH_STATE_HMAC_KEY"))
	cfg.PostLogoutRedirectURI = strings.TrimSpace(get("POST_LOGOUT_REDIRECT_URI"))

	// CookieSecure defaults to prod (true in prod, false in dev).
	// Explicit COOKIE_SECURE overrides either way — useful for the
	// dev compose stack when the developer tests with an HTTPS
	// reverse proxy locally.
	cs, err := parseBoolWithDefault(get("COOKIE_SECURE"), prod)
	if err != nil {
		return nil, fmt.Errorf("config: COOKIE_SECURE: %w", err)
	}
	cfg.CookieSecure = cs

	cma, err := parseIntWithDefault(get("COOKIE_MAX_AGE_SECONDS"), 1209600)
	if err != nil {
		return nil, fmt.Errorf("config: COOKIE_MAX_AGE_SECONDS: %w", err)
	}
	cfg.CookieMaxAgeSeconds = cma

	sae, err := parseIntWithDefault(get("SESSION_ABSOLUTE_EXPIRES_HOURS"), 168)
	if err != nil {
		return nil, fmt.Errorf("config: SESSION_ABSOLUTE_EXPIRES_HOURS: %w", err)
	}
	cfg.SessionAbsoluteExpiresHours = sae

	sit, err := parseIntWithDefault(get("SESSION_IDLE_TIMEOUT_MINUTES"), 1440)
	if err != nil {
		return nil, fmt.Errorf("config: SESSION_IDLE_TIMEOUT_MINUTES: %w", err)
	}
	cfg.SessionIdleTimeoutMinutes = sit

	ec, err := parseBoolWithDefault(get("BFF_ENFORCE_CSRF_LOGOUT"), false)
	if err != nil {
		return nil, fmt.Errorf("config: BFF_ENFORCE_CSRF_LOGOUT: %w", err)
	}
	cfg.BFFEnforceCSRFOnLogout = ec

	re, err := parseBoolWithDefault(get("REGISTRATION_ENABLED"), true)
	if err != nil {
		return nil, fmt.Errorf("config: REGISTRATION_ENABLED: %w", err)
	}
	cfg.RegistrationEnabled = re

	tf, err := parseBoolWithDefault(get("TRUST_FORWARDED_FOR"), false)
	if err != nil {
		return nil, fmt.Errorf("config: TRUST_FORWARDED_FOR: %w", err)
	}
	cfg.TrustForwardedFor = tf

	rl, err := loadRateLimit(get)
	if err != nil {
		return nil, err
	}
	cfg.RateLimit = rl

	// Required-in-prod variables: in dev, fall back to the inventory
	// default; in prod, leave the field empty so ValidateFor* can
	// flag it later if the active subcommand actually needs it.
	required := []struct {
		field *string
		name  string
		def   string
	}{
		{&cfg.DatabaseURL, "DATABASE_URL", defaultDatabaseURL},
		{&cfg.S3Endpoint, "S3_ENDPOINT", defaultS3Endpoint},
		{&cfg.S3AccessKeyID, "S3_ACCESS_KEY_ID", defaultS3AccessKeyID},
		{&cfg.S3SecretAccessKey, "S3_SECRET_ACCESS_KEY", defaultS3SecretAccessKey},
		{&cfg.S3Bucket, "S3_BUCKET", defaultS3Bucket},
	}
	for _, r := range required {
		v := strings.TrimSpace(get(r.name))
		if v == "" && !prod {
			v = r.def
		}
		*r.field = v
	}

	if raw := strings.TrimSpace(get("MAX_UPLOAD_BYTES")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("config: MAX_UPLOAD_BYTES must be an integer: %w", err)
		}
		if n <= 0 {
			return nil, errors.New("config: MAX_UPLOAD_BYTES must be positive")
		}
		cfg.MaxUploadBytes = n
	} else {
		cfg.MaxUploadBytes = defaultMaxUploadBytes
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("config: LOG_LEVEL must be one of debug|info|warn|error, got %q", cfg.LogLevel)
	}

	return cfg, nil
}

// loadRateLimit reads the RATE_LIMIT_* tier settings (mi-tnru). Each
// per-tier requests/window value defaults from the inventory and must
// be a positive integer when set — a zero or negative budget would
// make the tier fail closed (reject everything), which is never the
// intent, so it is rejected as a config error.
func loadRateLimit(get func(string) string) (RateLimitConfig, error) {
	enabled, err := parseBoolWithDefault(get("RATE_LIMIT_ENABLED"), true)
	if err != nil {
		return RateLimitConfig{}, fmt.Errorf("config: RATE_LIMIT_ENABLED: %w", err)
	}
	rl := RateLimitConfig{Enabled: enabled}

	tiers := []struct {
		tier       *RateLimitTier
		reqEnv     string
		winEnv     string
		reqDefault int
		winDefault int
	}{
		{&rl.Auth, "RATE_LIMIT_AUTH_REQUESTS", "RATE_LIMIT_AUTH_WINDOW_SECONDS", defaultRateLimitAuthRequests, defaultRateLimitAuthWindowSec},
		{&rl.Read, "RATE_LIMIT_READ_REQUESTS", "RATE_LIMIT_READ_WINDOW_SECONDS", defaultRateLimitReadRequests, defaultRateLimitReadWindowSec},
		{&rl.Write, "RATE_LIMIT_WRITE_REQUESTS", "RATE_LIMIT_WRITE_WINDOW_SECONDS", defaultRateLimitWriteRequests, defaultRateLimitWriteWindowSec},
		{&rl.File, "RATE_LIMIT_FILE_REQUESTS", "RATE_LIMIT_FILE_WINDOW_SECONDS", defaultRateLimitFileRequests, defaultRateLimitFileWindowSec},
	}
	for _, t := range tiers {
		req, err := parsePositiveIntWithDefault(get(t.reqEnv), t.reqDefault)
		if err != nil {
			return RateLimitConfig{}, fmt.Errorf("config: %s: %w", t.reqEnv, err)
		}
		win, err := parsePositiveIntWithDefault(get(t.winEnv), t.winDefault)
		if err != nil {
			return RateLimitConfig{}, fmt.Errorf("config: %s: %w", t.winEnv, err)
		}
		t.tier.Requests = req
		t.tier.WindowSeconds = win
	}
	return rl, nil
}

func orDefault(v, def string) string {
	if s := strings.TrimSpace(v); s != "" {
		return s
	}
	return def
}

// parsePositiveIntWithDefault parses a strictly-positive integer from
// raw; empty returns def. Zero and negatives are rejected — used for
// the rate-limit budgets where a non-positive value is meaningless.
func parsePositiveIntWithDefault(raw string, def int) (int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("expected integer, got %q: %w", raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("expected positive integer, got %d", n)
	}
	return n, nil
}

// parseBoolWithDefault parses "true"/"false"/"1"/"0" (and a small
// set of common variants) from an env var; empty falls through to
// def. Anything else surfaces as an error so a typo
// (`COOKIE_SECURE=yes`) does not silently flip the cookie to
// insecure.
func parseBoolWithDefault(raw string, def bool) (bool, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "":
		return def, nil
	case "true", "1", "t":
		return true, nil
	case "false", "0", "f":
		return false, nil
	default:
		return false, fmt.Errorf("expected true/false, got %q", raw)
	}
}

// parseIntWithDefault parses a positive integer from raw; empty
// returns def. Negative values are rejected — every numeric env
// var in the §15 inventory is a duration / size that has no
// meaningful negative interpretation.
func parseIntWithDefault(raw string, def int) (int, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("expected integer, got %q: %w", raw, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("expected non-negative integer, got %d", n)
	}
	return n, nil
}

// ValidateForServe enforces the prod-strictness rule for the serve
// subcommand: every "required in prod" variable in the §15 inventory
// must be set. In dev, Load() has already filled defaults, so this
// is a no-op.
func (c *Config) ValidateForServe() error {
	if c.Env != "prod" {
		return nil
	}
	checks := []struct {
		name string
		val  string
	}{
		{"DATABASE_URL", c.DatabaseURL},
		{"S3_ENDPOINT", c.S3Endpoint},
		{"S3_ACCESS_KEY_ID", c.S3AccessKeyID},
		{"S3_SECRET_ACCESS_KEY", c.S3SecretAccessKey},
		{"S3_BUCKET", c.S3Bucket},
	}
	for _, r := range checks {
		if r.val == "" {
			return fmt.Errorf("config: %s is required when ENV=prod", r.name)
		}
	}
	return nil
}

// ValidateForMigrate enforces the prod-strictness rule for the
// migrate subcommand. The migrate path only talks to Postgres, so
// the required set narrows to DATABASE_URL — operators can run the
// migrate Job without injecting S3 credentials.
func (c *Config) ValidateForMigrate() error {
	if c.Env != "prod" {
		return nil
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL is required when ENV=prod")
	}
	return nil
}

// IsDev reports whether the binary should apply dev-only behavior
// (bucket auto-create, default credentials, etc.).
func (c *Config) IsDev() bool { return c.Env != "prod" }
