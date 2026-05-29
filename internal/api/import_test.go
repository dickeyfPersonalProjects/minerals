package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/portability"
)

// newImportHarness wires a New() handler whose stub-auth path resolves to
// an active user, with every import collaborator backed by an in-memory
// fake. It returns the handler plus the collector and storage fakes for
// assertions.
func newImportHarness(t *testing.T) (http.Handler, *fakeCollectorRepo, *fakeStorage) {
	t.Helper()
	repo := newFakeUserRepo()
	seedActiveProfile(t, repo, "Alice", nil)

	specs := newFakeSpecimenRepo()
	colls := newFakeCollectorRepo()
	store := newFakeStorage()

	h := New(Deps{
		Users: repo,
		Import: &ImportServiceDeps{
			Collectors:         colls,
			Files:              newFakeFileRepo(),
			Specimens:          specs,
			Photos:             newFakePhotoRepo(),
			Journal:            newFakeJournalRepo(),
			JournalFiles:       newFakeJournalAttachmentRepo(),
			SpecimenCollectors: newFakeChainRepo(specs, colls),
			QRSheets:           newFakeQRSheetRepo(),
			Storage:            store,
			MaxUploadBytes:     10 << 20,
			RunInTx: func(_ context.Context, fn func(tx domain.Tx) error) error {
				return fn(nil)
			},
			CatalogNumbers: func(context.Context, uuid.UUID) (map[string]struct{}, error) {
				return map[string]struct{}{}, nil
			},
		},
	})
	return h, colls, store
}

// minimalArchive builds a tiny valid archive: manifest + a single
// collector record (no binaries), enough to exercise the endpoint.
func minimalArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	write := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}

	mf, _ := json.Marshal(portability.Manifest{
		SchemaVersion: portability.SchemaVersion,
		Application:   portability.Application,
		ExportedAt:    time.Unix(0, 0).UTC(),
		Counts:        portability.Counts{Collectors: 1},
	})
	write(portability.ManifestPath, mf)

	cr, _ := json.Marshal(portability.CollectorRecord{
		ID: "018f0000-0000-7000-8000-000000000001", Name: "Imported Collector",
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
	})
	write(portability.CollectorsPath, append(cr, '\n'))

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// doImport posts archive bytes as multipart/form-data to /api/v1/import.
func doImport(t *testing.T, h http.Handler, archive []byte, dryRun bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "export.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(archive)); err != nil {
		t.Fatalf("copy archive: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	url := "/api/v1/import"
	if dryRun {
		url += "?dryRun=true"
	}
	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeReport(t *testing.T, rec *httptest.ResponseRecorder) portability.Report {
	t.Helper()
	var r portability.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode report: %v (raw=%s)", err, rec.Body.String())
	}
	return r
}

func TestImportEndpoint_DryRun(t *testing.T) {
	h, colls, store := newImportHarness(t)

	rec := doImport(t, h, minimalArchive(t), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	report := decodeReport(t, rec)
	if !report.DryRun || report.Committed {
		t.Errorf("expected dry-run non-committed report: %+v", report)
	}
	if report.Counts.Collectors != 1 {
		t.Errorf("expected 1 collector counted, got %d", report.Counts.Collectors)
	}
	if len(colls.rows) != 0 {
		t.Errorf("dry-run must not write collectors")
	}
	if len(store.objects) != 0 {
		t.Errorf("dry-run must not write objects")
	}
}

func TestImportEndpoint_Commit(t *testing.T) {
	h, colls, _ := newImportHarness(t)

	rec := doImport(t, h, minimalArchive(t), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	report := decodeReport(t, rec)
	if !report.Committed {
		t.Errorf("expected committed report: %+v", report)
	}
	if got := len(colls.rows); got != 1 {
		t.Errorf("expected 1 collector created, got %d", got)
	}
}

func TestImportEndpoint_MalformedArchive(t *testing.T) {
	h, _, _ := newImportHarness(t)

	rec := doImport(t, h, []byte("not a zip at all"), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if code := decodeErrCode(t, rec); code != portability.CodeMalformedArchive {
		t.Errorf("error code = %q, want %q", code, portability.CodeMalformedArchive)
	}
}
