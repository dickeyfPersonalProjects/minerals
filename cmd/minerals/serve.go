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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/config"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage"
	"github.com/dickeyfPersonalProjects/minerals/internal/web"
)

// dbPinger adapts pgxpool.Pool's Ping to the api.Pinger interface.
type dbPinger struct{ pool *pgxpool.Pool }

func (d dbPinger) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

func runServe(_ []string) error {
	cfg, err := config.Load()
	if err != nil {
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

	if err := verifySchemaVersion(rootCtx, cfg.DatabaseURL); err != nil {
		return err
	}

	expected, err := highestMigration()
	if err != nil {
		return fmt.Errorf("serve: read embedded migrations: %w", err)
	}

	deps := api.Deps{
		DB:              dbPinger{pool: pool},
		Storage:         store,
		SchemaVersion:   func(ctx context.Context) (uint, bool, error) { return schemaVersion(ctx, cfg.DatabaseURL) },
		ExpectedVersion: expected,
		WebHandler:      web.Handler(),
	}
	handler := api.New(deps)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

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
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("serve: shutdown: %w", err)
	}
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

// parseCount is a small convenience for the migrate subcommand.
func parseCount(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}
