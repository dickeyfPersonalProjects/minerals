//go:build integration

// Journal integration tier (mi-b5n.3 / CONTRACT §9). Lifts the
// HTTP-handler tests for the per-specimen journal log to real
// Postgres so the wire shape, error envelope, pagination, immutability
// rule, and §17 markdown render path are validated against the same
// stack production runs. The unit-tier file (journal_test.go) keeps
// pure validation cases that don't need a database (e.g. the
// `created_at_immutable` 400 path).
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
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
)

// journalView mirrors api.JournalView's wire fields. We could import
// api.JournalView directly, but keeping a local struct documents the
// JSON contract the integration test exercises.
type journalView struct {
	ID         uuid.UUID `json:"id"`
	SpecimenID uuid.UUID `json:"specimen_id"`
	AuthorID   uuid.UUID `json:"author_id"`
	BodyMD     string    `json:"body_md"`
	BodyHTML   string    `json:"body_html"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type journalListResponse struct {
	Items      []journalView `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

// newJournalServer wires api.New against the supplied pool with only
// the journal surface enabled — the bead is scoped to journal, so we
// deliberately omit the rest of the Deps to keep route registration
// minimal and the test self-explanatory.
func newJournalServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	h := api.New(api.Deps{
		Journal: &api.JournalServiceDeps{
			Entries: db.NewJournalEntryPostgres(pool),
		},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// seedJournalSpecimen inserts a minimum-viable specimens row so the
// journal_entries.specimen_id FK is satisfied. We stay below the
// SpecimenRepo abstraction to keep the fixture independent of repo
// evolution — same approach photos_integration_test takes.
func seedJournalSpecimen(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC()
	const q = `
		INSERT INTO specimens (id, type, name, author_id, type_data, created_at, updated_at)
		VALUES ($1, 'mineral', $2, $3, '{}'::jsonb, $4, $4)`
	if _, err := pool.Exec(ctx, q, id, "journal-test", auth.StubUser.ID, now); err != nil {
		t.Fatalf("seed specimen: %v", err)
	}
	return id
}

// journalDoReq is a journal-specific request helper that returns the
// full *http.Response (we need access to headers like Location and
// Content-Type that the shared doJSON helper drops). The shared
// doJSON in specimens_integration_test.go covers the status+body
// pattern other tests use; journal needs richer assertions.
func journalDoReq(t *testing.T, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, raw
}

// TestIntegration_Journal_CreateGetRoundtrip drives the §10 create
// path against real Postgres and asserts the response shape, the
// Location header, and the §17 markdown render output land verbatim
// in the GET round-trip — closing the loop end-to-end on the
// production wiring.
func TestIntegration_Journal_CreateGetRoundtrip(t *testing.T) {
	pool := scopedDB(t)
	ctx := context.Background()
	specimenID := seedJournalSpecimen(ctx, t, pool)
	srv := newJournalServer(t, pool)

	resp, raw := journalDoReq(t, http.MethodPost,
		srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal",
		map[string]any{"body_md": "# title\n\nplain **bold** text."})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.StatusCode, raw)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/api/v1/journal/") {
		t.Errorf("Location header = %q", loc)
	}
	var created journalView
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create: %v body=%s", err, raw)
	}
	if created.SpecimenID != specimenID {
		t.Errorf("specimen_id = %s, want %s", created.SpecimenID, specimenID)
	}
	if created.AuthorID != auth.StubUser.ID {
		t.Errorf("author_id = %s, want %s (v1 humaAuth must populate StubUser)",
			created.AuthorID, auth.StubUser.ID)
	}
	if !strings.Contains(created.BodyHTML, "<h1>title</h1>") {
		t.Errorf("body_html missing rendered heading: %q", created.BodyHTML)
	}
	if !strings.Contains(created.BodyHTML, "<strong>bold</strong>") {
		t.Errorf("body_html missing bold: %q", created.BodyHTML)
	}

	// Verify the row actually landed in Postgres — not just the
	// response shape — by counting rows in the scoped schema.
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM journal_entries WHERE id = $1", created.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row in journal_entries; got %d", n)
	}

	// GET round-trip.
	getResp, getRaw := journalDoReq(t, http.MethodGet, srv.URL+loc, nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getResp.StatusCode, getRaw)
	}
	var got journalView
	if err := json.Unmarshal(getRaw, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("get id = %s, want %s", got.ID, created.ID)
	}
	if got.BodyMD != "# title\n\nplain **bold** text." {
		t.Errorf("body_md drift: %q", got.BodyMD)
	}
	if got.BodyHTML != created.BodyHTML {
		t.Errorf("body_html drift between create and get: %q vs %q", got.BodyHTML, created.BodyHTML)
	}
}

// TestIntegration_Journal_ListPagination exercises the cursor-paged
// list endpoint against real Postgres. Insert N+1 entries with
// staggered created_at, request a page smaller than N, and verify the
// next_cursor closes the gap — this is the §10.3 contract.
func TestIntegration_Journal_ListPagination(t *testing.T) {
	pool := scopedDB(t)
	ctx := context.Background()
	specimenID := seedJournalSpecimen(ctx, t, pool)
	srv := newJournalServer(t, pool)

	// Five entries with stable, strictly-decreasing created_at so the
	// (created_at DESC, id DESC) ordering is deterministic.
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		body := map[string]any{"body_md": "entry " + string(rune('A'+i))}
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost,
			srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal",
			bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d: status %d", i, resp.StatusCode)
		}
		// Rewrite created_at so ordering is deterministic regardless
		// of how fast the inserts ran. This is a test-only nudge that
		// keeps the cursor assertions stable.
		if _, err := pool.Exec(ctx,
			`UPDATE journal_entries SET created_at = $1
			 WHERE id = (SELECT id FROM journal_entries
			             WHERE specimen_id = $2 ORDER BY created_at DESC LIMIT 1)`,
			base.Add(time.Duration(i)*time.Second), specimenID); err != nil {
			t.Fatalf("backfill created_at: %v", err)
		}
	}

	// Page 1: limit=2 should return the two most-recent rows plus a
	// non-nil next_cursor.
	resp, raw := journalDoReq(t, http.MethodGet,
		srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal?limit=2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page1 status = %d body=%s", resp.StatusCode, raw)
	}
	var page1 journalListResponse
	if err := json.Unmarshal(raw, &page1); err != nil {
		t.Fatalf("decode page1: %v body=%s", err, raw)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(page1.Items))
	}
	if page1.NextCursor == nil || *page1.NextCursor == "" {
		t.Fatalf("page1 next_cursor missing — pagination broken")
	}
	for _, item := range page1.Items {
		if item.BodyHTML == "" {
			t.Errorf("item missing rendered body_html: %+v", item)
		}
	}

	// Page 2: feed cursor back; expect remaining 3 rows and a nil
	// next_cursor when the page exhausts the dataset.
	resp2, raw2 := journalDoReq(t, http.MethodGet,
		srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal?limit=10&cursor="+*page1.NextCursor, nil)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("page2 status = %d body=%s", resp2.StatusCode, raw2)
	}
	var page2 journalListResponse
	if err := json.Unmarshal(raw2, &page2); err != nil {
		t.Fatalf("decode page2: %v body=%s", err, raw2)
	}
	if len(page2.Items) != 3 {
		t.Errorf("page2 items = %d, want 3", len(page2.Items))
	}
	if page2.NextCursor != nil {
		t.Errorf("page2 next_cursor = %v, want nil at end of dataset", *page2.NextCursor)
	}

	// Filter behaviour: a different specimen's id must return an
	// empty page even though rows exist for the seeded specimen.
	other := seedJournalSpecimen(ctx, t, pool)
	resp3, raw3 := journalDoReq(t, http.MethodGet,
		srv.URL+"/api/v1/specimens/"+other.String()+"/journal", nil)
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("other-specimen status = %d body=%s", resp3.StatusCode, raw3)
	}
	var page3 journalListResponse
	if err := json.Unmarshal(raw3, &page3); err != nil {
		t.Fatalf("decode page3: %v body=%s", err, raw3)
	}
	if len(page3.Items) != 0 {
		t.Errorf("other-specimen items = %d, want 0", len(page3.Items))
	}
}

// TestIntegration_Journal_PatchPartialFields verifies PATCH semantics
// against real Postgres: body_md updates re-render body_html and bump
// updated_at, but created_at and author_id stay frozen (§2 immutability).
func TestIntegration_Journal_PatchPartialFields(t *testing.T) {
	pool := scopedDB(t)
	ctx := context.Background()
	specimenID := seedJournalSpecimen(ctx, t, pool)
	srv := newJournalServer(t, pool)

	createResp, createRaw := journalDoReq(t, http.MethodPost,
		srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal",
		map[string]any{"body_md": "v1 text"})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d body=%s", createResp.StatusCode, createRaw)
	}
	var created journalView
	if err := json.Unmarshal(createRaw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Backdate created_at so we can prove the patch doesn't move it.
	frozenCreated := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Millisecond)
	if _, err := pool.Exec(ctx,
		`UPDATE journal_entries SET created_at = $1 WHERE id = $2`,
		frozenCreated, created.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	patchResp, patchRaw := journalDoReq(t, http.MethodPatch,
		srv.URL+"/api/v1/journal/"+created.ID.String(),
		map[string]any{"body_md": "v2 *italic*"})
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d body=%s", patchResp.StatusCode, patchRaw)
	}
	var patched journalView
	if err := json.Unmarshal(patchRaw, &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.BodyMD != "v2 *italic*" {
		t.Errorf("body_md = %q", patched.BodyMD)
	}
	if !strings.Contains(patched.BodyHTML, "<em>italic</em>") {
		t.Errorf("body_html missing em: %q", patched.BodyHTML)
	}
	if !patched.CreatedAt.Equal(frozenCreated) {
		t.Errorf("created_at mutated: was %v now %v", frozenCreated, patched.CreatedAt)
	}
	if !patched.UpdatedAt.After(frozenCreated) {
		t.Errorf("updated_at not bumped past frozen created_at: %v", patched.UpdatedAt)
	}
	if patched.AuthorID != created.AuthorID {
		t.Errorf("author_id changed across patch: %s -> %s", created.AuthorID, patched.AuthorID)
	}

	// Verify the DB row matches — the response could lie. created_at
	// stored should equal frozenCreated to the millisecond.
	var dbCreated, dbUpdated time.Time
	var dbBody string
	if err := pool.QueryRow(ctx,
		`SELECT body_md, created_at, updated_at FROM journal_entries WHERE id = $1`,
		created.ID).Scan(&dbBody, &dbCreated, &dbUpdated); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if dbBody != "v2 *italic*" {
		t.Errorf("db body_md = %q", dbBody)
	}
	if !dbCreated.Equal(frozenCreated) {
		t.Errorf("db created_at = %v, want %v", dbCreated, frozenCreated)
	}
}

// TestIntegration_Journal_DeleteHappyPath drives the §10 delete path.
// A 204 means the row is gone and a subsequent GET returns 404 with
// the §10 error envelope.
func TestIntegration_Journal_DeleteHappyPath(t *testing.T) {
	pool := scopedDB(t)
	ctx := context.Background()
	specimenID := seedJournalSpecimen(ctx, t, pool)
	srv := newJournalServer(t, pool)

	createResp, createRaw := journalDoReq(t, http.MethodPost,
		srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal",
		map[string]any{"body_md": "to be deleted"})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d body=%s", createResp.StatusCode, createRaw)
	}
	var created journalView
	if err := json.Unmarshal(createRaw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	delResp, delRaw := journalDoReq(t, http.MethodDelete,
		srv.URL+"/api/v1/journal/"+created.ID.String(), nil)
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d body=%s", delResp.StatusCode, delRaw)
	}

	// Row gone in DB.
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM journal_entries WHERE id = $1", created.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("row still present after delete: count=%d", n)
	}

	// GET should now 404 with the §10 envelope.
	getResp, getRaw := journalDoReq(t, http.MethodGet,
		srv.URL+"/api/v1/journal/"+created.ID.String(), nil)
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("get-after-delete: %d body=%s", getResp.StatusCode, getRaw)
	}
	var env envelopeBody
	if err := json.Unmarshal(getRaw, &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, getRaw)
	}
	if env.Error.Code != "journal_entry_not_found" {
		t.Errorf("error.code = %q, want journal_entry_not_found", env.Error.Code)
	}
	if env.Error.Message == "" {
		t.Errorf("error.message missing")
	}
}

// TestIntegration_Journal_SpecimenDeleteCascades validates the
// ON DELETE CASCADE on journal_entries.specimen_id (migration 0001).
// When the parent specimen vanishes, every journal entry attached to
// it must vanish too — otherwise we'd have orphaned rows the API
// could still hand out via GET.
func TestIntegration_Journal_SpecimenDeleteCascades(t *testing.T) {
	pool := scopedDB(t)
	ctx := context.Background()
	specimenID := seedJournalSpecimen(ctx, t, pool)
	srv := newJournalServer(t, pool)

	// Create two entries on the specimen.
	for i, body := range []string{"first", "second"} {
		resp, raw := journalDoReq(t, http.MethodPost,
			srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal",
			map[string]any{"body_md": body})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d: %d body=%s", i, resp.StatusCode, raw)
		}
	}

	// Drop the parent specimen directly via SQL (the API surface
	// doesn't expose a specimen-delete to this test; the cascade is a
	// schema-level contract, not a handler-level one).
	if _, err := pool.Exec(ctx,
		`DELETE FROM specimens WHERE id = $1`, specimenID); err != nil {
		t.Fatalf("delete specimen: %v", err)
	}

	// Both journal rows must be gone via the ON DELETE CASCADE FK.
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM journal_entries WHERE specimen_id = $1", specimenID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected cascade to remove all journal entries; %d remain", n)
	}

	// The list endpoint should now be a successful empty page — the
	// handler does not 404 on a missing specimen by design (§10.3
	// treats the list as a filter, not a parent lookup).
	resp, raw := journalDoReq(t, http.MethodGet,
		srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-cascade list status = %d body=%s", resp.StatusCode, raw)
	}
	var page journalListResponse
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("post-cascade items = %d, want 0", len(page.Items))
	}
}

// TestIntegration_Journal_ErrorEnvelopeOnInvalidUUID exercises the
// §10 error envelope shape for a non-404 error path: a malformed
// UUID in the path. We assert the wire shape (`error.code`,
// `error.message`) rather than the specific status because the huma
// 422 validator and the api parseUUID helper return different codes
// but both go through the §10 envelope.
func TestIntegration_Journal_ErrorEnvelopeOnInvalidUUID(t *testing.T) {
	pool := scopedDB(t)
	srv := newJournalServer(t, pool)

	resp, raw := journalDoReq(t, http.MethodGet,
		srv.URL+"/api/v1/journal/not-a-uuid", nil)
	if resp.StatusCode < 400 {
		t.Fatalf("expected client error, got %d body=%s", resp.StatusCode, raw)
	}
	var env envelopeBody
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, raw)
	}
	if env.Error.Code == "" {
		t.Errorf("error.code empty — envelope shape broken: %s", raw)
	}
	if env.Error.Message == "" {
		t.Errorf("error.message empty — envelope shape broken: %s", raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("error response Content-Type = %q, want application/json*", ct)
	}
}

// TestIntegration_Journal_AuthChainRejectsMissingUser validates the
// §13 auth contract: when no user is in the request context, the
// /api/v1/* fallback responds with the 401 envelope. Journal routes
// in v1 run under the humaAuth shim which always populates StubUser
// (so they can't 401 directly), but the protected fallback chain
// (auth.Auth → auth.RequireUser) is the same one that will wrap
// journal once real auth replaces the stub — so this test pins the
// rejection contract.
//
// We wrap the journal handler with auth.RequireUser BEFORE auth.Auth
// to simulate the future "real auth failed" path: RequireUser sees
// an empty context and must 401. Without this scaffolding the v1
// stub auth would silently succeed.
func TestIntegration_Journal_AuthChainRejectsMissingUser(t *testing.T) {
	pool := scopedDB(t)
	specimenID := seedJournalSpecimen(context.Background(), t, pool)

	base := api.New(api.Deps{
		Journal: &api.JournalServiceDeps{
			Entries: db.NewJournalEntryPostgres(pool),
		},
	})
	// RequireUser only — no Auth — so the context never gets a user.
	// Any request landing on a protected route must 401 with the §10
	// envelope.
	guarded := auth.RequireUser(base)
	srv := httptest.NewServer(guarded)
	t.Cleanup(srv.Close)

	resp, raw := journalDoReq(t, http.MethodGet,
		srv.URL+"/api/v1/specimens/"+specimenID.String()+"/journal", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s, want 401", resp.StatusCode, raw)
	}
	var env envelopeBody
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, raw)
	}
	if env.Error.Code != "unauthorized" {
		t.Errorf("error.code = %q, want unauthorized", env.Error.Code)
	}
}
