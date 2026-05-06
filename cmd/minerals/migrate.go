package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"

	"github.com/dickeyfPersonalProjects/minerals/internal/config"
)

// runMigrate dispatches the migrate subcommand. Supported actions:
//
//	minerals migrate up
//	minerals migrate down [N]
//	minerals migrate version
//	minerals migrate create NAME=...
//
// The first three operate against DATABASE_URL via the embedded
// migration FS (per CONTRACT.md §6). `create` writes new files to
// the on-disk migrations/ directory and does NOT touch the database.
func runMigrate(args []string) error {
	if len(args) < 1 {
		return errors.New("migrate: missing action (up | down [N] | version | create NAME=...)")
	}
	action := args[0]
	rest := args[1:]

	switch action {
	case "create":
		return migrateCreate(rest)
	case "up":
		return withMigrate(func(m *migrate.Migrate) error {
			err := m.Up()
			if errors.Is(err, migrate.ErrNoChange) {
				slog.Info("migrate up: no change")
				return nil
			}
			if err != nil {
				return fmt.Errorf("migrate up: %w", err)
			}
			slog.Info("migrate up: applied")
			return nil
		})
	case "down":
		n, err := parseCount(firstOr(rest, ""), 1)
		if err != nil {
			return fmt.Errorf("migrate down: invalid N: %w", err)
		}
		if n <= 0 {
			return errors.New("migrate down: N must be positive")
		}
		return withMigrate(func(m *migrate.Migrate) error {
			err := m.Steps(-n)
			if errors.Is(err, migrate.ErrNoChange) {
				slog.Info("migrate down: no change")
				return nil
			}
			if err != nil {
				return fmt.Errorf("migrate down: %w", err)
			}
			slog.Info("migrate down: rolled back", "steps", n)
			return nil
		})
	case "version":
		return withMigrate(func(m *migrate.Migrate) error {
			v, dirty, err := m.Version()
			if errors.Is(err, migrate.ErrNilVersion) {
				fmt.Println("no migrations applied")
				return nil
			}
			if err != nil {
				return fmt.Errorf("migrate version: %w", err)
			}
			fmt.Printf("version=%d dirty=%t\n", v, dirty)
			return nil
		})
	default:
		return fmt.Errorf("migrate: unknown action %q (want: up | down [N] | version | create NAME=...)", action)
	}
}

func withMigrate(fn func(*migrate.Migrate) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	configureLogger(cfg.LogLevel)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	m, err := newMigrate(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()

	return fn(m)
}

// migrateCreate scaffolds an empty up/down pair on disk. It uses the
// next available NNNN sequence (highest existing + 1, or 0001 if
// none exist). NAME is taken from a NAME=foo argument or, as a
// convenience, the first non-flag argument.
func migrateCreate(args []string) error {
	name := ""
	for _, a := range args {
		if strings.HasPrefix(a, "NAME=") {
			name = strings.TrimPrefix(a, "NAME=")
			break
		}
	}
	if name == "" && len(args) > 0 && !strings.Contains(args[0], "=") {
		name = args[0]
	}
	if name == "" {
		return errors.New("migrate create: NAME=... is required")
	}
	name = strings.TrimSpace(name)
	if !isSnakeCase(name) {
		return fmt.Errorf("migrate create: NAME must be snake_case, got %q", name)
	}

	dir := "migrations"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("migrate create: ensure dir: %w", err)
	}

	next, err := nextMigrationNumber(dir)
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("%04d_%s", next, name)
	upPath := filepath.Join(dir, prefix+".up.sql")
	downPath := filepath.Join(dir, prefix+".down.sql")

	for _, p := range []string{upPath, downPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("migrate create: %s already exists", p)
		}
	}
	if err := os.WriteFile(upPath, []byte(""), 0o644); err != nil {
		return fmt.Errorf("migrate create: write up: %w", err)
	}
	if err := os.WriteFile(downPath, []byte(""), 0o644); err != nil {
		return fmt.Errorf("migrate create: write down: %w", err)
	}
	fmt.Println(upPath)
	fmt.Println(downPath)
	return nil
}

// nextMigrationNumber scans dir on disk (NOT the embedded FS — at
// scaffold time, the file we're creating is not yet embedded) and
// returns the next available sequence number.
func nextMigrationNumber(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 1, nil
		}
		return 0, fmt.Errorf("read dir: %w", err)
	}
	max := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationVersionRegex.FindStringSubmatch(e.Name())
		if len(m) < 2 {
			continue
		}
		var n int
		_, _ = fmt.Sscanf(m[1], "%d", &n)
		if n > max {
			max = n
		}
	}
	return max + 1, nil
}

func firstOr(args []string, def string) string {
	if len(args) > 0 {
		return args[0]
	}
	return def
}

func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
