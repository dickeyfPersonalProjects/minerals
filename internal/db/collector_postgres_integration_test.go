//go:build integration

package db_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// scopedDB returns a pool whose connections see only an isolated
// per-test schema. Migrations are run inside that schema, so every
// integration test starts on a clean copy of the v1 schema and tears
// it down on exit.
func scopedDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping db integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	schema := "coll_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	admin, err := pgxpool.New(ctx, rawDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(admin.Close)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("drop schema: %v", err)
		}
	})

	scoped := dsnWithSearchPath(t, rawDSN, schema)

	m, err := migrate.New("file://"+migrationsDir(t), scoped)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, scoped)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func dsnWithSearchPath(t *testing.T, raw, schema string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller path")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("abs migrations dir: %v", err)
	}
	return abs
}

func authedCtx() context.Context {
	return auth.WithUser(context.Background(), auth.StubUser)
}

func mkCollector(name string, notes *string) domain.Collector {
	now := time.Now().UTC()
	return domain.Collector{
		ID:        domain.NewID(),
		Name:      name,
		Notes:     notes,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestIntegration_CreateAndGet(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	notes := "noted"
	c := mkCollector("Roundtrip", &notes)

	if err := repo.Create(ctx, nil, c); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != c.Name {
		t.Errorf("name: got %q want %q", got.Name, c.Name)
	}
	if got.Notes == nil || *got.Notes != "noted" {
		t.Errorf("notes: %v", got.Notes)
	}
	if got.AuthorID != auth.StubUser.ID {
		t.Errorf("author_id = %v, want StubUser %v", got.AuthorID, auth.StubUser.ID)
	}
}

func TestIntegration_CreateDuplicateNameConflict(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	first := mkCollector("Same Name", nil)
	second := mkCollector("Same Name", nil)

	if err := repo.Create(ctx, nil, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := repo.Create(ctx, nil, second)
	if !errors.Is(err, domain.ErrCollectorConflict) {
		t.Fatalf("second create: got %v, want ErrCollectorConflict", err)
	}
}

func TestIntegration_GetMissingReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	_, err := repo.GetByID(authedCtx(), uuid.New())
	if !errors.Is(err, domain.ErrCollectorNotFound) {
		t.Fatalf("got %v, want ErrCollectorNotFound", err)
	}
}

func TestIntegration_UpdatePersists(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	c := mkCollector("Original", nil)
	if err := repo.Create(ctx, nil, c); err != nil {
		t.Fatalf("create: %v", err)
	}

	notes := "updated"
	c.Name = "Renamed"
	c.Notes = &notes
	c.UpdatedAt = time.Now().UTC().Add(time.Second)
	if err := repo.Update(ctx, nil, c); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Renamed" {
		t.Errorf("name: %q", got.Name)
	}
	if got.Notes == nil || *got.Notes != "updated" {
		t.Errorf("notes: %v", got.Notes)
	}
}

func TestIntegration_UpdateMissingReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	c := mkCollector("ghost", nil)
	err := repo.Update(authedCtx(), nil, c)
	if !errors.Is(err, domain.ErrCollectorNotFound) {
		t.Fatalf("got %v, want ErrCollectorNotFound", err)
	}
}

func TestIntegration_DeleteRemovesRow(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	c := mkCollector("doomed", nil)
	if err := repo.Create(ctx, nil, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, nil, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, c.ID); !errors.Is(err, domain.ErrCollectorNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

// TestIntegration_DeleteReferencedReturns409 wires a fake specimen +
// specimen_collectors row directly via SQL, then asserts the delete
// translates the FK violation into domain.ErrCollectorReferenced.
func TestIntegration_DeleteReferencedReturns409(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	c := mkCollector("linked", nil)
	if err := repo.Create(ctx, nil, c); err != nil {
		t.Fatalf("create collector: %v", err)
	}

	// Insert a specimen + linkage row by hand. Specimen schema is
	// pulled from migrations 0001_init.
	specimenID := domain.NewID()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', 'fake-sp', $2, $3, $3)`,
		specimenID, auth.StubUser.ID, now); err != nil {
		t.Fatalf("insert specimen: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO specimen_collectors (specimen_id, collector_id, position, created_at)
		VALUES ($1, $2, 0, $3)`,
		specimenID, c.ID, now); err != nil {
		t.Fatalf("insert link: %v", err)
	}

	err := repo.Delete(ctx, nil, c.ID)
	if !errors.Is(err, domain.ErrCollectorReferenced) {
		t.Fatalf("got %v, want ErrCollectorReferenced", err)
	}
}

func TestIntegration_ListPaginatesAndOrders(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	// Insert 5 collectors at increasing created_at so the (created_at
	// DESC, id DESC) ordering is well-defined regardless of UUIDv7
	// jitter.
	created := []domain.Collector{}
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	for i := 0; i < 5; i++ {
		c := domain.Collector{
			ID:        domain.NewID(),
			Name:      "row-" + uuid.NewString()[:8], // unique per row
			CreatedAt: base.Add(time.Duration(i) * time.Second),
			UpdatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := repo.Create(ctx, nil, c); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		created = append(created, c)
	}

	// First page of 3.
	page1, cur1, err := repo.List(ctx, domain.CollectorFilter{}, domain.Page{Limit: 3})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1 size = %d, want 3", len(page1))
	}
	// Newest-first means page1[0] should be the one we created last.
	if page1[0].ID != created[4].ID {
		t.Errorf("page1[0] = %v, want %v", page1[0].ID, created[4].ID)
	}
	if cur1 == "" {
		t.Errorf("expected non-empty cursor; got empty")
	}

	// Second page.
	page2, cur2, err := repo.List(ctx, domain.CollectorFilter{}, domain.Page{Limit: 3, Cursor: string(cur1)})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2 size = %d, want 2 (5 total minus 3 already shown)", len(page2))
	}
	if cur2 != "" {
		t.Errorf("expected empty cursor at end; got %q", cur2)
	}

	// Sanity: combined IDs cover all 5 created.
	seen := map[uuid.UUID]bool{}
	for _, p := range append(page1, page2...) {
		seen[p.ID] = true
	}
	for _, c := range created {
		if !seen[c.ID] {
			t.Errorf("missing %v from combined pages", c.ID)
		}
	}
}

func TestIntegration_ListFilterByQuery(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	for _, n := range []string{"Apple Pie", "Banana Bread", "Apricot Jam"} {
		c := mkCollector(n, nil)
		if err := repo.Create(ctx, nil, c); err != nil {
			t.Fatalf("create %q: %v", n, err)
		}
	}

	out, _, err := repo.List(ctx, domain.CollectorFilter{Query: "ap"}, domain.Page{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("got %d, want 2 (Apple Pie + Apricot Jam): %v", len(out), out)
	}
	for _, c := range out {
		lower := strings.ToLower(c.Name)
		if !strings.Contains(lower, "ap") {
			t.Errorf("unexpected match %q", c.Name)
		}
	}
}

func TestIntegration_AuthorIDPopulated(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewCollectorPostgres(pool)

	customUser := auth.User{
		ID:    domain.NewID(),
		Email: "tester@example.invalid",
	}
	ctx := auth.WithUser(context.Background(), customUser)

	c := mkCollector("authorial", nil)
	if err := repo.Create(ctx, nil, c); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AuthorID != customUser.ID {
		t.Errorf("author_id = %v, want %v", got.AuthorID, customUser.ID)
	}
}
