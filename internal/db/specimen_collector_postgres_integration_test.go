//go:build integration

package db_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// scopedDB / authedCtx / mkCollector / mkSpecimen live in
// collector_postgres_integration_test.go and
// specimen_postgres_integration_test.go. This file reuses them.

// seedCollectors creates n collectors with deterministic names and
// returns their ids in creation order.
func seedCollectors(t *testing.T, repo *db.CollectorPostgres, names ...string) []uuid.UUID {
	t.Helper()
	ctx := authedCtx()
	out := make([]uuid.UUID, 0, len(names))
	for _, n := range names {
		c := mkCollector(n, nil)
		if err := repo.Create(ctx, nil, c); err != nil {
			t.Fatalf("seed collector %q: %v", n, err)
		}
		out = append(out, c.ID)
	}
	return out
}

func seedSpecimen(t *testing.T, repo *db.SpecimenPostgres, name string) uuid.UUID {
	t.Helper()
	ctx := authedCtx()
	s := mkSpecimen(domain.SpecimenMineral, name)
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("seed specimen %q: %v", name, err)
	}
	return s.ID
}

func TestIntegration_SpecimenCollectors_EmptyChainByDefault(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	specID := seedSpecimen(t, specRepo, "no-collectors")

	chain, err := linkRepo.GetChain(ctx, nil, specID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if len(chain) != 0 {
		t.Errorf("expected empty chain, got %d links", len(chain))
	}
}

func TestIntegration_SpecimenCollectors_ReplaceSetsChain(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	specID := seedSpecimen(t, specRepo, "set-chain")
	cIDs := seedCollectors(t, collRepo, "alice", "bob", "carol")

	if err := linkRepo.ReplaceChain(ctx, nil, specID, cIDs); err != nil {
		t.Fatalf("replace chain: %v", err)
	}

	got, err := linkRepo.GetChain(ctx, nil, specID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if len(got) != len(cIDs) {
		t.Fatalf("len got=%d want=%d", len(got), len(cIDs))
	}
	for i, link := range got {
		if link.Position != i+1 {
			t.Errorf("position[%d]: got %d want %d", i, link.Position, i+1)
		}
		if link.Collector.ID != cIDs[i] {
			t.Errorf("collector[%d]: got %v want %v", i, link.Collector.ID, cIDs[i])
		}
	}
}

func TestIntegration_SpecimenCollectors_ReplaceReorders(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	specID := seedSpecimen(t, specRepo, "reorder")
	cIDs := seedCollectors(t, collRepo, "x", "y", "z")

	if err := linkRepo.ReplaceChain(ctx, nil, specID, cIDs); err != nil {
		t.Fatalf("first replace: %v", err)
	}

	reversed := []uuid.UUID{cIDs[2], cIDs[1], cIDs[0]}
	if err := linkRepo.ReplaceChain(ctx, nil, specID, reversed); err != nil {
		t.Fatalf("reorder replace: %v", err)
	}

	got, err := linkRepo.GetChain(ctx, nil, specID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len got=%d want=3", len(got))
	}
	for i, want := range reversed {
		if got[i].Collector.ID != want {
			t.Errorf("position %d: got %v want %v", i+1, got[i].Collector.ID, want)
		}
		if got[i].Position != i+1 {
			t.Errorf("position field [%d]: got %d want %d", i, got[i].Position, i+1)
		}
	}
}

func TestIntegration_SpecimenCollectors_ReplaceWithEmptyClears(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	specID := seedSpecimen(t, specRepo, "clearable")
	cIDs := seedCollectors(t, collRepo, "p", "q")

	if err := linkRepo.ReplaceChain(ctx, nil, specID, cIDs); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	if err := linkRepo.ReplaceChain(ctx, nil, specID, nil); err != nil {
		t.Fatalf("clear chain: %v", err)
	}

	got, err := linkRepo.GetChain(ctx, nil, specID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("chain should be cleared, got %d links", len(got))
	}
}

func TestIntegration_SpecimenCollectors_ReplaceMissingSpecimen(t *testing.T) {
	pool := scopedDB(t)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	cIDs := seedCollectors(t, collRepo, "lonely")

	err := linkRepo.ReplaceChain(ctx, nil, uuid.New(), cIDs)
	if !errors.Is(err, domain.ErrSpecimenNotFound) {
		t.Fatalf("want ErrSpecimenNotFound, got %v", err)
	}
}

func TestIntegration_SpecimenCollectors_ReplaceMissingCollector(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	specID := seedSpecimen(t, specRepo, "needs-existing")
	cIDs := seedCollectors(t, collRepo, "real")

	mixed := []uuid.UUID{cIDs[0], uuid.New()}
	err := linkRepo.ReplaceChain(ctx, nil, specID, mixed)
	if !errors.Is(err, domain.ErrCollectorNotFound) {
		t.Fatalf("want ErrCollectorNotFound, got %v", err)
	}

	// Atomicity: chain must be unchanged (still empty).
	got, err := linkRepo.GetChain(ctx, nil, specID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("partial replace leaked %d links into the chain", len(got))
	}
}

func TestIntegration_SpecimenCollectors_DeletingSpecimenCascades(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	specID := seedSpecimen(t, specRepo, "doomed")
	cIDs := seedCollectors(t, collRepo, "linked")

	if err := linkRepo.ReplaceChain(ctx, nil, specID, cIDs); err != nil {
		t.Fatalf("seed chain: %v", err)
	}
	if err := specRepo.Delete(ctx, nil, specID); err != nil {
		t.Fatalf("delete specimen: %v", err)
	}

	// After delete the collector still exists but the chain is gone
	// — verify by querying the join table directly through the repo.
	chain, err := linkRepo.GetChain(ctx, nil, specID)
	if err != nil {
		t.Fatalf("get chain after delete: %v", err)
	}
	if len(chain) != 0 {
		t.Errorf("cascade leaked %d links", len(chain))
	}

	// And the collector is still deletable in isolation.
	if err := collRepo.Delete(ctx, nil, cIDs[0]); err != nil {
		t.Fatalf("delete unreferenced collector: %v", err)
	}
}

func TestIntegration_SpecimenCollectors_CollectorDeleteBlockedByChain(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	specID := seedSpecimen(t, specRepo, "still-using")
	cIDs := seedCollectors(t, collRepo, "in-use")

	if err := linkRepo.ReplaceChain(ctx, nil, specID, cIDs); err != nil {
		t.Fatalf("seed chain: %v", err)
	}

	// mi-yvt rule: deleting a referenced collector returns
	// ErrCollectorReferenced (the FK is ON DELETE RESTRICT).
	err := collRepo.Delete(ctx, nil, cIDs[0])
	if !errors.Is(err, domain.ErrCollectorReferenced) {
		t.Fatalf("want ErrCollectorReferenced, got %v", err)
	}
}

func TestIntegration_SpecimenList_FilteredByCollector(t *testing.T) {
	pool := scopedDB(t)
	specRepo := db.NewSpecimenPostgres(pool)
	collRepo := db.NewCollectorPostgres(pool)
	linkRepo := db.NewSpecimenCollectorPostgres(pool)
	ctx := authedCtx()

	a := seedSpecimen(t, specRepo, "spec-a")
	b := seedSpecimen(t, specRepo, "spec-b")
	c := seedSpecimen(t, specRepo, "spec-c")
	cIDs := seedCollectors(t, collRepo, "alice", "bob")

	// a → [alice], b → [bob, alice], c → []
	if err := linkRepo.ReplaceChain(ctx, nil, a, []uuid.UUID{cIDs[0]}); err != nil {
		t.Fatalf("link a: %v", err)
	}
	if err := linkRepo.ReplaceChain(ctx, nil, b, []uuid.UUID{cIDs[1], cIDs[0]}); err != nil {
		t.Fatalf("link b: %v", err)
	}

	// Filter on alice should return a and b (deduped — no JOIN
	// row-multiplication), not c.
	aliceID := cIDs[0]
	got, _, err := specRepo.List(ctx, domain.SpecimenFilter{CollectorID: &aliceID}, domain.Page{})
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	gotIDs := make(map[uuid.UUID]int)
	for _, s := range got {
		gotIDs[s.ID]++
	}
	if gotIDs[a] != 1 || gotIDs[b] != 1 {
		t.Errorf("alice filter: want {a:1, b:1}, got %v", gotIDs)
	}
	if _, present := gotIDs[c]; present {
		t.Errorf("alice filter: c should not appear, got %v", gotIDs)
	}
	// Bob filter returns just b.
	bobID := cIDs[1]
	got, _, err = specRepo.List(ctx, domain.SpecimenFilter{CollectorID: &bobID}, domain.Page{})
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if len(got) != 1 || got[0].ID != b {
		t.Errorf("bob filter: want [b], got %d items", len(got))
	}

	// Unknown collector filter returns empty without error.
	missing := uuid.New()
	got, _, err = specRepo.List(ctx, domain.SpecimenFilter{CollectorID: &missing}, domain.Page{})
	if err != nil {
		t.Fatalf("list missing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing collector filter: want empty, got %d", len(got))
	}
}
