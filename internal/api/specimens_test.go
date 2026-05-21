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
	mu   sync.Mutex
	rows map[uuid.UUID]domain.Specimen
	// photoFiles records (specimen_id -> set of file_ids of photos
	// on that specimen). HasPhotoWithFile checks against it, and
	// tests opt in by calling addPhotoFile when they need
	// main_image_id validation to succeed.
	photoFiles map[uuid.UUID]map[uuid.UUID]struct{}
	createErr  error
	updateErr  error
	deleteErr  error
	listErr    error
	listCalls  int
}

func newFakeSpecimenRepo() *fakeSpecimenRepo {
	return &fakeSpecimenRepo{
		rows:       map[uuid.UUID]domain.Specimen{},
		photoFiles: map[uuid.UUID]map[uuid.UUID]struct{}{},
	}
}

// addPhotoFile registers a (specimen_id, file_id) pair so the fake's
// HasPhotoWithFile returns true for it. Tests covering main_image_id
// PATCH happy paths use this to satisfy the validation guard.
func (f *fakeSpecimenRepo) addPhotoFile(specimenID, fileID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set, ok := f.photoFiles[specimenID]
	if !ok {
		set = map[uuid.UUID]struct{}{}
		f.photoFiles[specimenID] = set
	}
	set[fileID] = struct{}{}
}

func (f *fakeSpecimenRepo) HasPhotoWithFile(_ context.Context, specimenID, fileID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set, ok := f.photoFiles[specimenID]
	if !ok {
		return false, nil
	}
	_, ok = set[fileID]
	return ok, nil
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
	f.listCalls++
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
		if filter.OwnerID != nil && s.AuthorID != *filter.OwnerID {
			continue
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

func TestSpecimensCreateFossil(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	body := map[string]any{
		"type":        "fossil",
		"name":        "T-rex tooth",
		"description": "Cretaceous theropod",
		"type_data": map[string]any{
			"taxon":             "Tyrannosaurus rex",
			"taxonomic_group":   "Dinosauria",
			"geologic_period":   "Cretaceous",
			"formation":         "Hell Creek Formation",
			"preservation_type": "Permineralized",
			"completeness":      "Complete",
			"prepared":          true,
			"prep_notes":        "Air-abrasion only",
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created SpecimenView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Type != domain.SpecimenFossil {
		t.Errorf("type = %q, want fossil", created.Type)
	}
	var td map[string]any
	_ = json.Unmarshal(created.TypeData, &td)
	if td["taxon"] != "Tyrannosaurus rex" {
		t.Errorf("taxon = %v", td["taxon"])
	}
	if td["prepared"] != true {
		t.Errorf("prepared = %v", td["prepared"])
	}
}

func TestSpecimensFossilFilterByType(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	// Seed: 1 fossil, 1 mineral. ?type=fossil must return only the fossil.
	bodyFossil := map[string]any{
		"type": "fossil", "name": "Ammonite",
		"type_data": map[string]any{"taxon": "Ammonite sp."},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, bodyFossil))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed fossil: %d body=%s", rec.Code, rec.Body.String())
	}
	_ = mustCreateMineral(t, h, "Galena", map[string]any{})

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/specimens?type=fossil", nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec2.Code, rec2.Body.String())
	}
	var lst specimenListBody
	if err := json.Unmarshal(rec2.Body.Bytes(), &lst); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(lst.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(lst.Items))
	}
	if lst.Items[0].Type != domain.SpecimenFossil {
		t.Errorf("filtered type = %q", lst.Items[0].Type)
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

// A user pasting HTML-flavored markup into the chemical_formula field
// (e.g. via copy-paste from a Mindat page) must be normalized on the
// way in so the column stays uniformly Unicode at rest (mi-c8v).
func TestSpecimensCreateNormalizesHTMLChemicalFormula(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	body := map[string]any{
		"type": "mineral",
		"name": "Curite",
		"type_data": map[string]any{
			"chemical_formula": "Pb(UO<sub>2</sub>)<sub>3</sub>O<sub>3</sub>(OH)<sub>2</sub> &middot; 3H<sub>2</sub>O",
		},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created SpecimenView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var td map[string]any
	if err := json.Unmarshal(created.TypeData, &td); err != nil {
		t.Fatalf("decode type_data: %v", err)
	}
	const want = "Pb(UO₂)₃O₃(OH)₂ · 3H₂O"
	if got := td["chemical_formula"]; got != want {
		t.Errorf("chemical_formula = %v, want %q", got, want)
	}
	if got, _ := td["chemical_formula"].(string); strings.ContainsAny(got, "<&") {
		t.Errorf("stored value still contains HTML markup: %q", got)
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

func TestSpecimensPatchMainImageIDSetsField(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	created := mustCreateMineral(t, h, "Q", map[string]any{})

	fileID := uuid.New()
	// Register the (specimen, file) pair in the fake so the
	// HasPhotoWithFile guard passes — mirrors a photo already
	// uploaded on this specimen with that file_id.
	repo.addPhotoFile(created.ID, fileID)

	body := map[string]any{"main_image_id": fileID.String()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var patched SpecimenView
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if patched.MainImageID == nil || *patched.MainImageID != fileID {
		t.Errorf("main_image_id: got %v, want %v", patched.MainImageID, fileID)
	}
}

func TestSpecimensPatchMainImageIDRejectsForeignFile(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)
	created := mustCreateMineral(t, h, "Q", map[string]any{})

	// foreignFile is NOT registered as a photo on `created`; the
	// PATCH must be rejected with 422 + invalid_main_image_id.
	foreignFile := uuid.New()
	body := map[string]any{"main_image_id": foreignFile.String()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "invalid_main_image_id" {
		t.Errorf("code = %q, want invalid_main_image_id", env.Error.Code)
	}
}

// TestSpecimensPatchVisibilityOverrides exercises the three-way
// absent / explicit-null / value semantics of the visibility_*
// override fields added for the per-field visibility surface
// (CONTRACT.md §13b, mi-fo8 / mi-xx6). Each sub-test issues a focused
// PATCH and asserts the SpecimenView response carries (or doesn't
// carry) the expected override.
func TestSpecimensPatchVisibilityOverrides(t *testing.T) {
	t.Run("set then clear via explicit null", func(t *testing.T) {
		repo := newFakeSpecimenRepo()
		h := newServerWithSpecimens(t, repo)
		created := mustCreateMineral(t, h, "Q", map[string]any{})

		// Set all three.
		raw := []byte(`{
			"visibility_price": "private",
			"visibility_acquired_from": "unlisted",
			"visibility_images": "public"
		}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("set status = %d body=%s", rec.Code, rec.Body.String())
		}
		var got SpecimenView
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.VisibilityPrice == nil || *got.VisibilityPrice != domain.VisibilityPrivate {
			t.Errorf("VisibilityPrice = %v, want private", got.VisibilityPrice)
		}
		if got.VisibilityAcquiredFrom == nil || *got.VisibilityAcquiredFrom != domain.VisibilityUnlisted {
			t.Errorf("VisibilityAcquiredFrom = %v, want unlisted", got.VisibilityAcquiredFrom)
		}
		if got.VisibilityImages == nil || *got.VisibilityImages != domain.VisibilityPublic {
			t.Errorf("VisibilityImages = %v, want public", got.VisibilityImages)
		}

		// Explicit null clears just one — the others must survive.
		raw = []byte(`{"visibility_price": null}`)
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("clear status = %d body=%s", rec.Code, rec.Body.String())
		}
		got = SpecimenView{}
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.VisibilityPrice != nil {
			t.Errorf("VisibilityPrice should be cleared, got %v", *got.VisibilityPrice)
		}
		if got.VisibilityAcquiredFrom == nil || *got.VisibilityAcquiredFrom != domain.VisibilityUnlisted {
			t.Errorf("VisibilityAcquiredFrom must survive null on price, got %v", got.VisibilityAcquiredFrom)
		}
		if got.VisibilityImages == nil || *got.VisibilityImages != domain.VisibilityPublic {
			t.Errorf("VisibilityImages must survive null on price, got %v", got.VisibilityImages)
		}
	})

	t.Run("omitted key preserves stored override", func(t *testing.T) {
		repo := newFakeSpecimenRepo()
		h := newServerWithSpecimens(t, repo)
		created := mustCreateMineral(t, h, "Q", map[string]any{})

		// Seed an override on price.
		raw := []byte(`{"visibility_price": "private"}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed status = %d body=%s", rec.Code, rec.Body.String())
		}

		// A PATCH that omits visibility_price entirely must leave it
		// alone (changing only the description).
		raw = []byte(`{"description": "updated"}`)
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("preserve status = %d body=%s", rec.Code, rec.Body.String())
		}
		var got SpecimenView
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.VisibilityPrice == nil || *got.VisibilityPrice != domain.VisibilityPrivate {
			t.Errorf("VisibilityPrice must survive omission, got %v", got.VisibilityPrice)
		}
	})

	t.Run("rejects invalid enum value", func(t *testing.T) {
		repo := newFakeSpecimenRepo()
		h := newServerWithSpecimens(t, repo)
		created := mustCreateMineral(t, h, "Q", map[string]any{})

		raw := []byte(`{"visibility_price": "secret"}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/specimens/"+created.ID.String(), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		// The schema validator's enum check pre-empts the handler's
		// defensive `applyVisibilityPatch` check — either layer is a
		// hard reject. 422 (huma schema validation) is the path that
		// fires in the happy case; 400 (handler) is the defense-in-
		// depth path if the schema is ever loosened.
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 422 or 400 — body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("absent on GET when never set", func(t *testing.T) {
		repo := newFakeSpecimenRepo()
		h := newServerWithSpecimens(t, repo)
		created := mustCreateMineral(t, h, "Q", map[string]any{})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens/"+created.ID.String(), nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		// The three keys must be absent (not `null`) on the wire when
		// no override has ever been written — preserves the "key absent
		// means fall through to the owner default" semantics for the
		// SPA's resolveScalar/resolveImage helpers.
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &raw)
		for _, k := range []string{"visibility_price", "visibility_acquired_from", "visibility_images"} {
			if _, present := raw[k]; present {
				t.Errorf("key %s should be absent on a never-set specimen, got %s", k, string(raw[k]))
			}
		}
	})
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

// TestSpecimensListScopeMineFiltersToOwner verifies the "browse my
// collection" view (mi-xue7): ?scope=mine restricts the list to the
// caller's own rows (the stub identity here), excluding rows authored
// by someone else regardless of visibility. Without scope, every
// visible row is returned.
func TestSpecimensListScopeMineFiltersToOwner(t *testing.T) {
	repo := newFakeSpecimenRepo()
	h := newServerWithSpecimens(t, repo)

	// Two rows owned by the stub caller (created through the handler).
	mustCreateMineral(t, h, "Mine A", map[string]any{})
	mustCreateMineral(t, h, "Mine B", map[string]any{})

	// One public row owned by a different author, injected directly so
	// it carries a foreign author_id.
	other := uuid.New()
	repo.mu.Lock()
	otherID := domain.NewID()
	repo.rows[otherID] = domain.Specimen{
		ID:         otherID,
		Type:       domain.SpecimenMineral,
		Name:       "Someone else's",
		Visibility: domain.VisibilityPublic,
		AuthorID:   other,
		TypeData:   []byte(`{}`),
	}
	repo.mu.Unlock()

	// scope=mine → only the caller's two rows.
	mineNames := listSpecimenNames(t, h, "/api/v1/specimens?scope=mine")
	if len(mineNames) != 2 {
		t.Fatalf("scope=mine returned %d items, want 2: %v", len(mineNames), mineNames)
	}
	for _, n := range mineNames {
		if n == "Someone else's" {
			t.Errorf("scope=mine leaked a foreign-authored row")
		}
	}

	// No scope → all three rows (the stub caller is not anonymous, so
	// the foreign public row is visible too).
	allNames := listSpecimenNames(t, h, "/api/v1/specimens")
	if len(allNames) != 3 {
		t.Errorf("unscoped list returned %d items, want 3: %v", len(allNames), allNames)
	}
}

// TestSpecimensListScopeMineAnonymousEmpty verifies that an anonymous
// caller asking for scope=mine gets an empty 200 page and the repo is
// never consulted — per CONTRACT.md §13 list endpoints never 401, and
// an anonymous caller owns nothing (mi-xue7).
func TestSpecimensListScopeMineAnonymousEmpty(t *testing.T) {
	repo := newFakeSpecimenRepo()
	// Seed a row so a non-short-circuited path would have something to
	// return; an anonymous scope=mine must still come back empty.
	id := domain.NewID()
	repo.rows[id] = domain.Specimen{
		ID:         id,
		Type:       domain.SpecimenMineral,
		Name:       "Public thing",
		Visibility: domain.VisibilityPublic,
		AuthorID:   uuid.New(),
		TypeData:   []byte(`{}`),
	}

	s := &SpecimenService{repo: repo}
	// context.Background() carries no auth.User → anonymous (uuid.Nil).
	out, err := s.list(context.Background(), &listSpecimensInput{Scope: "mine"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Body.Items) != 0 {
		t.Errorf("anonymous scope=mine returned %d items, want 0", len(out.Body.Items))
	}
	if repo.listCalls != 0 {
		t.Errorf("anonymous scope=mine consulted the repo %d times, want 0", repo.listCalls)
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

// listSpecimenNames GETs a specimens list URL, asserts 200, and
// returns the names of the returned items.
func listSpecimenNames(t *testing.T, h http.Handler, url string) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %q: status %d body=%s", url, rec.Code, rec.Body.String())
	}
	var lst specimenListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &lst); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	names := make([]string, 0, len(lst.Items))
	for _, it := range lst.Items {
		names = append(names, it.Name)
	}
	return names
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
