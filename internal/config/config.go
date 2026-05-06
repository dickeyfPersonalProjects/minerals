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
}

// Defaults for ENV=dev or unset. Mirrors the inventory in CONTRACT.md
// §15. Required-in-prod variables are not defaulted when ENV=prod;
// see Load.
const (
	defaultPort              = "8080"
	defaultDatabaseURL       = "postgres://minerals:minerals@localhost:5432/minerals?sslmode=disable"
	defaultS3Endpoint        = "http://localhost:9000"
	defaultS3AccessKeyID     = "minioadmin"
	defaultS3SecretAccessKey = "minioadmin"
	defaultS3Bucket          = "minerals-dev"
	defaultS3Region          = "us-east-1"
	defaultMaxUploadBytes    = int64(104857600) // 100 MiB
	defaultLogLevel          = "info"
	defaultEnv               = "dev"
)

// Load reads the environment and produces a validated Config. Returns
// an error naming any missing or malformed variable.
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

	required := []struct {
		name  string
		field *string
		def   string
	}{
		{"DATABASE_URL", &cfg.DatabaseURL, defaultDatabaseURL},
		{"S3_ENDPOINT", &cfg.S3Endpoint, defaultS3Endpoint},
		{"S3_ACCESS_KEY_ID", &cfg.S3AccessKeyID, defaultS3AccessKeyID},
		{"S3_SECRET_ACCESS_KEY", &cfg.S3SecretAccessKey, defaultS3SecretAccessKey},
		{"S3_BUCKET", &cfg.S3Bucket, defaultS3Bucket},
	}
	for _, r := range required {
		v := strings.TrimSpace(get(r.name))
		if v == "" {
			if prod {
				return nil, fmt.Errorf("config: %s is required when ENV=prod", r.name)
			}
			v = r.def
		}
		*r.field = v
	}

	// ENV is required in prod (the inventory marks it required) — but
	// we infer prod from ENV itself. Treat empty ENV as dev (consistent
	// with the §15 rule that empty == unset == dev defaults).
	// Nothing to validate here beyond what was captured above.

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

func orDefault(v, def string) string {
	if s := strings.TrimSpace(v); s != "" {
		return s
	}
	return def
}

// IsDev reports whether the binary should apply dev-only behavior
// (bucket auto-create, default credentials, etc.).
func (c *Config) IsDev() bool { return c.Env != "prod" }
