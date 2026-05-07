//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// scopedDB / authedCtx live in collector_postgres_integration_test.go.
// This file reuses them.

func mkSpecimen(t domain.SpecimenType, name string) domain.Specimen {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return domain.Specimen{
		ID:         domain.NewID(),
		Type:       t,
		Name:       name,
		Visibility: domain.VisibilityPrivate,
		CreatedAt:  now,
		UpdatedAt:  now,
		TypeData:   []byte(`{}`),
	}
}

func TestIntegration_Specimen_CreateAndGetRoundtrip_AllTypes(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	// Mineral with full type_data fields.
	formula := "SiO2"
	mohs := 7.0
	color := "clear"
	mData, _ := json.Marshal(domain.MineralData{
		ChemicalFormula: &formula,
		MohsHardness:    &mohs,
		Color:           &color,
	})
	mineral := mkSpecimen(domain.SpecimenMineral, "Quartz")
	mineral.TypeData = mData
	mineral.Description = "Clear hexagonal crystal."
	if err := repo.Create(ctx, nil, mineral); err != nil {
		t.Fatalf("create mineral: %v", err)
	}

	// Rock with rock-specific data.
	rockType := "igneous"
	rData, _ := json.Marshal(domain.RockData{RockType: &rockType})
	rock := mkSpecimen(domain.SpecimenRock, "Granite")
	rock.TypeData = rData
	if err := repo.Create(ctx, nil, rock); err != nil {
		t.Fatalf("create rock: %v", err)
	}

	// Meteorite.
	classification := "L6"
	fall := "find"
	metData, _ := json.Marshal(domain.MeteoriteData{
		Classification: &classification,
		FallOrFind:     &fall,
	})
	meteorite := mkSpecimen(domain.SpecimenMeteorite, "NWA 869")
	meteorite.TypeData = metData
	if err := repo.Create(ctx, nil, meteorite); err != nil {
		t.Fatalf("create meteorite: %v", err)
	}

	// Roundtrip each.
	for _, want := range []domain.Specimen{mineral, rock, meteorite} {
		got, err := repo.GetByID(ctx, want.ID)
		if err != nil {
			t.Fatalf("get %v: %v", want.Type, err)
		}
		if got.Name != want.Name {
			t.Errorf("name: got %q want %q", got.Name, want.Name)
		}
		if got.Type != want.Type {
			t.Errorf("type: got %q want %q", got.Type, want.Type)
		}
		if got.AuthorID != auth.StubUser.ID {
			t.Errorf("author_id: got %v, want StubUser %v", got.AuthorID, auth.StubUser.ID)
		}
		if !db.IsRecentUUIDv7(got.ID, time.Now(), 24*time.Hour) {
			t.Errorf("id %v is not a recent UUIDv7", got.ID)
		}
	}

	// type_data preserved.
	got, _ := repo.GetByID(ctx, mineral.ID)
	var md domain.MineralData
	if err := json.Unmarshal(got.TypeData, &md); err != nil {
		t.Fatalf("unmarshal mineral type_data: %v", err)
	}
	if md.ChemicalFormula == nil || *md.ChemicalFormula != "SiO2" {
		t.Errorf("chemical_formula round-trip lost: %v", md)
	}
}

func TestIntegration_Specimen_CatalogNumberConflict(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	cn := "FD-001"
	first := mkSpecimen(domain.SpecimenMineral, "first")
	first.CatalogNumber = &cn
	if err := repo.Create(ctx, nil, first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	second := mkSpecimen(domain.SpecimenMineral, "second")
	second.CatalogNumber = &cn
	err := repo.Create(ctx, nil, second)
	if !errors.Is(err, domain.ErrSpecimenConflict) {
		t.Fatalf("got %v, want ErrSpecimenConflict", err)
	}
}

func TestIntegration_Specimen_GetMissingNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	_, err := repo.GetByID(authedCtx(), uuid.New())
	if !errors.Is(err, domain.ErrSpecimenNotFound) {
		t.Fatalf("got %v, want ErrSpecimenNotFound", err)
	}
}

func TestIntegration_Specimen_UpdatePersistsAndPreservesType(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	s := mkSpecimen(domain.SpecimenRock, "Original")
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create: %v", err)
	}

	s.Name = "Renamed"
	s.UpdatedAt = time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if err := repo.Update(ctx, nil, s); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Renamed" {
		t.Errorf("name not updated: %q", got.Name)
	}
}

func TestIntegration_Specimen_UpdateRejectsTypeChange(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	s := mkSpecimen(domain.SpecimenMineral, "Mystery")
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Try to flip the type to meteorite — repo rejects.
	s.Type = domain.SpecimenMeteorite
	err := repo.Update(ctx, nil, s)
	if !errors.Is(err, domain.ErrSpecimenTypeImmutable) {
		t.Fatalf("got %v, want ErrSpecimenTypeImmutable", err)
	}

	// Stored row is unchanged.
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Type != domain.SpecimenMineral {
		t.Errorf("stored type changed: got %q", got.Type)
	}
}

func TestIntegration_Specimen_UpdateMissingNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	s := mkSpecimen(domain.SpecimenMineral, "ghost")
	err := repo.Update(authedCtx(), nil, s)
	if !errors.Is(err, domain.ErrSpecimenNotFound) {
		t.Fatalf("got %v, want ErrSpecimenNotFound", err)
	}
}

func TestIntegration_Specimen_DeleteCascadesSpecimenCollectors(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	ctx := authedCtx()

	// Create a specimen + a collector + a join row, then delete the
	// specimen and assert the join row is gone (FK CASCADE).
	s := mkSpecimen(domain.SpecimenMineral, "linked")
	if err := specRepo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create specimen: %v", err)
	}
	c := domain.Collector{
		ID:        domain.NewID(),
		Name:      "linked-collector",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := collRepo.Create(ctx, nil, c); err != nil {
		t.Fatalf("create collector: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO specimen_collectors (specimen_id, collector_id, position, created_at)
		VALUES ($1, $2, 0, now())`, s.ID, c.ID); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	if err := specRepo.Delete(ctx, nil, s.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM specimen_collectors WHERE specimen_id = $1`, s.ID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("specimen_collectors row not cascaded: count=%d", n)
	}
}

func TestIntegration_Specimen_DeleteRejectsWhenPhotosExist(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	s := mkSpecimen(domain.SpecimenMineral, "with-photo")
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Insert a fake file and a photo referencing the specimen.
	fileID := domain.NewID()
	if _, err := pool.Exec(ctx, `
		INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
		VALUES ($1, $2, 'image/jpeg', 1024, 'deadbeef', $3, now())`,
		fileID, "k/"+fileID.String(), auth.StubUser.ID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO photos (id, specimen_id, file_id, position, created_at)
		VALUES ($1, $2, $3, 0, now())`,
		domain.NewID(), s.ID, fileID); err != nil {
		t.Fatalf("insert photo: %v", err)
	}

	err := repo.Delete(ctx, nil, s.ID)
	if !errors.Is(err, domain.ErrSpecimenReferenced) {
		t.Fatalf("got %v, want ErrSpecimenReferenced", err)
	}
}

func TestIntegration_Specimen_DeleteRejectsWhenJournalEntriesExist(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	s := mkSpecimen(domain.SpecimenMineral, "with-journal")
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO journal_entries (id, specimen_id, author_id, body_md, created_at, updated_at)
		VALUES ($1, $2, $3, 'note', now(), now())`,
		domain.NewID(), s.ID, auth.StubUser.ID); err != nil {
		t.Fatalf("insert journal entry: %v", err)
	}

	err := repo.Delete(ctx, nil, s.ID)
	if !errors.Is(err, domain.ErrSpecimenReferenced) {
		t.Fatalf("got %v, want ErrSpecimenReferenced", err)
	}
}

func TestIntegration_Specimen_DeleteMissingNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	err := repo.Delete(authedCtx(), nil, uuid.New())
	if !errors.Is(err, domain.ErrSpecimenNotFound) {
		t.Fatalf("got %v, want ErrSpecimenNotFound", err)
	}
}

func TestIntegration_Specimen_ListFilters(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	// Seed: 2 minerals (one with catalog number), 1 rock public,
	// 1 meteorite acquired in 2025.
	mineralA := mkSpecimen(domain.SpecimenMineral, "MineralA")
	mineralB := mkSpecimen(domain.SpecimenMineral, "MineralB")
	cn := "FD-100"
	mineralB.CatalogNumber = &cn
	rockA := mkSpecimen(domain.SpecimenRock, "RockA")
	rockA.Visibility = domain.VisibilityPublic
	met := mkSpecimen(domain.SpecimenMeteorite, "MetA")
	old := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	met.AcquiredAt = &old

	for _, s := range []domain.Specimen{mineralA, mineralB, rockA, met} {
		if err := repo.Create(ctx, nil, s); err != nil {
			t.Fatalf("seed %s: %v", s.Name, err)
		}
	}

	mineral := domain.SpecimenMineral
	rows, _, err := repo.List(ctx, domain.SpecimenFilter{Type: &mineral}, domain.Page{})
	if err != nil {
		t.Fatalf("filter type: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("type=mineral: got %d, want 2", len(rows))
	}

	pub := domain.VisibilityPublic
	rows, _, err = repo.List(ctx, domain.SpecimenFilter{Visibility: &pub}, domain.Page{})
	if err != nil {
		t.Fatalf("filter visibility: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "RockA" {
		t.Errorf("visibility=public: got %v", rows)
	}

	yes := true
	rows, _, err = repo.List(ctx, domain.SpecimenFilter{HasCatalogNumber: &yes}, domain.Page{})
	if err != nil {
		t.Fatalf("filter has_catalog_number: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "MineralB" {
		t.Errorf("has_catalog=true: got %v", rows)
	}

	no := false
	rows, _, err = repo.List(ctx, domain.SpecimenFilter{HasCatalogNumber: &no}, domain.Page{})
	if err != nil {
		t.Fatalf("filter no catalog: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("has_catalog=false: got %d, want 3", len(rows))
	}

	// acquired_after: rows with acquired_at >= 2025-06-01 should
	// exclude met (Jan 2025). rocks/minerals have nil acquired_at,
	// which Postgres' >= excludes.
	since := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	rows, _, err = repo.List(ctx, domain.SpecimenFilter{AcquiredAfter: &since}, domain.Page{})
	if err != nil {
		t.Fatalf("filter acquired_after: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("acquired_after future: got %d, want 0 (NULLs excluded)", len(rows))
	}

	until := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	rows, _, err = repo.List(ctx, domain.SpecimenFilter{AcquiredBefore: &until}, domain.Page{})
	if err != nil {
		t.Fatalf("filter acquired_before: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "MetA" {
		t.Errorf("acquired_before: got %v", rows)
	}
}

func TestIntegration_Specimen_ListCollectorIDStub(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	for i := 0; i < 3; i++ {
		s := mkSpecimen(domain.SpecimenMineral, "row")
		if err := repo.Create(ctx, nil, s); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	cid := uuid.New()
	rows, cur, err := repo.List(ctx, domain.SpecimenFilter{CollectorID: &cid}, domain.Page{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("collector_id stub should return empty results, got %d", len(rows))
	}
	if cur != "" {
		t.Errorf("expected empty cursor, got %q", cur)
	}
}

func TestIntegration_Specimen_ListPaginationDefault(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	// Seed 5 specimens with strictly-increasing created_at so ordering
	// is well-defined regardless of UUIDv7 jitter.
	created := []domain.Specimen{}
	base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	for i := 0; i < 5; i++ {
		s := mkSpecimen(domain.SpecimenMineral, "row")
		s.CreatedAt = base.Add(time.Duration(i) * time.Second)
		s.UpdatedAt = s.CreatedAt
		if err := repo.Create(ctx, nil, s); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		created = append(created, s)
	}

	page1, cur, err := repo.List(ctx, domain.SpecimenFilter{}, domain.Page{Limit: 3})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1 size = %d, want 3", len(page1))
	}
	if page1[0].ID != created[4].ID {
		t.Errorf("page1[0] = %v, want newest %v", page1[0].ID, created[4].ID)
	}
	if cur == "" {
		t.Fatal("expected non-empty cursor")
	}

	page2, cur2, err := repo.List(ctx, domain.SpecimenFilter{}, domain.Page{Limit: 3, Cursor: string(cur)})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2 size = %d, want 2", len(page2))
	}
	if cur2 != "" {
		t.Errorf("end-of-results cursor should be empty, got %q", cur2)
	}
}

func TestIntegration_Specimen_ListSearch(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	// Seed: three specimens whose name/description carry distinct
	// keywords. Name has weight A; description has weight B; so
	// matches in the name should rank above matches in description.
	a := mkSpecimen(domain.SpecimenMineral, "Spectacular Quartz")
	a.Description = "common rock-forming material"
	b := mkSpecimen(domain.SpecimenMineral, "Mystery Sample")
	b.Description = "a brilliant quartz specimen with unusual color"
	c := mkSpecimen(domain.SpecimenRock, "Granite")
	c.Description = "igneous, no relation"

	for _, s := range []domain.Specimen{a, b, c} {
		if err := repo.Create(ctx, nil, s); err != nil {
			t.Fatalf("seed %q: %v", s.Name, err)
		}
	}

	// Search for "quartz" — both a and b should match; a should
	// outrank b (name carries weight A vs description's B).
	rows, _, err := repo.List(ctx, domain.SpecimenFilter{Query: "quartz"}, domain.Page{})
	if err != nil {
		t.Fatalf("search quartz: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d matches, want 2: %v", len(rows), specimenNames(rows))
	}
	if rows[0].Name != "Spectacular Quartz" {
		t.Errorf("ordering: got %q first, want 'Spectacular Quartz' (weight A > B)",
			rows[0].Name)
	}
	// "granite" must not appear in the quartz query.
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Name), "granite") {
			t.Errorf("granite leaked into quartz query")
		}
	}

	// Cursor pagination on a ranked query: limit=1 returns top
	// match, follow-up cursor returns the rest.
	page1, cur, err := repo.List(ctx, domain.SpecimenFilter{Query: "quartz"}, domain.Page{Limit: 1})
	if err != nil {
		t.Fatalf("ranked page1: %v", err)
	}
	if len(page1) != 1 || page1[0].Name != "Spectacular Quartz" {
		t.Fatalf("ranked page1 = %v", specimenNames(page1))
	}
	if cur == "" {
		t.Fatal("expected ranked cursor")
	}
	// A default cursor (issued under no-q ordering) MUST be
	// rejected when q is added — and the inverse: the rank cursor
	// MUST be rejected when q is removed. Verify the second
	// direction.
	if _, _, err := repo.List(ctx, domain.SpecimenFilter{}, domain.Page{Cursor: string(cur)}); err == nil {
		t.Error("expected error using rank cursor without q=")
	}
	page2, cur2, err := repo.List(ctx, domain.SpecimenFilter{Query: "quartz"}, domain.Page{Limit: 1, Cursor: string(cur)})
	if err != nil {
		t.Fatalf("ranked page2: %v", err)
	}
	if len(page2) != 1 || page2[0].Name != "Mystery Sample" {
		t.Fatalf("ranked page2 = %v", specimenNames(page2))
	}
	if cur2 != "" {
		t.Errorf("ranked end cursor should be empty, got %q", cur2)
	}
}

func TestIntegration_Specimen_AuthorIDPopulated(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)

	customUser := auth.User{
		ID:    domain.NewID(),
		Email: "tester@example.invalid",
	}
	ctx := auth.WithUser(context.Background(), customUser)

	s := mkSpecimen(domain.SpecimenMineral, "authored")
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AuthorID != customUser.ID {
		t.Errorf("author_id = %v, want %v", got.AuthorID, customUser.ID)
	}
}

func TestIntegration_Specimen_IDIsRecentUUIDv7(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewSpecimenPostgres(pool)
	ctx := authedCtx()

	s := mkSpecimen(domain.SpecimenMineral, "v7-check")
	before := time.Now()
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.ID.Version() != 7 {
		t.Fatalf("ID version = %d, want 7", s.ID.Version())
	}
	if !db.IsRecentUUIDv7(s.ID, before, 5*time.Minute) {
		t.Errorf("UUIDv7 timestamp prefix not recent: %v", s.ID)
	}
}

func specimenNames(rows []domain.Specimen) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}
