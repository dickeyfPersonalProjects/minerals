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

func TestLoad_ProdRequiresEachVar(t *testing.T) {
	full := map[string]string{
		"ENV":                  "prod",
		"DATABASE_URL":         "postgres://prod/db",
		"S3_ENDPOINT":          "https://s3.example.com",
		"S3_ACCESS_KEY_ID":     "AKIA",
		"S3_SECRET_ACCESS_KEY": "secret",
		"S3_BUCKET":            "minerals-prod",
	}

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
			_, err := loadFrom(envFunc(env))
			if err == nil {
				t.Fatalf("expected error when %s is missing", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q must name the missing variable %s", err.Error(), name)
			}
		})
	}
}

func TestLoad_ProdAllPresent(t *testing.T) {
	cfg, err := loadFrom(envFunc(map[string]string{
		"ENV":                  "prod",
		"DATABASE_URL":         "postgres://prod/db",
		"S3_ENDPOINT":          "https://s3.example.com",
		"S3_ACCESS_KEY_ID":     "AKIA",
		"S3_SECRET_ACCESS_KEY": "secret",
		"S3_BUCKET":            "minerals-prod",
	}))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.IsDev() {
		t.Error("IsDev = true in prod")
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
