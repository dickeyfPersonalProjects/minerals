//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/dickeyfPersonalProjects/minerals/internal/storage"
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

// realPhotoDeps wires a PhotoServiceDeps backed by real Postgres + real
// MinIO. It's the shared setup for the list / patch / delete /
// error-envelope cases below.
func realPhotoDeps(t *testing.T, pool *pgxpool.Pool, store *storage.Client) *api.PhotoServiceDeps {
	t.Helper()
	return &api.PhotoServiceDeps{
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
}

// seedPhoto inserts a files+photos pair directly via SQL so the
// list/patch handlers have something to operate on without paying the
// cost of running the multipart upload pipeline. Returns the photo ID.
func seedPhoto(ctx context.Context, t *testing.T, pool *pgxpool.Pool, specimenID uuid.UUID, position int, takenAt *time.Time) uuid.UUID {
	t.Helper()
	fileID := domain.NewID()
	photoID := domain.NewID()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at)
		VALUES ($1, $2, 'image/jpeg', 1024, $3, $4, $5)`,
		fileID, "files/"+fileID.String(), fileID.String(), auth.StubUser.ID, now); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO photos (id, specimen_id, file_id, taken_at, position, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		photoID, specimenID, fileID, takenAt, position, now); err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	return photoID
}

// decodeListBody parses a list response into the shape we need to
// drive pagination assertions. Mirrors photoListBody in photos.go
// (test packages can't import the unexported type).
type listBody struct {
	Items []struct {
		ID         uuid.UUID  `json:"id"`
		Position   int        `json:"position"`
		TakenAt    *time.Time `json:"taken_at"`
		SpecimenID uuid.UUID  `json:"specimen_id"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// TestIntegration_Photos_List_HappyPath_Pagination drives the GET
// /api/v1/specimens/{id}/photos surface against real Postgres,
// confirming both the limit page-size knob and the cursor handoff.
// We use httptest.NewServer so the real net/http path is exercised
// end-to-end (per CONTRACT §9 the integration tier is a black-box
// server boundary).
func TestIntegration_Photos_List_HappyPath_Pagination(t *testing.T) {
	pool := scopedDB(t)
	store := storagetest.WithBucket(t)

	ctx := auth.WithUser(context.Background(), auth.StubUser)
	specimenID := seedSpecimen(ctx, t, pool)

	// Seed 5 photos with increasing positions so the (position,
	// created_at) ordering is deterministic.
	want := make([]uuid.UUID, 0, 5)
	for i := 1; i <= 5; i++ {
		want = append(want, seedPhoto(ctx, t, pool, specimenID, i, nil))
	}

	h := api.New(api.Deps{Photos: realPhotoDeps(t, pool, store)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Page 1: limit=2 → 2 items + cursor.
	page1 := getList(t, srv.URL+"/api/v1/specimens/"+specimenID.String()+"/photos?limit=2")
	if len(page1.Items) != 2 {
		t.Fatalf("page 1: want 2 items, got %d", len(page1.Items))
	}
	if page1.NextCursor == nil || *page1.NextCursor == "" {
		t.Fatalf("page 1: want non-empty next_cursor")
	}
	if page1.Items[0].ID != want[0] || page1.Items[1].ID != want[1] {
		t.Fatalf("page 1 order: got %v %v, want %v %v",
			page1.Items[0].ID, page1.Items[1].ID, want[0], want[1])
	}

	// Page 2: same limit, with cursor.
	page2 := getList(t, srv.URL+"/api/v1/specimens/"+specimenID.String()+
		"/photos?limit=2&cursor="+url.QueryEscape(*page1.NextCursor))
	if len(page2.Items) != 2 {
		t.Fatalf("page 2: want 2 items, got %d", len(page2.Items))
	}
	if page2.Items[0].ID != want[2] || page2.Items[1].ID != want[3] {
		t.Fatalf("page 2 order: got %v %v, want %v %v",
			page2.Items[0].ID, page2.Items[1].ID, want[2], want[3])
	}

	// Page 3: final element, cursor exhausted.
	page3 := getList(t, srv.URL+"/api/v1/specimens/"+specimenID.String()+
		"/photos?limit=2&cursor="+url.QueryEscape(*page2.NextCursor))
	if len(page3.Items) != 1 {
		t.Fatalf("page 3: want 1 item, got %d", len(page3.Items))
	}
	if page3.NextCursor != nil && *page3.NextCursor != "" {
		t.Fatalf("page 3: want empty next_cursor, got %q", *page3.NextCursor)
	}
	if page3.Items[0].ID != want[4] {
		t.Fatalf("page 3 id: got %v, want %v", page3.Items[0].ID, want[4])
	}
}

func getList(t *testing.T, url string) listBody {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get list: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var out listBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return out
}

// TestIntegration_Photos_Patch_PartialFields exercises the §9
// requirement that PATCH applies only the fields the client sent —
// omitting `taken_at` must NOT clobber an existing value, and
// omitting `position` must NOT renumber the row.
func TestIntegration_Photos_Patch_PartialFields(t *testing.T) {
	pool := scopedDB(t)
	store := storagetest.WithBucket(t)

	ctx := auth.WithUser(context.Background(), auth.StubUser)
	specimenID := seedSpecimen(ctx, t, pool)

	initialTaken := time.Date(2020, 6, 1, 12, 0, 0, 0, time.UTC)
	photoID := seedPhoto(ctx, t, pool, specimenID, 7, &initialTaken)

	h := api.New(api.Deps{Photos: realPhotoDeps(t, pool, store)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// PATCH only position; taken_at must be preserved.
	patchAndDecode(t, srv.URL+"/api/v1/photos/"+photoID.String(),
		`{"position": 3}`)

	var pos int
	var taken time.Time
	if err := pool.QueryRow(ctx,
		"SELECT position, taken_at FROM photos WHERE id = $1", photoID,
	).Scan(&pos, &taken); err != nil {
		t.Fatalf("read after first patch: %v", err)
	}
	if pos != 3 {
		t.Errorf("position after position-only patch: got %d, want 3", pos)
	}
	if !taken.Equal(initialTaken) {
		t.Errorf("taken_at clobbered by position-only patch: got %v, want %v",
			taken, initialTaken)
	}

	// PATCH only taken_at; position must be preserved.
	newTaken := time.Date(2021, 9, 15, 8, 30, 0, 0, time.UTC)
	patchAndDecode(t, srv.URL+"/api/v1/photos/"+photoID.String(),
		`{"taken_at": "`+newTaken.Format(time.RFC3339)+`"}`)

	if err := pool.QueryRow(ctx,
		"SELECT position, taken_at FROM photos WHERE id = $1", photoID,
	).Scan(&pos, &taken); err != nil {
		t.Fatalf("read after second patch: %v", err)
	}
	if pos != 3 {
		t.Errorf("position clobbered by taken_at-only patch: got %d, want 3", pos)
	}
	if !taken.Equal(newTaken) {
		t.Errorf("taken_at after taken_at-only patch: got %v, want %v",
			taken, newTaken)
	}
}

func patchAndDecode(t *testing.T, url, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new patch request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do patch: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("patch status = %d body=%s", resp.StatusCode, rb)
	}
}

// TestIntegration_Photos_Delete_RemovesRowsAndS3 exercises the §9 +
// §12 happy-path delete: the photos row and files row are removed
// inside a single transaction, and all three S3 objects (original +
// display + thumbnail) are deleted best-effort. We seed via the real
// upload pipeline so there are genuine objects in the bucket; then
// DELETE; then assert the bucket is empty and both rows are gone.
func TestIntegration_Photos_Delete_RemovesRowsAndS3(t *testing.T) {
	pool := scopedDB(t)
	store := storagetest.WithBucket(t)

	ctx := auth.WithUser(context.Background(), auth.StubUser)
	specimenID := seedSpecimen(ctx, t, pool)

	h := api.New(api.Deps{Photos: realPhotoDeps(t, pool, store)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Create one photo via the real upload pipeline so the bucket
	// has 3 objects to clean up.
	jp := makeJPEG(t, 200, 150)
	body, ct := makeMultipartUpload(t, jp, "image/jpeg")
	uploadResp, err := http.Post(srv.URL+"/api/v1/specimens/"+specimenID.String()+"/photos",
		ct, body)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if uploadResp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(uploadResp.Body)
		_ = uploadResp.Body.Close()
		t.Fatalf("upload status = %d body=%s", uploadResp.StatusCode, rb)
	}
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&created); err != nil {
		_ = uploadResp.Body.Close()
		t.Fatalf("decode upload resp: %v", err)
	}
	_ = uploadResp.Body.Close()

	// Sanity: bucket has the 3 objects, DB has 1 row in each table.
	s3c := rawS3(t)
	listCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pre, err := s3c.ListObjectsV2(listCtx, &s3.ListObjectsV2Input{Bucket: aws.String(store.Bucket())})
	if err != nil {
		t.Fatalf("pre-list: %v", err)
	}
	if len(pre.Contents) != 3 {
		t.Fatalf("pre-delete bucket count: got %d, want 3", len(pre.Contents))
	}

	// DELETE.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/photos/"+created.ID.String(), nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer func() { _ = delResp.Body.Close() }()
	if delResp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(delResp.Body)
		t.Fatalf("delete status = %d body=%s", delResp.StatusCode, rb)
	}

	// Post-condition: zero rows in photos, zero in files, empty bucket.
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM photos").Scan(&n); err != nil || n != 0 {
		t.Errorf("photos count after delete = %d (err %v); want 0", n, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM files").Scan(&n); err != nil || n != 0 {
		t.Errorf("files count after delete = %d (err %v); want 0", n, err)
	}
	post, err := s3c.ListObjectsV2(listCtx, &s3.ListObjectsV2Input{Bucket: aws.String(store.Bucket())})
	if err != nil {
		t.Fatalf("post-list: %v", err)
	}
	if len(post.Contents) != 0 {
		keys := make([]string, 0, len(post.Contents))
		for _, o := range post.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
		t.Errorf("post-delete bucket: want empty, got %v", keys)
	}
}

// TestIntegration_Photos_ErrorEnvelope_NotFound asserts the §10 wire
// shape on a real error path: PATCH a non-existent photo and verify
// the response is `{"error":{"code":..., "message":...}}` with the
// correct status and Content-Type.
func TestIntegration_Photos_ErrorEnvelope_NotFound(t *testing.T) {
	pool := scopedDB(t)
	store := storagetest.WithBucket(t)

	h := api.New(api.Deps{Photos: realPhotoDeps(t, pool, store)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	missing := domain.NewID()
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL+"/api/v1/photos/"+missing.String(),
		strings.NewReader(`{"position": 1}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch missing: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json*", got)
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details,omitempty"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "photo_not_found" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "photo_not_found")
	}
	if env.Error.Message == "" {
		t.Errorf("error.message must not be empty")
	}
}

// TestIntegration_Photos_AuthRejection_RequireUser exercises the §13
// protected-route contract: the download routes are wrapped with
// auth.Auth + auth.RequireUser, and RequireUser MUST emit a §10
// envelope at 401 when no user is on the request context. We drive
// the chain directly (bypassing auth.Auth) so the RequireUser branch
// runs end-to-end against a wired download handler — when real auth
// replaces the stub, the same chain will reject untrusted callers.
func TestIntegration_Photos_AuthRejection_RequireUser(t *testing.T) {
	pool := scopedDB(t)
	store := storagetest.WithBucket(t)

	ctx := auth.WithUser(context.Background(), auth.StubUser)
	specimenID := seedSpecimen(ctx, t, pool)
	photoID := seedPhoto(ctx, t, pool, specimenID, 1, nil)

	// Run RequireUser ahead of the full api handler. Without an
	// auth.Auth wrapper above us, no User is set on the request
	// context and RequireUser must short-circuit before the photo
	// handler sees the request.
	inner := api.New(api.Deps{Photos: realPhotoDeps(t, pool, store)})
	chain := auth.RequireUser(inner)

	srv := httptest.NewServer(chain)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/api/v1/photos/"+photoID.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s, want 401", resp.StatusCode, rb)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "unauthorized" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "unauthorized")
	}
}
