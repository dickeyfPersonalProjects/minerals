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

// seedJournalSpecimen inserts a minimal specimen row so
// journal_entries.specimen_id can satisfy its FK and returns the new id.
func seedJournalSpecimen(t *testing.T, ctx context.Context, pool *pgxpool.Pool, when time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', 'jrnl-test', $2, $3, $3)`,
		id, uuid.MustParse("00000000-0000-0000-0000-000000000001"), when); err != nil {
		t.Fatalf("seed specimen: %v", err)
	}
	return id
}

func TestIntegration_JournalEntry_CreateAndGet(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)

	repo := db.NewJournalEntryPostgres(pool)
	id := domain.NewID()
	if err := repo.Create(ctx, nil, domain.JournalEntry{
		ID: id, SpecimenID: specID, BodyMD: "# initial",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SpecimenID != specID {
		t.Errorf("specimen_id = %s, want %s", got.SpecimenID, specID)
	}
	if got.BodyMD != "# initial" {
		t.Errorf("body_md = %q", got.BodyMD)
	}
	if got.AuthorID == uuid.Nil {
		t.Errorf("author_id was zero (auth context wiring broken)")
	}
}

func TestIntegration_JournalEntry_Create_FKViolationMapsToNotFound(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	repo := db.NewJournalEntryPostgres(pool)

	now := time.Now().UTC()
	err := repo.Create(ctx, nil, domain.JournalEntry{
		ID:         domain.NewID(),
		SpecimenID: uuid.New(), // never inserted
		BodyMD:     "x",
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if !errors.Is(err, domain.ErrJournalEntryNotFound) {
		t.Fatalf("got %v, want ErrJournalEntryNotFound", err)
	}
}

func TestIntegration_JournalEntry_ListPaginates(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)

	repo := db.NewJournalEntryPostgres(pool)
	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(i) * time.Second)
		if err := repo.Create(ctx, nil, domain.JournalEntry{
			ID: domain.NewID(), SpecimenID: specID, BodyMD: "entry",
			CreatedAt: ts, UpdatedAt: ts,
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	page1, cursor, err := repo.ListBySpecimen(ctx, specID, domain.Page{Limit: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 len = %d, want 2", len(page1))
	}
	if cursor == "" {
		t.Fatal("expected non-empty cursor on page 1")
	}

	page2, cursor2, err := repo.ListBySpecimen(ctx, specID, domain.Page{Limit: 2, Cursor: string(cursor)})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2 len = %d, want 2", len(page2))
	}
	page3, cursor3, err := repo.ListBySpecimen(ctx, specID, domain.Page{Limit: 2, Cursor: string(cursor2)})
	if err != nil {
		t.Fatalf("list page 3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page 3 len = %d, want 1", len(page3))
	}
	if cursor3 != "" {
		t.Errorf("page 3 cursor = %q, want empty", cursor3)
	}

	for i, p := range [][]domain.JournalEntry{page1, page2} {
		if !p[0].CreatedAt.After(p[1].CreatedAt) && !p[0].CreatedAt.Equal(p[1].CreatedAt) {
			t.Errorf("page %d not ordered DESC: %v / %v", i+1, p[0].CreatedAt, p[1].CreatedAt)
		}
	}
}

func TestIntegration_JournalEntry_Update_PreservesCreatedAt(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)

	repo := db.NewJournalEntryPostgres(pool)
	id := domain.NewID()
	if err := repo.Create(ctx, nil, domain.JournalEntry{
		ID: id, SpecimenID: specID, BodyMD: "v1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	original, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Caller mutates created_at — repo must ignore it (only body_md
	// and updated_at are in the SET list, see journal_entry_postgres.go).
	later := now.Add(1 * time.Hour)
	bumped := original
	bumped.BodyMD = "v2"
	bumped.CreatedAt = later
	bumped.UpdatedAt = later
	if err := repo.Update(ctx, nil, bumped); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get post-update: %v", err)
	}
	if !got.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("created_at mutated: was %v now %v", original.CreatedAt, got.CreatedAt)
	}
	if got.BodyMD != "v2" {
		t.Errorf("body_md = %q, want v2", got.BodyMD)
	}
}

func TestIntegration_JournalEntry_Update_NotFound(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	repo := db.NewJournalEntryPostgres(pool)

	err := repo.Update(ctx, nil, domain.JournalEntry{
		ID: domain.NewID(), BodyMD: "x", UpdatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, domain.ErrJournalEntryNotFound) {
		t.Errorf("got %v, want ErrJournalEntryNotFound", err)
	}
}

func TestIntegration_JournalEntry_Delete_409WhenAttachmentsExist(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()
	specID := seedJournalSpecimen(t, ctx, pool, now)

	files := db.NewFilePostgres(pool)
	repo := db.NewJournalEntryPostgres(pool)

	entryID := domain.NewID()
	if err := repo.Create(ctx, nil, domain.JournalEntry{
		ID: entryID, SpecimenID: specID, BodyMD: "text",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create entry: %v", err)
	}

	// Insert a journal_entry_files row directly so the entry has an
	// attachment without going through the (not-yet-implemented) C-2
	// upload path.
	fid := domain.NewID()
	if err := files.Create(ctx, nil, domain.File{
		ID: fid, S3Key: "files/" + fid.String(),
		ContentType: "image/jpeg", ByteSize: 1, SHA256: "x",
		UploadedAt: now,
	}); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO journal_entry_files (entry_id, file_id, position, created_at)
		VALUES ($1, $2, 1, $3)`, entryID, fid, now); err != nil {
		t.Fatalf("insert journal_entry_files: %v", err)
	}

	if err := repo.Delete(ctx, nil, entryID); !errors.Is(err, domain.ErrJournalEntryConflict) {
		t.Fatalf("delete with attachments: got %v, want ErrJournalEntryConflict", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM journal_entry_files WHERE entry_id = $1`, entryID); err != nil {
		t.Fatalf("clean attachments: %v", err)
	}
	if err := repo.Delete(ctx, nil, entryID); err != nil {
		t.Fatalf("delete after detach: %v", err)
	}
	if _, err := repo.GetByID(ctx, entryID); !errors.Is(err, domain.ErrJournalEntryNotFound) {
		t.Errorf("get after delete: got %v, want NotFound", err)
	}
}
