package portability

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fullArchive builds a representative archive exercising every entity
// type and cross-reference, and returns the bytes plus the archive-local
// ids used so assertions can reference them.
func fullArchive(t *testing.T) (raw []byte, ids struct {
	collector, specimen, photoFile, journalFile, entry, qrsheet string
}) {
	t.Helper()
	b := newArchiveBuilder()
	ids.collector = newUUIDStr()
	ids.specimen = newUUIDStr()
	ids.photoFile = newUUIDStr()
	ids.journalFile = newUUIDStr()
	ids.entry = newUUIDStr()
	ids.qrsheet = newUUIDStr()

	b.add(CollectorsPath, CollectorRecord{
		ID: ids.collector, Name: "Jane Collector", Notes: strptr("note"),
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
	})

	imgFile := b.addFile(ids.photoFile, "image/png", tinyPNG(t))
	b.addFile(ids.journalFile, "application/pdf", []byte("%PDF-1.4 fake"))

	b.add(SpecimensPath, SpecimenRecord{
		ID: ids.specimen, Type: "mineral", CatalogNumber: strptr("CAT-1"),
		Name: "Quartz", Description: "desc", Visibility: "private",
		PriceCents: i64ptr(1000), Tagged: true,
		MainImageFileID: strptr(imgFile.ID),
		VisibilityPrice: strptr("public"),
		CreatedAt:       time.Unix(3, 0).UTC(), UpdatedAt: time.Unix(4, 0).UTC(),
		CollectorIDs: []string{ids.collector},
	})

	b.add(PhotosPath, PhotoRecord{
		ID: newUUIDStr(), SpecimenID: ids.specimen, FileID: ids.photoFile,
		Kind: "visible", Position: 1, CreatedAt: time.Unix(5, 0).UTC(),
	})

	b.add(JournalEntriesPath, JournalEntryRecord{
		ID: ids.entry, SpecimenID: ids.specimen, BodyMD: "hello",
		CreatedAt: time.Unix(6, 0).UTC(), UpdatedAt: time.Unix(7, 0).UTC(),
		Attachments: []JournalAttachment{{FileID: ids.journalFile, Position: 1, CreatedAt: time.Unix(8, 0).UTC()}},
	})

	b.add(QRSheetsPath, QRSheetRecord{
		ID: ids.qrsheet, Template: "avery-5160",
		CreatedAt: time.Unix(9, 0).UTC(), UpdatedAt: time.Unix(10, 0).UTC(),
		Specimens: []QRSheetSpecimen{{SpecimenID: ids.specimen, Position: 1, AddedAt: time.Unix(11, 0).UTC()}},
	})

	b.manifest.Counts = Counts{Collectors: 1, Files: 2, Specimens: 1, Photos: 1, JournalEntries: 1, QRSheets: 1}
	return b.build(t), ids
}

func newUUIDStr() string    { return uuid.Must(uuid.NewV7()).String() }
func i64ptr(v int64) *int64 { return &v }

func TestImport_Commit_Full(t *testing.T) {
	raw, ids := fullArchive(t)
	c := newCapture()
	store := newFakeObjectStore()
	imp := NewImporter(testDeps(c, store))

	report, err := imp.Run(importerCtx(), raw, false)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !report.Committed || report.DryRun {
		t.Fatalf("expected committed non-dry-run report, got %+v", report)
	}
	if len(c.collectors) != 1 || len(c.specimens) != 1 || len(c.files) != 2 ||
		len(c.photos) != 1 || len(c.journal) != 1 || len(c.journalFiles) != 1 || len(c.qrSheets) != 1 {
		t.Fatalf("unexpected created counts: %+v", c)
	}

	// IDs are regenerated (not the archive-local strings) but
	// cross-references stay internally consistent.
	spec := c.specimens[0]
	if spec.ID.String() == ids.specimen {
		t.Errorf("specimen id was not regenerated")
	}
	if spec.CatalogNumber == nil || *spec.CatalogNumber != "CAT-1" {
		t.Errorf("catalog number not preserved: %v", spec.CatalogNumber)
	}
	if !spec.CreatedAt.Equal(time.Unix(3, 0).UTC()) {
		t.Errorf("created_at not preserved: %v", spec.CreatedAt)
	}
	if spec.MainImageID == nil || *spec.MainImageID != c.files[0].ID {
		t.Errorf("main_image_id not remapped to the new file id: %v", spec.MainImageID)
	}
	if c.photos[0].SpecimenID != spec.ID {
		t.Errorf("photo.specimen_id not remapped: got %v want %v", c.photos[0].SpecimenID, spec.ID)
	}
	// photo's file is the image file (first created).
	if c.photos[0].FileID != c.files[0].ID {
		t.Errorf("photo.file_id not remapped to image file")
	}
	if c.journal[0].SpecimenID != spec.ID {
		t.Errorf("journal.specimen_id not remapped")
	}
	if c.journalFiles[0].EntryID != c.journal[0].ID {
		t.Errorf("journal attachment entry_id not remapped")
	}
	// collector chain remapped to the new collector id.
	chain, ok := c.chains[spec.ID]
	if !ok || len(chain) != 1 || chain[0] != c.collectors[0].ID {
		t.Errorf("specimen-collector chain not remapped: %v", c.chains)
	}
	// QR sheet created for the importer + one membership.
	if len(c.qrMembers) != 1 || c.qrMembers[0].Specimen != spec.ID {
		t.Errorf("qr sheet membership not remapped: %+v", c.qrMembers)
	}

	// File binaries uploaded under new keys; image got display+thumb.
	newImgKey := FileBinaryPrefix + c.files[0].ID.String()
	if _, ok := store.objects[newImgKey]; !ok {
		t.Errorf("original image not uploaded at %s", newImgKey)
	}
	if _, ok := store.objects[newImgKey+".display.jpg"]; !ok {
		t.Errorf("display variant not uploaded")
	}
	if _, ok := store.objects[newImgKey+".thumb.jpg"]; !ok {
		t.Errorf("thumb variant not uploaded")
	}
	if len(report.ImageFailures) != 0 {
		t.Errorf("unexpected image failures: %v", report.ImageFailures)
	}
}

func TestImport_DryRun_WritesNothing(t *testing.T) {
	raw, _ := fullArchive(t)
	c := newCapture()
	store := newFakeObjectStore()
	imp := NewImporter(testDeps(c, store))

	report, err := imp.Run(importerCtx(), raw, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !report.DryRun || report.Committed {
		t.Fatalf("expected dry-run, non-committed report: %+v", report)
	}
	if report.Counts.Specimens != 1 || report.Counts.Files != 2 || report.Counts.QRSheets != 1 {
		t.Errorf("dry-run counts wrong: %+v", report.Counts)
	}
	if len(c.specimens)+len(c.collectors)+len(c.files)+len(c.photos)+len(c.journal)+len(c.qrSheets) != 0 {
		t.Errorf("dry-run wrote to the DB: %+v", c)
	}
	if len(store.objects) != 0 {
		t.Errorf("dry-run wrote to object storage: %v", store.objects)
	}
}

func TestImport_CatalogConflict_Suffixes(t *testing.T) {
	raw, ids := fullArchive(t)
	c := newCapture()
	store := newFakeObjectStore()
	// Importer already uses "CAT-1".
	imp := NewImporter(testDeps(c, store, "CAT-1"))

	report, err := imp.Run(importerCtx(), raw, false)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(report.Conflicts) != 1 || report.Conflicts[0].Kind != ConflictCatalogNumber {
		t.Fatalf("expected one catalog conflict, got %+v", report.Conflicts)
	}
	if report.Conflicts[0].SpecimenID != ids.specimen {
		t.Errorf("conflict references wrong specimen: %v", report.Conflicts[0])
	}
	if c.specimens[0].CatalogNumber == nil || *c.specimens[0].CatalogNumber != "CAT-1 (import 1)" {
		t.Errorf("expected suffixed catalog number, got %v", c.specimens[0].CatalogNumber)
	}
}

func TestImport_RejectsNewerSchema(t *testing.T) {
	b := newArchiveBuilder()
	b.manifest.SchemaVersion = SchemaVersion + 1
	raw := b.build(t)
	imp := NewImporter(testDeps(newCapture(), newFakeObjectStore()))

	_, err := imp.Run(importerCtx(), raw, true)
	assertValidationCode(t, err, CodeIncompatibleSchema)
}

func TestImport_RejectsWrongApplication(t *testing.T) {
	b := newArchiveBuilder()
	b.manifest.Application = "not-minerals"
	raw := b.build(t)
	imp := NewImporter(testDeps(newCapture(), newFakeObjectStore()))

	_, err := imp.Run(importerCtx(), raw, true)
	assertValidationCode(t, err, CodeMalformedArchive)
}

func TestImport_RejectsBrokenReference(t *testing.T) {
	b := newArchiveBuilder()
	b.add(SpecimensPath, SpecimenRecord{
		ID: newUUIDStr(), Type: "mineral", Name: "X", Visibility: "private",
		CollectorIDs: []string{newUUIDStr()}, // references a collector not in the archive
	})
	raw := b.build(t)
	imp := NewImporter(testDeps(newCapture(), newFakeObjectStore()))

	_, err := imp.Run(importerCtx(), raw, true)
	assertValidationCode(t, err, CodeReference)
}

func TestImport_RejectsHashMismatch(t *testing.T) {
	b := newArchiveBuilder()
	rec := b.addFile(newUUIDStr(), "image/png", tinyPNG(t))
	// Corrupt the recorded hash so the binary fails verification.
	for i := range b.jsonl[FilesPath] {
		fr := b.jsonl[FilesPath][i].(FileRecord)
		if fr.ID == rec.ID {
			fr.SHA256 = "deadbeef"
			b.jsonl[FilesPath][i] = fr
		}
	}
	raw := b.build(t)
	imp := NewImporter(testDeps(newCapture(), newFakeObjectStore()))

	_, err := imp.Run(importerCtx(), raw, true)
	assertValidationCode(t, err, CodeIntegrity)
}

func TestImport_RejectsMissingBinary(t *testing.T) {
	b := newArchiveBuilder()
	// Record a file but never add its binary.
	b.add(FilesPath, FileRecord{
		ID: newUUIDStr(), Path: FileBinaryPrefix + "missing",
		ContentType: "image/png", ByteSize: 3, SHA256: "abc",
	})
	raw := b.build(t)
	imp := NewImporter(testDeps(newCapture(), newFakeObjectStore()))

	_, err := imp.Run(importerCtx(), raw, true)
	assertValidationCode(t, err, CodeIntegrity)
}

func TestImport_RejectsNotAZip(t *testing.T) {
	imp := NewImporter(testDeps(newCapture(), newFakeObjectStore()))
	_, err := imp.Run(importerCtx(), []byte("definitely not a zip"), true)
	assertValidationCode(t, err, CodeMalformedArchive)
}

func TestImport_QRSheetSkippedWhenExisting(t *testing.T) {
	raw, _ := fullArchive(t)
	c := newCapture()
	c.existingSheet = true // importer already has a sheet
	store := newFakeObjectStore()
	imp := NewImporter(testDeps(c, store))

	report, err := imp.Run(importerCtx(), raw, false)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(c.qrSheets) != 0 {
		t.Errorf("expected no QR sheet created when one exists, got %d", len(c.qrSheets))
	}
	if report.Counts.QRSheets != 0 {
		t.Errorf("expected QRSheets count 0, got %d", report.Counts.QRSheets)
	}
	if len(report.Warnings) == 0 {
		t.Errorf("expected a warning about the skipped QR sheet")
	}
}

func TestImport_ImageUploadFailureIsBestEffort(t *testing.T) {
	raw, _ := fullArchive(t)
	c := newCapture()
	store := newFakeObjectStore()
	store.failAll = true // every object-store write fails
	imp := NewImporter(testDeps(c, store))

	report, err := imp.Run(importerCtx(), raw, false)
	if err != nil {
		t.Fatalf("commit must succeed despite upload failures: %v", err)
	}
	if !report.Committed {
		t.Fatalf("expected DB commit even when uploads fail")
	}
	// Both file originals fail to upload; failures are reported, not fatal.
	if len(report.ImageFailures) == 0 {
		t.Fatalf("expected image upload failures to be reported")
	}
	// DB rows are still present (the source of truth committed).
	if len(c.specimens) != 1 || len(c.files) != 2 {
		t.Errorf("expected DB rows committed despite upload failure: %+v", c)
	}
}

func assertValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error with code %q, got nil", code)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Code != code {
		t.Fatalf("expected code %q, got %q (%s)", code, ve.Code, ve.Message)
	}
}
