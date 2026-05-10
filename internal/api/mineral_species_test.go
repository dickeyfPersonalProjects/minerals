package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/mindat"
)

// fakeMineralSpeciesRepo is an in-memory domain.MineralSpeciesRepo
// for handler tests. The test gives back rows in insertion order;
// FindByName does a case-insensitive substring match, matching the
// production repo's contract.
type fakeMineralSpeciesRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]domain.MineralSpecies
}

func newFakeMineralSpeciesRepo() *fakeMineralSpeciesRepo {
	return &fakeMineralSpeciesRepo{rows: map[uuid.UUID]domain.MineralSpecies{}}
}

func (f *fakeMineralSpeciesRepo) Create(_ context.Context, _ domain.Tx, s domain.MineralSpecies) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.rows {
		if strings.EqualFold(existing.Name, s.Name) {
			return domain.ErrMineralSpeciesConflict
		}
		if existing.MindatID != nil && s.MindatID != nil && *existing.MindatID == *s.MindatID {
			return domain.ErrMineralSpeciesConflict
		}
	}
	f.rows[s.ID] = s
	return nil
}

func (f *fakeMineralSpeciesRepo) GetByID(_ context.Context, id uuid.UUID) (domain.MineralSpecies, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.rows[id]
	if !ok {
		return domain.MineralSpecies{}, domain.ErrMineralSpeciesNotFound
	}
	return s, nil
}

func (f *fakeMineralSpeciesRepo) FindByName(_ context.Context, q string) ([]domain.MineralSpecies, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.MineralSpecies{}
	needle := strings.ToLower(strings.TrimSpace(q))
	for _, r := range f.rows {
		if needle == "" || strings.Contains(strings.ToLower(r.Name), needle) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeMineralSpeciesRepo) FindByMindatID(_ context.Context, mindatID string) (domain.MineralSpecies, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.MindatID != nil && *r.MindatID == mindatID {
			return r, nil
		}
	}
	return domain.MineralSpecies{}, domain.ErrMineralSpeciesNotFound
}

// fakeMindat is a configurable MindatLookup. The default behavior is
// "no result" — set Result or Err per test.
type fakeMindat struct {
	mu     sync.Mutex
	calls  int
	result *mindat.MineralRecord
	err    error
}

func (f *fakeMindat) LookupByName(_ context.Context, _ string) (*mindat.MineralRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.result == nil {
		return nil, mindat.ErrNotFound
	}
	cp := *f.result
	return &cp, nil
}

func newServerWithMineralSpecies(t *testing.T, repo domain.MineralSpeciesRepo, lookup MindatLookup) http.Handler {
	t.Helper()
	return New(Deps{MineralSpecies: &MineralSpeciesServiceDeps{Repo: repo, Mindat: lookup}})
}

type mineralSpeciesListResponse struct {
	Items []MineralSpeciesView `json:"items"`
}

func TestListMineralSpecies_DBOnlyMode_NoMatch(t *testing.T) {
	// No Mindat client wired (DB-only mode). Empty DB returns 200
	// with empty items — never 404.
	repo := newFakeMineralSpeciesRepo()
	h := newServerWithMineralSpecies(t, repo, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mineral-species?q=quartz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body mineralSpeciesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 0 {
		t.Errorf("got %d items, want 0", len(body.Items))
	}
}

func TestListMineralSpecies_DBHit(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	id := domain.NewID()
	repo.rows[id] = domain.MineralSpecies{
		ID:     id,
		Name:   "Quartz",
		Source: domain.MineralSpeciesSourceUser,
		Data:   []byte(`{"chemical_formula":"SiO2"}`),
	}
	mindat := &fakeMindat{}
	h := newServerWithMineralSpecies(t, repo, mindat)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mineral-species?q=quart", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body mineralSpeciesListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	if body.Items[0].Name != "Quartz" {
		t.Errorf("name = %q", body.Items[0].Name)
	}
	if mindat.calls != 0 {
		t.Errorf("Mindat called %d times, want 0 (DB hit should short-circuit)", mindat.calls)
	}
	if body.Items[0].Data.ChemicalFormula == nil || *body.Items[0].Data.ChemicalFormula != "SiO2" {
		t.Errorf("data not decoded into view")
	}
}

func TestListMineralSpecies_MindatFallthrough_StoresAndReturns(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	formula := "SiO2"
	hardness := 7.0
	lookup := &fakeMindat{
		result: &mindat.MineralRecord{
			Name:        "Quartz",
			MindatID:    "12345",
			Attribution: "data via Mindat (CC-BY-NC-SA 4.0)",
			Data: domain.MineralData{
				ChemicalFormula: &formula,
				MohsHardness:    &hardness,
			},
		},
	}
	h := newServerWithMineralSpecies(t, repo, lookup)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mineral-species?q=quartz", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body mineralSpeciesListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	if body.Items[0].Source != "mindat" {
		t.Errorf("source = %q", body.Items[0].Source)
	}
	if body.Items[0].Attribution == nil || !strings.Contains(*body.Items[0].Attribution, "Mindat") {
		t.Errorf("attribution not set")
	}
	if lookup.calls != 1 {
		t.Errorf("Mindat called %d times, want 1", lookup.calls)
	}

	// Second call: DB now has the row, Mindat must NOT be hit again.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/v1/mineral-species?q=quartz", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second status=%d", rec2.Code)
	}
	if lookup.calls != 1 {
		t.Errorf("Mindat called %d times after DB hit, want still 1", lookup.calls)
	}
}

func TestListMineralSpecies_MindatRateLimitedDegradesGracefully(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	lookup := &fakeMindat{err: mindat.ErrRateLimited}
	h := newServerWithMineralSpecies(t, repo, lookup)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mineral-species?q=quartz", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s — must not crash on rate-limit", rec.Code, rec.Body.String())
	}
	var body mineralSpeciesListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Items) != 0 {
		t.Errorf("items = %d, want 0 on Mindat rate-limit", len(body.Items))
	}
}

func TestListMineralSpecies_EmptyQueryDoesNotHitMindat(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	lookup := &fakeMindat{}
	h := newServerWithMineralSpecies(t, repo, lookup)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mineral-species", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if lookup.calls != 0 {
		t.Errorf("Mindat called %d times for empty query, want 0", lookup.calls)
	}
}

func TestCreateMineralSpecies_UserSource(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	h := newServerWithMineralSpecies(t, repo, nil)

	body, _ := json.Marshal(map[string]any{
		"name": "Custom Mineral",
		"data": map[string]any{
			"chemical_formula": "X2Y",
			"color":            "blue",
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mineral-species", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/mineral-species/") {
		t.Errorf("Location = %q", loc)
	}
	var view MineralSpeciesView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Source != "user" {
		t.Errorf("source = %q, want \"user\"", view.Source)
	}
	if view.AuthorID == uuid.Nil {
		t.Errorf("author_id not populated")
	}
	if view.Data.ChemicalFormula == nil || *view.Data.ChemicalFormula != "X2Y" {
		t.Errorf("data not round-tripped")
	}
}

func TestCreateMineralSpecies_DuplicateNameConflict(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	id := domain.NewID()
	repo.rows[id] = domain.MineralSpecies{ID: id, Name: "Quartz"}

	h := newServerWithMineralSpecies(t, repo, nil)
	body, _ := json.Marshal(map[string]any{
		"name": "Quartz",
		"data": map[string]any{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mineral-species", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateMineralSpecies_InvalidData(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	h := newServerWithMineralSpecies(t, repo, nil)

	bad := 99.9
	body, _ := json.Marshal(map[string]any{
		"name": "Bad",
		"data": map[string]any{"mohs_hardness": bad},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mineral-species", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetMineralSpecies_NotFound(t *testing.T) {
	repo := newFakeMineralSpeciesRepo()
	h := newServerWithMineralSpecies(t, repo, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mineral-species/"+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
