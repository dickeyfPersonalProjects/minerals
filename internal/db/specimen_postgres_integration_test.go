//go:build integration

package db_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// fixture spins up an isolated per-test schema, runs the migrations
// into it, returns a pool scoped to that schema plus a cleanup hook.
type fixture struct {
	pool   *pgxpool.Pool
	repo   *db.SpecimenPostgres
	schema string
	dsn    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping specimen repo integration tests")
	}
	ctx := context.Background()

	schema := "spec_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	adminPool, err := pgxpool.New(ctx, rawDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminPool.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(adminPool.Close)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := adminPool.Exec(cctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	scopedDSN := dsnWithSearchPath(t, rawDSN, schema)
	migrationsURL := "file://" + migrationsDir(t)
	m, err := migrate.New(migrationsURL, scopedDSN)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		t.Logf("migrate.Close: src=%v db=%v", srcErr, dbErr)
	}

	pool, err := pgxpool.New(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return &fixture{
		pool:   pool,
		repo:   db.NewSpecimenPostgres(pool),
		schema: schema,
		dsn:    scopedDSN,
	}
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve caller path")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(filepath.Join(repoRoot, "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return abs
}

func dsnWithSearchPath(t *testing.T, raw, schema string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// --- helpers ---

// stubAuthor is the v1 auth stub user id (mirrors auth.StubUser.ID).
var stubAuthor = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// makeSpecimen returns a domain.Specimen ready for Create with sane
// defaults for the given type.
func makeSpecimen(t *testing.T, typ domain.SpecimenType, name string) domain.Specimen {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	td := []byte(`{}`)
	switch typ {
	case domain.SpecimenMineral:
		td = []byte(`{"chemical_formula":"SiO2","color":"clear"}`)
	case domain.SpecimenRock:
		td = []byte(`{"rock_type":"igneous","composition":"granite"}`)
	case domain.SpecimenMeteorite:
		td = []byte(`{"classification":"L6","fall_or_find":"find"}`)
	}
	return domain.Specimen{
		ID:         domain.NewID(),
		Type:       typ,
		Name:       name,
		Visibility: domain.VisibilityPrivate,
		AuthorID:   stubAuthor,
		TypeData:   td,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestSpecimenRepo_CreateGetRoundtrip_AllTypes(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	for _, typ := range []domain.SpecimenType{
		domain.SpecimenMineral, domain.SpecimenRock, domain.SpecimenMeteorite,
	} {
		t.Run(string(typ), func(t *testing.T) {
			s := makeSpecimen(t, typ, "test "+string(typ))
			if err := fx.repo.Create(ctx, fx.pool, s); err != nil {
				t.Fatalf("create: %v", err)
			}
			got, err := fx.repo.GetByID(ctx, s.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.ID != s.ID {
				t.Errorf("id mismatch")
			}
			if got.Type != typ {
				t.Errorf("type: got %s, want %s", got.Type, typ)
			}
			if got.AuthorID != stubAuthor {
				t.Errorf("author_id: got %s, want %s", got.AuthorID, stubAuthor)
			}
			if !uuidIsV7(got.ID) {
				t.Errorf("id is not UUIDv7: %s", got.ID)
			}
			// type_data round-trips with at least the keys we sent.
			var rt map[string]any
			if err := json.Unmarshal(got.TypeData, &rt); err != nil {
				t.Fatalf("type_data unmarshal: %v", err)
			}
			if len(rt) == 0 {
				t.Errorf("expected non-empty type_data, got %s", got.TypeData)
			}
		})
	}
}

func TestSpecimenRepo_GetByID_NotFound(t *testing.T) {
	fx := newFixture(t)
	_, err := fx.repo.GetByID(context.Background(), domain.NewID())
	if err != domain.ErrSpecimenNotFound {
		t.Fatalf("got %v, want ErrSpecimenNotFound", err)
	}
}

func TestSpecimenRepo_CatalogNumberConflict(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	a := makeSpecimen(t, domain.SpecimenMineral, "a")
	cn := "MIN-001"
	a.CatalogNumber = &cn
	if err := fx.repo.Create(ctx, fx.pool, a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	b := makeSpecimen(t, domain.SpecimenRock, "b")
	b.CatalogNumber = &cn
	err := fx.repo.Create(ctx, fx.pool, b)
	if err != domain.ErrSpecimenConflict {
		t.Fatalf("got %v, want ErrSpecimenConflict", err)
	}
}

func TestSpecimenRepo_Update_PreservesOmittedAndRejectsTypeChange(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	s := makeSpecimen(t, domain.SpecimenMineral, "old name")
	desc := "old description"
	s.Description = desc
	if err := fx.repo.Create(ctx, fx.pool, s); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Service-layer-style PATCH: fetch, mutate name only, write back.
	cur, err := fx.repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	cur.Name = "new name"
	cur.UpdatedAt = time.Now().UTC()
	if err := fx.repo.Update(ctx, fx.pool, cur); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := fx.repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "new name" {
		t.Errorf("name not updated")
	}
	if got.Description != desc {
		t.Errorf("description was clobbered: %q", got.Description)
	}

	// Try to flip the type — must be rejected.
	got.Type = domain.SpecimenRock
	if err := fx.repo.Update(ctx, fx.pool, got); err != domain.ErrSpecimenTypeImmutable {
		t.Fatalf("type change: got %v, want ErrSpecimenTypeImmutable", err)
	}
}

func TestSpecimenRepo_Delete_CascadesAndBlocksWhenChildren(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// Empty specimen: delete should succeed.
	bare := makeSpecimen(t, domain.SpecimenMineral, "bare")
	if err := fx.repo.Create(ctx, fx.pool, bare); err != nil {
		t.Fatalf("create bare: %v", err)
	}
	if err := pgx.BeginFunc(ctx, fx.pool, func(tx pgx.Tx) error {
		return fx.repo.Delete(ctx, tx, bare.ID)
	}); err != nil {
		t.Fatalf("delete bare: %v", err)
	}
	if _, err := fx.repo.GetByID(ctx, bare.ID); err != domain.ErrSpecimenNotFound {
		t.Errorf("after delete: got %v, want ErrSpecimenNotFound", err)
	}

	// Specimen with photo child: delete should fail with referenced.
	withPhoto := makeSpecimen(t, domain.SpecimenRock, "withphoto")
	if err := fx.repo.Create(ctx, fx.pool, withPhoto); err != nil {
		t.Fatalf("create withphoto: %v", err)
	}
	insertFakePhoto(ctx, t, fx.pool, withPhoto.ID)
	err := pgx.BeginFunc(ctx, fx.pool, func(tx pgx.Tx) error {
		return fx.repo.Delete(ctx, tx, withPhoto.ID)
	})
	if err != domain.ErrSpecimenReferenced {
		t.Fatalf("delete withphoto: got %v, want ErrSpecimenReferenced", err)
	}

	// Specimen with journal child: same behavior.
	withJournal := makeSpecimen(t, domain.SpecimenMeteorite, "withjournal")
	if err := fx.repo.Create(ctx, fx.pool, withJournal); err != nil {
		t.Fatalf("create withjournal: %v", err)
	}
	insertFakeJournal(ctx, t, fx.pool, withJournal.ID)
	err = pgx.BeginFunc(ctx, fx.pool, func(tx pgx.Tx) error {
		return fx.repo.Delete(ctx, tx, withJournal.ID)
	})
	if err != domain.ErrSpecimenReferenced {
		t.Fatalf("delete withjournal: got %v, want ErrSpecimenReferenced", err)
	}
}

// insertFakePhoto wires a photos row to the given specimen. We bypass
// the (unimplemented in v1) photo upload pipeline by writing a minimal
// files row first.
func insertFakePhoto(ctx context.Context, t *testing.T, pool *pgxpool.Pool, specimenID uuid.UUID) {
	t.Helper()
	fileID := domain.NewID()
	_, err := pool.Exec(ctx, `
		INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
		VALUES ($1, $2, 'image/jpeg', 1, repeat('a', 64), $3, now())`,
		fileID, "files/"+fileID.String(), stubAuthor)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	photoID := domain.NewID()
	_, err = pool.Exec(ctx, `
		INSERT INTO photos (id, specimen_id, file_id, position, created_at)
		VALUES ($1, $2, $3, 0, now())`,
		photoID, specimenID, fileID)
	if err != nil {
		t.Fatalf("insert photo: %v", err)
	}
}

func insertFakeJournal(ctx context.Context, t *testing.T, pool *pgxpool.Pool, specimenID uuid.UUID) {
	t.Helper()
	id := domain.NewID()
	_, err := pool.Exec(ctx, `
		INSERT INTO journal_entries (id, specimen_id, author_id, body_md, created_at, updated_at)
		VALUES ($1, $2, $3, 'note', now(), now())`,
		id, specimenID, stubAuthor)
	if err != nil {
		t.Fatalf("insert journal: %v", err)
	}
}

func TestSpecimenRepo_List_Filters(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// Three specimens, varying types and visibility.
	a := makeSpecimen(t, domain.SpecimenMineral, "alpha")
	a.Visibility = domain.VisibilityPrivate
	cn := "CAT-A"
	a.CatalogNumber = &cn
	when := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	a.AcquiredAt = &when

	b := makeSpecimen(t, domain.SpecimenRock, "beta")
	b.Visibility = domain.VisibilityPublic
	when2 := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	b.AcquiredAt = &when2

	c := makeSpecimen(t, domain.SpecimenMineral, "gamma")
	c.Visibility = domain.VisibilityPublic

	for _, s := range []domain.Specimen{a, b, c} {
		if err := fx.repo.Create(ctx, fx.pool, s); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}

	// Filter by type=mineral.
	mineral := domain.SpecimenMineral
	got, _, err := fx.repo.List(ctx, domain.SpecimenFilter{Type: &mineral}, domain.Page{Limit: 100})
	if err != nil {
		t.Fatalf("list type: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("type=mineral: got %d, want 2", len(got))
	}

	// Filter by visibility=public.
	pub := domain.VisibilityPublic
	got, _, err = fx.repo.List(ctx, domain.SpecimenFilter{Visibility: &pub}, domain.Page{Limit: 100})
	if err != nil {
		t.Fatalf("list visibility: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("visibility=public: got %d, want 2", len(got))
	}

	// has_catalog_number=true.
	yes := true
	got, _, err = fx.repo.List(ctx, domain.SpecimenFilter{HasCatalogNumber: &yes}, domain.Page{Limit: 100})
	if err != nil {
		t.Fatalf("list has_catalog: %v", err)
	}
	if len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("has_catalog_number=true: got %d items, expected 1 (a)", len(got))
	}

	// acquired_after.
	after := "2026-02-01"
	got, _, err = fx.repo.List(ctx, domain.SpecimenFilter{AcquiredAfter: &after}, domain.Page{Limit: 100})
	if err != nil {
		t.Fatalf("list acquired_after: %v", err)
	}
	if len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("acquired_after: got %d items, expected 1 (b)", len(got))
	}

	// collector_id stub: any non-nil id returns nothing.
	cid := domain.NewID()
	got, _, err = fx.repo.List(ctx, domain.SpecimenFilter{CollectorID: &cid}, domain.Page{Limit: 100})
	if err != nil {
		t.Fatalf("list collector_id: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("collector_id stub: expected 0, got %d", len(got))
	}
}

func TestSpecimenRepo_List_TsvSearch(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// Crafted descriptions so quartz-related queries rank first.
	quartz := makeSpecimen(t, domain.SpecimenMineral, "quartz cluster")
	quartz.Description = "transparent quartz with clear vugs and minor calcite"

	calcite := makeSpecimen(t, domain.SpecimenMineral, "calcite specimen")
	calcite.Description = "yellow calcite from a sedimentary site"

	rock := makeSpecimen(t, domain.SpecimenRock, "granite slab")
	rock.Description = "coarse granite, no quartz veins of note"

	for _, s := range []domain.Specimen{quartz, calcite, rock} {
		if err := fx.repo.Create(ctx, fx.pool, s); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}

	got, _, err := fx.repo.List(ctx, domain.SpecimenFilter{Query: "quartz"}, domain.Page{Limit: 10})
	if err != nil {
		t.Fatalf("list q=quartz: %v", err)
	}
	if len(got) < 1 {
		t.Fatalf("expected at least 1 hit for quartz, got %d", len(got))
	}
	if got[0].ID != quartz.ID {
		t.Errorf("quartz should rank first; got %s (%s)", got[0].Name, got[0].ID)
	}
}

func TestSpecimenRepo_List_DefaultCursorPagination(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// Insert 3 specimens with distinct created_at values.
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 3; i++ {
		s := makeSpecimen(t, domain.SpecimenMineral, "p")
		s.CreatedAt = now.Add(time.Duration(i) * time.Hour)
		s.UpdatedAt = s.CreatedAt
		if err := fx.repo.Create(ctx, fx.pool, s); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	page1, next1, err := fx.repo.List(ctx, domain.SpecimenFilter{}, domain.Page{Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 size: got %d, want 2", len(page1))
	}
	if next1 == "" {
		t.Fatal("expected next cursor on page 1")
	}
	page2, next2, err := fx.repo.List(ctx, domain.SpecimenFilter{}, domain.Page{Limit: 2, Cursor: string(next1)})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page 2 size: got %d, want 1", len(page2))
	}
	if next2 != "" {
		t.Errorf("expected end-of-pages on page 2, got cursor %q", next2)
	}
}

// uuidIsV7 verifies that the version nibble is 7 and the timestamp
// prefix encodes a recent time (within the last day, well within the
// "first 6 bytes encode a recent timestamp" acceptance bullet).
func uuidIsV7(id uuid.UUID) bool {
	if id.Version() != 7 {
		return false
	}
	var ts [8]byte
	copy(ts[2:], id[:6])
	ms := int64(binary.BigEndian.Uint64(ts[:]))
	t := time.UnixMilli(ms)
	delta := time.Since(t)
	return delta >= -time.Minute && delta < 24*time.Hour
}
