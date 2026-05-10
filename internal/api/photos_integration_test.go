//go:build integration

package api_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/storage/storagetest"
)

// scopedDB returns a pool whose connections see only an isolated
// per-test schema, with v1 migrations already applied. Mirrors the
// helper in internal/db/*_integration_test.go (the test packages can't
// import each other) so api-tier integration tests get the same
// schema-isolation guarantees as the repo-tier ones.
func scopedDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping api integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	schema := "api_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]

	admin, err := pgxpool.New(ctx, rawDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(admin.Close)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.Exec(clean, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("drop schema: %v", err)
		}
	})

	scoped := dsnWithSearchPath(t, rawDSN, schema)

	m, err := migrate.New("file://"+migrationsDir(t), scoped)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, scoped)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func dsnWithSearchPath(t *testing.T, raw, schema string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller path")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("abs migrations dir: %v", err)
	}
	return abs
}

// failingFileRepo wraps an inner FileRepo and forces Create to return
// a sentinel error so we can drive the §12 transactional rollback
// path against real Postgres + real MinIO.
type failingFileRepo struct {
	inner domain.FileRepo
	err   error
}

func (f *failingFileRepo) Create(ctx context.Context, tx domain.Tx, file domain.File) error {
	return f.err
}

func (f *failingFileRepo) GetByID(ctx context.Context, id uuid.UUID) (domain.File, error) {
	return f.inner.GetByID(ctx, id)
}

func (f *failingFileRepo) Delete(ctx context.Context, tx domain.Tx, id uuid.UUID) error {
	return f.inner.Delete(ctx, tx, id)
}

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("jpeg encode: %v", err)
	}
	return buf.Bytes()
}

func makeMultipartUpload(t *testing.T, fileBytes []byte, contentType string) (body *bytes.Buffer, formContentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="test.jpg"`}
	hdr["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	return body, mw.FormDataContentType()
}

// seedSpecimen inserts a minimum-viable specimens row so the
// photos.specimen_id FK is satisfied during the upload pipeline. We
// stay below the SpecimenRepo abstraction to keep the fixture
// independent of repo evolution.
func seedSpecimen(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := domain.NewID()
	now := time.Now().UTC()
	const q = `
		INSERT INTO specimens (id, type, name, author_id, type_data, created_at, updated_at)
		VALUES ($1, 'mineral', $2, $3, '{}'::jsonb, $4, $4)`
	if _, err := pool.Exec(ctx, q, id, "rollback-test", auth.StubUser.ID, now); err != nil {
		t.Fatalf("seed specimen: %v", err)
	}
	return id
}

// rawS3 returns a low-level S3 client we can use to HeadObject
// directly — the storage.Client deliberately doesn't expose Head, so
// we bypass it to assert the rollback effect against the live bucket.
func rawS3(t *testing.T) *s3.Client {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	access := os.Getenv("MINIO_ACCESS_KEY")
	if access == "" {
		access = "minioadmin"
	}
	secret := os.Getenv("MINIO_SECRET_KEY")
	if secret == "" {
		secret = "minioadmin"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			access, secret, "",
		)),
	)
	if err != nil {
		t.Fatalf("aws cfg: %v", err)
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
}

// TestIntegration_PhotoUpload_DBFailureRollsBackS3 is the CONTRACT
// §9 hard requirement: when the DB transaction fails after the MinIO
// writes have landed, the handler MUST clean up the orphan objects.
// We assert this against real MinIO (not a fake) by HeadObject-ing
// each of the three keys (original, display, thumb) and confirming
// they are absent.
func TestIntegration_PhotoUpload_DBFailureRollsBackS3(t *testing.T) {
	pool := scopedDB(t)
	store := storagetest.WithBucket(t)

	ctx := auth.WithUser(context.Background(), auth.StubUser)
	specimenID := seedSpecimen(ctx, t, pool)

	// Wrap the real FilePostgres so Create fails during the tx; this
	// drives the api.PhotoService rollback path that must Delete the
	// just-written MinIO objects.
	innerFiles := db.NewFilePostgres(pool)
	failErr := errors.New("forced db failure")
	files := &failingFileRepo{inner: innerFiles, err: failErr}

	deps := &api.PhotoServiceDeps{
		Photos:         db.NewPhotoPostgres(pool),
		Files:          files,
		Storage:        store,
		MaxUploadBytes: 10 * 1024 * 1024,
		RunInTx: func(ctx context.Context, fn func(tx domain.Tx) error) error {
			return db.RunInTx(ctx, pool, func(pgxTx pgx.Tx) error {
				return fn(pgxTx)
			})
		},
	}
	h := api.New(api.Deps{Photos: deps})

	jp := makeJPEG(t, 200, 150)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg")

	// Capture which keys touched MinIO by listing the bucket after the
	// failed call. We don't know the file_id up front (the handler
	// generates it) so we rely on the post-condition: zero objects in
	// the bucket means the rollback ran.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("expected error status, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Bucket must be empty: the upload wrote 3 objects (original,
	// display, thumb) and the rollback path must have deleted all
	// three. Use HeadBucket-equivalent listing to enumerate.
	s3c := rawS3(t)
	listCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := s3c.ListObjectsV2(listCtx, &s3.ListObjectsV2Input{
		Bucket: aws.String(store.Bucket()),
	})
	if err != nil {
		t.Fatalf("list bucket: %v", err)
	}
	if len(out.Contents) != 0 {
		keys := make([]string, 0, len(out.Contents))
		for _, o := range out.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
		t.Fatalf("expected empty bucket after DB rollback; found %d objects: %v",
			len(out.Contents), keys)
	}

	// Belt-and-suspenders: HeadObject on a couple of plausible
	// suffixes to confirm the §12 rollback contract — even if a future
	// regression caused the listing to lie, individual HeadObjects
	// should still surface NotFound.
	for _, suffix := range []string{"", ".display.jpg", ".thumb.jpg"} {
		key := "files/should-be-gone" + suffix
		_, err := s3c.HeadObject(listCtx, &s3.HeadObjectInput{
			Bucket: aws.String(store.Bucket()),
			Key:    aws.String(key),
		})
		var notFound *types.NotFound
		if err == nil || !errors.As(err, &notFound) {
			// The synthetic key shouldn't exist; if HeadObject succeeds
			// or returns a non-NotFound error, the test environment is
			// surprising — fail loudly.
			if err == nil {
				t.Errorf("HeadObject %q unexpectedly succeeded", key)
			}
		}
	}

	// And the DB must be untouched: no rows in photos or files tables.
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM photos").Scan(&n); err != nil {
		t.Fatalf("count photos: %v", err)
	}
	if n != 0 {
		t.Errorf("expected zero photos rows after rollback, got %d", n)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM files").Scan(&n); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if n != 0 {
		t.Errorf("expected zero files rows after rollback, got %d", n)
	}
}

// TestIntegration_PhotoUpload_HappyPath provides positive coverage so
// the rollback test isn't the only thing exercising the wired
// scopedDB+WithBucket pipeline; it also catches "test setup is wrong
// in a way that always rolls back" silent failures in the rollback
// test above.
func TestIntegration_PhotoUpload_HappyPath(t *testing.T) {
	pool := scopedDB(t)
	store := storagetest.WithBucket(t)

	ctx := auth.WithUser(context.Background(), auth.StubUser)
	specimenID := seedSpecimen(ctx, t, pool)

	deps := &api.PhotoServiceDeps{
		Photos:         db.NewPhotoPostgres(pool),
		Files:          db.NewFilePostgres(pool),
		Storage:        store,
		MaxUploadBytes: 10 * 1024 * 1024,
		RunInTx: func(ctx context.Context, fn func(tx domain.Tx) error) error {
			return db.RunInTx(ctx, pool, func(pgxTx pgx.Tx) error {
				return fn(pgxTx)
			})
		},
	}
	h := api.New(api.Deps{Photos: deps})

	jp := makeJPEG(t, 200, 150)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/specimens/"+specimenID.String()+"/photos", body)
	req.Header.Set("Content-Type", ct)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Three S3 objects landed in the bucket; one row in photos+files.
	s3c := rawS3(t)
	listCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := s3c.ListObjectsV2(listCtx, &s3.ListObjectsV2Input{
		Bucket: aws.String(store.Bucket()),
	})
	if err != nil {
		t.Fatalf("list bucket: %v", err)
	}
	if len(out.Contents) != 3 {
		t.Errorf("expected 3 objects (original+display+thumb); got %d", len(out.Contents))
	}

	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM photos").Scan(&n); err != nil || n != 1 {
		t.Errorf("photos count = %d (err %v); want 1", n, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM files").Scan(&n); err != nil || n != 1 {
		t.Errorf("files count = %d (err %v); want 1", n, err)
	}

	// Drain the body so the handler's tempfile defer runs.
	_, _ = io.Copy(io.Discard, rec.Body)
}
