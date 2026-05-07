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
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// fakeCollectorRepo is an in-memory domain.CollectorRepo for handler
// tests. Goroutine-safe because httptest hands handlers a fresh
// goroutine per request.
type fakeCollectorRepo struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]domain.Collector
	createErr error
	updateErr error
	deleteErr error
	listErr   error
}

func newFakeCollectorRepo() *fakeCollectorRepo {
	return &fakeCollectorRepo{rows: map[uuid.UUID]domain.Collector{}}
}

func (f *fakeCollectorRepo) Create(_ context.Context, _ domain.Tx, c domain.Collector) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	for _, existing := range f.rows {
		if existing.Name == c.Name {
			return domain.ErrCollectorConflict
		}
	}
	f.rows[c.ID] = c
	return nil
}

func (f *fakeCollectorRepo) GetByID(_ context.Context, id uuid.UUID) (domain.Collector, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.rows[id]
	if !ok {
		return domain.Collector{}, domain.ErrCollectorNotFound
	}
	return c, nil
}

func (f *fakeCollectorRepo) Update(_ context.Context, _ domain.Tx, c domain.Collector) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	if _, ok := f.rows[c.ID]; !ok {
		return domain.ErrCollectorNotFound
	}
	for id, existing := range f.rows {
		if id != c.ID && existing.Name == c.Name {
			return domain.ErrCollectorConflict
		}
	}
	f.rows[c.ID] = c
	return nil
}

func (f *fakeCollectorRepo) Delete(_ context.Context, _ domain.Tx, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.rows[id]; !ok {
		return domain.ErrCollectorNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeCollectorRepo) List(_ context.Context, filter domain.CollectorFilter, _ domain.Page) ([]domain.Collector, domain.Cursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, "", f.listErr
	}
	out := []domain.Collector{}
	q := strings.ToLower(strings.TrimSpace(filter.Query))
	for _, c := range f.rows {
		if q != "" && !strings.Contains(strings.ToLower(c.Name), q) {
			continue
		}
		out = append(out, c)
	}
	return out, "", nil
}

// envelopeBody mirrors the §10 envelope for decoding error responses.
type envelopeBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func newServerWithCollectors(t *testing.T, repo domain.CollectorRepo) http.Handler {
	t.Helper()
	return New(Deps{Collectors: repo})
}

func TestCollectorsCreateAndGet(t *testing.T) {
	repo := newFakeCollectorRepo()
	h := newServerWithCollectors(t, repo)

	body, _ := json.Marshal(map[string]any{"name": "Alice", "notes": "test"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/collectors", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" || !strings.HasPrefix(loc, "/api/v1/collectors/") {
		t.Errorf("Location header = %q", loc)
	}
	var created CollectorView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create body: %v", err)
	}
	if created.Name != "Alice" {
		t.Errorf("name = %q", created.Name)
	}
	if created.ID == uuid.Nil {
		t.Errorf("missing id")
	}
	if created.AuthorID == uuid.Nil {
		t.Errorf("author_id not populated; auth middleware not running")
	}

	// GET roundtrip.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/collectors/"+created.ID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec2.Code, rec2.Body.String())
	}
	var got CollectorView
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.ID != created.ID || got.Name != created.Name {
		t.Errorf("get mismatch: %+v vs %+v", got, created)
	}
}

func TestCollectorsCreateConflict(t *testing.T) {
	repo := newFakeCollectorRepo()
	h := newServerWithCollectors(t, repo)

	body := []byte(`{"name":"dup"}`)
	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/collectors", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := post(); rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d", rec.Code)
	}
	rec := post()
	if rec.Code != http.StatusConflict {
		t.Fatalf("second create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "collector_conflict" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestCollectorsGetNotFound(t *testing.T) {
	h := newServerWithCollectors(t, newFakeCollectorRepo())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors/"+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "collector_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestCollectorsGetInvalidID(t *testing.T) {
	h := newServerWithCollectors(t, newFakeCollectorRepo())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors/not-a-uuid", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "invalid_id" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestCollectorsPatch(t *testing.T) {
	repo := newFakeCollectorRepo()
	h := newServerWithCollectors(t, repo)

	id := uuid.New()
	original := domain.Collector{
		ID:        id,
		Name:      "before",
		CreatedAt: time.Now().UTC().Add(-time.Hour),
		UpdatedAt: time.Now().UTC().Add(-time.Hour),
	}
	repo.rows[id] = original

	patch, _ := json.Marshal(map[string]any{"name": "after"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/collectors/"+id.String(), bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got CollectorView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "after" {
		t.Errorf("name = %q", got.Name)
	}
	if !got.UpdatedAt.After(original.UpdatedAt) {
		t.Errorf("updated_at not bumped: %v vs %v", got.UpdatedAt, original.UpdatedAt)
	}
	if repo.rows[id].Name != "after" {
		t.Errorf("repo not updated")
	}
}

func TestCollectorsPatchOmittedFieldsPreserve(t *testing.T) {
	repo := newFakeCollectorRepo()
	h := newServerWithCollectors(t, repo)

	id := uuid.New()
	notes := "keep me"
	repo.rows[id] = domain.Collector{
		ID:        id,
		Name:      "stable",
		Notes:     &notes,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	// Empty body — both fields omitted.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/collectors/"+id.String(), bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.rows[id].Name != "stable" {
		t.Errorf("name overwritten: %q", repo.rows[id].Name)
	}
	if repo.rows[id].Notes == nil || *repo.rows[id].Notes != "keep me" {
		t.Errorf("notes overwritten: %v", repo.rows[id].Notes)
	}
}

func TestCollectorsDeleteSuccess(t *testing.T) {
	repo := newFakeCollectorRepo()
	id := uuid.New()
	repo.rows[id] = domain.Collector{ID: id, Name: "doomed"}
	h := newServerWithCollectors(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/collectors/"+id.String(), nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, exists := repo.rows[id]; exists {
		t.Errorf("row still present")
	}
}

func TestCollectorsDeleteReferencedReturns409(t *testing.T) {
	repo := newFakeCollectorRepo()
	id := uuid.New()
	repo.rows[id] = domain.Collector{ID: id, Name: "linked"}
	repo.deleteErr = domain.ErrCollectorReferenced
	h := newServerWithCollectors(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/collectors/"+id.String(), nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "collector_referenced" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestCollectorsList(t *testing.T) {
	repo := newFakeCollectorRepo()
	for _, n := range []string{"Apple", "Banana", "Apricot"} {
		id := uuid.New()
		repo.rows[id] = domain.Collector{ID: id, Name: n,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	}
	h := newServerWithCollectors(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors?q=ap", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body collectorListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Errorf("items = %d, want 2 (Apple, Apricot); got %v", len(body.Items), body.Items)
	}
	for _, it := range body.Items {
		lower := strings.ToLower(it.Name)
		if !strings.Contains(lower, "ap") {
			t.Errorf("item %q does not match q=ap", it.Name)
		}
	}
}

func TestCollectorsOpenAPISpecAdvertisesRoutes(t *testing.T) {
	h := newServerWithCollectors(t, newFakeCollectorRepo())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var spec struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantPaths := []string{
		"/api/v1/collectors",
		"/api/v1/collectors/{id}",
	}
	for _, p := range wantPaths {
		if _, ok := spec.Paths[p]; !ok {
			t.Errorf("spec missing path %q (have %v)", p, keysOf(spec.Paths))
		}
	}
}

func TestCollectorsRoutesAreNotRegisteredWithoutRepo(t *testing.T) {
	// Confirms that when no repo is wired, requests to /api/v1/collectors
	// fall through to the catch-all 404 envelope (so the server.go test
	// `TestApiV1NotFoundReturnsEnvelope` still describes accurate behavior
	// for unrouted paths).
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
