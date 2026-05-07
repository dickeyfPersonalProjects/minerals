package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// fakeJournalAttachmentRepo is an in-memory
// domain.JournalEntryFileRepo for handler tests.
type fakeJournalAttachmentRepo struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]domain.JournalEntryFile // keyed by file_id
	createErr error
}

func newFakeJournalAttachmentRepo() *fakeJournalAttachmentRepo {
	return &fakeJournalAttachmentRepo{
		rows: map[uuid.UUID]domain.JournalEntryFile{},
	}
}

func (f *fakeJournalAttachmentRepo) Create(_ context.Context, _ domain.Tx, j domain.JournalEntryFile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.rows[j.FileID] = j
	return nil
}

func (f *fakeJournalAttachmentRepo) GetByFileID(_ context.Context, fileID uuid.UUID) (domain.JournalEntryFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.rows[fileID]
	if !ok {
		return domain.JournalEntryFile{}, domain.ErrJournalAttachmentNotFound
	}
	return j, nil
}

func (f *fakeJournalAttachmentRepo) ListByEntry(_ context.Context, entryID uuid.UUID) ([]domain.JournalEntryFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.JournalEntryFile
	for _, j := range f.rows {
		if j.EntryID == entryID {
			out = append(out, j)
		}
	}
	// Stable order by position then file_id (mirrors the postgres
	// repo's ORDER BY).
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].Position < out[i].Position ||
				(out[k].Position == out[i].Position && out[k].FileID.String() < out[i].FileID.String()) {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out, nil
}

func (f *fakeJournalAttachmentRepo) Delete(_ context.Context, _ domain.Tx, fileID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[fileID]; !ok {
		return domain.ErrJournalAttachmentNotFound
	}
	delete(f.rows, fileID)
	return nil
}

func (f *fakeJournalAttachmentRepo) MaxPosition(_ context.Context, _ domain.Tx, entryID uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	max := 0
	for _, j := range f.rows {
		if j.EntryID == entryID && j.Position > max {
			max = j.Position
		}
	}
	return max, nil
}

func newJournalFileServer(t *testing.T) (
	http.Handler,
	*fakeJournalRepo,
	*fakeJournalAttachmentRepo,
	*fakeFileRepo,
	*fakeStorage,
) {
	t.Helper()
	entries := newFakeJournalRepo()
	attachments := newFakeJournalAttachmentRepo()
	files := newFakeFileRepo()
	store := newFakeStorage()
	deps := &JournalFileServiceDeps{
		Entries:        entries,
		Attachments:    attachments,
		Files:          files,
		Storage:        store,
		RunInTx:        nullTxRunner,
		MaxUploadBytes: 10 * 1024 * 1024,
	}
	return New(Deps{JournalFiles: deps}), entries, attachments, files, store
}

// makeJournalUpload builds a single-file multipart POST body.
func makeJournalUpload(t *testing.T, fileBytes []byte, contentType string) (body *bytes.Buffer, formContentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="test"`}
	hdr["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	return body, mw.FormDataContentType()
}

// seedEntry registers an entry id with the in-memory journal repo so
// upload/list/delete handlers can resolve it.
func seedEntry(t *testing.T, entries *fakeJournalRepo, specID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	entries.rows[id] = domain.JournalEntry{ID: id, SpecimenID: specID, BodyMD: "test"}
	return id
}

func TestJournalFileUpload_PDFRoundtrip(t *testing.T) {
	h, entries, attachments, files, store := newJournalFileServer(t)
	entryID := seedEntry(t, entries, uuid.New())

	pdfBytes := []byte("%PDF-1.4\n% fake pdf bytes for testing\n")
	body, ct := makeJournalUpload(t, pdfBytes, "application/pdf")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+entryID.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/files/") {
		t.Errorf("Location = %q", loc)
	}

	var got JournalFileView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EntryID != entryID {
		t.Errorf("entry id mismatch: got %s want %s", got.EntryID, entryID)
	}
	if got.Position != 1 {
		t.Errorf("first attachment position = %d, want 1", got.Position)
	}
	if got.ContentType != "application/pdf" {
		t.Errorf("content_type = %q", got.ContentType)
	}
	if got.ByteSize != int64(len(pdfBytes)) {
		t.Errorf("byte_size = %d, want %d", got.ByteSize, len(pdfBytes))
	}
	if len(got.SHA256) != 64 {
		t.Errorf("sha256 length = %d", len(got.SHA256))
	}

	// Storage: only the original — no variants.
	originalKey := "files/" + got.FileID.String()
	if len(store.objects) != 1 {
		t.Errorf("storage objects = %d, want 1 (no variants for journal attachments)", len(store.objects))
	}
	if _, ok := store.objects[originalKey]; !ok {
		t.Errorf("missing original object: %s", originalKey)
	}
	if !bytes.Equal(store.objects[originalKey], pdfBytes) {
		t.Errorf("stored bytes differ from input")
	}

	// Attachment row + file row both present.
	if _, err := attachments.GetByFileID(context.Background(), got.FileID); err != nil {
		t.Errorf("attachment row: %v", err)
	}
	if _, err := files.GetByID(context.Background(), got.FileID); err != nil {
		t.Errorf("file row: %v", err)
	}
}

func TestJournalFileUpload_PositionIncrements(t *testing.T) {
	h, entries, _, _, _ := newJournalFileServer(t)
	entryID := seedEntry(t, entries, uuid.New())

	for i, want := range []int{1, 2, 3} {
		body, ct := makeJournalUpload(t,
			[]byte(fmt.Sprintf("file %d", i)), "text/plain")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/journal/"+entryID.String()+"/files", body)
		req.Header.Set("Content-Type", ct)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %d: %d body=%s", i, rec.Code, rec.Body.String())
		}
		var got JournalFileView
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Position != want {
			t.Errorf("upload %d: position = %d, want %d", i, got.Position, want)
		}
	}
}

func TestJournalFileUpload_RejectsUnsupportedMediaType(t *testing.T) {
	h, entries, _, _, _ := newJournalFileServer(t)
	entryID := seedEntry(t, entries, uuid.New())

	// application/x-msdownload is NOT in the v1 allowlist.
	body, ct := makeJournalUpload(t, []byte("MZ\x90\x00"), "application/x-msdownload")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+entryID.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var env envelopeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode env: %v", err)
	}
	if env.Error.Code != "unsupported_media_type" {
		t.Errorf("code = %q", env.Error.Code)
	}
	allowed, ok := env.Error.Details["allowed"].([]any)
	if !ok || len(allowed) == 0 {
		t.Errorf("expected details.allowed to list types, got %v", env.Error.Details)
	}
}

func TestJournalFileUpload_RejectsOversizedPayload(t *testing.T) {
	entries := newFakeJournalRepo()
	attachments := newFakeJournalAttachmentRepo()
	files := newFakeFileRepo()
	store := newFakeStorage()
	entryID := seedEntry(t, entries, uuid.New())
	deps := &JournalFileServiceDeps{
		Entries:        entries,
		Attachments:    attachments,
		Files:          files,
		Storage:        store,
		RunInTx:        nullTxRunner,
		MaxUploadBytes: 256, // tight cap so a small payload still trips it
	}
	h := New(Deps{JournalFiles: deps})

	big := bytes.Repeat([]byte("A"), 4096)
	body, ct := makeJournalUpload(t, big, "text/plain")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+entryID.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		// Huma may surface MaxBytesError as 400 if the multipart parse
		// itself trips the cap; either status satisfies §12.
		t.Errorf("status = %d body=%s (want 413 or 400)", rec.Code, rec.Body.String())
	}
}

func TestJournalFileUpload_EntryNotFound(t *testing.T) {
	h, _, _, _, store := newJournalFileServer(t)
	missing := uuid.New()

	body, ct := makeJournalUpload(t, []byte("hello"), "text/plain")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+missing.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.objects) != 0 {
		t.Errorf("storage object created for missing entry: %d", len(store.objects))
	}
}

func TestJournalFileUpload_CleanupOnDBFailure(t *testing.T) {
	entries := newFakeJournalRepo()
	attachments := newFakeJournalAttachmentRepo()
	files := newFakeFileRepo()
	store := newFakeStorage()
	entryID := seedEntry(t, entries, uuid.New())
	attachments.createErr = errors.New("forced db failure")

	deps := &JournalFileServiceDeps{
		Entries:        entries,
		Attachments:    attachments,
		Files:          files,
		Storage:        store,
		RunInTx:        nullTxRunner,
		MaxUploadBytes: 10 * 1024 * 1024,
	}
	h := New(Deps{JournalFiles: deps})

	body, ct := makeJournalUpload(t, []byte("hello"), "text/plain")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+entryID.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("expected error, got %d", rec.Code)
	}
	if len(store.objects) != 0 {
		t.Errorf("expected MinIO objects to be cleaned up after DB failure, got %d", len(store.objects))
	}
}

func TestJournalFileList_OrdersByPosition(t *testing.T) {
	h, entries, _, _, _ := newJournalFileServer(t)
	entryID := seedEntry(t, entries, uuid.New())

	// Upload three files; they get positions 1, 2, 3.
	for i := 0; i < 3; i++ {
		body, ct := makeJournalUpload(t,
			[]byte(fmt.Sprintf("payload-%d", i)), "text/plain")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/journal/"+entryID.String()+"/files", body)
		req.Header.Set("Content-Type", ct)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %d: %d", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/journal/"+entryID.String()+"/files", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rec.Code, rec.Body.String())
	}
	var got journalFileListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("len = %d", len(got.Items))
	}
	for i, want := range []int{1, 2, 3} {
		if got.Items[i].Position != want {
			t.Errorf("items[%d].position = %d, want %d", i, got.Items[i].Position, want)
		}
	}
}

func TestJournalFileList_EntryNotFound(t *testing.T) {
	h, _, _, _, _ := newJournalFileServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/journal/"+uuid.New().String()+"/files", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestJournalFileDelete_RemovesAllRowsAndObject(t *testing.T) {
	h, entries, attachments, files, store := newJournalFileServer(t)
	entryID := seedEntry(t, entries, uuid.New())

	body, ct := makeJournalUpload(t, []byte("hello"), "text/plain")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+entryID.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var view JournalFileView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete,
		"/api/v1/journal-files/"+view.FileID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("delete: %d body=%s", rec2.Code, rec2.Body.String())
	}

	if _, err := attachments.GetByFileID(context.Background(), view.FileID); !errors.Is(err, domain.ErrJournalAttachmentNotFound) {
		t.Errorf("attachment row still present")
	}
	if _, err := files.GetByID(context.Background(), view.FileID); !errors.Is(err, domain.ErrFileNotFound) {
		t.Errorf("file row still present")
	}
	if len(store.objects) != 0 {
		t.Errorf("storage objects remain: %d", len(store.objects))
	}
}

func TestJournalFileDelete_NotFound(t *testing.T) {
	h, _, _, _, _ := newJournalFileServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/journal-files/"+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestJournalFileDownload_ServesOriginalBytes(t *testing.T) {
	h, entries, _, _, _ := newJournalFileServer(t)
	entryID := seedEntry(t, entries, uuid.New())

	payload := []byte("plain text body for download test")
	body, ct := makeJournalUpload(t, payload, "text/plain")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+entryID.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var view JournalFileView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)

	// Download via /api/v1/files/{file_id}.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+view.FileID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("download: %d body=%s", rec2.Code, rec2.Body.String())
	}
	if got := rec2.Header().Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type = %q", got)
	}
	if !bytes.Equal(rec2.Body.Bytes(), payload) {
		t.Errorf("payload mismatch")
	}
	etag := rec2.Header().Get("ETag")
	if etag == "" {
		t.Errorf("missing ETag")
	}
	// Non-image content types should be served as `attachment`.
	if disp := rec2.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment;") {
		t.Errorf("disposition = %q (want attachment; for non-image)", disp)
	}

	// If-None-Match returns 304.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+view.FileID.String(), nil)
	req3.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: status = %d, want 304", rec3.Code)
	}
}

func TestJournalFileDownload_ImageDispositionInline(t *testing.T) {
	h, entries, _, _, _ := newJournalFileServer(t)
	entryID := seedEntry(t, entries, uuid.New())

	// Bytes don't need to be a real image — the handler uses stored
	// content-type, never sniffs.
	body, ct := makeJournalUpload(t, []byte("\xff\xd8\xff\xe0fake"), "image/jpeg")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/journal/"+entryID.String()+"/files", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var view JournalFileView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+view.FileID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("download: %d body=%s", rec2.Code, rec2.Body.String())
	}
	if disp := rec2.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "inline;") {
		t.Errorf("disposition = %q (want inline; for image)", disp)
	}
}

func TestJournalFileDownload_NotFound(t *testing.T) {
	h, _, _, _, _ := newJournalFileServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/"+uuid.New().String(), nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
