//go:build integration
// +build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestMigrateUpVersion is an integration test that builds the
// minerals binary, runs `migrate up` against a real Postgres
// (configured via DATABASE_URL — falls back to the §15 dev default),
// then runs `migrate version` and verifies the reported version
// matches the highest migration number embedded in the binary.
//
// The test is skipped when migrations/ is empty (bd #1 not yet
// landed) so this file can ship before the schema does without
// blocking CI.
func TestMigrateUpVersion(t *testing.T) {
	files, err := migrationFiles()
	if err != nil {
		t.Fatalf("migrationFiles: %v", err)
	}
	hasUp := false
	for _, f := range files {
		if strings.HasSuffix(f, ".up.sql") {
			hasUp = true
			break
		}
	}
	if !hasUp {
		t.Skip("migrations/ has no *.up.sql files; skipping until bd #1 lands")
	}

	expected, err := highestMigration()
	if err != nil {
		t.Fatalf("highestMigration: %v", err)
	}
	if expected == 0 {
		t.Skip("highestMigration returned 0; nothing to verify")
	}

	repoRoot := repoRootFromTest(t)

	bin := filepath.Join(t.TempDir(), "minerals")
	build := exec.Command("go", "build", "-o", bin, "./cmd/minerals")
	build.Dir = repoRoot
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}

	env := append(os.Environ(), "ENV=dev")

	up := exec.Command(bin, "migrate", "up")
	up.Env = env
	up.Stdout, up.Stderr = os.Stdout, os.Stderr
	if err := up.Run(); err != nil {
		t.Fatalf("migrate up: %v (is dev Postgres running on localhost:5432?)", err)
	}

	var verOut bytes.Buffer
	ver := exec.Command(bin, "migrate", "version")
	ver.Env = env
	ver.Stdout = &verOut
	ver.Stderr = os.Stderr
	if err := ver.Run(); err != nil {
		t.Fatalf("migrate version: %v", err)
	}

	gotVersion, gotDirty := parseVersionOutput(t, verOut.String())
	if gotDirty {
		t.Fatalf("migration is dirty after up: %s", verOut.String())
	}
	if gotVersion != expected {
		t.Fatalf("version=%d, want %d (output=%q)", gotVersion, expected, verOut.String())
	}
}

// repoRootFromTest finds the repo root by walking up from the test
// file's directory until it sees a go.mod.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod up the tree)")
		}
		dir = parent
	}
}

// parseVersionOutput pulls the (version, dirty) tuple out of a line
// like "version=3 dirty=false".
func parseVersionOutput(t *testing.T, s string) (uint, bool) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		fields := strings.Fields(line)
		var v uint
		var dirty bool
		matched := false
		for _, f := range fields {
			switch {
			case strings.HasPrefix(f, "version="):
				n, err := strconv.ParseUint(strings.TrimPrefix(f, "version="), 10, 32)
				if err != nil {
					continue
				}
				v = uint(n)
				matched = true
			case strings.HasPrefix(f, "dirty="):
				b, err := strconv.ParseBool(strings.TrimPrefix(f, "dirty="))
				if err != nil {
					continue
				}
				dirty = b
				matched = true
			}
		}
		if matched {
			return v, dirty
		}
	}
	t.Fatalf("could not parse version output: %q", s)
	return 0, false
}
