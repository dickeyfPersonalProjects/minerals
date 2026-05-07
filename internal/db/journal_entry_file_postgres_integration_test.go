//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// seedJournalEntry inserts a journal entry for use as a parent of
// journal_entry_files rows in tests.
func seedJournalEntry(t *testing.T, ctx context.Context, pool *pgxpool.Pool, specID uuid.UUID, when time.Time) uuid.UUID {
	t.Helper()
	repo := db.NewJournalEntryPostgres(pool)
	id := domain.NewID()
	if err := repo.Create(ctx, nil, domain.JournalEntry{
		ID: id, SpecimenID: specID, BodyMD: "attachments-test",
		CreatedAt: when, UpdatedAt: when,
	}); err != nil {
		t.Fatalf("seed journal entry: %v", err)
	}
	return id
}

func seedFile(t *testing.T, ctx context.Context, files *db.FilePostgres, contentType string, when time.Time) uuid.UUID {
	t.Helper()
	id := domain.NewID()
	if err := files.Create(ctx, nil, domain.File{
		ID: id, S3Key: "files/" + id.String(),
		ContentType: contentType, ByteSize: 12, SHA256: "deadbeef",
		UploadedAt: when,
	}); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	return id
}

func TestIntegration_JournalEntryFile_CreateAndGet(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)
	entryID := seedJournalEntry(t, ctx, pool, specID, now)

	files := db.NewFilePostgres(pool)
	attachments := db.NewJournalEntryFilePostgres(pool)
	fileID := seedFile(t, ctx, files, "application/pdf", now)

	if err := attachments.Create(ctx, nil, domain.JournalEntryFile{
		EntryID: entryID, FileID: fileID, Position: 1, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	got, err := attachments.GetByFileID(ctx, fileID)
	if err != nil {
		t.Fatalf("get by file id: %v", err)
	}
	if got.EntryID != entryID || got.FileID != fileID || got.Position != 1 {
		t.Errorf("unexpected row: %+v", got)
	}
}

func TestIntegration_JournalEntryFile_Create_FKViolationMapsToNotFound(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()

	files := db.NewFilePostgres(pool)
	fileID := seedFile(t, ctx, files, "application/pdf", now)

	attachments := db.NewJournalEntryFilePostgres(pool)
	// entry_id never inserted — FK violation expected.
	err := attachments.Create(ctx, nil, domain.JournalEntryFile{
		EntryID: uuid.New(), FileID: fileID, Position: 1, CreatedAt: now,
	})
	if !errors.Is(err, domain.ErrJournalEntryNotFound) {
		t.Fatalf("got %v, want ErrJournalEntryNotFound", err)
	}
}

func TestIntegration_JournalEntryFile_ListByEntry_OrdersByPosition(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)
	entryID := seedJournalEntry(t, ctx, pool, specID, now)

	files := db.NewFilePostgres(pool)
	attachments := db.NewJournalEntryFilePostgres(pool)
	for _, pos := range []int{3, 1, 2} {
		fid := seedFile(t, ctx, files, "application/pdf", now)
		if err := attachments.Create(ctx, nil, domain.JournalEntryFile{
			EntryID: entryID, FileID: fid, Position: pos, CreatedAt: now,
		}); err != nil {
			t.Fatalf("create %d: %v", pos, err)
		}
	}

	rows, err := attachments.ListByEntry(ctx, entryID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len = %d", len(rows))
	}
	for i, want := range []int{1, 2, 3} {
		if rows[i].Position != want {
			t.Errorf("rows[%d].position = %d, want %d", i, rows[i].Position, want)
		}
	}
}

func TestIntegration_JournalEntryFile_MaxPosition(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)
	entryID := seedJournalEntry(t, ctx, pool, specID, now)

	files := db.NewFilePostgres(pool)
	attachments := db.NewJournalEntryFilePostgres(pool)

	if max, err := attachments.MaxPosition(ctx, nil, entryID); err != nil || max != 0 {
		t.Errorf("empty MaxPosition = (%d, %v)", max, err)
	}

	for _, pos := range []int{2, 7, 5} {
		fid := seedFile(t, ctx, files, "application/pdf", now)
		if err := attachments.Create(ctx, nil, domain.JournalEntryFile{
			EntryID: entryID, FileID: fid, Position: pos, CreatedAt: now,
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	max, err := attachments.MaxPosition(ctx, nil, entryID)
	if err != nil {
		t.Fatalf("MaxPosition: %v", err)
	}
	if max != 7 {
		t.Errorf("MaxPosition = %d, want 7", max)
	}
}

func TestIntegration_JournalEntryFile_DeleteRemovesRow(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)
	entryID := seedJournalEntry(t, ctx, pool, specID, now)

	files := db.NewFilePostgres(pool)
	attachments := db.NewJournalEntryFilePostgres(pool)
	fileID := seedFile(t, ctx, files, "application/pdf", now)
	if err := attachments.Create(ctx, nil, domain.JournalEntryFile{
		EntryID: entryID, FileID: fileID, Position: 1, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := attachments.Delete(ctx, nil, fileID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := attachments.GetByFileID(ctx, fileID); !errors.Is(err, domain.ErrJournalAttachmentNotFound) {
		t.Errorf("after delete, GetByFileID = %v, want ErrJournalAttachmentNotFound", err)
	}

	if err := attachments.Delete(ctx, nil, fileID); !errors.Is(err, domain.ErrJournalAttachmentNotFound) {
		t.Errorf("delete missing = %v, want ErrJournalAttachmentNotFound", err)
	}
}
