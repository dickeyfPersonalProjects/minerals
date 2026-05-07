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

// fakeJournalRepo is an in-memory domain.JournalEntryRepo for handler
// tests.
type fakeJournalRepo struct {
	mu          sync.Mutex
	rows        map[uuid.UUID]domain.JournalEntry
	hasFilesFor map[uuid.UUID]bool // entry ids that should 409 on delete
}

func newFakeJournalRepo() *fakeJournalRepo {
	return &fakeJournalRepo{
		rows:        map[uuid.UUID]domain.JournalEntry{},
		hasFilesFor: map[uuid.UUID]bool{},
	}
}

func (f *fakeJournalRepo) Create(_ context.Context, _ domain.Tx, e domain.JournalEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[e.ID] = e
	return nil
}

func (f *fakeJournalRepo) GetByID(_ context.Context, id uuid.UUID) (domain.JournalEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.rows[id]
	if !ok {
		return domain.JournalEntry{}, domain.ErrJournalEntryNotFound
	}
	return e, nil
}

func (f *fakeJournalRepo) Update(_ context.Context, _ domain.Tx, e domain.JournalEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.rows[e.ID]
	if !ok {
		return domain.ErrJournalEntryNotFound
	}
	// Mirror the postgres repo: created_at is immutable at the SQL
	// layer, so the in-memory fake preserves the stored value too.
	cur.BodyMD = e.BodyMD
	cur.UpdatedAt = e.UpdatedAt
	f.rows[e.ID] = cur
	return nil
}

func (f *fakeJournalRepo) Delete(_ context.Context, _ domain.Tx, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return domain.ErrJournalEntryNotFound
	}
	if f.hasFilesFor[id] {
		return domain.ErrJournalEntryConflict
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeJournalRepo) ListBySpecimen(_ context.Context, specimenID uuid.UUID, page domain.Page) ([]domain.JournalEntry, domain.Cursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []domain.JournalEntry{}
	for _, e := range f.rows {
		if e.SpecimenID == specimenID {
			out = append(out, e)
		}
	}
	return out, "", nil
}

func newServerWithJournal(t *testing.T, repo domain.JournalEntryRepo) http.Handler {
	t.Helper()
	return New(Deps{Journal: &JournalServiceDeps{Entries: repo}})
}

func TestJournalCreateAndGet(t *testing.T) {
	repo := newFakeJournalRepo()
	h := newServerWithJournal(t, repo)

	specID := uuid.New()
	body, _ := json.Marshal(map[string]any{"body_md": "# heading\n\nSome **bold** text."})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens/"+specID.String()+"/journal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/journal/") {
		t.Errorf("Location header = %q", loc)
	}
	var created JournalView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.SpecimenID != specID {
		t.Errorf("specimen_id = %s, want %s", created.SpecimenID, specID)
	}
	if created.AuthorID == uuid.Nil {
		t.Errorf("author_id not populated; auth middleware not running")
	}
	if !strings.Contains(created.BodyHTML, "<h1>heading</h1>") {
		t.Errorf("body_html missing rendered heading: %q", created.BodyHTML)
	}
	if !strings.Contains(created.BodyHTML, "<strong>bold</strong>") {
		t.Errorf("body_html missing bold: %q", created.BodyHTML)
	}

	// GET roundtrip.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/journal/"+created.ID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rec2.Code, rec2.Body.String())
	}
	var got JournalView
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != created.ID || got.BodyMD != created.BodyMD {
		t.Errorf("roundtrip mismatch")
	}
	if got.BodyHTML != created.BodyHTML {
		t.Errorf("body_html drift: %q vs %q", got.BodyHTML, created.BodyHTML)
	}
}

func TestJournalCreateRequiresBodyMD(t *testing.T) {
	repo := newFakeJournalRepo()
	h := newServerWithJournal(t, repo)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens/"+uuid.New().String()+"/journal", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestJournalGetNotFound(t *testing.T) {
	h := newServerWithJournal(t, newFakeJournalRepo())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/journal/"+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "journal_entry_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestJournalPatchPreservesCreatedAt(t *testing.T) {
	repo := newFakeJournalRepo()
	h := newServerWithJournal(t, repo)

	id := uuid.New()
	originalCreated := time.Now().UTC().Add(-2 * time.Hour)
	repo.rows[id] = domain.JournalEntry{
		ID: id, SpecimenID: uuid.New(), BodyMD: "v1",
		CreatedAt: originalCreated, UpdatedAt: originalCreated,
	}

	patch, _ := json.Marshal(map[string]any{"body_md": "v2 *italic*"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/journal/"+id.String(), bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got JournalView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.CreatedAt.Equal(originalCreated) {
		t.Errorf("created_at mutated: was %v now %v", originalCreated, got.CreatedAt)
	}
	if got.BodyMD != "v2 *italic*" {
		t.Errorf("body_md = %q", got.BodyMD)
	}
	if !strings.Contains(got.BodyHTML, "<em>italic</em>") {
		t.Errorf("body_html missing em: %q", got.BodyHTML)
	}
	if !got.UpdatedAt.After(originalCreated) {
		t.Errorf("updated_at not bumped")
	}
}

func TestJournalPatchRejectsCreatedAt(t *testing.T) {
	repo := newFakeJournalRepo()
	h := newServerWithJournal(t, repo)

	id := uuid.New()
	originalCreated := time.Now().UTC().Add(-2 * time.Hour)
	repo.rows[id] = domain.JournalEntry{
		ID: id, BodyMD: "v1",
		CreatedAt: originalCreated, UpdatedAt: originalCreated,
	}

	// Even sending the existing value must 400 — the immutability
	// rule is "don't accept created_at in PATCH at all".
	patch, _ := json.Marshal(map[string]any{
		"body_md":    "v2",
		"created_at": originalCreated.Format(time.RFC3339Nano),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/journal/"+id.String(), bytes.NewReader(patch))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "created_at_immutable" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
	if repo.rows[id].BodyMD != "v1" {
		t.Errorf("body_md was mutated despite 400: %q", repo.rows[id].BodyMD)
	}
}

func TestJournalDelete409WhenAttachmentsExist(t *testing.T) {
	repo := newFakeJournalRepo()
	h := newServerWithJournal(t, repo)

	id := uuid.New()
	repo.rows[id] = domain.JournalEntry{ID: id, BodyMD: "x"}
	repo.hasFilesFor[id] = true

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/journal/"+id.String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "journal_entry_referenced" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestJournalDeleteSuccess(t *testing.T) {
	repo := newFakeJournalRepo()
	h := newServerWithJournal(t, repo)

	id := uuid.New()
	repo.rows[id] = domain.JournalEntry{ID: id, BodyMD: "x"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/journal/"+id.String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, exists := repo.rows[id]; exists {
		t.Errorf("row still present after delete")
	}
}

func TestJournalListReturnsRendered(t *testing.T) {
	repo := newFakeJournalRepo()
	h := newServerWithJournal(t, repo)

	specID := uuid.New()
	now := time.Now().UTC()
	for _, body := range []string{"first **e1**", "second"} {
		repo.rows[uuid.New()] = domain.JournalEntry{
			ID: uuid.New(), SpecimenID: specID, BodyMD: body,
			CreatedAt: now, UpdatedAt: now,
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens/"+specID.String()+"/journal", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp journalListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.BodyHTML == "" {
			t.Errorf("item missing body_html: %+v", item)
		}
	}
}
