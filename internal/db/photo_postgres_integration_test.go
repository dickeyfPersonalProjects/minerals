//go:build integration

package db_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

func TestIntegration_PhotoCreateAndGet(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()

	// Seed a specimen so photo FK is satisfiable.
	specID := uuid.New()
	now := time.Now().UTC()
	_, err := pool.Exec(ctx, `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', 'photo-test', $2, $3, $3)`,
		specID, uuid.MustParse("00000000-0000-0000-0000-000000000001"), now)
	if err != nil {
		t.Fatalf("seed specimen: %v", err)
	}

	files := db.NewFilePostgres(pool)
	photos := db.NewPhotoPostgres(pool)

	fileID := domain.NewID()
	if err := files.Create(ctx, nil, domain.File{
		ID:          fileID,
		S3Key:       "files/" + fileID.String(),
		ContentType: "image/jpeg",
		ByteSize:    1234,
		SHA256:      "abcd",
		UploadedAt:  now,
	}); err != nil {
		t.Fatalf("create file: %v", err)
	}

	pid := domain.NewID()
	taken := now
	if err := photos.Create(ctx, nil, domain.Photo{
		ID:         pid,
		SpecimenID: specID,
		FileID:     fileID,
		TakenAt:    &taken,
		Position:   1,
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("create photo: %v", err)
	}

	got, err := photos.GetByID(ctx, pid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SpecimenID != specID || got.FileID != fileID {
		t.Errorf("ids: %+v", got)
	}
	if got.Position != 1 {
		t.Errorf("position = %d", got.Position)
	}
}

func TestIntegration_PhotoListBySpecimen_OrdersByPosition(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()

	specID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', 'photo-test', $2, $3, $3)`,
		specID, uuid.MustParse("00000000-0000-0000-0000-000000000001"), now); err != nil {
		t.Fatalf("seed specimen: %v", err)
	}
	files := db.NewFilePostgres(pool)
	photos := db.NewPhotoPostgres(pool)

	for _, pos := range []int{3, 1, 2} {
		fid := domain.NewID()
		if err := files.Create(ctx, nil, domain.File{
			ID: fid, S3Key: "files/" + fid.String(),
			ContentType: "image/jpeg", ByteSize: 1, SHA256: "x",
			UploadedAt: now,
		}); err != nil {
			t.Fatalf("create file: %v", err)
		}
		if err := photos.Create(ctx, nil, domain.Photo{
			ID: domain.NewID(), SpecimenID: specID, FileID: fid,
			Position: pos, CreatedAt: now,
		}); err != nil {
			t.Fatalf("create photo: %v", err)
		}
	}

	rows, _, err := photos.ListBySpecimen(ctx, specID, domain.Page{Limit: 50})
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

func TestIntegration_PhotoCreate_FKViolationMapsToNotFound(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	files := db.NewFilePostgres(pool)
	photos := db.NewPhotoPostgres(pool)

	// File row exists, but specimen does NOT.
	fid := domain.NewID()
	now := time.Now().UTC()
	if err := files.Create(ctx, nil, domain.File{
		ID: fid, S3Key: "files/" + fid.String(),
		ContentType: "image/jpeg", ByteSize: 1, SHA256: "x",
		UploadedAt: now,
	}); err != nil {
		t.Fatalf("create file: %v", err)
	}
	err := photos.Create(ctx, nil, domain.Photo{
		ID: domain.NewID(), SpecimenID: uuid.New(), FileID: fid,
		Position: 1, CreatedAt: now,
	})
	if !errors.Is(err, domain.ErrPhotoNotFound) {
		t.Fatalf("got %v, want ErrPhotoNotFound", err)
	}
}

// TestIntegration_PhotoKindRoundtrip exercises the photo_kind enum
// (migrations 0005 + 0007): Create with each allowed value round-trips
// through GetByID; Update changes the value; the empty zero-value falls
// back to 'visible' (matches the column default and the Create defaulting).
func TestIntegration_PhotoKindRoundtrip(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()

	specID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', 'photo-kind-test', $2, $3, $3)`,
		specID, uuid.MustParse("00000000-0000-0000-0000-000000000001"), now); err != nil {
		t.Fatalf("seed specimen: %v", err)
	}
	files := db.NewFilePostgres(pool)
	photos := db.NewPhotoPostgres(pool)

	for _, kind := range []domain.PhotoKind{
		domain.PhotoKindVisible,
		domain.PhotoKindUVSW,
		domain.PhotoKindUVMW,
		domain.PhotoKindUVLW,
		domain.PhotoKindOther,
	} {
		fid := domain.NewID()
		if err := files.Create(ctx, nil, domain.File{
			ID: fid, S3Key: "files/" + fid.String(),
			ContentType: "image/jpeg", ByteSize: 1, SHA256: "k" + string(kind),
			UploadedAt: now,
		}); err != nil {
			t.Fatalf("create file: %v", err)
		}
		pid := domain.NewID()
		if err := photos.Create(ctx, nil, domain.Photo{
			ID: pid, SpecimenID: specID, FileID: fid,
			Kind: kind, Position: 1, CreatedAt: now,
		}); err != nil {
			t.Fatalf("create photo (%s): %v", kind, err)
		}
		got, err := photos.GetByID(ctx, pid)
		if err != nil {
			t.Fatalf("get (%s): %v", kind, err)
		}
		if got.Kind != kind {
			t.Errorf("kind round-trip: got %q, want %q", got.Kind, kind)
		}
	}

	// Zero-value Kind on Create defaults to 'visible'.
	fid := domain.NewID()
	if err := files.Create(ctx, nil, domain.File{
		ID: fid, S3Key: "files/" + fid.String(),
		ContentType: "image/jpeg", ByteSize: 1, SHA256: "default",
		UploadedAt: now,
	}); err != nil {
		t.Fatalf("create file: %v", err)
	}
	pid := domain.NewID()
	if err := photos.Create(ctx, nil, domain.Photo{
		ID: pid, SpecimenID: specID, FileID: fid,
		Position: 1, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create photo (default): %v", err)
	}
	got, err := photos.GetByID(ctx, pid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Kind != domain.PhotoKindVisible {
		t.Errorf("default kind = %q, want visible", got.Kind)
	}

	// Update flips it to UV LW.
	got.Kind = domain.PhotoKindUVLW
	if err := photos.Update(ctx, nil, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, err := photos.GetByID(ctx, pid)
	if err != nil {
		t.Fatalf("re-get: %v", err)
	}
	if after.Kind != domain.PhotoKindUVLW {
		t.Errorf("after update kind = %q, want uv_lw", after.Kind)
	}
}

func TestIntegration_PhotoMaxPosition(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	now := time.Now().UTC()

	specID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO specimens (id, type, name, author_id, created_at, updated_at)
		VALUES ($1, 'mineral', 'photo-test', $2, $3, $3)`,
		specID, uuid.MustParse("00000000-0000-0000-0000-000000000001"), now); err != nil {
		t.Fatalf("seed specimen: %v", err)
	}
	files := db.NewFilePostgres(pool)
	photos := db.NewPhotoPostgres(pool)

	if max, err := photos.MaxPosition(ctx, nil, specID); err != nil || max != 0 {
		t.Errorf("empty MaxPosition = (%d, %v)", max, err)
	}

	for _, pos := range []int{2, 7, 5} {
		fid := domain.NewID()
		if err := files.Create(ctx, nil, domain.File{
			ID: fid, S3Key: "files/" + fid.String(),
			ContentType: "image/jpeg", ByteSize: 1, SHA256: "x",
			UploadedAt: now,
		}); err != nil {
			t.Fatalf("create file: %v", err)
		}
		if err := photos.Create(ctx, nil, domain.Photo{
			ID: domain.NewID(), SpecimenID: specID, FileID: fid,
			Position: pos, CreatedAt: now,
		}); err != nil {
			t.Fatalf("create photo: %v", err)
		}
	}
	max, err := photos.MaxPosition(ctx, nil, specID)
	if err != nil {
		t.Fatalf("MaxPosition: %v", err)
	}
	if max != 7 {
		t.Errorf("MaxPosition = %d, want 7", max)
	}
}
