package api

import (
	"bytes"
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
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

// fakeQRSheetRepo is an in-memory domain.QRSheetRepo for handler
// tests. Mirrors the pattern in collectors_test.go: a single struct
// is goroutine-safe via mu and exposes injectable error hooks for the
// negative paths the SQL repo's integration tests don't cover at the
// HTTP layer.
type fakeQRSheetRepo struct {
	mu        sync.Mutex
	sheets    map[uuid.UUID]domain.QRSheet        // by user_id
	entries   map[uuid.UUID][]domain.QRSheetEntry // by sheet_id, position-ascending
	specimens map[uuid.UUID]struct{}              // known specimens (for AddSpecimen's ErrSpecimenNotFound check)
	getErr    error
	createErr error
	updateErr error
	deleteErr error
	addErr    error
	removeErr error
	listErr   error
}

func newFakeQRSheetRepo() *fakeQRSheetRepo {
	return &fakeQRSheetRepo{
		sheets:    map[uuid.UUID]domain.QRSheet{},
		entries:   map[uuid.UUID][]domain.QRSheetEntry{},
		specimens: map[uuid.UUID]struct{}{},
	}
}

func (f *fakeQRSheetRepo) GetByUser(_ context.Context, userID uuid.UUID) (domain.QRSheet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return domain.QRSheet{}, f.getErr
	}
	s, ok := f.sheets[userID]
	if !ok {
		return domain.QRSheet{}, domain.ErrQRSheetNotFound
	}
	return s, nil
}

func (f *fakeQRSheetRepo) Create(_ context.Context, _ domain.Tx, s domain.QRSheet) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	if _, ok := f.sheets[s.UserID]; ok {
		return domain.ErrQRSheetConflict
	}
	f.sheets[s.UserID] = s
	return nil
}

func (f *fakeQRSheetRepo) UpdateTemplate(_ context.Context, _ domain.Tx,
	userID uuid.UUID, template domain.QRSheetTemplate, updatedAt time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	s, ok := f.sheets[userID]
	if !ok {
		return domain.ErrQRSheetNotFound
	}
	s.Template = template
	s.UpdatedAt = updatedAt
	f.sheets[userID] = s
	return nil
}

func (f *fakeQRSheetRepo) Delete(_ context.Context, _ domain.Tx, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	s, ok := f.sheets[userID]
	if !ok {
		return domain.ErrQRSheetNotFound
	}
	delete(f.sheets, userID)
	delete(f.entries, s.ID)
	return nil
}

func (f *fakeQRSheetRepo) AddSpecimen(_ context.Context, _ domain.Tx,
	userID, specimenID uuid.UUID, addedAt time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return f.addErr
	}
	sheet, ok := f.sheets[userID]
	if !ok {
		return domain.ErrQRSheetNotFound
	}
	if _, ok := f.specimens[specimenID]; !ok {
		return domain.ErrSpecimenNotFound
	}
	// Idempotent: re-adding leaves position unchanged.
	for _, e := range f.entries[sheet.ID] {
		if e.SpecimenID == specimenID {
			return nil
		}
	}
	f.entries[sheet.ID] = append(f.entries[sheet.ID], domain.QRSheetEntry{
		SpecimenID:   specimenID,
		SpecimenName: "spec-" + specimenID.String()[:8],
		Position:     len(f.entries[sheet.ID]) + 1,
		AddedAt:      addedAt,
	})
	return nil
}

func (f *fakeQRSheetRepo) RemoveSpecimen(_ context.Context, _ domain.Tx,
	userID, specimenID uuid.UUID,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.removeErr != nil {
		return f.removeErr
	}
	sheet, ok := f.sheets[userID]
	if !ok {
		return domain.ErrQRSheetNotFound
	}
	rows := f.entries[sheet.ID]
	idx := -1
	for i, e := range rows {
		if e.SpecimenID == specimenID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return domain.ErrQRSheetSpecimenNotFound
	}
	rows = append(rows[:idx], rows[idx+1:]...)
	for i := range rows {
		rows[i].Position = i + 1
	}
	f.entries[sheet.ID] = rows
	return nil
}

func (f *fakeQRSheetRepo) ListSpecimens(_ context.Context, sheetID uuid.UUID) ([]domain.QRSheetEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]domain.QRSheetEntry, len(f.entries[sheetID]))
	copy(out, f.entries[sheetID])
	return out, nil
}

// seedSheet creates an active sheet for StubUser and registers the
// supplied specimens (so AddSpecimen accepts them).
func (f *fakeQRSheetRepo) seedSheet(template domain.QRSheetTemplate, specimens ...uuid.UUID) domain.QRSheet {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	sheet := domain.QRSheet{
		ID:        uuid.New(),
		UserID:    uuid.MustParse("00000000-0000-0000-0000-000000000001"), // StubUser.ID
		Template:  template,
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.sheets[sheet.UserID] = sheet
	for _, id := range specimens {
		f.specimens[id] = struct{}{}
	}
	return sheet
}

func (f *fakeQRSheetRepo) registerSpecimens(ids ...uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		f.specimens[id] = struct{}{}
	}
}

func (f *fakeQRSheetRepo) appendEntry(sheetID, specimenID uuid.UUID, name string, photoID *uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[sheetID] = append(f.entries[sheetID], domain.QRSheetEntry{
		SpecimenID:   specimenID,
		SpecimenName: name,
		Position:     len(f.entries[sheetID]) + 1,
		AddedAt:      time.Now().UTC(),
		FirstPhotoID: photoID,
	})
}

func newServerWithQRSheets(t *testing.T, repo domain.QRSheetRepo) http.Handler {
	t.Helper()
	return New(Deps{QRSheets: repo})
}

// qrSheetBody mirrors the §10 QRSheetView shape for decoding response
// bodies in tests.
type qrSheetBody struct {
	ID        string `json:"id"`
	Template  string `json:"template"`
	PageCount int    `json:"page_count"`
	Specimens []struct {
		SpecimenID   string  `json:"specimen_id"`
		Name         string  `json:"name"`
		Position     int     `json:"position"`
		ThumbnailURL *string `json:"thumbnail_url"`
	} `json:"specimens"`
}

func TestQRSheetGet_HappyPathIncludesPageCountAndThumbnail(t *testing.T) {
	repo := newFakeQRSheetRepo()
	sheet := repo.seedSheet("avery-5164") // capacity 6 → 7 specimens = 2 pages
	specID := uuid.New()
	photoID := uuid.New()
	repo.appendEntry(sheet.ID, specID, "calcite", &photoID)
	for i := 0; i < 6; i++ {
		repo.appendEntry(sheet.ID, uuid.New(), "filler", nil)
	}
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/qr-sheet", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got qrSheetBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Template != "avery-5164" {
		t.Errorf("template = %q", got.Template)
	}
	if got.PageCount != 2 {
		t.Errorf("page_count = %d want 2 (7 specimens / 6 per page)", got.PageCount)
	}
	if len(got.Specimens) != 7 {
		t.Fatalf("specimens = %d want 7", len(got.Specimens))
	}
	want := "/api/v1/photos/" + photoID.String() + "/thumb"
	if got.Specimens[0].ThumbnailURL == nil || *got.Specimens[0].ThumbnailURL != want {
		t.Errorf("thumbnail_url = %v want %q", got.Specimens[0].ThumbnailURL, want)
	}
	if got.Specimens[1].ThumbnailURL != nil {
		t.Errorf("entry without photo should have nil thumbnail; got %q", *got.Specimens[1].ThumbnailURL)
	}
}

func TestQRSheetGet_ReturnsEmptySheetWithZeroPageCount(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/qr-sheet", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got qrSheetBody
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.PageCount != 0 {
		t.Errorf("page_count = %d want 0", got.PageCount)
	}
	if len(got.Specimens) != 0 {
		t.Errorf("specimens = %d want 0", len(got.Specimens))
	}
}

func TestQRSheetGet_NotFoundEnvelope(t *testing.T) {
	h := newServerWithQRSheets(t, newFakeQRSheetRepo())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/qr-sheet", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetGet_ListSpecimensError_Returns500(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	repo.listErr = errors.New("boom")
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/qr-sheet", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "internal_error" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetCreate_HappyPathReturns201AndLocation(t *testing.T) {
	repo := newFakeQRSheetRepo()
	h := newServerWithQRSheets(t, repo)

	body, _ := json.Marshal(map[string]any{"template": "avery-5160"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/qr-sheet" {
		t.Errorf("Location = %q want /api/v1/qr-sheet", loc)
	}
	var got qrSheetBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Template != "avery-5160" {
		t.Errorf("template = %q", got.Template)
	}
	if got.PageCount != 0 || len(got.Specimens) != 0 {
		t.Errorf("expected empty sheet, got pages=%d specs=%d", got.PageCount, len(got.Specimens))
	}
}

func TestQRSheetCreate_InvalidTemplate_Returns400(t *testing.T) {
	h := newServerWithQRSheets(t, newFakeQRSheetRepo())

	body, _ := json.Marshal(map[string]any{"template": "totally-bogus"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "invalid_template" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetCreate_Conflict_Returns409(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	body, _ := json.Marshal(map[string]any{"template": "avery-5163"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_conflict" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetPatch_HappyPathSwitchesTemplate(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	body, _ := json.Marshal(map[string]any{"template": "avery-l7160"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/qr-sheet", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got qrSheetBody
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Template != "avery-l7160" {
		t.Errorf("template = %q want avery-l7160", got.Template)
	}
}

func TestQRSheetPatch_InvalidTemplate_Returns400(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	body, _ := json.Marshal(map[string]any{"template": "nope"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/qr-sheet", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "invalid_template" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetPatch_NoSheet_Returns404(t *testing.T) {
	h := newServerWithQRSheets(t, newFakeQRSheetRepo())

	body, _ := json.Marshal(map[string]any{"template": "avery-5160"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/qr-sheet", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetDelete_HappyPathReturns204(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/qr-sheet", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := repo.sheets[uuid.MustParse("00000000-0000-0000-0000-000000000001")]; ok {
		t.Errorf("sheet still present in repo after delete")
	}
}

func TestQRSheetDelete_NoSheet_Returns404(t *testing.T) {
	h := newServerWithQRSheets(t, newFakeQRSheetRepo())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/qr-sheet", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetAddSpecimen_HappyPathAppends(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	spec := uuid.New()
	repo.registerSpecimens(spec)
	h := newServerWithQRSheets(t, repo)

	body, _ := json.Marshal(map[string]any{"specimen_id": spec.String()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/specimens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got qrSheetBody
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Specimens) != 1 || got.Specimens[0].SpecimenID != spec.String() {
		t.Errorf("specimen list = %+v", got.Specimens)
	}
	if got.Specimens[0].Position != 1 {
		t.Errorf("position = %d want 1", got.Specimens[0].Position)
	}
}

func TestQRSheetAddSpecimen_InvalidUUID_Returns400(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	body := []byte(`{"specimen_id":"not-a-uuid"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/specimens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "invalid_id" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetAddSpecimen_NoSheet_Returns404(t *testing.T) {
	h := newServerWithQRSheets(t, newFakeQRSheetRepo())

	body, _ := json.Marshal(map[string]any{"specimen_id": uuid.New().String()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/specimens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetAddSpecimen_SpecimenMissing_Returns404(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	body, _ := json.Marshal(map[string]any{"specimen_id": uuid.New().String()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/specimens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "specimen_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetRemoveSpecimen_HappyPathReturns204(t *testing.T) {
	repo := newFakeQRSheetRepo()
	sheet := repo.seedSheet("avery-5160")
	spec := uuid.New()
	repo.appendEntry(sheet.ID, spec, "to-remove", nil)
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/qr-sheet/specimens/"+spec.String(), nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.entries[sheet.ID]) != 0 {
		t.Errorf("expected entry removed, got %d", len(repo.entries[sheet.ID]))
	}
}

func TestQRSheetRemoveSpecimen_InvalidUUID_Returns400(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/qr-sheet/specimens/not-a-uuid", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "invalid_id" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetRemoveSpecimen_NotOnSheet_Returns404(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/qr-sheet/specimens/"+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_specimen_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetGeneratePDF_HappyPathReturnsPDFBytes(t *testing.T) {
	repo := newFakeQRSheetRepo()
	sheet := repo.seedSheet("avery-5160")
	for i := 0; i < 2; i++ {
		repo.appendEntry(sheet.ID, uuid.New(), fmt.Sprintf("s%d", i), nil)
	}
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/pdf", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q want application/pdf", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="qr-sheet.pdf"`) {
		t.Errorf("Content-Disposition = %q missing filename", cd)
	}
	if cl := rec.Header().Get("Content-Length"); cl == "" {
		t.Errorf("Content-Length not set")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		head := rec.Body.Bytes()
		if len(head) > 16 {
			head = head[:16]
		}
		t.Errorf("response is not a PDF (first bytes: %q)", string(head))
	}
}

func TestQRSheetGeneratePDF_NoSheet_Returns404(t *testing.T) {
	h := newServerWithQRSheets(t, newFakeQRSheetRepo())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/pdf", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_not_found" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetGeneratePDF_EmptySheet_Returns400(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/pdf", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_sheet_empty" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetGeneratePDF_ListSpecimensError_Returns500(t *testing.T) {
	repo := newFakeQRSheetRepo()
	repo.seedSheet("avery-5160")
	repo.listErr = errors.New("db down")
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/pdf", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "internal_error" {
		t.Errorf("error.code = %q", env.Error.Code)
	}
}

func TestQRSheetGeneratePDF_UnknownStoredTemplate_Returns500(t *testing.T) {
	// Hand-edited DB scenario: a template not in the v1 vocabulary
	// lands on the GET path. The API layer validates writes, so this
	// can only fire post-hoc — the handler must respond 500 rather
	// than panic.
	repo := newFakeQRSheetRepo()
	sheet := repo.seedSheet("totally-bogus")
	repo.appendEntry(sheet.ID, uuid.New(), "x", nil)
	h := newServerWithQRSheets(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/qr-sheet/pdf", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "qr_template_unknown" {
		t.Errorf("error.code = %q want qr_template_unknown", env.Error.Code)
	}
}

func TestRequestBaseURL(t *testing.T) {
	// requestBaseURL feeds the QR payload via qrSheetBaseURLMiddleware;
	// the scheme/host derivation is testable in isolation and avoids
	// scanning compressed PDF bytes for the URL.
	tlsState := &cryptotls.ConnectionState{}
	cases := []struct {
		name    string
		host    string
		tls     *cryptotls.ConnectionState
		headers map[string]string
		want    string
	}{
		{
			name: "plain http honours r.Host",
			host: "example.com",
			want: "http://example.com",
		},
		{
			name: "tls connection yields https",
			host: "example.com",
			tls:  tlsState,
			want: "https://example.com",
		},
		{
			name:    "x-forwarded-proto overrides scheme",
			host:    "example.com",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    "https://example.com",
		},
		{
			name:    "x-forwarded-host overrides host",
			host:    "internal.local",
			headers: map[string]string{"X-Forwarded-Host": "public.example.com"},
			want:    "http://public.example.com",
		},
		{
			name: "both forwarded headers stack",
			host: "internal.local",
			headers: map[string]string{
				"X-Forwarded-Proto": "https",
				"X-Forwarded-Host":  "public.example.com",
			},
			want: "https://public.example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Host = tc.host
			r.TLS = tc.tls
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := requestBaseURL(r); got != tc.want {
				t.Errorf("requestBaseURL = %q want %q", got, tc.want)
			}
		})
	}
}

func TestBaseURLFromContext_AbsentReturnsEmpty(t *testing.T) {
	// Defensive guard: handlers must not panic when the middleware
	// isn't wired (e.g. a test that calls into a service directly).
	if got := baseURLFromContext(context.Background()); got != "" {
		t.Errorf("baseURLFromContext on bare ctx = %q want \"\"", got)
	}
}

func TestQRSheetOpenAPISpecAdvertisesRoutes(t *testing.T) {
	h := newServerWithQRSheets(t, newFakeQRSheetRepo())
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
		"/api/v1/qr-sheet",
		"/api/v1/qr-sheet/specimens",
		"/api/v1/qr-sheet/specimens/{specimen_id}",
		"/api/v1/qr-sheet/pdf",
	}
	for _, p := range wantPaths {
		if _, ok := spec.Paths[p]; !ok {
			t.Errorf("spec missing path %q (have %v)", p, keysOf(spec.Paths))
		}
	}
}

func TestQRSheetRoutesAreNotRegisteredWithoutRepo(t *testing.T) {
	// With QRSheets nil, registerQRSheetOperations returns early so
	// the routes are absent and the /api/v1/* catch-all 404 envelope
	// handles requests.
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/qr-sheet", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

// newAuthzQRSheetService builds a QRSheetService with a live Casbin
// enforcer (seeded with the §13 v2 default policies) and a real
// SpecimenRepo, so the per-specimen authorization on the add path
// actually runs — the server-level handler tests above wire neither
// (Specimens is nil, the enforcer inactive) and the check is a no-op.
// Used by the add-specimen authorization regression tests below.
func newAuthzQRSheetService(t *testing.T, repo domain.QRSheetRepo, specimens domain.SpecimenRepo) *QRSheetService {
	t.Helper()
	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("authz.NewEnforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	return &QRSheetService{repo: repo, specimens: specimens, authz: authzGuard{enforcer: enf}}
}

// seedSheetFor seeds an empty sheet owned by userID (seedSheet hardcodes
// the StubUser id, but the authz tests need the sheet owner to be an
// arbitrary caller). Registers the supplied specimen ids so the repo's
// own ErrSpecimenNotFound guard passes for the success paths.
func (f *fakeQRSheetRepo) seedSheetFor(userID uuid.UUID, template domain.QRSheetTemplate, specimens ...uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	f.sheets[userID] = domain.QRSheet{
		ID: uuid.New(), UserID: userID, Template: template, CreatedAt: now, UpdatedAt: now,
	}
	for _, id := range specimens {
		f.specimens[id] = struct{}{}
	}
}

// TestQRSheetAddSpecimenAuthorizesSpecimen is the regression guard for
// mi-os89: addSpecimen() authorized the sheet (own) but appended an
// arbitrary specimen_id with no view check. Because loadView surfaces
// the specimen name + first-photo thumbnail, a caller could pull
// another user's *private* specimen name/thumbnail onto their own
// sheet by guessing its id. The fix view-checks the specimen first.
//
// The matrix proves the boundary: a stranger's private specimen is
// denied (rewritten to 404 specimen_not_found so existence never
// leaks), while a public specimen — whose name/thumbnail are not
// private — and the caller's own specimen both stay addable.
func TestQRSheetAddSpecimenAuthorizesSpecimen(t *testing.T) {
	caller := uuid.New()   // owns the sheet, doing the add
	stranger := uuid.New() // owns the specimen being added

	cases := []struct {
		name       string
		owner      uuid.UUID
		visibility domain.Visibility
		wantAdded  bool // true => append succeeds, false => 404 + nothing persisted
	}{
		{"stranger private specimen denied", stranger, domain.VisibilityPrivate, false},
		{"stranger public specimen allowed", stranger, domain.VisibilityPublic, true},
		{"own private specimen allowed", caller, domain.VisibilityPrivate, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeQRSheetRepo()
			specRepo := newFakeSpecimenRepo()
			specID := uuid.New()
			specRepo.rows[specID] = domain.Specimen{
				ID: specID, AuthorID: tc.owner, Visibility: tc.visibility,
			}
			repo.seedSheetFor(caller, "avery-5160", specID)
			s := newAuthzQRSheetService(t, repo, specRepo)

			in := &addQRSheetSpecimenInput{Body: addQRSheetSpecimenBody{SpecimenID: specID.String()}}
			ctx := auth.WithUser(context.Background(),
				auth.User{ID: caller, Roles: []string{"user"}})
			out, err := s.addSpecimen(ctx, in)

			if tc.wantAdded {
				if err != nil {
					t.Fatalf("add failed: %v", err)
				}
				if len(out.Body.Specimens) != 1 || out.Body.Specimens[0].SpecimenID != specID {
					t.Errorf("specimen list = %+v, want the added specimen", out.Body.Specimens)
				}
				return
			}

			var ae *apiError
			if !errors.As(err, &ae) {
				t.Fatalf("expected *apiError, got %T (%v)", err, err)
			}
			if ae.Status != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (existence must not leak)", ae.Status)
			}
			if ae.Envelope.Code != "specimen_not_found" {
				t.Errorf("code = %q, want specimen_not_found", ae.Envelope.Code)
			}
			if entries := repo.entries[repo.sheets[caller].ID]; len(entries) != 0 {
				t.Errorf("specimen persisted despite denial: %d entries", len(entries))
			}
		})
	}
}

// TestQRSheetAddSpecimenMissingUnderAuthz pins the 404 on the enforce
// path itself (active enforcer + real specimen repo): a named specimen
// that does not exist surfaces specimen_not_found from the
// authorization preflight, before the repo insert.
func TestQRSheetAddSpecimenMissingUnderAuthz(t *testing.T) {
	caller := uuid.New()
	repo := newFakeQRSheetRepo()
	repo.seedSheetFor(caller, "avery-5160")
	s := newAuthzQRSheetService(t, repo, newFakeSpecimenRepo())

	in := &addQRSheetSpecimenInput{Body: addQRSheetSpecimenBody{SpecimenID: uuid.New().String()}}
	ctx := auth.WithUser(context.Background(),
		auth.User{ID: caller, Roles: []string{"user"}})
	_, err := s.addSpecimen(ctx, in)

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
}
