package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// fakeSpecimenRepo is an in-memory domain.SpecimenRepo for handler
// tests. The fake is intentionally narrow — it covers the bits the
// HTTP layer touches (errors propagated, fields preserved); the repo
// implementation has its own integration tests for the SQL paths.
type fakeSpecimenRepo struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]domain.Specimen
	createErr error
	updateErr error
	deleteErr error
	listErr   error
}

func newFakeSpecimenRepo() *fakeSpecimenRepo {
	return &fakeSpecimenRepo{rows: map[uuid.UUID]domain.Specimen{}}
}

func (f *fakeSpecimenRepo) Create(ctx context.Context, _ domain.Tx, s domain.Specimen) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	if s.AuthorID == uuid.Nil {
		// Repos populate author_id from auth ctx — mirror that here.
		s.AuthorID = auth.FromContext(ctx).ID
	}
	if s.CatalogNumber != nil {
		for _, e := range f.rows {
			if e.CatalogNumber != nil && *e.CatalogNumber == *s.CatalogNumber {
				return domain.ErrSpecimenConflict
			}
		}
	}
	f.rows[s.ID] = s
	return nil
}

func (f *fakeSpecimenRepo) GetByID(_ context.Context, id uuid.UUID) (domain.Specimen, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.rows[id]
	if !ok {
		return domain.Specimen{}, domain.ErrSpecimenNotFound
	}
	return s, nil
}

func (f *fakeSpecimenRepo) Update(_ context.Context, _ domain.Tx, s domain.Specimen) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	cur, ok := f.rows[s.ID]
	if !ok {
		return domain.ErrSpecimenNotFound
	}
	if cur.Type != s.Type {
		return domain.ErrSpecimenTypeImmutable
	}
	if s.CatalogNumber != nil {
		for id, e := range f.rows {
			if id == s.ID {
				continue
			}
			if e.CatalogNumber != nil && *e.CatalogNumber == *s.CatalogNumber {
				return domain.ErrSpecimenConflict
			}
		}
	}
	f.rows[s.ID] = s
	return nil
}

func (f *fakeSpecimenRepo) Delete(_ context.Context, _ domain.Tx, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.rows[id]; !ok {
		return domain.ErrSpecimenNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeSpecimenRepo) List(_ context.Context, filter domain.SpecimenFilter, _ domain.Page) ([]domain.Specimen, domain.Cursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, "", f.listErr
	}
	if filter.CollectorID != nil {
		return []domain.Specimen{}, "", nil
	}
	out := []domain.Specimen{}
	for _, s := range f.rows {
		if filter.Type != nil && s.Type != *filter.Type {
			continue
		}
		if filter.Visibility != nil && s.Visibility != *filter.Visibility {
			continue
		}
		if filter.HasCatalogNumber != nil {
			if *filter.HasCatalogNumber && s.CatalogNumber == nil {
				continue
			}
			if !*filter.HasCatalogNumber && s.CatalogNumber != nil {
				continue
			}
		}
		out = append(out, s)
	}
	return out, "", nil
}

func newServerWithSpecimens(t *testing.T, repo domain.SpecimenRepo) http.Handler {
	t.Helper()
	return New(Deps{Specimens: repo})
}

func TestSpecimensCreateAndGet(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	body := map[string]any{
		"type":        "mineral",
		"name":        "Quartz",
		"description": "Clear crystal",
		"type_data": map[string]any{
			"chemical_formula": "SiO2",
			"mohs_hardness":    7.0,
			"color":            "clear",
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/specimens/") {
		t.Errorf("Location = %q", loc)
	}
	var created SpecimenView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Type != domain.SpecimenMineral {
		t.Errorf("type = %q", created.Type)
	}
	if created.AuthorID == uuid.Nil {
		t.Error("author_id not populated")
	}
	if created.Visibility != domain.VisibilityPrivate {
		t.Errorf("default visibility = %q, want private", created.Visibility)
	}

	// GET roundtrip.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/specimens/"+created.ID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec2.Code)
	}
}

func TestSpecimensCreateRejectsInvalidTypeData(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	body := map[string]any{
		"type": "rock",
		"name": "Mystery rock",
		"type_data": map[string]any{
			"rock_type": "plutonic", // not in {igneous, sedimentary, metamorphic}
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "invalid_type_data" {
		t.Errorf("code = %q", env.Error.Code)
	}
}

func TestSpecimensCreateRejectsTypeDataShapeMismatch(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	// Mixing fields from two type-data shapes (chemical_formula
	// belongs to MineralData; classification belongs to
	// MeteoriteData) doesn't match any of the three closed schemas,
	// so the huma validator rejects it before reaching the handler.
	// This is the contract surface that makes type_data discipline
	// enforceable from the OpenAPI spec alone.
	body := map[string]any{
		"type": "mineral",
		"name": "Q",
		"type_data": map[string]any{
			"classification":   "L6",
			"chemical_formula": "SiO2",
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — body=%s", rec.Code, rec.Body.String())
	}
}

func TestSpecimensPatchPreservesOmittedFields(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	// Seed one specimen.
	created := mustCreateMineral(t, h, "Q", map[string]any{
		"chemical_formula": "SiO2",
		"color":            "clear",
		"mohs_hardness":    7.0,
	})

	// PATCH only color; chemical_formula and mohs_hardness must
	// survive.
	body := map[string]any{
		"type_data": map[string]any{
			"color": "milky",
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var patched SpecimenView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	var td map[string]any
	_ = json.Unmarshal(patched.TypeData, &td)
	if td["color"] != "milky" {
		t.Errorf("color = %v, want milky", td["color"])
	}
	if td["chemical_formula"] != "SiO2" {
		t.Errorf("chemical_formula not preserved: %v", td)
	}
	// JSON numbers come back as float64.
	if td["mohs_hardness"].(float64) != 7.0 {
		t.Errorf("mohs_hardness lost: %v", td)
	}
}

func TestSpecimensPatchTypeDataNullClearsField(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	created := mustCreateMineral(t, h, "Q", map[string]any{
		"color":            "clear",
		"chemical_formula": "SiO2",
	})

	// Explicit `null` on color should clear it.
	raw := []byte(`{"type_data": {"color": null}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var patched SpecimenView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	var td map[string]any
	_ = json.Unmarshal(patched.TypeData, &td)
	if _, ok := td["color"]; ok {
		t.Errorf("color should be cleared, got %v", td)
	}
	if td["chemical_formula"] != "SiO2" {
		t.Errorf("chemical_formula lost: %v", td)
	}
}

func TestSpecimensPatchRejectsTypeChange(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	created := mustCreateMineral(t, h, "Q", map[string]any{})

	body := map[string]any{
		"type": "meteorite",
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "specimen_type_immutable" {
		t.Errorf("code = %q", env.Error.Code)
	}
}

func TestSpecimensPatchAcceptsMatchingType(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	created := mustCreateMineral(t, h, "Q", map[string]any{})

	// Sending the existing type back is a no-op, must succeed.
	body := map[string]any{
		"type": "mineral",
		"name": "Q-Updated",
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSpecimensDeleteOK(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	created := mustCreateMineral(t, h, "Q", map[string]any{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/specimens/"+created.ID.String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSpecimensDeleteReferenced409(t *testing.T) {
	repo := newFakeSpecimenRepo()
	repo.deleteErr = domain.ErrSpecimenReferenced
	h := newServerWithSpecimens(t, repo)
	created := mustCreateMineral(t, h, "Q", map[string]any{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/specimens/"+created.ID.String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "specimen_referenced" {
		t.Errorf("code = %q", env.Error.Code)
	}
}

func TestSpecimensDeleteNotFound(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/specimens/"+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSpecimensCreateConflictOnCatalogNumber(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	mustCreateMineralWithCatalog(t, h, "first", "FD-001")

	body := map[string]any{
		"type":           "mineral",
		"name":           "second",
		"catalog_number": "FD-001",
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSpecimensListCollectorIDStub(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	mustCreateMineral(t, h, "A", map[string]any{})
	mustCreateMineral(t, h, "B", map[string]any{})

	// `collector_id=` shortcuts to empty results in v1 (B-2 stub).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens?collector_id="+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var lst specimenListBody
	_ = json.Unmarshal(rec.Body.Bytes(), &lst)
	if len(lst.Items) != 0 {
		t.Errorf("collector_id stub should return empty page, got %d items", len(lst.Items))
	}
}

func TestSpecimensListInvalidDateFormat400(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens?acquired_after=yesterday", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSpecimensListInvalidDateRange422(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/specimens?acquired_after=2026-12-01&acquired_before=2026-01-01", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

// Helpers ----------------------------------------------------------

func mustCreateMineral(t *testing.T, h http.Handler, name string, typeData map[string]any) SpecimenView {
	t.Helper()
	body := map[string]any{
		"type":      "mineral",
		"name":      name,
		"type_data": typeData,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create %q: status %d body=%s", name, rec.Code, rec.Body.String())
	}
	var sv SpecimenView
	if err := json.Unmarshal(rec.Body.Bytes(), &sv); err != nil {
		t.Fatalf("seed decode: %v", err)
	}
	return sv
}

func mustCreateMineralWithCatalog(t *testing.T, h http.Handler, name, catalog string) SpecimenView {
	t.Helper()
	body := map[string]any{
		"type":           "mineral",
		"name":           name,
		"catalog_number": catalog,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create %q: status %d body=%s", name, rec.Code, rec.Body.String())
	}
	var sv SpecimenView
	if err := json.Unmarshal(rec.Body.Bytes(), &sv); err != nil {
		t.Fatalf("seed decode: %v", err)
	}
	return sv
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(b)
}

// silence unused-import warnings on this test file when iterating.
var (
	_ = errors.Is
	_ = time.Now
)
