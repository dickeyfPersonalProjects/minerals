// Package storagetest provides per-test scaffolding around a real
// MinIO/S3 endpoint for integration-tier tests of internal/storage and
// the upload pipelines that depend on it (CONTRACT.md §9 / §12 / §15).
//
// Each call to WithBucket creates a uniquely-named bucket via the
// configured endpoint, returns a wired *storage.Client scoped to that
// bucket, and registers a t.Cleanup that empties and removes the
// bucket on test exit. The CI service container provides MinIO at
// MINIO_ENDPOINT (with MINIO_ACCESS_KEY / MINIO_SECRET_KEY); when
// those env vars are absent the helper t.Skips so unit-tier `go test`
// without MinIO continues to run cleanly.
package storagetest

import (
	"context"
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
)

// Default region used when MINIO_REGION isn't set. MinIO ignores the
// region but the AWS SDK requires one to be configured.
const defaultRegion = "us-east-1"

// WithBucket creates a new MinIO bucket with a UUID-derived name,
// returns a *storage.Client wired to it, and arranges for the bucket
// to be emptied and deleted when t finishes. The test is skipped when
// MINIO_ENDPOINT is empty so the helper is safe to use in code that
// also runs under unit-tier (no-MinIO) `go test`.
func WithBucket(t *testing.T) *storage.Client {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_ENDPOINT not set; skipping storage integration test")
	}
	access := envOr("MINIO_ACCESS_KEY", "minioadmin")
	secret := envOr("MINIO_SECRET_KEY", "minioadmin")
	region := envOr("MINIO_REGION", defaultRegion)

	bucket := "stt-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, err := storage.New(ctx, storage.Options{
		Endpoint:        endpoint,
		AccessKeyID:     access,
		SecretAccessKey: secret,
		Region:          region,
		Bucket:          bucket,
	})
	if err != nil {
		t.Fatalf("storagetest: new client: %v", err)
	}
	if err := client.EnsureBucket(ctx); err != nil {
		t.Fatalf("storagetest: ensure bucket %q: %v", bucket, err)
	}

	// Cleanup uses a fresh raw S3 client so we can list+delete bucket
	// contents (storage.Client deliberately doesn't expose List). The
	// scope of the privilege boundary stays inside this helper.
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := emptyAndDeleteBucket(clean, endpoint, access, secret, region, bucket); err != nil {
			t.Logf("storagetest: cleanup bucket %q: %v", bucket, err)
		}
	})

	return client
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// emptyAndDeleteBucket lists every object in the bucket, deletes them,
// then removes the bucket. Pagination is handled via the V2 paginator
// so test fixtures with many objects (variants, journal attachments)
// don't trip the 1000-object ListObjects ceiling.
func emptyAndDeleteBucket(ctx context.Context, endpoint, access, secret, region, bucket string) error {
	cfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion(region),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			access, secret, "",
		)),
	)
	if err != nil {
		return err
	}
	api := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})

	paginator := s3.NewListObjectsV2Paginator(api, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			if _, err := api.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(bucket),
				Key:    obj.Key,
			}); err != nil {
				return err
			}
		}
	}

	_, err = api.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	return err
}
