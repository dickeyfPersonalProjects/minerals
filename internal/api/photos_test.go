package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage"
)

// fakePhotoRepo is an in-memory PhotoRepo for handler tests.
type fakePhotoRepo struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]domain.Photo
	specimens map[uuid.UUID]bool // populated specimen IDs; used to fake FK violation
	createErr error
}

func newFakePhotoRepo() *fakePhotoRepo {
	return &fakePhotoRepo{
		rows:      map[uuid.UUID]domain.Photo{},
		specimens: map[uuid.UUID]bool{},
	}
}

func (f *fakePhotoRepo) Create(_ context.Context, _ domain.Tx, p domain.Photo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	if !f.specimens[p.SpecimenID] {
		return domain.ErrPhotoNotFound // fakes the FK violation mapping
	}
	f.rows[p.ID] = p
	return nil
}

func (f *fakePhotoRepo) GetByID(_ context.Context, id uuid.UUID) (domain.Photo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[id]
	if !ok {
		return domain.Photo{}, domain.ErrPhotoNotFound
	}
	return p, nil
}

func (f *fakePhotoRepo) Update(_ context.Context, _ domain.Tx, p domain.Photo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[p.ID]; !ok {
		return domain.ErrPhotoNotFound
	}
	f.rows[p.ID] = p
	return nil
}

func (f *fakePhotoRepo) Delete(_ context.Context, _ domain.Tx, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return domain.ErrPhotoNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakePhotoRepo) ListBySpecimen(_ context.Context, specimenID uuid.UUID, _ domain.Page) ([]domain.Photo, domain.Cursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Photo
	for _, p := range f.rows {
		if p.SpecimenID == specimenID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out, "", nil
}

func (f *fakePhotoRepo) MaxPosition(_ context.Context, _ domain.Tx, specimenID uuid.UUID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	max := 0
	for _, p := range f.rows {
		if p.SpecimenID == specimenID && p.Position > max {
			max = p.Position
		}
	}
	return max, nil
}

// fakeFileRepo is an in-memory FileRepo.
type fakeFileRepo struct {
	mu   sync.Mutex
	rows map[uuid.UUID]domain.File
}

func newFakeFileRepo() *fakeFileRepo { return &fakeFileRepo{rows: map[uuid.UUID]domain.File{}} }

func (f *fakeFileRepo) Create(_ context.Context, _ domain.Tx, file domain.File) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.rows {
		if existing.S3Key == file.S3Key {
			return domain.ErrFileConflict
		}
	}
	f.rows[file.ID] = file
	return nil
}

func (f *fakeFileRepo) GetByID(_ context.Context, id uuid.UUID) (domain.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.rows[id]
	if !ok {
		return domain.File{}, domain.ErrFileNotFound
	}
	return x, nil
}

func (f *fakeFileRepo) Delete(_ context.Context, _ domain.Tx, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return domain.ErrFileNotFound
	}
	delete(f.rows, id)
	return nil
}

// fakeStorage is an in-memory PhotoStorage.
type fakeStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{objects: map[string][]byte{}, types: map[string]string{}}
}

func (s *fakeStorage) Upload(_ context.Context, key string, body io.Reader, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	s.types[key] = contentType
	return nil
}

func (s *fakeStorage) UploadIfNotExists(ctx context.Context, key string, body io.Reader, contentType string) error {
	s.mu.Lock()
	if _, exists := s.objects[key]; exists {
		s.mu.Unlock()
		return storage.ErrAlreadyExists
	}
	s.mu.Unlock()
	return s.Upload(ctx, key, body, contentType)
}

func (s *fakeStorage) Download(_ context.Context, key string) (io.ReadCloser, http.Header, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, nil, storage.ErrNotFound
	}
	hdr := http.Header{}
	hdr.Set("Content-Type", s.types[key])
	return io.NopCloser(bytes.NewReader(data)), hdr, nil
}

func (s *fakeStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	delete(s.types, key)
	return nil
}

func nullTxRunner(ctx context.Context, fn func(tx domain.Tx) error) error {
	return fn(nil)
}

func newPhotoServer(t *testing.T) (http.Handler, *fakePhotoRepo, *fakeFileRepo, *fakeStorage) {
	t.Helper()
	photos := newFakePhotoRepo()
	files := newFakeFileRepo()
	store := newFakeStorage()
	deps := &PhotoServiceDeps{
		Photos:         photos,
		Files:          files,
		Storage:        store,
		RunInTx:        nullTxRunner,
		MaxUploadBytes: 10 * 1024 * 1024,
	}
	return New(Deps{Photos: deps}), photos, files, store
}

// makeJPEG produces a small valid JPEG of the given dimensions.
func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	return buf.Bytes()
}

func makeMultipartUpload(t *testing.T, fileBytes []byte, contentType string, takenAt string) (body *bytes.Buffer, formContentType string) {
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
	if takenAt != "" {
		if err := mw.WriteField("taken_at", takenAt); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	return body, mw.FormDataContentType()
}

func TestPhotoUpload_Roundtrip(t *testing.T) {
	h, photos, files, store := newPhotoServer(t)
	specimenID := uuid.New()
	photos.specimens[specimenID] = true

	jp := makeJPEG(t, 200, 150)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/photos/") {
		t.Errorf("Location = %q", loc)
	}

	var got PhotoView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SpecimenID != specimenID {
		t.Errorf("specimen id mismatch")
	}
	if got.Position != 1 {
		t.Errorf("first photo position = %d, want 1", got.Position)
	}
	if got.ContentType != "image/jpeg" {
		t.Errorf("content_type = %q", got.ContentType)
	}
	if len(got.SHA256) != 64 {
		t.Errorf("sha256 length = %d", len(got.SHA256))
	}

	// All three MinIO objects exist.
	originalKey := "files/" + got.FileID.String()
	for _, k := range []string{originalKey, originalKey + ".display.jpg", originalKey + ".thumb.jpg"} {
		if _, ok := store.objects[k]; !ok {
			t.Errorf("missing storage object: %s", k)
		}
	}

	// File row exists with the right metadata.
	fileRow, err := files.GetByID(context.Background(), got.FileID)
	if err != nil {
		t.Fatalf("file row: %v", err)
	}
	if fileRow.ContentType != "image/jpeg" {
		t.Errorf("file content_type = %q", fileRow.ContentType)
	}
	wantSHA := sha256.Sum256(store.objects[originalKey])
	if fileRow.SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Errorf("sha256 mismatch")
	}
}

func TestPhotoUpload_RejectsUnsupportedMediaType(t *testing.T) {
	h, photos, _, _ := newPhotoServer(t)
	specimenID := uuid.New()
	photos.specimens[specimenID] = true

	body, ct := makeMultipartUpload(t, []byte("hello world"), "text/plain", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
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
		t.Errorf("expected details.allowed to list allowed types, got %v", env.Error.Details)
	}
}

func TestPhotoUpload_RejectsOversizedPayload(t *testing.T) {
	photos := newFakePhotoRepo()
	files := newFakeFileRepo()
	store := newFakeStorage()
	specimenID := uuid.New()
	photos.specimens[specimenID] = true
	deps := &PhotoServiceDeps{
		Photos:         photos,
		Files:          files,
		Storage:        store,
		RunInTx:        nullTxRunner,
		MaxUploadBytes: 1024, // 1 KiB cap
	}
	h := New(Deps{Photos: deps})

	jp := makeJPEG(t, 600, 400) // larger than 1 KiB
	body, ct := makeMultipartUpload(t, jp, "image/jpeg", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		// Huma may surface MaxBytesError as 400 if it occurs during
		// multipart parse; either status satisfies the §12 cap rule.
		t.Errorf("status = %d body=%s (want 413 or 400)", rec.Code, rec.Body.String())
	}
}

func TestPhotoUpload_SpecimenNotFound(t *testing.T) {
	h, _, _, _ := newPhotoServer(t)
	missing := uuid.New()

	jp := makeJPEG(t, 100, 100)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+missing.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPhotoUpload_CleanupOnDBFailure(t *testing.T) {
	photos := newFakePhotoRepo()
	files := newFakeFileRepo()
	store := newFakeStorage()
	specimenID := uuid.New()
	photos.specimens[specimenID] = true
	photos.createErr = fmt.Errorf("forced db failure")

	deps := &PhotoServiceDeps{
		Photos:         photos,
		Files:          files,
		Storage:        store,
		RunInTx:        nullTxRunner,
		MaxUploadBytes: 10 * 1024 * 1024,
	}
	h := New(Deps{Photos: deps})

	jp := makeJPEG(t, 100, 100)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("expected error, got %d", rec.Code)
	}
	if len(store.objects) != 0 {
		t.Errorf("expected MinIO objects to be cleaned up after DB failure, got %d", len(store.objects))
	}
}

func TestPhotoDownload_OriginalAndVariants(t *testing.T) {
	h, photos, _, _ := newPhotoServer(t)
	specimenID := uuid.New()
	photos.specimens[specimenID] = true

	jp := makeJPEG(t, 200, 150)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d %s", rec.Code, rec.Body.String())
	}
	var view PhotoView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)

	// Download original.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/photos/"+view.ID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("download original: %d body=%s", rec2.Code, rec2.Body.String())
	}
	if got := rec2.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("original Content-Type = %q", got)
	}
	if etag := rec2.Header().Get("ETag"); etag == "" {
		t.Errorf("missing ETag")
	}
	if disp := rec2.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "inline;") {
		t.Errorf("disposition = %q (want inline; for image)", disp)
	}

	// Download display variant.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/photos/"+view.ID.String()+"/display", nil)
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("download display: %d body=%s", rec3.Code, rec3.Body.String())
	}
	if got := rec3.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("display Content-Type = %q", got)
	}

	// Download thumb variant.
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/photos/"+view.ID.String()+"/thumb", nil)
	h.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("download thumb: %d body=%s", rec4.Code, rec4.Body.String())
	}

	// If-None-Match returns 304.
	etag := rec2.Header().Get("ETag")
	rec5 := httptest.NewRecorder()
	req5 := httptest.NewRequest(http.MethodGet, "/api/v1/photos/"+view.ID.String(), nil)
	req5.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: status = %d, want 304", rec5.Code)
	}
}

func TestPhotoDelete_RemovesAllObjects(t *testing.T) {
	h, photos, files, store := newPhotoServer(t)
	specimenID := uuid.New()
	photos.specimens[specimenID] = true

	jp := makeJPEG(t, 100, 100)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var view PhotoView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)

	// Delete.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/api/v1/photos/"+view.ID.String(), nil)
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("delete: %d body=%s", rec2.Code, rec2.Body.String())
	}

	// All gone.
	if _, err := photos.GetByID(context.Background(), view.ID); !errors.Is(err, domain.ErrPhotoNotFound) {
		t.Errorf("photo row still present")
	}
	if _, err := files.GetByID(context.Background(), view.FileID); !errors.Is(err, domain.ErrFileNotFound) {
		t.Errorf("file row still present")
	}
	if len(store.objects) != 0 {
		t.Errorf("storage objects remain: %d", len(store.objects))
	}
}

func TestPhotoPatch_UpdatesPositionAndTakenAt(t *testing.T) {
	h, photos, files, _ := newPhotoServer(t)
	specimenID := uuid.New()
	photos.specimens[specimenID] = true

	now := time.Now().UTC().Truncate(time.Second)
	fileID := uuid.New()
	files.rows[fileID] = domain.File{ID: fileID, S3Key: "files/" + fileID.String(),
		ContentType: "image/jpeg", ByteSize: 100, SHA256: strings.Repeat("a", 64),
		UploadedAt: now}
	photoID := uuid.New()
	photos.rows[photoID] = domain.Photo{
		ID: photoID, SpecimenID: specimenID, FileID: fileID,
		Position: 1, CreatedAt: now,
	}

	bodyJSON := `{"position": 5, "taken_at": "2026-01-02T03:04:05Z"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/photos/"+photoID.String(), strings.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d body=%s", rec.Code, rec.Body.String())
	}
	updated := photos.rows[photoID]
	if updated.Position != 5 {
		t.Errorf("position = %d, want 5", updated.Position)
	}
	if updated.TakenAt == nil || !updated.TakenAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("taken_at = %v", updated.TakenAt)
	}
}

func TestPhotoList_OrdersByPosition(t *testing.T) {
	h, photos, files, _ := newPhotoServer(t)
	specimenID := uuid.New()
	photos.specimens[specimenID] = true

	now := time.Now().UTC()
	for _, pos := range []int{3, 1, 2} {
		fid := uuid.New()
		files.rows[fid] = domain.File{
			ID: fid, S3Key: "files/" + fid.String(),
			ContentType: "image/jpeg", ByteSize: 1, SHA256: strings.Repeat("b", 64),
			UploadedAt: now,
		}
		pid := uuid.New()
		photos.rows[pid] = domain.Photo{
			ID: pid, SpecimenID: specimenID, FileID: fid,
			Position: pos, CreatedAt: now,
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/specimens/"+specimenID.String()+"/photos", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rec.Code, rec.Body.String())
	}
	var got photoListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("len=%d", len(got.Items))
	}
	for i, want := range []int{1, 2, 3} {
		if got.Items[i].Position != want {
			t.Errorf("items[%d].position = %d, want %d", i, got.Items[i].Position, want)
		}
	}
}
