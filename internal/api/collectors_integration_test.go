//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// Collectors integration suite (mi-b5n.4 / CONTRACT.md §9 R5).
//
// Lifts internal/api collectors handler coverage to integration tier:
// httptest.NewServer in front of api.New() wired against a real
// Postgres pool (scopedDB) so the SQL repo, validation, error mapping,
// and HTTP envelope all exercise together.
//
// scopedDB, doJSON, and envelopeBody live in the sibling
// *_integration_test.go files (specimens / photos) — all three suites
// share `package api_test` so the helpers are reused, not duplicated.
//
// Collectors don't touch object storage in v1 (storage is reserved for
// photos + journal_entry_files), so this suite intentionally does NOT
// wire storagetest.WithBucket — keeping it out lets `make
// test-integration` exercise collectors even when MINIO isn't running.
// This mirrors the choice made in specimens_integration_test.go.

// collectorsTestSrv stands up an httptest.NewServer wrapped around
// api.New() with the real Postgres-backed CollectorRepo. Returns the
// server and the pool so individual tests can poke the DB directly
// (e.g., asserting reference behavior against specimen_collectors).
func collectorsTestSrv(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	pool := scopedDB(t)
	h := api.New(api.Deps{
		Collectors:         db.NewCollectorPostgres(pool),
		Specimens:          db.NewSpecimenPostgres(pool),
		SpecimenCollectors: db.NewSpecimenCollectorPostgres(pool),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, pool
}

// createCollector seeds a collector via the HTTP create handler and
// returns the decoded view. Failing the create fails the test — it's
// a fixture, not the system under test.
func createCollector(t *testing.T, srv *httptest.Server, name string, opts ...func(map[string]any)) api.CollectorView {
	t.Helper()
	body := map[string]any{"name": name}
	for _, o := range opts {
		o(body)
	}
	status, data := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/collectors", body)
	if status != http.StatusCreated {
		t.Fatalf("seed collector %q: status %d body=%s", name, status, data)
	}
	var cv api.CollectorView
	if err := json.Unmarshal(data, &cv); err != nil {
		t.Fatalf("seed decode: %v", err)
	}
	return cv
}

func withCollectorNotes(notes string) func(map[string]any) {
	return func(m map[string]any) { m["notes"] = notes }
}

// TestIntegration_Collectors_CreateAndGet covers POST happy path +
// GET round-trip through real Postgres. Mirrors the unit-tier
// TestCollectorsCreateAndGet but with the SQL repo wired in.
func TestIntegration_Collectors_CreateAndGet(t *testing.T) {
	srv, _ := collectorsTestSrv(t)

	notes := "test collector"
	body := map[string]any{
		"name":  "Marie Curie",
		"notes": notes,
	}
	status, data := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/collectors", body)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, data)
	}
	var created api.CollectorView
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.AuthorID != auth.StubUser.ID {
		t.Errorf("author_id = %v, want StubUser %v", created.AuthorID, auth.StubUser.ID)
	}
	if created.Name != "Marie Curie" {
		t.Errorf("name = %q, want Marie Curie", created.Name)
	}
	if created.Notes == nil || *created.Notes != notes {
		t.Errorf("notes = %v, want %q", created.Notes, notes)
	}

	// GET roundtrip — verifies the row survived a real INSERT + SELECT.
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors/"+created.ID.String(), nil)
	if status != http.StatusOK {
		t.Fatalf("get status=%d body=%s", status, data)
	}
	var got api.CollectorView
	_ = json.Unmarshal(data, &got)
	if got.ID != created.ID || got.Name != created.Name {
		t.Errorf("get mismatch: %+v vs %+v", got, created)
	}
	if got.Notes == nil || *got.Notes != notes {
		t.Errorf("notes round-trip lost: %v", got.Notes)
	}
}

// TestIntegration_Collectors_List_Pagination_HappyPath verifies the
// cursor handshake against real Postgres. Default ordering is
// (created_at DESC, id DESC) so the most-recent collector appears
// first.
func TestIntegration_Collectors_List_Pagination_HappyPath(t *testing.T) {
	srv, _ := collectorsTestSrv(t)

	// Seed 5 collectors. The repo uses `created_at` for ordering, and
	// Postgres timestamps have microsecond precision — request bursts
	// in a tight loop can land in the same microsecond. Sleep a beat
	// between each so the natural DESC order matches the order we
	// created them (and the test can assert page boundaries).
	want := make([]string, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("collector-%02d", i)
		_ = createCollector(t, srv, name)
		want[4-i] = name // reversed: newest first
		time.Sleep(2 * time.Millisecond)
	}

	// First page: limit=2 should yield items[0..1] and a non-nil cursor.
	status, data := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors?limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("page-1 status=%d body=%s", status, data)
	}
	var page1 struct {
		Items      []api.CollectorView `json:"items"`
		NextCursor *string             `json:"next_cursor"`
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
		srv.URL+"/api/v1/collectors?limit=2&cursor="+*page1.NextCursor, nil)
	if status != http.StatusOK {
		t.Fatalf("page-2 status=%d body=%s", status, data)
	}
	var page2 struct {
		Items      []api.CollectorView `json:"items"`
		NextCursor *string             `json:"next_cursor"`
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
		srv.URL+"/api/v1/collectors?limit=2&cursor="+*page2.NextCursor, nil)
	if status != http.StatusOK {
		t.Fatalf("page-3 status=%d body=%s", status, data)
	}
	var page3 struct {
		Items      []api.CollectorView `json:"items"`
		NextCursor *string             `json:"next_cursor"`
	}
	_ = json.Unmarshal(data, &page3)
	if len(page3.Items) != 1 {
		t.Fatalf("page-3 items = %d, want 1 (5 total, 2+2 consumed)", len(page3.Items))
	}
	if page3.NextCursor != nil {
		t.Errorf("page-3 NextCursor = %q, want nil (end of results)", *page3.NextCursor)
	}
}

// TestIntegration_Collectors_List_QueryFilter exercises the `q`
// substring filter against real SQL (ILIKE on name). The unit-tier
// fakes can't catch a typo in the LIKE escape / ILIKE clause, so the
// integration tier owns this coverage.
func TestIntegration_Collectors_List_QueryFilter(t *testing.T) {
	srv, _ := collectorsTestSrv(t)

	_ = createCollector(t, srv, "Apple Picker")
	_ = createCollector(t, srv, "Banana Grower")
	apricot := createCollector(t, srv, "Apricot Forager")

	// Case-insensitive substring on name; "ap" should match Apple and
	// Apricot but not Banana.
	status, data := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors?q=ap", nil)
	if status != http.StatusOK {
		t.Fatalf("q=ap status=%d body=%s", status, data)
	}
	var body struct {
		Items []api.CollectorView `json:"items"`
	}
	_ = json.Unmarshal(data, &body)
	if len(body.Items) != 2 {
		t.Errorf("q=ap items = %d, want 2 (got names=%v)", len(body.Items), namesOf(body.Items))
	}
	for _, it := range body.Items {
		if !strings.Contains(strings.ToLower(it.Name), "ap") {
			t.Errorf("filter leaked %q (does not contain 'ap')", it.Name)
		}
	}

	// A narrower query that uniquely matches one row proves SQL
	// substring (not prefix-only) is what's wired.
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors?q=forager", nil)
	if status != http.StatusOK {
		t.Fatalf("q=forager status=%d", status)
	}
	var only struct {
		Items []api.CollectorView `json:"items"`
	}
	_ = json.Unmarshal(data, &only)
	if len(only.Items) != 1 || only.Items[0].ID != apricot.ID {
		t.Errorf("q=forager items = %+v, want only %s", only.Items, apricot.ID)
	}
}

// TestIntegration_Collectors_Patch_PartialFields verifies §10 PATCH
// merge semantics against the real SQL UPDATE: omitted fields are
// preserved, present fields overwrite, and updated_at advances.
func TestIntegration_Collectors_Patch_PartialFields(t *testing.T) {
	srv, _ := collectorsTestSrv(t)

	created := createCollector(t, srv, "Original",
		withCollectorNotes("keep these notes"))

	// PATCH only name; notes must survive untouched.
	patchBody := map[string]any{"name": "Renamed"}
	status, data := doJSON(t, srv.Client(), http.MethodPatch,
		srv.URL+"/api/v1/collectors/"+created.ID.String(), patchBody)
	if status != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", status, data)
	}
	var patched api.CollectorView
	if err := json.Unmarshal(data, &patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.Name != "Renamed" {
		t.Errorf("name = %q, want Renamed", patched.Name)
	}
	if patched.Notes == nil || *patched.Notes != "keep these notes" {
		t.Errorf("notes not preserved across name-only PATCH: %v", patched.Notes)
	}
	if !patched.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updated_at should advance on PATCH: created=%v patched=%v",
			created.UpdatedAt, patched.UpdatedAt)
	}

	// GET roundtrip — confirms UPDATE actually persisted (the response
	// body could in principle echo the in-memory value without writing).
	status, data = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors/"+created.ID.String(), nil)
	if status != http.StatusOK {
		t.Fatalf("get-after-patch status=%d body=%s", status, data)
	}
	var refetched api.CollectorView
	_ = json.Unmarshal(data, &refetched)
	if refetched.Name != "Renamed" || refetched.Notes == nil || *refetched.Notes != "keep these notes" {
		t.Errorf("refetch mismatch: name=%q notes=%v", refetched.Name, refetched.Notes)
	}
}

// TestIntegration_Collectors_Delete_HappyPath proves DELETE removes
// the row and a subsequent GET reports the §10 not-found envelope.
func TestIntegration_Collectors_Delete_HappyPath(t *testing.T) {
	srv, _ := collectorsTestSrv(t)
	cv := createCollector(t, srv, "to-be-deleted")

	status, data := doJSON(t, srv.Client(), http.MethodDelete,
		srv.URL+"/api/v1/collectors/"+cv.ID.String(), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", status, data)
	}

	// Post-delete GET must 404.
	status, _ = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors/"+cv.ID.String(), nil)
	if status != http.StatusNotFound {
		t.Errorf("post-delete GET status=%d, want 404", status)
	}
}

// TestIntegration_Collectors_Delete_Referenced_409 verifies the §10
// conflict envelope when a collector is still linked from
// specimen_collectors. The FK on that table is ON DELETE RESTRICT
// (migration 0001_init), so Postgres raises foreign_key_violation,
// the SQL repo maps it to domain.ErrCollectorReferenced, and the
// handler emits a 409 with code collector_referenced. The unit tests
// cover the fake's referenced path; this test proves the full SQL →
// domain → envelope chain is wired correctly.
func TestIntegration_Collectors_Delete_Referenced_409(t *testing.T) {
	srv, pool := collectorsTestSrv(t)
	ctx := context.Background()

	cv := createCollector(t, srv, "linked-collector")

	// Seed a specimen and link it to the collector directly via the
	// pool. Going through the chain HTTP surface would work, but
	// inserting the link row keeps the assertion focused on the FK
	// behavior under DELETE rather than on the chain handler.
	specimenID := uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx,
		`INSERT INTO specimens (id, type, name, author_id, visibility, created_at, updated_at, type_data)
		 VALUES ($1,'mineral',$2,$3,'private',$4,$4,'{}'::jsonb)`,
		specimenID, "ref-test-"+specimenID.String()[:8], auth.StubUser.ID, now); err != nil {
		t.Fatalf("seed specimen: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO specimen_collectors (specimen_id, collector_id, position, created_at) VALUES ($1,$2,$3,$4)`,
		specimenID, cv.ID, 1, now); err != nil {
		t.Fatalf("seed specimen_collectors: %v", err)
	}

	// DELETE the collector via HTTP — the FK violation should surface
	// as a 409 collector_referenced envelope.
	status, data := doJSON(t, srv.Client(), http.MethodDelete,
		srv.URL+"/api/v1/collectors/"+cv.ID.String(), nil)
	if status != http.StatusConflict {
		t.Fatalf("delete status=%d body=%s, want 409", status, data)
	}
	var env envelopeBody
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "collector_referenced" {
		t.Errorf("error.code = %q, want collector_referenced", env.Error.Code)
	}
	if env.Error.Message == "" {
		t.Error("error.message should be non-empty")
	}

	// The collector row must survive the failed DELETE.
	status, _ = doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors/"+cv.ID.String(), nil)
	if status != http.StatusOK {
		t.Errorf("collector should survive failed DELETE; GET status=%d", status)
	}
}

// TestIntegration_Collectors_Create_NameConflict_409 verifies the §10
// conflict envelope on a real UNIQUE-violation from Postgres. The
// unit tests cover the fake's conflict path; this test proves the
// SQL repo's mapping from pg unique_violation to
// domain.ErrCollectorConflict to the HTTP envelope is wired.
func TestIntegration_Collectors_Create_NameConflict_409(t *testing.T) {
	srv, _ := collectorsTestSrv(t)

	_ = createCollector(t, srv, "duplicate-name")

	status, data := doJSON(t, srv.Client(), http.MethodPost, srv.URL+"/api/v1/collectors",
		map[string]any{"name": "duplicate-name"})
	if status != http.StatusConflict {
		t.Fatalf("status=%d body=%s", status, data)
	}
	var env envelopeBody
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "collector_conflict" {
		t.Errorf("error.code = %q, want collector_conflict", env.Error.Code)
	}
	if env.Error.Details == nil || env.Error.Details["field"] != "name" {
		t.Errorf("error.details should pinpoint name, got %v", env.Error.Details)
	}
}

// TestIntegration_Collectors_Get_NotFound_ErrorEnvelope confirms the
// §10 error-envelope shape on a real 404 from the SQL repo (not the
// fake). Code, message, and the JSON envelope structure are part of
// the contract.
func TestIntegration_Collectors_Get_NotFound_ErrorEnvelope(t *testing.T) {
	srv, _ := collectorsTestSrv(t)

	status, data := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors/"+uuid.New().String(), nil)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", status, data)
	}
	var env envelopeBody
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "collector_not_found" {
		t.Errorf("error.code = %q, want collector_not_found", env.Error.Code)
	}
	if env.Error.Message == "" {
		t.Error("error.message should be non-empty")
	}
	// The envelope MUST carry the `error` key (§10).
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	if _, ok := raw["error"]; !ok {
		t.Error("envelope missing `error` key")
	}
}

// TestIntegration_Collectors_RequireUser_401 confirms the
// auth.RequireUser middleware that guards /api/v1/* protected routes
// correctly emits the §10 unauthorized envelope when no user is in
// the request context.
//
// v1 stub auth.Auth always populates StubUser, so we can't reach this
// branch through api.New() directly. The test composes the same
// auth.RequireUser middleware around a sentinel handler and asserts
// the rejection contract — when real-auth replaces auth.Auth (post-v1),
// invalid credentials will route through exactly this code path, and
// the §13 contract demands a 401 envelope here.
func TestIntegration_Collectors_RequireUser_401(t *testing.T) {
	srv, _ := collectorsTestSrv(t)

	// Sanity: with auth.Auth in place the collectors route serves the
	// list (200). This proves the real handler is reachable so the 401
	// below isn't a routing artifact.
	status, _ := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors", nil)
	if status != http.StatusOK {
		t.Fatalf("control: GET /collectors status=%d, want 200", status)
	}

	// Now stand up a parallel server whose chain mirrors production
	// (mux → /api/v1/* fallback wrapped in RequireUser) but with
	// auth.Auth deliberately absent. Hitting any /api/v1/collectors*
	// path through this chain MUST produce a 401 envelope.
	guarded := auth.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// If we ever reach this handler the test fails — RequireUser
		// must short-circuit before the inner handler runs.
		w.WriteHeader(http.StatusTeapot)
	}))
	noAuthSrv := httptest.NewServer(guarded)
	t.Cleanup(noAuthSrv.Close)

	status, data := doJSON(t, srv.Client(), http.MethodGet,
		noAuthSrv.URL+"/api/v1/collectors", nil)
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

// namesOf is a tiny test-only helper for failure messages when an
// items-count assertion misses.
func namesOf(items []api.CollectorView) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}
