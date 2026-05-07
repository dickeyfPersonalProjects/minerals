// Package storage wraps the AWS SDK v2 S3 client we use against
// MinIO. Variant generation, EXIF filtering, and other per-feature
// logic land in subsequent beads (per CONTRACT.md §12); this file
// exposes only the raw transport.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Client is the application-facing wrapper around s3.Client. It is
// scoped to a single bucket — the runtime configuration determines
// which one (per CONTRACT.md §12).
type Client struct {
	api    *s3.Client
	bucket string
}

// Options carries the values needed to build a Client. They mirror
// the §15 env vars: endpoint, access key, secret key, region, bucket.
type Options struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	Bucket          string
}

// New constructs a Client. The S3 client is configured with
// UsePathStyle: true (per §12 / §16) so it works against MinIO.
func New(ctx context.Context, opts Options) (*Client, error) {
	cfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion(opts.Region),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			opts.AccessKeyID, opts.SecretAccessKey, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load aws config: %w", err)
	}
	api := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if opts.Endpoint != "" {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		}
	})
	return &Client{api: api, bucket: opts.Bucket}, nil
}

// Bucket returns the bucket name the client targets.
func (c *Client) Bucket() string { return c.bucket }

// Upload writes body to the configured bucket under key with the
// supplied content type.
func (c *Client) Upload(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := c.api.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage: upload %s: %w", key, err)
	}
	return nil
}

// ErrAlreadyExists is returned by UploadIfNotExists when the conditional
// put rejected the write because an object with that key already exists.
var ErrAlreadyExists = errors.New("storage: object already exists")

// UploadIfNotExists is the conditional-put variant required by §12 for
// the original-bytes write step: it sends If-None-Match: * so the
// underlying object store (MinIO / S3) refuses to overwrite an existing
// key. Returns ErrAlreadyExists on the rejection path; other errors
// surface as wrapped errors.
func (c *Client) UploadIfNotExists(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := c.api.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("storage: upload %s: %w", key, err)
	}
	return nil
}

// Download fetches the object at key. The caller MUST close the
// returned io.ReadCloser. Headers carry the object's stored metadata
// (Content-Type, Content-Length, ETag) for the handler to set on the
// HTTP response (per §17 file-serving hygiene).
func (c *Client) Download(ctx context.Context, key string) (io.ReadCloser, http.Header, error) {
	out, err := c.api.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("storage: download %s: %w", key, err)
	}
	hdr := http.Header{}
	if out.ContentType != nil {
		hdr.Set("Content-Type", *out.ContentType)
	}
	if out.ContentLength != nil {
		hdr.Set("Content-Length", fmt.Sprintf("%d", *out.ContentLength))
	}
	if out.ETag != nil {
		hdr.Set("ETag", *out.ETag)
	}
	return out.Body, hdr, nil
}

// ErrNotFound is returned by Download when the requested key does not
// exist in the bucket.
var ErrNotFound = errors.New("storage: object not found")

// Delete removes the object at key. Missing objects are not treated
// as errors (idempotent semantics).
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.api.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}

// EnsureBucket idempotently creates the configured bucket. Per §12
// this is invoked at startup ONLY when ENV=dev (or unset); production
// expects the bucket to already exist.
func (c *Client) EnsureBucket(ctx context.Context) error {
	_, err := c.api.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err == nil {
		return nil
	}
	if !isNotFound(err) {
		return fmt.Errorf("storage: head bucket %s: %w", c.bucket, err)
	}
	_, err = c.api.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		var already *types.BucketAlreadyOwnedByYou
		if errors.As(err, &already) {
			return nil
		}
		return fmt.Errorf("storage: create bucket %s: %w", c.bucket, err)
	}
	return nil
}

// HeadBucket exposes a HeadBucket call for /readyz.
func (c *Client) HeadBucket(ctx context.Context) error {
	_, err := c.api.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		return fmt.Errorf("storage: head bucket %s: %w", c.bucket, err)
	}
	return nil
}

// isPreconditionFailed reports whether err is a 412 from S3 (the
// outcome of a failed If-None-Match: * conditional put).
func isPreconditionFailed(err error) bool {
	var resp *smithyhttp.ResponseError
	if errors.As(err, &resp) && resp.HTTPStatusCode() == http.StatusPreconditionFailed {
		return true
	}
	return false
}

// isNotFound reports whether err is a "404"-like response from S3.
// We rely on the smithy http response wrapper rather than string-
// matching error messages.
func isNotFound(err error) bool {
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var noSuchBucket *types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return true
	}
	var resp *smithyhttp.ResponseError
	if errors.As(err, &resp) && resp.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	return false
}
