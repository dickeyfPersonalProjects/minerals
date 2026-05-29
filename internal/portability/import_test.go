package portability

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// --- archive builder -----------------------------------------------------

// archiveBuilder assembles an in-memory ZIP for tests: JSONL entity
// collections, file binaries (with hashes auto-recorded), and a manifest.
type archiveBuilder struct {
	manifest Manifest
	jsonl    map[string][]any // path -> records
	binaries map[string][]byte
	files    []FileRecord
}

func newArchiveBuilder() *archiveBuilder {
	return &archiveBuilder{
		manifest: Manifest{SchemaVersion: SchemaVersion, Application: Application, ExportedAt: time.Unix(0, 0).UTC(), ExportedBy: "exporter-sub"},
		jsonl:    map[string][]any{},
		binaries: map[string][]byte{},
	}
}

func (b *archiveBuilder) add(path string, rec any) *archiveBuilder {
	b.jsonl[path] = append(b.jsonl[path], rec)
	return b
}

// addFile records a FileRecord (with computed hash/size) and its binary
// at the conventional path.
func (b *archiveBuilder) addFile(id, contentType string, data []byte) FileRecord {
	sum := sha256.Sum256(data)
	rec := FileRecord{
		ID:          id,
		Path:        FileBinaryPrefix + id,
		ContentType: contentType,
		ByteSize:    int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		UploadedAt:  time.Unix(0, 0).UTC(),
	}
	b.files = append(b.files, rec)
	b.add(FilesPath, rec)
	b.binaries[rec.Path] = data
	return rec
}

func (b *archiveBuilder) build(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	writeEntry := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	mf, err := json.Marshal(b.manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeEntry(ManifestPath, mf)

	for path, recs := range b.jsonl {
		var lines bytes.Buffer
		for _, r := range recs {
			line, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("marshal %s record: %v", path, err)
			}
			lines.Write(line)
			lines.WriteByte('\n')
		}
		writeEntry(path, lines.Bytes())
	}
	for path, data := range b.binaries {
		writeEntry(path, data)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// tinyPNG returns a decodable 2x2 PNG so imageproc.Generate succeeds.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func strptr(s string) *string { return &s }

// --- in-memory fakes -----------------------------------------------------

type capture struct {
	mu           sync.Mutex
	collectors   []domain.Collector
	files        []domain.File
	specimens    []domain.Specimen
	photos       []domain.Photo
	journal      []domain.JournalEntry
	journalFiles []domain.JournalEntryFile
	chains       map[uuid.UUID][]uuid.UUID
	qrSheets     []domain.QRSheet
	qrMembers    []struct {
		User, Specimen uuid.UUID
		AddedAt        time.Time
	}
	existingSheet bool
}

func newCapture() *capture { return &capture{chains: map[uuid.UUID][]uuid.UUID{}} }

// fake repos. Only the methods the engine calls do real work; the rest
// satisfy the interfaces with zero behavior.

type fakeCollectors struct{ c *capture }

func (f fakeCollectors) Create(_ context.Context, _ domain.Tx, c domain.Collector) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.collectors = append(f.c.collectors, c)
	return nil
}
func (fakeCollectors) GetByID(context.Context, uuid.UUID) (domain.Collector, error) {
	return domain.Collector{}, domain.ErrCollectorNotFound
}
func (fakeCollectors) Update(context.Context, domain.Tx, domain.Collector) error { return nil }
func (fakeCollectors) Delete(context.Context, domain.Tx, uuid.UUID) error        { return nil }
func (fakeCollectors) List(context.Context, domain.CollectorFilter, domain.Page) ([]domain.Collector, domain.Cursor, error) {
	return nil, "", nil
}

type fakeFiles struct{ c *capture }

func (f fakeFiles) Create(_ context.Context, _ domain.Tx, file domain.File) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.files = append(f.c.files, file)
	return nil
}
func (fakeFiles) GetByID(context.Context, uuid.UUID) (domain.File, error) {
	return domain.File{}, domain.ErrFileNotFound
}
func (fakeFiles) Delete(context.Context, domain.Tx, uuid.UUID) error { return nil }

type fakeSpecimens struct{ c *capture }

func (f fakeSpecimens) Create(_ context.Context, _ domain.Tx, s domain.Specimen) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.specimens = append(f.c.specimens, s)
	return nil
}
func (fakeSpecimens) GetByID(context.Context, uuid.UUID) (domain.Specimen, error) {
	return domain.Specimen{}, domain.ErrSpecimenNotFound
}
func (fakeSpecimens) Update(context.Context, domain.Tx, domain.Specimen) error { return nil }
func (fakeSpecimens) Delete(context.Context, domain.Tx, uuid.UUID) error       { return nil }
func (fakeSpecimens) List(context.Context, domain.SpecimenFilter, domain.Page) ([]domain.Specimen, domain.Cursor, error) {
	return nil, "", nil
}
func (fakeSpecimens) HasPhotoWithFile(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

type fakePhotos struct{ c *capture }

func (f fakePhotos) Create(_ context.Context, _ domain.Tx, p domain.Photo) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.photos = append(f.c.photos, p)
	return nil
}
func (fakePhotos) GetByID(context.Context, uuid.UUID) (domain.Photo, error) {
	return domain.Photo{}, domain.ErrPhotoNotFound
}
func (fakePhotos) Update(context.Context, domain.Tx, domain.Photo) error { return nil }
func (fakePhotos) Delete(context.Context, domain.Tx, uuid.UUID) error    { return nil }
func (fakePhotos) ListBySpecimen(context.Context, uuid.UUID, domain.Page) ([]domain.Photo, domain.Cursor, error) {
	return nil, "", nil
}
func (fakePhotos) MaxPosition(context.Context, domain.Tx, uuid.UUID) (int, error) { return 0, nil }

type fakeJournal struct{ c *capture }

func (f fakeJournal) Create(_ context.Context, _ domain.Tx, e domain.JournalEntry) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.journal = append(f.c.journal, e)
	return nil
}
func (fakeJournal) GetByID(context.Context, uuid.UUID) (domain.JournalEntry, error) {
	return domain.JournalEntry{}, domain.ErrJournalEntryNotFound
}
func (fakeJournal) Update(context.Context, domain.Tx, domain.JournalEntry) error { return nil }
func (fakeJournal) Delete(context.Context, domain.Tx, uuid.UUID) error           { return nil }
func (fakeJournal) ListBySpecimen(context.Context, uuid.UUID, domain.Page) ([]domain.JournalEntry, domain.Cursor, error) {
	return nil, "", nil
}

type fakeJournalFiles struct{ c *capture }

func (f fakeJournalFiles) Create(_ context.Context, _ domain.Tx, j domain.JournalEntryFile) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.journalFiles = append(f.c.journalFiles, j)
	return nil
}
func (fakeJournalFiles) GetByFileID(context.Context, uuid.UUID) (domain.JournalEntryFile, error) {
	return domain.JournalEntryFile{}, domain.ErrJournalAttachmentNotFound
}
func (fakeJournalFiles) ListByEntry(context.Context, uuid.UUID) ([]domain.JournalEntryFile, error) {
	return nil, nil
}
func (fakeJournalFiles) Delete(context.Context, domain.Tx, uuid.UUID) error { return nil }
func (fakeJournalFiles) MaxPosition(context.Context, domain.Tx, uuid.UUID) (int, error) {
	return 0, nil
}

type fakeSpecimenCollectors struct{ c *capture }

func (f fakeSpecimenCollectors) GetChain(context.Context, domain.Tx, uuid.UUID) ([]domain.SpecimenCollectorLink, error) {
	return nil, nil
}
func (f fakeSpecimenCollectors) ReplaceChain(_ context.Context, _ domain.Tx, specimenID uuid.UUID, ids []uuid.UUID) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.chains[specimenID] = ids
	return nil
}

type fakeQRSheets struct{ c *capture }

func (f fakeQRSheets) GetByUser(_ context.Context, _ uuid.UUID) (domain.QRSheet, error) {
	if f.c.existingSheet {
		return domain.QRSheet{ID: domain.NewID()}, nil
	}
	return domain.QRSheet{}, domain.ErrQRSheetNotFound
}
func (f fakeQRSheets) Create(_ context.Context, _ domain.Tx, s domain.QRSheet) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.qrSheets = append(f.c.qrSheets, s)
	return nil
}
func (fakeQRSheets) UpdateTemplate(context.Context, domain.Tx, uuid.UUID, domain.QRSheetTemplate, time.Time) error {
	return nil
}
func (fakeQRSheets) Delete(context.Context, domain.Tx, uuid.UUID) error { return nil }
func (f fakeQRSheets) AddSpecimen(_ context.Context, _ domain.Tx, userID, specimenID uuid.UUID, addedAt time.Time) error {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.qrMembers = append(f.c.qrMembers, struct {
		User, Specimen uuid.UUID
		AddedAt        time.Time
	}{userID, specimenID, addedAt})
	return nil
}
func (fakeQRSheets) RemoveSpecimen(context.Context, domain.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (fakeQRSheets) ListSpecimens(context.Context, uuid.UUID) ([]domain.QRSheetEntry, error) {
	return nil, nil
}

// fakeObjectStore records uploads.
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	failKey string // exact key that should fail on upload
	failAll bool   // when true, every upload fails
}

func newFakeObjectStore() *fakeObjectStore { return &fakeObjectStore{objects: map[string][]byte{}} }

func (s *fakeObjectStore) UploadIfNotExists(_ context.Context, key string, body io.Reader, _ string) error {
	return s.put(key, body)
}
func (s *fakeObjectStore) Upload(_ context.Context, key string, body io.Reader, _ string) error {
	return s.put(key, body)
}
func (s *fakeObjectStore) put(key string, body io.Reader) error {
	if s.failAll || (s.failKey != "" && key == s.failKey) {
		return errors.New("simulated upload failure")
	}
	data, _ := io.ReadAll(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	return nil
}
func (s *fakeObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// testDeps builds an Importer over fresh fakes and returns both.
func testDeps(c *capture, store *fakeObjectStore, existingCatalogs ...string) Deps {
	existing := map[string]struct{}{}
	for _, e := range existingCatalogs {
		existing[e] = struct{}{}
	}
	return Deps{
		Collectors:         fakeCollectors{c},
		Files:              fakeFiles{c},
		Specimens:          fakeSpecimens{c},
		Photos:             fakePhotos{c},
		Journal:            fakeJournal{c},
		JournalFiles:       fakeJournalFiles{c},
		SpecimenCollectors: fakeSpecimenCollectors{c},
		QRSheets:           fakeQRSheets{c},
		Storage:            store,
		RunInTx:            func(_ context.Context, fn func(tx domain.Tx) error) error { return fn(nil) },
		CatalogNumbers: func(context.Context, uuid.UUID) (map[string]struct{}, error) {
			return existing, nil
		},
	}
}

func importerCtx() context.Context {
	return auth.WithUser(context.Background(), auth.User{ID: domain.NewID(), Sub: "importer-sub"})
}
