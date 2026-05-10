//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/storage"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage/storagetest"
)

func intCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestIntegration_UploadAndDownloadRoundtrip(t *testing.T) {
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	key := "round/" + uuid.NewString()
	want := []byte("hello, minio")
	if err := c.Upload(ctx, key, bytes.NewReader(want), "text/plain"); err != nil {
		t.Fatalf("upload: %v", err)
	}

	body, hdr, err := c.Download(ctx, key)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("body mismatch: got %q want %q", got, want)
	}
	if ct := hdr.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("content-type = %q, want text/plain", ct)
	}
}

func TestIntegration_UploadIfNotExists_HappyPath(t *testing.T) {
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	key := "cond/" + uuid.NewString()
	if err := c.UploadIfNotExists(ctx, key, strings.NewReader("first"), "text/plain"); err != nil {
		t.Fatalf("first put: %v", err)
	}
	body, _, err := c.Download(ctx, key)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, _ := io.ReadAll(body)
	if string(got) != "first" {
		t.Errorf("body = %q, want %q", got, "first")
	}
}

// TestIntegration_UploadIfNotExists_AlreadyExists exercises the
// isPreconditionFailed (412) classifier path: a second conditional put
// to the same key must surface storage.ErrAlreadyExists rather than a
// raw S3 error, and must not overwrite the original object.
func TestIntegration_UploadIfNotExists_AlreadyExists(t *testing.T) {
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	key := "cond/" + uuid.NewString()
	if err := c.UploadIfNotExists(ctx, key, strings.NewReader("first"), "text/plain"); err != nil {
		t.Fatalf("first put: %v", err)
	}
	err := c.UploadIfNotExists(ctx, key, strings.NewReader("second"), "text/plain")
	if !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("second put: got %v, want ErrAlreadyExists", err)
	}

	// Original bytes survived.
	body, _, err := c.Download(ctx, key)
	if err != nil {
		t.Fatalf("download after rejected overwrite: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, _ := io.ReadAll(body)
	if string(got) != "first" {
		t.Errorf("conditional-put rejection still overwrote bytes: got %q", got)
	}
}

// TestIntegration_Download_NotFound exercises the isNotFound classifier
// path on the GetObject side.
func TestIntegration_Download_NotFound(t *testing.T) {
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	_, _, err := c.Download(ctx, "missing/"+uuid.NewString())
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestIntegration_Delete_HappyPath(t *testing.T) {
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	key := "del/" + uuid.NewString()
	if err := c.Upload(ctx, key, strings.NewReader("payload"), "text/plain"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := c.Download(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("post-delete download: got %v, want ErrNotFound", err)
	}
}

// TestIntegration_Delete_Idempotent confirms the §12 cleanup rule:
// deleting a non-existent key is not an error so handler rollback
// paths can iterate without partial-state guards.
func TestIntegration_Delete_Idempotent(t *testing.T) {
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	if err := c.Delete(ctx, "never-existed/"+uuid.NewString()); err != nil {
		t.Errorf("delete missing key: %v", err)
	}
}

func TestIntegration_EnsureBucket_Idempotent(t *testing.T) {
	// WithBucket already calls EnsureBucket once; calling it again
	// against the same client must succeed without creating a duplicate
	// or returning an error.
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	if err := c.EnsureBucket(ctx); err != nil {
		t.Errorf("second EnsureBucket: %v", err)
	}
	if err := c.EnsureBucket(ctx); err != nil {
		t.Errorf("third EnsureBucket: %v", err)
	}
}

func TestIntegration_HeadBucket_Exists(t *testing.T) {
	c := storagetest.WithBucket(t)
	ctx := intCtx(t)

	if err := c.HeadBucket(ctx); err != nil {
		t.Errorf("head existing bucket: %v", err)
	}
}

// TestIntegration_HeadBucket_NotExists exercises the isNotFound
// classifier path on the HeadBucket side: a freshly-built client
// pointed at a never-created bucket must surface a non-nil error
// (which /readyz then reports as DEGRADED per §13).
func TestIntegration_HeadBucket_NotExists(t *testing.T) {
	endpoint := envOrSkip(t, "MINIO_ENDPOINT")
	access := envOr("MINIO_ACCESS_KEY", "minioadmin")
	secret := envOr("MINIO_SECRET_KEY", "minioadmin")

	ctx := intCtx(t)
	missing := "missing-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	c, err := storage.New(ctx, storage.Options{
		Endpoint:        endpoint,
		AccessKeyID:     access,
		SecretAccessKey: secret,
		Region:          "us-east-1",
		Bucket:          missing,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := c.HeadBucket(ctx); err == nil {
		t.Errorf("HeadBucket on missing bucket returned nil; want error")
	}
}

// TestIntegration_EnsureBucket_CreatesMissing exercises the create
// side of EnsureBucket against a name we know doesn't exist yet.
func TestIntegration_EnsureBucket_CreatesMissing(t *testing.T) {
	endpoint := envOrSkip(t, "MINIO_ENDPOINT")
	access := envOr("MINIO_ACCESS_KEY", "minioadmin")
	secret := envOr("MINIO_SECRET_KEY", "minioadmin")

	ctx := intCtx(t)
	bucket := "ens-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	c, err := storage.New(ctx, storage.Options{
		Endpoint:        endpoint,
		AccessKeyID:     access,
		SecretAccessKey: secret,
		Region:          "us-east-1",
		Bucket:          bucket,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = rawDeleteBucket(clean, endpoint, access, secret, bucket)
	})

	if err := c.EnsureBucket(ctx); err != nil {
		t.Fatalf("first EnsureBucket: %v", err)
	}
	if err := c.HeadBucket(ctx); err != nil {
		t.Errorf("HeadBucket after EnsureBucket: %v", err)
	}
}

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set; skipping storage integration test", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func rawDeleteBucket(ctx context.Context, endpoint, access, secret, bucket string) error {
	cfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			access, secret, "",
		)),
	)
	if err != nil {
		return err
	}
	api := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(endpoint)
	})
	_, err = api.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	return err
}
