//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
)

// apiFixture wires the full router against a per-test schema and
// returns an httptest.Server the tests can hit end-to-end.
type apiFixture struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	rawDSN := os.Getenv("DATABASE_URL")
	if rawDSN == "" {
		t.Skip("DATABASE_URL not set; skipping specimens API integration tests")
	}
	ctx := context.Background()

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
		cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.Exec(cctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("drop schema: %v", err)
		}
	})

	scoped := dsnWithSearchPath(t, rawDSN, schema)

	mig, err := migrate.New("file://"+migrationsDir(t), scoped)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	if err := mig.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if srcErr, dbErr := mig.Close(); srcErr != nil || dbErr != nil {
		t.Logf("migrate.Close: src=%v db=%v", srcErr, dbErr)
	}

	pool, err := pgxpool.New(ctx, scoped)
	if err != nil {
		t.Fatalf("scoped pool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := db.NewSpecimenPostgres(pool)
	handler := api.New(api.Deps{
		Specimens: api.SpecimensDeps{Repo: repo, Pool: pool},
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &apiFixture{srv: srv, pool: pool}
}

func dsnWithSearchPath(t *testing.T, raw, schema string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(filepath.Join(repoRoot, "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return abs
}

// stubAuthor is the v1 auth.StubUser id (mirrors auth.StubUser.ID).
const stubAuthor = "00000000-0000-0000-0000-000000000001"

// do is a tiny request helper: marshals body to JSON and returns the
// raw response body bytes plus the status.
func (f *apiFixture) do(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, out
}

type specimenViewPayload struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Name          string         `json:"name"`
	AuthorID      string         `json:"author_id"`
	CatalogNumber *string        `json:"catalog_number,omitempty"`
	TypeData      map[string]any `json:"type_data"`
}

func TestAPI_CreateGetRoundtripAllTypes(t *testing.T) {
	fx := newAPIFixture(t)

	cases := []struct {
		typ string
		td  map[string]any
	}{
		{"mineral", map[string]any{"chemical_formula": "SiO2"}},
		{"rock", map[string]any{"rock_type": "igneous"}},
		{"meteorite", map[string]any{"classification": "L6", "fall_or_find": "find"}},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			body := map[string]any{
				"type":      c.typ,
				"name":      c.typ + " sample",
				"type_data": c.td,
			}
			resp, raw := fx.do(t, http.MethodPost, "/api/v1/specimens", body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("create: status=%d body=%s", resp.StatusCode, raw)
			}
			loc := resp.Header.Get("Location")
			if loc == "" {
				t.Fatalf("missing Location header")
			}

			// GET via the Location header round-trips.
			resp2, raw2 := fx.do(t, http.MethodGet, loc, nil)
			if resp2.StatusCode != http.StatusOK {
				t.Fatalf("get: status=%d body=%s", resp2.StatusCode, raw2)
			}
			var v specimenViewPayload
			if err := json.Unmarshal(raw2, &v); err != nil {
				t.Fatalf("decode get: %v", err)
			}
			if v.Type != c.typ {
				t.Errorf("type: got %s want %s", v.Type, c.typ)
			}
			if v.AuthorID != stubAuthor {
				t.Errorf("author_id: got %s want %s", v.AuthorID, stubAuthor)
			}
			if _, err := uuid.Parse(v.ID); err != nil {
				t.Errorf("id not a uuid: %s", v.ID)
			}
			parsed, _ := uuid.Parse(v.ID)
			if parsed.Version() != 7 {
				t.Errorf("id is not UUIDv7: %s", v.ID)
			}
		})
	}
}

func TestAPI_CreateRejectsCatalogNumberConflict(t *testing.T) {
	fx := newAPIFixture(t)

	body := map[string]any{
		"type":           "mineral",
		"name":           "first",
		"catalog_number": "DUP-1",
		"type_data":      map[string]any{},
	}
	resp, raw := fx.do(t, http.MethodPost, "/api/v1/specimens", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d %s", resp.StatusCode, raw)
	}
	resp, raw = fx.do(t, http.MethodPost, "/api/v1/specimens", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup create: status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestAPI_PatchRejectsTypeChange(t *testing.T) {
	fx := newAPIFixture(t)
	resp, raw := fx.do(t, http.MethodPost, "/api/v1/specimens", map[string]any{
		"type": "mineral", "name": "p", "type_data": map[string]any{},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	loc := resp.Header.Get("Location")
	resp, raw = fx.do(t, http.MethodPatch, loc, map[string]any{"type": "rock"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("patch type change: status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestAPI_PatchMergesTypeData(t *testing.T) {
	fx := newAPIFixture(t)

	resp, _ := fx.do(t, http.MethodPost, "/api/v1/specimens", map[string]any{
		"type":      "mineral",
		"name":      "merge me",
		"type_data": map[string]any{"chemical_formula": "SiO2", "color": "clear"},
	})
	loc := resp.Header.Get("Location")

	resp, raw := fx.do(t, http.MethodPatch, loc, map[string]any{
		"type_data": map[string]any{
			"color":          nil,        // clears
			"crystal_system": "trigonal", // adds
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: status=%d body=%s", resp.StatusCode, raw)
	}
	var v specimenViewPayload
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v.TypeData["chemical_formula"] != "SiO2" {
		t.Errorf("chemical_formula was clobbered: %v", v.TypeData)
	}
	if _, ok := v.TypeData["color"]; ok {
		t.Errorf("color should have been cleared: %v", v.TypeData)
	}
	if v.TypeData["crystal_system"] != "trigonal" {
		t.Errorf("crystal_system should have been set: %v", v.TypeData)
	}
}

func TestAPI_DeleteSucceedsAndReturns404OnSecondAttempt(t *testing.T) {
	fx := newAPIFixture(t)
	resp, _ := fx.do(t, http.MethodPost, "/api/v1/specimens", map[string]any{
		"type": "rock", "name": "delete me", "type_data": map[string]any{},
	})
	loc := resp.Header.Get("Location")

	resp, raw := fx.do(t, http.MethodDelete, loc, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d %s", resp.StatusCode, raw)
	}
	resp, raw = fx.do(t, http.MethodGet, loc, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: %d %s", resp.StatusCode, raw)
	}
}

func TestAPI_List_FilterAndSearch(t *testing.T) {
	fx := newAPIFixture(t)

	specs := []map[string]any{
		{"type": "mineral", "name": "quartz cluster",
			"description": "transparent quartz with calcite vugs",
			"type_data":   map[string]any{}},
		{"type": "rock", "name": "granite slab",
			"description": "coarse granite with quartz veins",
			"type_data":   map[string]any{"rock_type": "igneous"}},
		{"type": "meteorite", "name": "L6 chondrite",
			"description": "weathered chondrite, nickel-iron",
			"type_data":   map[string]any{}},
	}
	for _, s := range specs {
		resp, raw := fx.do(t, http.MethodPost, "/api/v1/specimens", s)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed create: %d %s", resp.StatusCode, raw)
		}
	}

	// type=rock filter.
	resp, raw := fx.do(t, http.MethodGet, "/api/v1/specimens?type=rock", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list type: %d %s", resp.StatusCode, raw)
	}
	var page struct {
		Items      []specimenViewPayload `json:"items"`
		NextCursor *string               `json:"next_cursor"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Type != "rock" {
		t.Errorf("type filter: got %d items, expected 1 rock", len(page.Items))
	}

	// q=quartz returns ranked results.
	resp, raw = fx.do(t, http.MethodGet, "/api/v1/specimens?q=quartz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list q: %d %s", resp.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) < 1 {
		t.Fatalf("expected at least 1 hit for quartz, got %d", len(page.Items))
	}
	if !strings.Contains(page.Items[0].Name, "quartz") {
		t.Errorf("expected quartz-titled specimen first; got %q", page.Items[0].Name)
	}
}

func TestAPI_RejectsInvalidTypeData(t *testing.T) {
	fx := newAPIFixture(t)
	resp, raw := fx.do(t, http.MethodPost, "/api/v1/specimens", map[string]any{
		"type":      "rock",
		"name":      "bad",
		"type_data": map[string]any{"rock_type": "andesite"}, // not in allow-list
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", resp.StatusCode, raw)
	}
}

func TestAPI_DeleteFailsWhenChildrenPresent(t *testing.T) {
	fx := newAPIFixture(t)
	resp, _ := fx.do(t, http.MethodPost, "/api/v1/specimens", map[string]any{
		"type": "mineral", "name": "with photos", "type_data": map[string]any{},
	})
	loc := resp.Header.Get("Location")
	id := strings.TrimPrefix(loc, "/api/v1/specimens/")

	// Insert a fake photo via raw SQL (bypass upload pipeline).
	ctx := context.Background()
	fileID := uuid.MustParse("01906f70-2ba8-7000-8000-aaaaaaaaaaaa")
	_, err := fx.pool.Exec(ctx,
		"INSERT INTO files (id, s3_key, content_type, byte_size, sha256, uploaded_by, uploaded_at) "+
			"VALUES ($1, $2, 'image/jpeg', 1, repeat('a', 64), $3, now())",
		fileID, fmt.Sprintf("files/%s", fileID), stubAuthor)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	photoID := uuid.MustParse("01906f70-2ba8-7000-8000-bbbbbbbbbbbb")
	_, err = fx.pool.Exec(ctx,
		"INSERT INTO photos (id, specimen_id, file_id, position, created_at) "+
			"VALUES ($1, $2, $3, 0, now())",
		photoID, uuid.MustParse(id), fileID)
	if err != nil {
		t.Fatalf("insert photo: %v", err)
	}

	resp, raw := fx.do(t, http.MethodDelete, loc, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete with children: status=%d body=%s", resp.StatusCode, raw)
	}
}
