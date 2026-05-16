// Package config loads runtime configuration from environment
// variables. Per CONTRACT.md §15 this is the sole entry point for
// reading the process environment; every other package receives
// values via dependency injection.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config is the v1 runtime configuration. All fields correspond to
// entries in the §15 inventory.
type Config struct {
	Port              string
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

	// PublicOIDCIssuerURL, PublicOIDCClientID, and PublicOIDCRedirectURI
	// are the SPA-facing OIDC settings the backend exposes through
	// `/api/v1/runtime-config` (mi-5ew). The `PUBLIC_` prefix marks
	// them as safe to ship to the browser. When all three are set the
	// SPA enables the OIDC login flow; when any is empty the SPA hides
	// the login UI. Backend-side JWT verification uses the non-public
	// OIDCIssuerURL / OIDCClientID below.
	PublicOIDCIssuerURL   string
	PublicOIDCClientID    string
	PublicOIDCRedirectURI string

	// PublicOIDCIssuerOrigin is the origin portion (scheme://host[:port])
	// of PublicOIDCIssuerURL. Derived at Load() so the CSP builder
	// (mi-cl1) has a guaranteed-well-formed source to drop into the
	// `connect-src` directive. Empty when PublicOIDCIssuerURL is unset.
	// §17 forbids wildcards, so we expose the origin only — never a path.
	PublicOIDCIssuerOrigin string

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
}

// Defaults for ENV=dev or unset. Mirrors the inventory in CONTRACT.md
// §15. Required-in-prod variables are not defaulted when ENV=prod;
// see Load.
const (
	defaultPort = "8080"
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
	cfg.LogLevel = orDefault(get("LOG_LEVEL"), defaultLogLevel)
	cfg.S3Region = orDefault(get("S3_REGION"), defaultS3Region)
	cfg.MindatAPIKey = strings.TrimSpace(get("MINDAT_API_KEY"))
	cfg.PublicOIDCIssuerURL = strings.TrimSpace(get("PUBLIC_OIDC_ISSUER_URL"))
	cfg.PublicOIDCClientID = strings.TrimSpace(get("PUBLIC_OIDC_CLIENT_ID"))
	cfg.PublicOIDCRedirectURI = strings.TrimSpace(get("PUBLIC_OIDC_REDIRECT_URI"))
	if cfg.PublicOIDCIssuerURL != "" {
		origin, err := parseIssuerOrigin(cfg.PublicOIDCIssuerURL)
		if err != nil {
			return nil, fmt.Errorf("config: PUBLIC_OIDC_ISSUER_URL: %w", err)
		}
		cfg.PublicOIDCIssuerOrigin = origin
	}
	cfg.OIDCIssuerURL = orDefault(get("OIDC_ISSUER_URL"), defaultOIDCIssuerURL)
	cfg.OIDCClientID = orDefault(get("OIDC_CLIENT_ID"), defaultOIDCClientID)
	cfg.OIDCJWKSURL = strings.TrimSpace(get("OIDC_JWKS_URL"))

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

// parseIssuerOrigin returns the scheme://host[:port] portion of an
// absolute http(s) URL. Anything else — relative URL, missing host,
// non-http(s) scheme, parse failure — is rejected. Used to feed the
// OIDC issuer origin into the CSP `connect-src` directive without
// risking a malformed value (trailing slash, embedded path) leaking
// into the policy.
func parseIssuerOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("malformed URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL %q has no host", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func orDefault(v, def string) string {
	if s := strings.TrimSpace(v); s != "" {
		return s
	}
	return def
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
