package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// exportHarness wires a New() handler whose stub-auth path resolves to
// an active user, plus the full export collaborator set as in-memory
// fakes. It returns the handler, the resolved owner, and the fakes the
// test seeds.
type exportHarness struct {
	h          http.Handler
	owner      domain.User
	specimens  *fakeSpecimenRepo
	photos     *fakePhotoRepo
	files      *fakeFileRepo
	journal    *fakeJournalRepo
	attach     *fakeJournalAttachmentRepo
	collectors *fakeCollectorRepo
	chain      *fakeChainRepo
	qr         *fakeQRSheetRepo
	store      *fakeStorage
}

func newExportHarness(t *testing.T) *exportHarness {
	t.Helper()
	users := newFakeUserRepo()
	owner := seedActiveProfile(t, users, "Alice", nil)

	specimens := newFakeSpecimenRepo()
	collectors := newFakeCollectorRepo()
	chain := newFakeChainRepo(specimens, collectors)

	h := exportHarness{
		owner:      owner,
		specimens:  specimens,
		photos:     newFakePhotoRepo(),
		files:      newFakeFileRepo(),
		journal:    newFakeJournalRepo(),
		attach:     newFakeJournalAttachmentRepo(),
		collectors: collectors,
		chain:      chain,
		qr:         newFakeQRSheetRepo(),
		store:      newFakeStorage(),
	}
	h.h = New(Deps{
		Users: users,
		Export: &ExportServiceDeps{
			Specimens:          h.specimens,
			Photos:             h.photos,
			JournalEntries:     h.journal,
			JournalFiles:       h.attach,
			Collectors:         h.collectors,
			SpecimenCollectors: h.chain,
			QRSheets:           h.qr,
			Files:              h.files,
			Storage:            h.store,
		},
	})
	return &h
}

// doExport drives GET /api/v1/export and parses the ZIP body into a
// name→bytes map.
func doExport(t *testing.T, h http.Handler) (*httptest.ResponseRecorder, map[string][]byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec, nil
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return rec, out
}

// jsonlLines splits a JSONL blob into its non-empty lines.
func jsonlLines(b []byte) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestExport_HappyPath_FullArchive seeds one of every owned entity for
// the caller (plus foreign rows that must NOT appear) and asserts the
// archive structure, counts, scoping, field fidelity, and binaries.
func TestExport_HappyPath_FullArchive(t *testing.T) {
	h := newExportHarness(t)
	owner := h.owner
	stranger := uuid.New()

	// Collectors: one owned, one foreign.
	c1 := domain.Collector{ID: domain.NewID(), Name: "Smithsonian", AuthorID: owner.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	c2 := domain.Collector{ID: domain.NewID(), Name: "Not Mine", AuthorID: stranger,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	h.collectors.rows[c1.ID] = c1
	h.collectors.rows[c2.ID] = c2

	// Specimens: one owned (with type_data + catalog number), one foreign.
	cat := "MIN-001"
	s1 := domain.Specimen{
		ID: domain.NewID(), Type: domain.SpecimenMineral, CatalogNumber: &cat,
		Name: "Fluorite", Description: "green cube", Visibility: domain.VisibilityPrivate,
		AuthorID: owner.ID, Tagged: true,
		TypeData:  []byte(`{"color":"green","mohs_hardness":4}`),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s2 := domain.Specimen{ID: domain.NewID(), Type: domain.SpecimenRock, Name: "Foreign",
		Visibility: domain.VisibilityPublic, AuthorID: stranger,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	h.specimens.rows[s1.ID] = s1
	h.specimens.rows[s2.ID] = s2

	// Collector chain: s1 → [c1].
	h.chain.chains[s1.ID] = []uuid.UUID{c1.ID}

	// Photo on s1, backed by a stored file.
	f1 := domain.File{ID: domain.NewID(), S3Key: "files/img1", ContentType: "image/jpeg",
		ByteSize: 5, SHA256: "abc123", UploadedBy: owner.ID, UploadedAt: time.Now().UTC()}
	h.files.rows[f1.ID] = f1
	h.store.objects[f1.S3Key] = []byte("JPEGB")
	h.store.types[f1.S3Key] = "image/jpeg"
	p1 := domain.Photo{ID: domain.NewID(), SpecimenID: s1.ID, FileID: f1.ID,
		Kind: domain.PhotoKindVisible, Position: 1, CreatedAt: time.Now().UTC()}
	h.photos.rows[p1.ID] = p1

	// Journal entry on s1 with a PDF attachment.
	j1 := domain.JournalEntry{ID: domain.NewID(), SpecimenID: s1.ID, AuthorID: owner.ID,
		BodyMD: "# field notes", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	h.journal.rows[j1.ID] = j1
	f2 := domain.File{ID: domain.NewID(), S3Key: "files/doc1", ContentType: "application/pdf",
		ByteSize: 3, SHA256: "def456", UploadedBy: owner.ID, UploadedAt: time.Now().UTC()}
	h.files.rows[f2.ID] = f2
	h.store.objects[f2.S3Key] = []byte("PDF")
	h.store.types[f2.S3Key] = "application/pdf"
	h.attach.rows[f2.ID] = domain.JournalEntryFile{EntryID: j1.ID, FileID: f2.ID,
		Position: 1, CreatedAt: time.Now().UTC()}

	// QR sheet with s1 as a member.
	h.qr.seedSheetFor(owner.ID, "avery-5160", s1.ID)
	sheetID := h.qr.sheets[owner.ID].ID
	h.qr.appendEntry(sheetID, s1.ID, "Fluorite", nil)

	rec, entries := doExport(t, h.h)

	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") ||
		!strings.Contains(cd, ".zip") {
		t.Errorf("Content-Disposition = %q, want attachment .zip", cd)
	}

	// --- manifest ---
	var man exportManifest
	mustJSON(t, entries["manifest.json"], &man)
	if man.SchemaVersion != exportSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", man.SchemaVersion, exportSchemaVersion)
	}
	if man.ExportedBy != auth.StubUserSub {
		t.Errorf("exportedBy = %q, want %q", man.ExportedBy, auth.StubUserSub)
	}
	wantCounts := exportCounts{Collectors: 1, Specimens: 1, JournalEntries: 1,
		QRSheets: 1, Images: 1, JournalFiles: 1}
	if man.Counts != wantCounts {
		t.Errorf("counts = %+v, want %+v", man.Counts, wantCounts)
	}
	if len(man.Images) != 1 {
		t.Fatalf("manifest images = %d, want 1", len(man.Images))
	}
	img := man.Images[0]
	if img.ImageID != p1.ID || img.SpecimenID != s1.ID || img.FileID != f1.ID {
		t.Errorf("image ids mismatch: %+v", img)
	}
	if img.ContentHash != "sha256:abc123" {
		t.Errorf("image contentHash = %q, want sha256:abc123", img.ContentHash)
	}
	wantImgPath := "images/" + s1.ID.String() + "/" + p1.ID.String() + ".jpg"
	if img.Path != wantImgPath {
		t.Errorf("image path = %q, want %q", img.Path, wantImgPath)
	}

	// --- collectors: owned only ---
	colLines := jsonlLines(entries["data/collectors.jsonl"])
	if len(colLines) != 1 {
		t.Fatalf("collectors.jsonl lines = %d, want 1 (foreign collector must be excluded)", len(colLines))
	}
	var colRec exportCollectorRecord
	mustJSON(t, []byte(colLines[0]), &colRec)
	if colRec.ID != c1.ID || colRec.Name != "Smithsonian" {
		t.Errorf("collector record = %+v, want c1", colRec)
	}

	// --- specimens: owned only, fields + chain round-trip ---
	specLines := jsonlLines(entries["data/specimens.jsonl"])
	if len(specLines) != 1 {
		t.Fatalf("specimens.jsonl lines = %d, want 1 (foreign specimen must be excluded)", len(specLines))
	}
	var specRec exportSpecimenRecord
	mustJSON(t, []byte(specLines[0]), &specRec)
	if specRec.ID != s1.ID || specRec.Name != "Fluorite" || !specRec.Tagged {
		t.Errorf("specimen record core fields mismatch: %+v", specRec)
	}
	if specRec.CatalogNumber == nil || *specRec.CatalogNumber != "MIN-001" {
		t.Errorf("specimen catalogNumber not round-tripped: %+v", specRec.CatalogNumber)
	}
	if string(specRec.TypeData) != `{"color":"green","mohs_hardness":4}` {
		t.Errorf("specimen typeData = %s, want raw JSON round-trip", specRec.TypeData)
	}
	if len(specRec.CollectorIDs) != 1 || specRec.CollectorIDs[0] != c1.ID {
		t.Errorf("specimen collectorIds = %v, want [%v]", specRec.CollectorIDs, c1.ID)
	}

	// --- journal: entry + attachment ref ---
	jLines := jsonlLines(entries["data/journal_entries.jsonl"])
	if len(jLines) != 1 {
		t.Fatalf("journal_entries.jsonl lines = %d, want 1", len(jLines))
	}
	var jRec exportJournalRecord
	mustJSON(t, []byte(jLines[0]), &jRec)
	if jRec.ID != j1.ID || jRec.BodyMD != "# field notes" {
		t.Errorf("journal record mismatch: %+v", jRec)
	}
	if len(jRec.Attachments) != 1 {
		t.Fatalf("journal attachments = %d, want 1", len(jRec.Attachments))
	}
	wantAttPath := "files/journal/" + j1.ID.String() + "/" + f2.ID.String() + ".pdf"
	if jRec.Attachments[0].Path != wantAttPath || jRec.Attachments[0].ContentHash != "sha256:def456" {
		t.Errorf("attachment ref mismatch: %+v", jRec.Attachments[0])
	}

	// --- qr sheet ---
	qLines := jsonlLines(entries["data/qrsheets.jsonl"])
	if len(qLines) != 1 {
		t.Fatalf("qrsheets.jsonl lines = %d, want 1", len(qLines))
	}
	var qRec exportQRSheetRecord
	mustJSON(t, []byte(qLines[0]), &qRec)
	if qRec.Template != "avery-5160" || len(qRec.Specimens) != 1 || qRec.Specimens[0].SpecimenID != s1.ID {
		t.Errorf("qrsheet record mismatch: %+v", qRec)
	}

	// --- binaries present with correct bytes ---
	if got := entries[wantImgPath]; !bytes.Equal(got, []byte("JPEGB")) {
		t.Errorf("image binary = %q, want JPEGB", got)
	}
	if got := entries[wantAttPath]; !bytes.Equal(got, []byte("PDF")) {
		t.Errorf("attachment binary = %q, want PDF", got)
	}

	// Empty-but-present data files are still expected to exist.
	for _, name := range []string{"data/collectors.jsonl", "data/specimens.jsonl",
		"data/journal_entries.jsonl", "data/qrsheets.jsonl"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("expected zip entry %q to be present", name)
		}
	}
}

// TestExport_EmptyCollection_StillValidArchive verifies a user with no
// data gets a well-formed archive: zero counts, present-but-empty data
// files, no images.
func TestExport_EmptyCollection_StillValidArchive(t *testing.T) {
	h := newExportHarness(t)

	_, entries := doExport(t, h.h)

	var man exportManifest
	mustJSON(t, entries["manifest.json"], &man)
	if (man.Counts != exportCounts{}) {
		t.Errorf("empty export counts = %+v, want all zero", man.Counts)
	}
	if len(man.Images) != 0 {
		t.Errorf("empty export images = %d, want 0", len(man.Images))
	}
	for _, name := range []string{"data/collectors.jsonl", "data/specimens.jsonl",
		"data/journal_entries.jsonl", "data/qrsheets.jsonl"} {
		if data, ok := entries[name]; !ok {
			t.Errorf("expected zip entry %q present", name)
		} else if len(jsonlLines(data)) != 0 {
			t.Errorf("entry %q should be empty, got %d lines", name, len(jsonlLines(data)))
		}
	}
}

// TestExport_NotWired_Returns404 confirms the registration gate: with
// no Export deps the route is unregistered and the /api/v1 catch-all
// 404s.
func TestExport_NotWired_Returns404(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when export not wired", rec.Code)
	}
}

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if len(b) == 0 {
		t.Fatalf("empty JSON payload for %T", v)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal %T: %v (raw=%s)", v, err, b)
	}
}
