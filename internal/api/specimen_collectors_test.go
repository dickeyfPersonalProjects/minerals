package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// fakeChainRepo is an in-memory domain.SpecimenCollectorRepo that
// covers the bits the HTTP layer touches. The repo has its own
// integration tests for SQL paths.
type fakeChainRepo struct {
	mu        sync.Mutex
	chains    map[uuid.UUID][]uuid.UUID
	specimens map[uuid.UUID]struct{}
	collrepo  *fakeCollectorRepo
}

func newFakeChainRepo(specs *fakeSpecimenRepo, colls *fakeCollectorRepo) *fakeChainRepo {
	f := &fakeChainRepo{
		chains:    map[uuid.UUID][]uuid.UUID{},
		specimens: map[uuid.UUID]struct{}{},
		collrepo:  colls,
	}
	specs.mu.Lock()
	for id := range specs.rows {
		f.specimens[id] = struct{}{}
	}
	specs.mu.Unlock()
	return f
}

// trackSpecimen registers id so future ReplaceChain calls won't
// 404. Tests that create specimens via the HTTP layer call this
// with the returned id.
func (f *fakeChainRepo) trackSpecimen(id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specimens[id] = struct{}{}
}

func (f *fakeChainRepo) GetChain(_ context.Context, _ domain.Tx, specimenID uuid.UUID) ([]domain.SpecimenCollectorLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := f.chains[specimenID]
	out := make([]domain.SpecimenCollectorLink, 0, len(ids))
	for i, cid := range ids {
		c, err := f.collrepo.GetByID(context.Background(), cid)
		if err != nil {
			continue
		}
		out = append(out, domain.SpecimenCollectorLink{Collector: c, Position: i + 1})
	}
	return out, nil
}

func (f *fakeChainRepo) ReplaceChain(_ context.Context, _ domain.Tx, specimenID uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.specimens[specimenID]; !ok {
		return domain.ErrSpecimenNotFound
	}
	for _, cid := range ids {
		if _, err := f.collrepo.GetByID(context.Background(), cid); err != nil {
			return domain.ErrCollectorNotFound
		}
	}
	f.chains[specimenID] = append([]uuid.UUID(nil), ids...)
	return nil
}

func newServerWithChain(t *testing.T, specs *fakeSpecimenRepo, colls *fakeCollectorRepo) (http.Handler, *fakeChainRepo) {
	t.Helper()
	chain := newFakeChainRepo(specs, colls)
	h := New(Deps{
		Specimens:          specs,
		Collectors:         colls,
		SpecimenCollectors: chain,
	})
	return h, chain
}

// createSpecimenForTest does a POST through the handler so author_id /
// id are populated the same way they are in production. Returns the
// new specimen's id.
func createSpecimenForTest(t *testing.T, h http.Handler) uuid.UUID {
	t.Helper()
	body := map[string]any{"type": "mineral", "name": "test-spec"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create specimen: %d %s", rec.Code, rec.Body.String())
	}
	var out SpecimenView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode created specimen: %v", err)
	}
	return out.ID
}

// createCollectorForTest does a POST through the handler.
func createCollectorForTest(t *testing.T, h http.Handler, name string) uuid.UUID {
	t.Helper()
	body := map[string]any{"name": name}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/collectors", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create collector %q: %d %s", name, rec.Code, rec.Body.String())
	}
	var out CollectorView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode created collector: %v", err)
	}
	return out.ID
}

func decodeChainBody(t *testing.T, raw []byte) specimenCollectorsBody {
	t.Helper()
	var body specimenCollectorsBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode chain body: %v: %s", err, string(raw))
	}
	return body
}

func TestChain_GetEmptyForFreshSpecimen(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, chain := newServerWithChain(t, specs, colls)
	specID := createSpecimenForTest(t, h)
	chain.trackSpecimen(specID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens/"+specID.String()+"/collectors", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get chain: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeChainBody(t, rec.Body.Bytes())
	if len(body.Items) != 0 {
		t.Errorf("expected empty chain, got %d items", len(body.Items))
	}
}

func TestChain_GetReturns404ForUnknownSpecimen(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, _ := newServerWithChain(t, specs, colls)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens/"+uuid.New().String()+"/collectors", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestChain_PutSetsAndReplaces(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, chain := newServerWithChain(t, specs, colls)
	specID := createSpecimenForTest(t, h)
	chain.trackSpecimen(specID)

	c1 := createCollectorForTest(t, h, "alice")
	c2 := createCollectorForTest(t, h, "bob")
	c3 := createCollectorForTest(t, h, "carol")

	body, _ := json.Marshal(map[string]any{
		"collector_ids": []string{c1.String(), c2.String(), c3.String()},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+specID.String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put chain: %d %s", rec.Code, rec.Body.String())
	}
	got := decodeChainBody(t, rec.Body.Bytes())
	if len(got.Items) != 3 {
		t.Fatalf("len got=%d want=3", len(got.Items))
	}
	wantOrder := []uuid.UUID{c1, c2, c3}
	for i, link := range got.Items {
		if link.Collector.ID != wantOrder[i] {
			t.Errorf("position %d: got %v want %v", i+1, link.Collector.ID, wantOrder[i])
		}
		if link.Position != i+1 {
			t.Errorf("position field [%d]: got %d want %d", i, link.Position, i+1)
		}
	}

	// Replace with reverse order.
	body, _ = json.Marshal(map[string]any{
		"collector_ids": []string{c3.String(), c1.String()},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+specID.String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body.String())
	}
	got = decodeChainBody(t, rec.Body.Bytes())
	if len(got.Items) != 2 || got.Items[0].Collector.ID != c3 || got.Items[1].Collector.ID != c1 {
		t.Errorf("replace shape wrong: %+v", got.Items)
	}
}

func TestChain_PutEmptyClears(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, chain := newServerWithChain(t, specs, colls)
	specID := createSpecimenForTest(t, h)
	chain.trackSpecimen(specID)
	c1 := createCollectorForTest(t, h, "alone")

	// Seed.
	body, _ := json.Marshal(map[string]any{"collector_ids": []string{c1.String()}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+specID.String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed: %d", rec.Code)
	}

	body, _ = json.Marshal(map[string]any{"collector_ids": []string{}})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+specID.String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body.String())
	}
	got := decodeChainBody(t, rec.Body.Bytes())
	if len(got.Items) != 0 {
		t.Errorf("clear left %d items", len(got.Items))
	}
}

func TestChain_PutRejectsDuplicateIDs(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, chain := newServerWithChain(t, specs, colls)
	specID := createSpecimenForTest(t, h)
	chain.trackSpecimen(specID)
	c1 := createCollectorForTest(t, h, "dup")

	body, _ := json.Marshal(map[string]any{
		"collector_ids": []string{c1.String(), c1.String()},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+specID.String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "duplicate_collector_id" {
		t.Errorf("code = %q, want duplicate_collector_id", env.Error.Code)
	}
}

func TestChain_PutRejectsMissingCollector(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, chain := newServerWithChain(t, specs, colls)
	specID := createSpecimenForTest(t, h)
	chain.trackSpecimen(specID)

	body, _ := json.Marshal(map[string]any{
		"collector_ids": []string{uuid.New().String()},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+specID.String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "collector_not_found" {
		t.Errorf("code = %q, want collector_not_found", env.Error.Code)
	}
}

func TestChain_PutRejectsUnknownSpecimen(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, _ := newServerWithChain(t, specs, colls)

	body, _ := json.Marshal(map[string]any{"collector_ids": []string{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+uuid.New().String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestChain_PutRejectsBadIDFormat(t *testing.T) {
	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	h, chain := newServerWithChain(t, specs, colls)
	specID := createSpecimenForTest(t, h)
	chain.trackSpecimen(specID)

	body, _ := json.Marshal(map[string]any{"collector_ids": []string{"not-a-uuid"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/specimens/"+specID.String()+"/collectors", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
}
