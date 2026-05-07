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

func TestIntegration_FileCreateAndGet(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	repo := db.NewFilePostgres(pool)

	now := time.Now().UTC()
	f := domain.File{
		ID:          domain.NewID(),
		S3Key:       "files/" + uuid.NewString(),
		ContentType: "image/jpeg",
		ByteSize:    4096,
		SHA256:      "deadbeef",
		UploadedAt:  now,
	}
	if err := repo.Create(ctx, nil, f); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(ctx, f.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.S3Key != f.S3Key || got.ContentType != f.ContentType || got.ByteSize != f.ByteSize {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, f)
	}
}

func TestIntegration_FileCreateDuplicateKeyConflict(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	repo := db.NewFilePostgres(pool)

	key := "files/" + uuid.NewString()
	first := domain.File{
		ID: domain.NewID(), S3Key: key, ContentType: "image/jpeg",
		ByteSize: 1, SHA256: "a", UploadedAt: time.Now().UTC(),
	}
	if err := repo.Create(ctx, nil, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	second := domain.File{
		ID: domain.NewID(), S3Key: key, ContentType: "image/jpeg",
		ByteSize: 1, SHA256: "b", UploadedAt: time.Now().UTC(),
	}
	if err := repo.Create(ctx, nil, second); !errors.Is(err, domain.ErrFileConflict) {
		t.Fatalf("got %v, want ErrFileConflict", err)
	}
}

func TestIntegration_FileGetMissingNotFound(t *testing.T) {
	pool := scopedDB(t)
	repo := db.NewFilePostgres(pool)
	if _, err := repo.GetByID(authedCtx(), uuid.New()); !errors.Is(err, domain.ErrFileNotFound) {
		t.Fatalf("got %v, want ErrFileNotFound", err)
	}
}

func TestIntegration_FileDeleteRemoves(t *testing.T) {
	pool := scopedDB(t)
	ctx := authedCtx()
	repo := db.NewFilePostgres(pool)

	f := domain.File{
		ID: domain.NewID(), S3Key: "files/" + uuid.NewString(),
		ContentType: "image/jpeg", ByteSize: 1, SHA256: "x",
		UploadedAt: time.Now().UTC(),
	}
	if err := repo.Create(ctx, nil, f); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.Delete(ctx, nil, f.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.Delete(ctx, nil, f.ID); !errors.Is(err, domain.ErrFileNotFound) {
		t.Fatalf("second delete: got %v, want ErrFileNotFound", err)
	}
}
