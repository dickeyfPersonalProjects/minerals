//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

func mkSpecies(name string, source domain.MineralSpeciesSource, mindatID *string) domain.MineralSpecies {
	now := time.Now().UTC()
	return domain.MineralSpecies{
		ID:        domain.NewID(),
		Name:      name,
		Source:    source,
		MindatID:  mindatID,
		Data:      []byte(`{}`),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestIntegration_MineralSpecies_CreateAndGet(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewMineralSpeciesPostgres(pool)
	ctx := authedCtx()

	mindatID := "12345"
	attribution := "data via Mindat (CC-BY-NC-SA 4.0)"
	s := mkSpecies("Quartz", domain.MineralSpeciesSourceMindat, &mindatID)
	s.Data = []byte(`{"chemical_formula":"SiO2","mohs_hardness":7}`)
	s.Attribution = &attribution

	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, s.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Quartz" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Source != domain.MineralSpeciesSourceMindat {
		t.Errorf("source = %q", got.Source)
	}
	if got.MindatID == nil || *got.MindatID != "12345" {
		t.Errorf("mindat_id = %v", got.MindatID)
	}
	if got.Attribution == nil || *got.Attribution != attribution {
		t.Errorf("attribution = %v", got.Attribution)
	}
	if string(got.Data) == "" {
		t.Errorf("data empty")
	}
}

func TestIntegration_MineralSpecies_DuplicateName(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewMineralSpeciesPostgres(pool)
	ctx := authedCtx()

	if err := repo.Create(ctx, nil, mkSpecies("Halite", domain.MineralSpeciesSourceUser, nil)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := repo.Create(ctx, nil, mkSpecies("Halite", domain.MineralSpeciesSourceUser, nil))
	if !errors.Is(err, domain.ErrMineralSpeciesConflict) {
		t.Fatalf("got %v, want ErrMineralSpeciesConflict", err)
	}
}

func TestIntegration_MineralSpecies_DuplicateMindatID(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewMineralSpeciesPostgres(pool)
	ctx := authedCtx()

	id := "99"
	if err := repo.Create(ctx, nil, mkSpecies("Pyrite", domain.MineralSpeciesSourceMindat, &id)); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := repo.Create(ctx, nil, mkSpecies("Pyrite Twin", domain.MineralSpeciesSourceMindat, &id))
	if !errors.Is(err, domain.ErrMineralSpeciesConflict) {
		t.Fatalf("got %v, want ErrMineralSpeciesConflict on duplicate mindat_id", err)
	}
}

func TestIntegration_MineralSpecies_FindByName_ILIKE(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewMineralSpeciesPostgres(pool)
	ctx := authedCtx()

	for _, n := range []string{"Quartz", "Smoky Quartz", "Calcite"} {
		if err := repo.Create(ctx, nil, mkSpecies(n, domain.MineralSpeciesSourceUser, nil)); err != nil {
			t.Fatalf("create %q: %v", n, err)
		}
	}

	rows, err := repo.FindByName(ctx, "quartz")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d, want 2 (quartz, smoky quartz)", len(rows))
	}

	all, err := repo.FindByName(ctx, "")
	if err != nil {
		t.Fatalf("find all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("find all = %d, want 3", len(all))
	}
}

func TestIntegration_MineralSpecies_FindByMindatID(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewMineralSpeciesPostgres(pool)
	ctx := authedCtx()

	id := "777"
	want := mkSpecies("Galena", domain.MineralSpeciesSourceMindat, &id)
	if err := repo.Create(ctx, nil, want); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.FindByMindatID(ctx, "777")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID != want.ID {
		t.Errorf("id mismatch")
	}

	_, err = repo.FindByMindatID(ctx, "missing")
	if !errors.Is(err, domain.ErrMineralSpeciesNotFound) {
		t.Errorf("got %v, want ErrMineralSpeciesNotFound", err)
	}
}

func TestIntegration_MineralSpecies_GetMissing(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewMineralSpeciesPostgres(pool)
	_, err := repo.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, domain.ErrMineralSpeciesNotFound) {
		t.Fatalf("got %v, want ErrMineralSpeciesNotFound", err)
	}
}

func TestIntegration_MineralSpecies_FindByName_LikeMetacharsEscaped(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewMineralSpeciesPostgres(pool)
	ctx := authedCtx()

	for _, n := range []string{"Plain Mineral", "Other"} {
		if err := repo.Create(ctx, nil, mkSpecies(n, domain.MineralSpeciesSourceUser, nil)); err != nil {
			t.Fatalf("create %q: %v", n, err)
		}
	}
	rows, err := repo.FindByName(ctx, "%")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d, want 0 (literal %% should not wildcard-match)", len(rows))
	}
}
