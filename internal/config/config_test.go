package config

import (
	"strings"
	"testing"
)

func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_DevDefaults(t *testing.T) {
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

func TestLoad_DevExplicit(t *testing.T) {
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
	cfg, err := loadFrom(envFunc(fullProdEnv()))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if err := cfg.ValidateForServe(); err != nil {
		t.Errorf("ValidateForServe: %v", err)
	}
}

func TestValidateForServe_DevIsNoop(t *testing.T) {
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
	cfg, err := loadFrom(envFunc(nil))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if err := cfg.ValidateForMigrate(); err != nil {
		t.Errorf("ValidateForMigrate in dev: %v", err)
	}
}

func TestValidate_BothMethodsAcceptFullConfigInAnyEnv(t *testing.T) {
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
	_, err := loadFrom(envFunc(map[string]string{"LOG_LEVEL": "verbose"}))
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("expected LOG_LEVEL validation error, got %v", err)
	}
}

func TestLoad_MaxUploadBytesValidation(t *testing.T) {
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
