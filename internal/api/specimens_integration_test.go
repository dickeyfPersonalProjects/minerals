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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// Specimens integration suite (mi-b5n.1 / CONTRACT.md §9 R5).
//
// Lifts internal/api specimens handler coverage to integration tier:
// httptest.NewServer in front of api.New() wired against a real
// Postgres pool (scopedDB) so the SQL repo, validation, error mapping,
// and HTTP envelope all exercise together.
//
// scopedDB lives in photos_integration_test.go; both files are in
// package api_test so the helper is shared.
//
// Specimens don't touch object storage in v1 (storage is reserved for
// photos + journal_entry_files), so this suite intentionally does NOT
// wire storagetest.WithBucket — keeping it out lets `make
// test-integration` exercise specimens even when MINIO isn't running.

// envelopeBody mirrors the §10 error envelope shape so tests can
// assert on { error: { code, message, details } }.
type envelopeBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// specimensTestSrv stands up an httptest.NewServer wrapped around
// api.New() with the real Postgres-backed SpecimenRepo. Returns the
// server and the pool so individual tests can poke the DB directly
// (e.g., asserting cascade behavior).
func specimensTestSrv(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	pool := scopedDB(t)
	h := api.New(api.Deps{
		Specimens:          db.NewSpecimenPostgres(pool),
		SpecimenCollectors: db.NewSpecimenCollectorPostgres(pool),
		Collectors:         db.NewCollectorPostgres(pool),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, pool
}

// doJSON is a thin wrapper that posts/patches JSON, reads the
// response body in full, and returns it alongside the status. Each
// integration test reads the body so the server can recycle the
// connection cleanly between requests.
func doJSON(t *testing.T, c *http.Client, method, url string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, data
}

// createMineral seeds a specimen via the HTTP create handler and
// returns the decoded view. Failing the create fails the test — it's
// a fixture, not the system under test.
func createMineral(t *testing.T, srv *httptest.Server, name string, opts ...func(map[string]any)) api.SpecimenView {
	t.Helper()
	body := map[string]any{
		"type": "mineral",
		"name": name,
	}
	for _, o := range opts {
		o(body)
	}
	status, data := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/specimens", body)
	if status != http.StatusCreated {
		t.Fatalf("seed create %q: status %d body=%s", name, status, data)
	}
	var sv api.SpecimenView
	if err := json.Unmarshal(data, &sv); err != nil {
		t.Fatalf("seed decode: %v", err)
	}
	return sv
}

// withCatalogNumber / withVisibility / withTypeData are option helpers
// for createMineral so individual tests stay readable.
func withCatalogNumber(cn string) func(map[string]any) {
	return func(m map[string]any) { m["catalog_number"] = cn }
}

func withVisibility(v string) func(map[string]any) {
	return func(m map[string]any) { m["visibility"] = v }
}

func withTypeData(td map[string]any) func(map[string]any) {
	return func(m map[string]any) { m["type_data"] = td }
}

// TestIntegration_Specimens_CreateAndGet covers POST happy path +
// GET round-trip through real Postgres. Mirrors the unit-tier
// TestSpecimensCreateAndGet but with the SQL repo wired in.
func TestIntegration_Specimens_CreateAndGet(t *testing.T) {
	srv, _ := specimensTestSrv(t)

	body := map[string]any{
		"type":        "mineral",
		"name":        "Quartz",
		"description": "Clear hexagonal crystal",
		"type_data": map[string]any{
			"chemical_formula": "SiO2",
			"mohs_hardness":    7.0,
			"color":            "clear",
		},
	}
	status, data := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/specimens", body)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, data)
	}
	var created api.SpecimenView
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.AuthorID != auth.StubUser.ID {
		t.Errorf("author_id = %v, want StubUser %v", created.AuthorID, auth.StubUser.ID)
	}
	if created.Visibility != domain.VisibilityPrivate {
		t.Errorf("visibility = %q, want private", created.Visibility)
	}

	// GET roundtrip — verifies the row survived a real INSERT + SELECT.
	status, data = doJSON(t, srv.Client(), http.MethodGet, srv.URL+"/api/v1/specimens/"+created.ID.String(), nil)
	if status != http.StatusOK {
		t.Fatalf("get status=%d body=%s", status, data)
	}
	var got api.SpecimenView
	_ = json.Unmarshal(data, &got)
	if got.Name != "Quartz" {
		t.Errorf("name = %q, want Quartz", got.Name)
	}
	var td map[string]any
	_ = json.Unmarshal(got.TypeData, &td)
	if td["chemical_formula"] != "SiO2" {
		t.Errorf("chemical_formula round-trip lost: %v", td)
	}
}

// TestIntegration_Specimens_List_Pagination_HappyPath verifies the
// cursor handshake against real Postgres. Default ordering is
// (created_at DESC, id DESC) so the most-recent specimen appears
// first.
func TestIntegration_Specimens_List_Pagination_HappyPath(t *testing.T) {
	srv, _ := specimensTestSrv(t)

	// Seed 5 specimens. The repo uses `created_at` for ordering, and
	// Postgres timestamps have microsecond precision — request bursts
	// in a tight loop can land in the same microsecond. Sleep a beat
	// between each so the natural DESC order matches the order we
	// created them (and the test can assert page boundaries).
	want := make([]string, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("specimen-%02d", i)
		_ = createMineral(t, srv, name)
		want[4-i] = name // reversed: newest first
		time.Sleep(2 * time.Millisecond)
	}

	// First page: limit=2 should yield items[0..1] and a non-nil cursor.
	status, data := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens?limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("page-1 status=%d body=%s", status, data)
	}
	var page1 struct {
		Items      []api.SpecimenView `json:"items"`
		NextCursor *string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(data, &page1); err != nil {
		t.Fatalf("decode page-1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page-1 items = %d, want 2", len(page1.Items))
	}
	if page1.NextCursor == nil {
		t.Fatal("page-1 NextCursor is nil; expected more pages")
	}
	if page1.Items[0].Name != want[0] || page1.Items[1].Name != want[1] {
		t.Errorf("page-1 order = [%s, %s], want [%s, %s]",
			page1.Items[0].Name, page1.Items[1].Name, want[0], want[1])
	}

	// Second page: follow the cursor.
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens?limit=2&cursor="+*page1.NextCursor, nil)
	if status != http.StatusOK {
		t.Fatalf("page-2 status=%d body=%s", status, data)
	}
	var page2 struct {
		Items      []api.SpecimenView `json:"items"`
		NextCursor *string            `json:"next_cursor"`
	}
	_ = json.Unmarshal(data, &page2)
	if len(page2.Items) != 2 {
		t.Fatalf("page-2 items = %d, want 2", len(page2.Items))
	}
	if page2.Items[0].Name != want[2] || page2.Items[1].Name != want[3] {
		t.Errorf("page-2 order = [%s, %s], want [%s, %s]",
			page2.Items[0].Name, page2.Items[1].Name, want[2], want[3])
	}

	// Last page: only one row remaining; cursor should be nil.
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens?limit=2&cursor="+*page2.NextCursor, nil)
	if status != http.StatusOK {
		t.Fatalf("page-3 status=%d body=%s", status, data)
	}
	var page3 struct {
		Items      []api.SpecimenView `json:"items"`
		NextCursor *string            `json:"next_cursor"`
	}
	_ = json.Unmarshal(data, &page3)
	if len(page3.Items) != 1 {
		t.Fatalf("page-3 items = %d, want 1 (5 total, 2+2 consumed)", len(page3.Items))
	}
	if page3.NextCursor != nil {
		t.Errorf("page-3 NextCursor = %q, want nil (end of results)", *page3.NextCursor)
	}
}

// TestIntegration_Specimens_List_Filters exercises the type and
// visibility filters against real SQL. Mixing the filters narrows the
// returned set; the unit-tier fakes can't catch SQL typos in
// applySharedFilters, so the integration tier owns this coverage.
func TestIntegration_Specimens_List_Filters(t *testing.T) {
	srv, _ := specimensTestSrv(t)

	// Two minerals (one private, one public), one rock (public).
	mPriv := createMineral(t, srv, "mineral-private")
	mPub := createMineral(t, srv, "mineral-public", withVisibility("public"))
	status, data := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/specimens", map[string]any{
		"type":       "rock",
		"name":       "rock-public",
		"visibility": "public",
	})
	if status != http.StatusCreated {
		t.Fatalf("seed rock: status=%d body=%s", status, data)
	}

	// Filter by type=mineral: should yield 2.
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens?type=mineral", nil)
	if status != http.StatusOK {
		t.Fatalf("filter type=mineral: status=%d body=%s", status, data)
	}
	var byType struct {
		Items []api.SpecimenView `json:"items"`
	}
	_ = json.Unmarshal(data, &byType)
	if len(byType.Items) != 2 {
		t.Errorf("type=mineral items = %d, want 2", len(byType.Items))
	}
	for _, it := range byType.Items {
		if it.Type != domain.SpecimenMineral {
			t.Errorf("filter leaked non-mineral: %v", it.Type)
		}
	}

	// Filter by visibility=public: should yield 2 (across types).
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens?visibility=public", nil)
	if status != http.StatusOK {
		t.Fatalf("filter visibility=public: status=%d", status)
	}
	var byVis struct {
		Items []api.SpecimenView `json:"items"`
	}
	_ = json.Unmarshal(data, &byVis)
	if len(byVis.Items) != 2 {
		t.Errorf("visibility=public items = %d, want 2", len(byVis.Items))
	}
	// Combined: type=mineral AND visibility=public → only mPub.
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens?type=mineral&visibility=public", nil)
	if status != http.StatusOK {
		t.Fatalf("combined filter: status=%d", status)
	}
	var combined struct {
		Items []api.SpecimenView `json:"items"`
	}
	_ = json.Unmarshal(data, &combined)
	if len(combined.Items) != 1 || combined.Items[0].ID != mPub.ID {
		t.Errorf("combined filter = %+v, want only %s", combined.Items, mPub.ID)
	}
	_ = mPriv // referenced for setup intent; private + non-public path proven by filter assertions
}

// TestIntegration_Specimens_Patch_PartialFields verifies §10 PATCH
// merge semantics against the real SQL UPDATE: omitted top-level
// fields are preserved, present fields overwrite, and an explicit
// null inside type_data clears that key.
func TestIntegration_Specimens_Patch_PartialFields(t *testing.T) {
	srv, _ := specimensTestSrv(t)

	created := createMineral(t, srv, "Quartz",
		withTypeData(map[string]any{
			"chemical_formula": "SiO2",
			"mohs_hardness":    7.0,
			"color":            "clear",
		}),
	)

	// PATCH only color + name; chemical_formula and mohs_hardness must
	// survive untouched. Description (top-level, untouched) must also
	// survive.
	patchBody := map[string]any{
		"name": "Quartz var. Rose",
		"type_data": map[string]any{
			"color": "rose",
		},
	}
	status, data := doJSON(t, srv.Client(), http.MethodPatch,
		srv.URL+"/api/v1/specimens/"+created.ID.String(), patchBody)
	if status != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", status, data)
	}
	var patched api.SpecimenView
	_ = json.Unmarshal(data, &patched)
	if patched.Name != "Quartz var. Rose" {
		t.Errorf("name = %q, want updated", patched.Name)
	}
	var td map[string]any
	_ = json.Unmarshal(patched.TypeData, &td)
	if td["color"] != "rose" {
		t.Errorf("color = %v, want rose", td["color"])
	}
	if td["chemical_formula"] != "SiO2" {
		t.Errorf("chemical_formula not preserved across PATCH: %v", td)
	}
	if td["mohs_hardness"].(float64) != 7.0 {
		t.Errorf("mohs_hardness not preserved across PATCH: %v", td)
	}
	if !patched.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updated_at should advance on PATCH: created=%v patched=%v",
			created.UpdatedAt, patched.UpdatedAt)
	}

	// Now clear `color` via explicit null in type_data. Other keys
	// must remain.
	raw := []byte(`{"type_data": {"color": null}}`)
	req, _ := http.NewRequest(http.MethodPatch,
		srv.URL+"/api/v1/specimens/"+created.ID.String(),
		bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("patch null: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("patch-null status=%d body=%s", resp.StatusCode, body)
	}
	var cleared api.SpecimenView
	_ = json.NewDecoder(resp.Body).Decode(&cleared)
	var td2 map[string]any
	_ = json.Unmarshal(cleared.TypeData, &td2)
	if _, ok := td2["color"]; ok {
		t.Errorf("color should be cleared by null, got %v", td2)
	}
	if td2["chemical_formula"] != "SiO2" {
		t.Errorf("chemical_formula lost while clearing color: %v", td2)
	}
}

// TestIntegration_Specimens_Delete_CascadesCollectors verifies the
// §11 cascade: deleting a specimen drops its specimen_collectors
// rows automatically (ON DELETE CASCADE at the FK level). This is
// the cascade behavior explicitly called out in the bead — the v1
// SpecimenPostgres.Delete refuses on photos/journal_entries but
// allows the specimen_collectors cascade through.
func TestIntegration_Specimens_Delete_CascadesCollectors(t *testing.T) {
	srv, pool := specimensTestSrv(t)
	ctx := context.Background()

	sv := createMineral(t, srv, "to-be-deleted")

	// Insert a collector directly so the FK exists, then attach it to
	// the specimen via specimen_collectors. Going through the HTTP
	// surface would work but couples the test to the collectors API
	// — going through the pool keeps the assertion narrow to "cascade
	// runs on DELETE".
	collectorID := uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx,
		`INSERT INTO collectors (id, name, author_id, created_at, updated_at) VALUES ($1,$2,$3,$4,$4)`,
		collectorID, "test-collector-"+collectorID.String()[:8], auth.StubUser.ID, now); err != nil {
		t.Fatalf("seed collector: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO specimen_collectors (specimen_id, collector_id, position, created_at) VALUES ($1,$2,$3,$4)`,
		sv.ID, collectorID, 1, now); err != nil {
		t.Fatalf("seed specimen_collectors: %v", err)
	}

	// Sanity: the link row exists.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM specimen_collectors WHERE specimen_id = $1`, sv.ID).Scan(&n); err != nil {
		t.Fatalf("count pre: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 link row pre-delete, got %d", n)
	}

	// DELETE the specimen via HTTP.
	status, data := doJSON(t, srv.Client(), http.MethodDelete,
		srv.URL+"/api/v1/specimens/"+sv.ID.String(), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", status, data)
	}

	// The cascade must have removed the link row.
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM specimen_collectors WHERE specimen_id = $1`, sv.ID).Scan(&n); err != nil {
		t.Fatalf("count post: %v", err)
	}
	if n != 0 {
		t.Errorf("expected cascade to delete specimen_collectors rows; got %d remaining", n)
	}

	// The collector itself must survive (ON DELETE RESTRICT in the
	// reverse direction: specimen_collectors → collectors). v1
	// shouldn't drop the collector when a specimen referencing it is
	// removed.
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM collectors WHERE id = $1`, collectorID).Scan(&n); err != nil {
		t.Fatalf("count collectors: %v", err)
	}
	if n != 1 {
		t.Errorf("collector row should survive specimen delete, got %d", n)
	}

	// And the specimen row is actually gone.
	status, _ = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+sv.ID.String(), nil)
	if status != http.StatusNotFound {
		t.Errorf("post-delete GET status=%d, want 404", status)
	}
}

// TestIntegration_Specimens_Get_NotFound_ErrorEnvelope confirms the
// §10 error-envelope shape on a real 404 from the SQL repo (not the
// fake). Code, message, and the JSON envelope structure are part of
// the contract.
func TestIntegration_Specimens_Get_NotFound_ErrorEnvelope(t *testing.T) {
	srv, _ := specimensTestSrv(t)

	status, data := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+uuid.New().String(), nil)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", status, data)
	}
	var env envelopeBody
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "specimen_not_found" {
		t.Errorf("error.code = %q, want specimen_not_found", env.Error.Code)
	}
	if env.Error.Message == "" {
		t.Error("error.message should be non-empty")
	}
	// The envelope MUST carry the `error` key (§10). Huma adds a
	// `$schema` sibling pointing at the registered ApiError schema —
	// that's framework metadata, not a contract violation, so we
	// don't assert against it.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	if _, ok := raw["error"]; !ok {
		t.Error("envelope missing `error` key")
	}
}

// TestIntegration_Specimens_RequireUser_401 confirms the
// auth.RequireUser middleware that guards every /api/v1/* protected
// route correctly emits the §10 unauthorized envelope when no user
// is in the request context.
//
// v1 stub auth.Auth always populates StubUser, so we can't reach
// this branch through api.New() directly. The test composes the
// same auth.RequireUser middleware that protects the production
// specimens routes around a sentinel handler and asserts the
// rejection contract — when real-auth replaces auth.Auth (post-v1),
// invalid credentials will route through exactly this code path,
// and the §13 contract demands a 401 envelope here.
func TestIntegration_Specimens_RequireUser_401(t *testing.T) {
	srv, _ := specimensTestSrv(t)

	// Sanity: with auth.Auth in place the specimens route serves the
	// list (200). This is the "control" half — proves the real
	// handler is reachable so the 401 below isn't a routing artifact.
	status, _ := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens", nil)
	if status != http.StatusOK {
		t.Fatalf("control: GET /specimens status=%d, want 200", status)
	}

	// Now stand up a parallel server whose chain mirrors production
	// (mux → /api/v1/* fallback wrapped in RequireUser) but with
	// auth.Auth deliberately absent. Hitting any /api/v1/specimens*
	// path through this chain MUST produce a 401 envelope.
	guarded := auth.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// If we ever reach this handler the test fails — RequireUser
		// must short-circuit before the inner handler runs.
		w.WriteHeader(http.StatusTeapot)
	}))
	noAuthSrv := httptest.NewServer(guarded)
	t.Cleanup(noAuthSrv.Close)

	status, data := doJSON(t, srv.Client(), http.MethodGet,
		noAuthSrv.URL+"/api/v1/specimens", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 from RequireUser-only chain, got %d body=%s", status, data)
	}
	var env envelopeBody
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode 401 envelope: %v", err)
	}
	if env.Error.Code != "unauthorized" {
		t.Errorf("401 envelope code = %q, want unauthorized", env.Error.Code)
	}
	if !strings.Contains(strings.ToLower(env.Error.Message), "authentication") {
		t.Errorf("401 envelope message = %q, want hint about authentication", env.Error.Message)
	}
}

// TestIntegration_Specimens_CatalogNumberConflict_409 verifies the
// §10 conflict envelope on a real UNIQUE-violation from Postgres.
// The unit tests cover the fake's conflict path; this test proves
// the SQL repo's mapping from pg unique_violation to
// domain.ErrSpecimenConflict to the HTTP envelope is wired.
func TestIntegration_Specimens_CatalogNumberConflict_409(t *testing.T) {
	srv, _ := specimensTestSrv(t)

	_ = createMineral(t, srv, "first", withCatalogNumber("FD-INT-001"))

	status, data := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/specimens",
		map[string]any{
			"type":           "mineral",
			"name":           "second",
			"catalog_number": "FD-INT-001",
		})
	if status != http.StatusConflict {
		t.Fatalf("status=%d body=%s", status, data)
	}
	var env envelopeBody
	_ = json.Unmarshal(data, &env)
	if env.Error.Code != "specimen_conflict" {
		t.Errorf("error.code = %q, want specimen_conflict", env.Error.Code)
	}
	if env.Error.Details == nil || env.Error.Details["field"] != "catalog_number" {
		t.Errorf("error.details should pinpoint catalog_number, got %v", env.Error.Details)
	}
}
