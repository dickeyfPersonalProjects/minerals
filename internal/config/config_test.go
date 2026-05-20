package config

import (
	"strings"
	"testing"
)

func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_DevDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want dev", cfg.Env)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %q, want %q", cfg.Port, defaultPort)
	}
	if cfg.AdminPort != defaultAdminPort {
		t.Errorf("AdminPort = %q, want %q", cfg.AdminPort, defaultAdminPort)
	}
	if cfg.DatabaseURL != defaultDatabaseURL {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.S3Endpoint != defaultS3Endpoint {
		t.Errorf("S3Endpoint = %q", cfg.S3Endpoint)
	}
	if cfg.S3Bucket != defaultS3Bucket {
		t.Errorf("S3Bucket = %q", cfg.S3Bucket)
	}
	if cfg.MaxUploadBytes != defaultMaxUploadBytes {
		t.Errorf("MaxUploadBytes = %d", cfg.MaxUploadBytes)
	}
	if !cfg.IsDev() {
		t.Error("IsDev = false")
	}
}

func TestLoad_OIDCRedirectURI(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{
		"OIDC_REDIRECT_URI": "https://www.example.com/auth/callback",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if got := cfg.OIDCRedirectURI; got != "https://www.example.com/auth/callback" {
		t.Errorf("OIDCRedirectURI = %q", got)
	}
	if cfg.OIDCRedirectURIFromLegacyEnv {
		t.Error("OIDCRedirectURIFromLegacyEnv = true, want false when OIDC_REDIRECT_URI is set")
	}
}

func TestLoad_OIDCRedirectURIEmptyByDefault(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCRedirectURI != "" {
		t.Errorf("OIDCRedirectURI default = %q, want empty", cfg.OIDCRedirectURI)
	}
	if cfg.OIDCRedirectURIFromLegacyEnv {
		t.Error("OIDCRedirectURIFromLegacyEnv = true, want false when unset")
	}
}

// TestLoad_OIDCRedirectURILegacyFallback covers the migration-window
// fallback: when only the deprecated PUBLIC_OIDC_REDIRECT_URI is set,
// the value is still read and the legacy-env flag is raised so the
// boot path can warn (mi-kebf).
func TestLoad_OIDCRedirectURILegacyFallback(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{
		"PUBLIC_OIDC_REDIRECT_URI": "https://legacy.example.com/auth/callback",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if got := cfg.OIDCRedirectURI; got != "https://legacy.example.com/auth/callback" {
		t.Errorf("OIDCRedirectURI = %q, want legacy fallback value", got)
	}
	if !cfg.OIDCRedirectURIFromLegacyEnv {
		t.Error("OIDCRedirectURIFromLegacyEnv = false, want true when only the legacy var is set")
	}
}

// TestLoad_OIDCRedirectURICanonicalWins ensures the new name takes
// precedence when both are present and the legacy flag stays off.
func TestLoad_OIDCRedirectURICanonicalWins(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{
		"OIDC_REDIRECT_URI":        "https://new.example.com/auth/callback",
		"PUBLIC_OIDC_REDIRECT_URI": "https://legacy.example.com/auth/callback",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if got := cfg.OIDCRedirectURI; got != "https://new.example.com/auth/callback" {
		t.Errorf("OIDCRedirectURI = %q, want canonical value", got)
	}
	if cfg.OIDCRedirectURIFromLegacyEnv {
		t.Error("OIDCRedirectURIFromLegacyEnv = true, want false when OIDC_REDIRECT_URI is set")
	}
}

func TestLoad_OIDCBackendDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCIssuerURL != defaultOIDCIssuerURL {
		t.Errorf("OIDCIssuerURL = %q, want %q", cfg.OIDCIssuerURL, defaultOIDCIssuerURL)
	}
	if cfg.OIDCClientID != defaultOIDCClientID {
		t.Errorf("OIDCClientID = %q, want %q", cfg.OIDCClientID, defaultOIDCClientID)
	}
}

func TestLoad_OIDCBackendExplicit(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{
		"OIDC_ISSUER_URL": "https://auth.example.com/realms/minerals",
		"OIDC_CLIENT_ID":  "minerals-api",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCIssuerURL != "https://auth.example.com/realms/minerals" {
		t.Errorf("OIDCIssuerURL = %q", cfg.OIDCIssuerURL)
	}
	if cfg.OIDCClientID != "minerals-api" {
		t.Errorf("OIDCClientID = %q", cfg.OIDCClientID)
	}
}

func TestLoad_OIDCJWKSURL(t *testing.T) {
	t.Parallel()
	// Unset → empty: the verifier falls back to OIDC discovery.
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCJWKSURL != "" {
		t.Errorf("OIDCJWKSURL default = %q, want empty", cfg.OIDCJWKSURL)
	}

	// Set → propagates verbatim (mi-dau: needed when the canonical
	// issuer URL is not reachable from inside the backend container).
	cfg, err = loadFrom(envFunc(map[string]string{
		"OIDC_JWKS_URL": "http://keycloak:8080/realms/minerals/protocol/openid-connect/certs",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCJWKSURL != "http://keycloak:8080/realms/minerals/protocol/openid-connect/certs" {
		t.Errorf("OIDCJWKSURL = %q", cfg.OIDCJWKSURL)
	}
}

func TestLoad_OIDCDiscoveryURL(t *testing.T) {
	t.Parallel()
	// Unset → empty: the BFF OAuth client discovers at OIDC_ISSUER_URL.
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCDiscoveryURL != "" {
		t.Errorf("OIDCDiscoveryURL default = %q, want empty", cfg.OIDCDiscoveryURL)
	}

	// Set → propagates verbatim (mi-8tnv: needed when the canonical
	// issuer URL is not reachable from inside the backend container —
	// same situation OIDC_JWKS_URL covers for the verifier).
	cfg, err = loadFrom(envFunc(map[string]string{
		"OIDC_DISCOVERY_URL": "http://keycloak:8080/realms/minerals",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCDiscoveryURL != "http://keycloak:8080/realms/minerals" {
		t.Errorf("OIDCDiscoveryURL = %q", cfg.OIDCDiscoveryURL)
	}
}

func TestLoad_DevExplicit(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{"ENV": "dev"}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q", cfg.Env)
	}
	if !cfg.IsDev() {
		t.Error("IsDev = false")
	}
}

// fullProdEnv returns an env map with every "required in prod"
// variable populated. Tests delete keys from a copy of this map to
// exercise per-subcommand validation.
func fullProdEnv() map[string]string {
	return map[string]string{
		"ENV":                  "prod",
		"DATABASE_URL":         "postgres://prod/db",
		"S3_ENDPOINT":          "https://s3.example.com",
		"S3_ACCESS_KEY_ID":     "AKIA",
		"S3_SECRET_ACCESS_KEY": "secret",
		"S3_BUCKET":            "minerals-prod",
	}
}

// TestLoad_ProdDoesNotEnforceStrictness confirms Load() no longer
// rejects missing required-in-prod vars — strictness moved to the
// per-subcommand ValidateFor* methods. Format errors (bad enum, bad
// integer) still fail at load time; see TestLoad_LogLevelValidation.
func TestLoad_ProdDoesNotEnforceStrictness(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{"ENV": "prod"}))
	if err != nil {
		t.Fatalf("loadFrom should not enforce strictness, got err=%v", err)
	}
	if cfg.Env != "prod" || cfg.IsDev() {
		t.Errorf("Env=%q IsDev=%v; want prod / non-dev", cfg.Env, cfg.IsDev())
	}
	if cfg.DatabaseURL != "" || cfg.S3Bucket != "" {
		t.Errorf("required-in-prod fields should be empty when unset; got DatabaseURL=%q S3Bucket=%q",
			cfg.DatabaseURL, cfg.S3Bucket)
	}
}

func TestValidateForServe_ProdRequiresEachVar(t *testing.T) {
	t.Parallel()
	full := fullProdEnv()
	for _, name := range []string{
		"DATABASE_URL",
		"S3_ENDPOINT",
		"S3_ACCESS_KEY_ID",
		"S3_SECRET_ACCESS_KEY",
		"S3_BUCKET",
	} {
		t.Run(name, func(t *testing.T) {
			env := make(map[string]string, len(full))
			for k, v := range full {
				env[k] = v
			}
			delete(env, name)
			cfg, err := loadFrom(envFunc(env))
			if err != nil {
				t.Fatalf("loadFrom: %v", err)
			}
			err = cfg.ValidateForServe()
			if err == nil {
				t.Fatalf("expected ValidateForServe error when %s is missing", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q must name the missing variable %s", err.Error(), name)
			}
		})
	}
}

func TestValidateForServe_ProdAllPresent(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(fullProdEnv()))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if err := cfg.ValidateForServe(); err != nil {
		t.Errorf("ValidateForServe: %v", err)
	}
}

func TestValidateForServe_DevIsNoop(t *testing.T) {
	t.Parallel()
	// In dev, Load fills defaults so ValidateForServe always passes.
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if err := cfg.ValidateForServe(); err != nil {
		t.Errorf("ValidateForServe in dev: %v", err)
	}
}

// TestValidateForMigrate_ProdNoS3Creds is the headline test for this
// refactor: in prod, the migrate subcommand must accept a config with
// no S3 credentials so the migrate Job can run without the operator
// injecting bucket creds it doesn't need.
func TestValidateForMigrate_ProdNoS3Creds(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{
		"ENV":          "prod",
		"DATABASE_URL": "postgres://prod/db",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if err := cfg.ValidateForMigrate(); err != nil {
		t.Errorf("ValidateForMigrate should accept missing S3 creds in prod: %v", err)
	}
}

func TestValidateForMigrate_ProdRequiresDatabaseURL(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{"ENV": "prod"}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	err = cfg.ValidateForMigrate()
	if err == nil {
		t.Fatal("expected ValidateForMigrate error when DATABASE_URL is missing in prod")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error %q must name DATABASE_URL", err.Error())
	}
}

func TestValidateForMigrate_DevIsNoop(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if err := cfg.ValidateForMigrate(); err != nil {
		t.Errorf("ValidateForMigrate in dev: %v", err)
	}
}

func TestValidate_BothMethodsAcceptFullConfigInAnyEnv(t *testing.T) {
	t.Parallel()
	for _, env := range []string{"dev", "prod"} {
		t.Run(env, func(t *testing.T) {
			m := fullProdEnv()
			m["ENV"] = env
			cfg, err := loadFrom(envFunc(m))
			if err != nil {
				t.Fatalf("loadFrom: %v", err)
			}
			if err := cfg.ValidateForServe(); err != nil {
				t.Errorf("ValidateForServe(%s): %v", env, err)
			}
			if err := cfg.ValidateForMigrate(); err != nil {
				t.Errorf("ValidateForMigrate(%s): %v", env, err)
			}
		})
	}
}

func TestLoad_LogLevelValidation(t *testing.T) {
	t.Parallel()
	_, err := loadFrom(envFunc(map[string]string{"LOG_LEVEL": "verbose"}))
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("expected LOG_LEVEL validation error, got %v", err)
	}
}

func TestLoad_MaxUploadBytesValidation(t *testing.T) {
	t.Parallel()
	_, err := loadFrom(envFunc(map[string]string{"MAX_UPLOAD_BYTES": "not-a-number"}))
	if err == nil || !strings.Contains(err.Error(), "MAX_UPLOAD_BYTES") {
		t.Fatalf("expected MAX_UPLOAD_BYTES error, got %v", err)
	}
	_, err = loadFrom(envFunc(map[string]string{"MAX_UPLOAD_BYTES": "0"}))
	if err == nil {
		t.Fatal("expected error for non-positive MAX_UPLOAD_BYTES")
	}
}

func TestLoad_EmptyStringTreatedAsUnset(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{
		"PORT":      "",
		"LOG_LEVEL": "",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %q (empty should fall back)", cfg.Port)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Errorf("LogLevel = %q (empty should fall back)", cfg.LogLevel)
	}
}

// TestLoad_BFFAuthDefaults confirms the BFF auth fields default
// correctly when unset: no client secret / HMAC key (BFF stays
// off), 14-day cookie, 7-day session cap, CSRF gate off, trust
// X-Forwarded-For off. Production overrides every one of these.
func TestLoad_BFFAuthDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCClientSecret != "" {
		t.Errorf("OIDCClientSecret = %q, want empty", cfg.OIDCClientSecret)
	}
	if cfg.OAuthStateHMACKey != "" {
		t.Errorf("OAuthStateHMACKey = %q, want empty", cfg.OAuthStateHMACKey)
	}
	if cfg.CookieMaxAgeSeconds != 1209600 {
		t.Errorf("CookieMaxAgeSeconds = %d, want 1209600", cfg.CookieMaxAgeSeconds)
	}
	if cfg.SessionAbsoluteExpiresHours != 168 {
		t.Errorf("SessionAbsoluteExpiresHours = %d, want 168", cfg.SessionAbsoluteExpiresHours)
	}
	if cfg.BFFEnforceCSRFOnLogout {
		t.Error("BFFEnforceCSRFOnLogout default should be false")
	}
	if cfg.TrustForwardedFor {
		t.Error("TrustForwardedFor default should be false")
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure default should be false in dev")
	}
}

// TestLoad_BFFAuthExplicit walks the full set of BFF auth env vars
// through loadFrom and asserts every field round-trips. Catches a
// typo in a field name or a swapped pair of getters during a
// future refactor.
func TestLoad_BFFAuthExplicit(t *testing.T) {
	t.Parallel()
	cfg, err := loadFrom(envFunc(map[string]string{
		"OIDC_CLIENT_SECRET":             "kc-secret",
		"OAUTH_STATE_HMAC_KEY":           "0123456789abcdef0123456789abcdef",
		"COOKIE_SECURE":                  "true",
		"COOKIE_MAX_AGE_SECONDS":         "3600",
		"SESSION_ABSOLUTE_EXPIRES_HOURS": "24",
		"POST_LOGOUT_REDIRECT_URI":       "https://app.example/",
		"BFF_ENFORCE_CSRF_LOGOUT":        "true",
		"TRUST_FORWARDED_FOR":            "true",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.OIDCClientSecret != "kc-secret" {
		t.Errorf("OIDCClientSecret = %q", cfg.OIDCClientSecret)
	}
	if cfg.OAuthStateHMACKey != "0123456789abcdef0123456789abcdef" {
		t.Errorf("OAuthStateHMACKey = %q", cfg.OAuthStateHMACKey)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure should be true")
	}
	if cfg.CookieMaxAgeSeconds != 3600 {
		t.Errorf("CookieMaxAgeSeconds = %d, want 3600", cfg.CookieMaxAgeSeconds)
	}
	if cfg.SessionAbsoluteExpiresHours != 24 {
		t.Errorf("SessionAbsoluteExpiresHours = %d, want 24", cfg.SessionAbsoluteExpiresHours)
	}
	if cfg.PostLogoutRedirectURI != "https://app.example/" {
		t.Errorf("PostLogoutRedirectURI = %q", cfg.PostLogoutRedirectURI)
	}
	if !cfg.BFFEnforceCSRFOnLogout {
		t.Error("BFFEnforceCSRFOnLogout should be true")
	}
	if !cfg.TrustForwardedFor {
		t.Error("TrustForwardedFor should be true")
	}
}

// TestLoad_CookieSecureDefaultsToProd locks the per-environment
// default — true in prod, false in dev — and the explicit
// override path. A misconfigured COOKIE_SECURE in prod could
// silently issue cookies in clear-text mode; the default-from-env
// behavior makes that hard to do by accident.
func TestLoad_CookieSecureDefaultsToProd(t *testing.T) {
	t.Parallel()
	prod, err := loadFrom(envFunc(map[string]string{"ENV": "prod"}))
	if err != nil {
		t.Fatalf("loadFrom prod: %v", err)
	}
	if !prod.CookieSecure {
		t.Error("CookieSecure default in prod should be true")
	}
	dev, err := loadFrom(envFunc(map[string]string{"ENV": "dev"}))
	if err != nil {
		t.Fatalf("loadFrom dev: %v", err)
	}
	if dev.CookieSecure {
		t.Error("CookieSecure default in dev should be false")
	}
}

// TestLoad_BFFAuthMalformedValues guards the explicit error
// messages parseBoolWithDefault / parseIntWithDefault emit when an
// env var carries a typo. Catching "yes" before it silently
// flips the cookie to insecure is the whole point of validating at
// load.
func TestLoad_BFFAuthMalformedValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"bool typo", map[string]string{"COOKIE_SECURE": "yes"}, "COOKIE_SECURE"},
		{"int typo", map[string]string{"COOKIE_MAX_AGE_SECONDS": "not-a-number"}, "COOKIE_MAX_AGE_SECONDS"},
		{"int negative", map[string]string{"SESSION_ABSOLUTE_EXPIRES_HOURS": "-1"}, "SESSION_ABSOLUTE_EXPIRES_HOURS"},
		{"csrf bool typo", map[string]string{"BFF_ENFORCE_CSRF_LOGOUT": "maybe"}, "BFF_ENFORCE_CSRF_LOGOUT"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadFrom(envFunc(tc.env))
			if err == nil {
				t.Fatalf("loadFrom accepted %v; want error mentioning %s", tc.env, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want mention of %s", err, tc.want)
			}
		})
	}
}
