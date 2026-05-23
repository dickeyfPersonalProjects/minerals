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
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// stubMarkdownRenderer is a no-op MarkdownRenderer for service-level
// tests that construct a JournalService directly (bypassing
// registerJournalOperations, which otherwise defaults Markdown).
type stubMarkdownRenderer struct{}

func (stubMarkdownRenderer) RenderString(src string) (string, error) { return src, nil }

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

func (f *fakeJournalRepo) ListBySpecimen(_ context.Context, specimenID uuid.UUID, _ domain.Page) ([]domain.JournalEntry, domain.Cursor, error) {
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

// newAuthzJournalService builds a JournalService with a live Casbin
// enforcer (seeded with the §13 v2 default policies) and a real
// SpecimenRepo, so per-resource authorization actually runs — the
// unit-test path used elsewhere wires neither and is a no-op. Used by
// the parent-specimen authorization regression tests below.
func newAuthzJournalService(t *testing.T, specimens domain.SpecimenRepo, entries domain.JournalEntryRepo) *JournalService {
	t.Helper()
	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("authz.NewEnforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	return &JournalService{
		deps:      JournalServiceDeps{Entries: entries, Markdown: stubMarkdownRenderer{}},
		specimens: specimens,
		authz:     authzGuard{enforcer: enf},
	}
}

// TestJournalCreateAuthorizesParentSpecimen is the regression guard for
// mi-f5sm: create() must authorize the parent specimen, not merely the
// caller's own journal-collection grant. Before the fix a user could
// attach a journal entry to any specimen they could name — the FK was
// the only protection (IDOR-adjacent). The matrix proves a non-owner is
// denied (private and public alike, since a public specimen is still
// not editable by a stranger) while the owner still succeeds.
func TestJournalCreateAuthorizesParentSpecimen(t *testing.T) {
	owner := uuid.New()    // user B — owns the specimen
	attacker := uuid.New() // user A — names B's specimen in the path

	cases := []struct {
		name       string
		visibility domain.Visibility
	}{
		{"private specimen", domain.VisibilityPrivate},
		{"public specimen", domain.VisibilityPublic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specRepo := newFakeSpecimenRepo()
			specID := uuid.New()
			specRepo.rows[specID] = domain.Specimen{
				ID: specID, AuthorID: owner, Visibility: tc.visibility,
			}
			journalRepo := newFakeJournalRepo()
			s := newAuthzJournalService(t, specRepo, journalRepo)

			in := &createJournalInput{
				SpecimenID: specID.String(),
				Body:       createJournalBody{BodyMD: "unauthorized entry"},
			}

			// Attacker (a valid `user`, but not the owner) is denied.
			attackerCtx := auth.WithUser(context.Background(),
				auth.User{ID: attacker, Roles: []string{"user"}})
			_, err := s.create(attackerCtx, in)
			var ae *apiError
			if !errors.As(err, &ae) {
				t.Fatalf("expected *apiError, got %T (%v)", err, err)
			}
			if ae.Status != http.StatusForbidden {
				t.Errorf("attacker create status = %d, want 403", ae.Status)
			}
			if len(journalRepo.rows) != 0 {
				t.Errorf("entry persisted despite denial: %d rows", len(journalRepo.rows))
			}

			// Owner of the specimen still succeeds.
			ownerCtx := auth.WithUser(context.Background(),
				auth.User{ID: owner, Roles: []string{"user"}})
			out, err := s.create(ownerCtx, in)
			if err != nil {
				t.Fatalf("owner create failed: %v", err)
			}
			if out.Body.SpecimenID != specID {
				t.Errorf("created specimen_id = %s, want %s", out.Body.SpecimenID, specID)
			}
			if out.Body.AuthorID != owner {
				t.Errorf("created author_id = %s, want owner %s", out.Body.AuthorID, owner)
			}
			if len(journalRepo.rows) != 1 {
				t.Errorf("owner create persisted %d rows, want 1", len(journalRepo.rows))
			}
		})
	}
}

// TestJournalCreateParentSpecimenMissing pins the 404 path: when the
// named parent specimen does not exist, create() surfaces the §10
// specimen_not_found envelope (from the authorization preflight) rather
// than failing later on the FK insert.
func TestJournalCreateParentSpecimenMissing(t *testing.T) {
	specRepo := newFakeSpecimenRepo() // empty — no such specimen
	journalRepo := newFakeJournalRepo()
	s := newAuthzJournalService(t, specRepo, journalRepo)

	in := &createJournalInput{
		SpecimenID: uuid.New().String(),
		Body:       createJournalBody{BodyMD: "orphan entry"},
	}
	ctx := auth.WithUser(context.Background(),
		auth.User{ID: uuid.New(), Roles: []string{"user"}})
	_, err := s.create(ctx, in)
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apiError, got %T (%v)", err, err)
	}
	if ae.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", ae.Status)
	}
	if ae.Envelope.Code != "specimen_not_found" {
		t.Errorf("code = %q, want specimen_not_found", ae.Envelope.Code)
	}
	if len(journalRepo.rows) != 0 {
		t.Errorf("entry persisted despite missing parent: %d rows", len(journalRepo.rows))
	}
}
