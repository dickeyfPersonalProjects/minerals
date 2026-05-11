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

// seedSpecimenSQL inserts a bare-bones specimens row and returns its id.
// QR sheet tests reuse this for the FK targets — the QR feature
// itself doesn't care about specimen content beyond id + name.
func seedSpecimenSQL(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', $2, $3, $4, $4)`,
		id, name, uuid.MustParse("00000000-0000-0000-0000-000000000001"), now)
	if err != nil {
		t.Fatalf("seed specimen %q: %v", name, err)
	}
	return id
}

func mkSheet(userID uuid.UUID, template domain.QRSheetTemplate) domain.QRSheet {
	now := time.Now().UTC()
	return domain.QRSheet{
		ID:        domain.NewID(),
		UserID:    userID,
		Template:  template,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestIntegration_QRSheet_CreateAndGet(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	sheet := mkSheet(userID, "avery-5160")
	if err := repo.Create(ctx, nil, sheet); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByUser(ctx, userID)
	if err != nil {
		t.Fatalf("get by user: %v", err)
	}
	if got.ID != sheet.ID || got.Template != "avery-5160" {
		t.Errorf("got %+v want id=%s template=avery-5160", got, sheet.ID)
	}
}

func TestIntegration_QRSheet_GetMissingReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	_, err := repo.GetByUser(authedCtx(), uuid.New())
	if !errors.Is(err, domain.ErrQRSheetNotFound) {
		t.Fatalf("got %v, want ErrQRSheetNotFound", err)
	}
}

func TestIntegration_QRSheet_CreateDuplicateUserConflict(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := repo.Create(ctx, nil, mkSheet(userID, "avery-5163"))
	if !errors.Is(err, domain.ErrQRSheetConflict) {
		t.Fatalf("got %v, want ErrQRSheetConflict", err)
	}
}

func TestIntegration_QRSheet_UpdateTemplate(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("create: %v", err)
	}

	// timestamptz round-trips at microsecond precision, so truncate
	// the test value before asserting equality.
	newTime := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	if err := repo.UpdateTemplate(ctx, nil, userID, "avery-l7160", newTime); err != nil {
		t.Fatalf("update template: %v", err)
	}

	got, err := repo.GetByUser(ctx, userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Template != "avery-l7160" {
		t.Errorf("template = %q want avery-l7160", got.Template)
	}
	if !got.UpdatedAt.Equal(newTime) {
		t.Errorf("updated_at not bumped: %v vs %v", got.UpdatedAt, newTime)
	}
}

func TestIntegration_QRSheet_UpdateTemplateMissingReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	err := repo.UpdateTemplate(authedCtx(), nil, uuid.New(), "avery-5160", time.Now().UTC())
	if !errors.Is(err, domain.ErrQRSheetNotFound) {
		t.Fatalf("got %v, want ErrQRSheetNotFound", err)
	}
}

func TestIntegration_QRSheet_DeleteCascadesSpecimens(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("create: %v", err)
	}
	spec := seedSpecimenSQL(t, pool, "to-cascade")
	if err := repo.AddSpecimen(ctx, nil, userID, spec, time.Now().UTC()); err != nil {
		t.Fatalf("add specimen: %v", err)
	}

	if err := repo.Delete(ctx, nil, userID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Sheet gone, sheet-specimens cascaded.
	if _, err := repo.GetByUser(ctx, userID); !errors.Is(err, domain.ErrQRSheetNotFound) {
		t.Fatalf("post-delete get: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM qr_sheet_specimens WHERE specimen_id = $1`, spec,
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Errorf("expected cascade delete, %d rows remain", remaining)
	}
}

func TestIntegration_QRSheet_AddSpecimen_AppendsAndIsIdempotent(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("create: %v", err)
	}
	specA := seedSpecimenSQL(t, pool, "A")
	specB := seedSpecimenSQL(t, pool, "B")
	now := time.Now().UTC()

	if err := repo.AddSpecimen(ctx, nil, userID, specA, now); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if err := repo.AddSpecimen(ctx, nil, userID, specB, now); err != nil {
		t.Fatalf("add B: %v", err)
	}
	// Idempotency: re-adding A is a success and does NOT shift positions.
	if err := repo.AddSpecimen(ctx, nil, userID, specA, now); err != nil {
		t.Fatalf("re-add A: %v", err)
	}

	sheet, _ := repo.GetByUser(ctx, userID)
	entries, err := repo.ListSpecimens(ctx, sheet.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2; entries=%+v", len(entries), entries)
	}
	if entries[0].SpecimenID != specA || entries[0].Position != 1 {
		t.Errorf("entry[0] = %+v want specA@1", entries[0])
	}
	if entries[1].SpecimenID != specB || entries[1].Position != 2 {
		t.Errorf("entry[1] = %+v want specB@2", entries[1])
	}
}

func TestIntegration_QRSheet_AddSpecimen_NoSheetReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	spec := seedSpecimenSQL(t, pool, "orphan")
	err := repo.AddSpecimen(ctx, nil, uuid.New(), spec, time.Now().UTC())
	if !errors.Is(err, domain.ErrQRSheetNotFound) {
		t.Fatalf("got %v, want ErrQRSheetNotFound", err)
	}
}

func TestIntegration_QRSheet_AddSpecimen_MissingSpecimenReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := repo.AddSpecimen(ctx, nil, userID, uuid.New(), time.Now().UTC())
	if !errors.Is(err, domain.ErrSpecimenNotFound) {
		t.Fatalf("got %v, want ErrSpecimenNotFound", err)
	}
}

func TestIntegration_QRSheet_RemoveSpecimen_RepacksPositions(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().UTC()
	specs := []uuid.UUID{
		seedSpecimenSQL(t, pool, "spec-A"),
		seedSpecimenSQL(t, pool, "spec-B"),
		seedSpecimenSQL(t, pool, "spec-C"),
		seedSpecimenSQL(t, pool, "spec-D"),
	}
	for _, s := range specs {
		if err := repo.AddSpecimen(ctx, nil, userID, s, now); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	// Remove the middle specimen (was position 2).
	if err := repo.RemoveSpecimen(ctx, nil, userID, specs[1]); err != nil {
		t.Fatalf("remove: %v", err)
	}

	sheet, _ := repo.GetByUser(ctx, userID)
	entries, err := repo.ListSpecimens(ctx, sheet.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	wantOrder := []uuid.UUID{specs[0], specs[2], specs[3]}
	if len(entries) != len(wantOrder) {
		t.Fatalf("len = %d want %d", len(entries), len(wantOrder))
	}
	for i, e := range entries {
		if e.SpecimenID != wantOrder[i] {
			t.Errorf("entry[%d].SpecimenID = %s, want %s", i, e.SpecimenID, wantOrder[i])
		}
		if e.Position != i+1 {
			t.Errorf("entry[%d].Position = %d, want %d (no gaps)", i, e.Position, i+1)
		}
	}
}

func TestIntegration_QRSheet_RemoveSpecimen_NoSheetReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	err := repo.RemoveSpecimen(authedCtx(), nil, uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrQRSheetNotFound) {
		t.Fatalf("got %v, want ErrQRSheetNotFound", err)
	}
}

func TestIntegration_QRSheet_RemoveSpecimen_NotOnSheetReturnsNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := repo.RemoveSpecimen(ctx, nil, userID, uuid.New())
	if !errors.Is(err, domain.ErrQRSheetSpecimenNotFound) {
		t.Fatalf("got %v, want ErrQRSheetSpecimenNotFound", err)
	}
}

func TestIntegration_QRSheet_ListSpecimens_IncludesFirstPhoto(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewQRSheetPostgres(pool)
	files := db.NewFilePostgres(pool)
	photos := db.NewPhotoPostgres(pool)
	ctx := authedCtx()

	userID := uuid.New()
	if err := repo.Create(ctx, nil, mkSheet(userID, "avery-5160")); err != nil {
		t.Fatalf("create: %v", err)
	}
	withPhoto := seedSpecimenSQL(t, pool, "with-photo")
	withoutPhoto := seedSpecimenSQL(t, pool, "without-photo")

	// Seed two photos for the first specimen so we can confirm the
	// LATERAL picks the lowest-position one.
	now := time.Now().UTC()
	mkPhoto := func(specID uuid.UUID, position int) uuid.UUID {
		fileID := domain.NewID()
		if err := files.Create(ctx, nil, domain.File{
			ID: fileID, S3Key: "k/" + fileID.String(),
			ContentType: "image/jpeg", ByteSize: 1, SHA256: "x",
			UploadedAt: now,
		}); err != nil {
			t.Fatalf("file: %v", err)
		}
		pid := domain.NewID()
		if err := photos.Create(ctx, nil, domain.Photo{
			ID: pid, SpecimenID: specID, FileID: fileID,
			Position: position, CreatedAt: now,
		}); err != nil {
			t.Fatalf("photo: %v", err)
		}
		return pid
	}
	firstPhotoID := mkPhoto(withPhoto, 1)
	_ = mkPhoto(withPhoto, 2)

	if err := repo.AddSpecimen(ctx, nil, userID, withPhoto, now); err != nil {
		t.Fatalf("add 1: %v", err)
	}
	if err := repo.AddSpecimen(ctx, nil, userID, withoutPhoto, now); err != nil {
		t.Fatalf("add 2: %v", err)
	}

	sheet, _ := repo.GetByUser(ctx, userID)
	entries, err := repo.ListSpecimens(ctx, sheet.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d", len(entries))
	}
	if entries[0].FirstPhotoID == nil || *entries[0].FirstPhotoID != firstPhotoID {
		t.Errorf("first photo id = %v, want %s", entries[0].FirstPhotoID, firstPhotoID)
	}
	if entries[0].SpecimenName != "with-photo" {
		t.Errorf("name = %q", entries[0].SpecimenName)
	}
	if entries[1].FirstPhotoID != nil {
		t.Errorf("specimen without photo got id %v", entries[1].FirstPhotoID)
	}
}
