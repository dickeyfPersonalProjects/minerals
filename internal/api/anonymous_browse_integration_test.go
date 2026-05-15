//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dickeyfPersonalProjects/minerals/internal/api"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/db"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// Anonymous-browsing integration suite (mi-7do / CONTRACT.md §13 v2).
//
// Drives the HTTP layer to confirm the v2 read-side rules:
//   - top-level list endpoints never 401 — anonymous callers see
//     visibility-filtered rows
//   - top-level detail endpoints 404 (not 403/401) when the caller
//     cannot see the row — no existence leak
//   - sub-resource lists 404 when the caller cannot see the parent
//   - write endpoints still 401 anonymous, 403 forbidden
//
// Unlike the other integration suites in this package, this one
// wires a *non-nil* TokenVerifier so a missing Authorization header
// is treated as truly anonymous (not StubUser). The verifier admits
// one canonical "Bearer valid-owner" token; any other token is
// rejected.

// fakeVerifierIT is a minimal TokenVerifier for integration tests.
// "valid-owner" maps to a seeded user; everything else 401s.
type fakeVerifierIT struct {
	tokens map[string]*oidc.Claims
}

func (f fakeVerifierIT) Verify(_ context.Context, raw string) (*oidc.Claims, error) {
	c, ok := f.tokens[raw]
	if !ok {
		return nil, errors.New("oidc: token not recognized")
	}
	return c, nil
}

// anonBrowseTestSrv stands up an httptest server with a real Casbin
// enforcer, a real Postgres pool, and a fake TokenVerifier. Returns
// the server, the owner-user UUID (creator of seeded fixtures), and
// a token that authenticates as that owner.
func anonBrowseTestSrv(t *testing.T) (srv *httptest.Server, pool *pgxpool.Pool, ownerID uuid.UUID, ownerToken string) {
	t.Helper()
	pool = scopedDB(t)

	enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enforcer); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	// Seed an owner row directly so the verifier-resolved Sub maps
	// to a known users.id. The HTTP requests below run with no
	// UserRepo wired (Users: nil), so the resolver chain collapses
	// to humaAuth-only and the claims-derived ID flows straight
	// through to the handlers.
	ownerID = domain.NewID()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, keycloak_sub, email, status)
		VALUES ($1, $2, $3, 'active')`,
		ownerID, ownerID.String(), ownerID.String()+"@example.invalid",
	); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	ownerToken = "valid-owner"
	verifier := fakeVerifierIT{tokens: map[string]*oidc.Claims{
		ownerToken: {
			Subject: ownerID.String(),
			Email:   ownerID.String() + "@example.invalid",
			Roles:   []string{"user"},
		},
	}}

	h := api.New(api.Deps{
		Specimens:          db.NewSpecimenPostgres(pool),
		Collectors:         db.NewCollectorPostgres(pool),
		SpecimenCollectors: db.NewSpecimenCollectorPostgres(pool),
		Enforcer:           enforcer,
		Verifier:           verifier,
	})
	srv = httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, pool, ownerID, ownerToken
}

// doAuthJSON is a doJSON analogue that attaches a Bearer token when
// non-empty. An empty token is the explicit anonymous-caller path.
func doAuthJSON(t *testing.T, c *http.Client, method, url, token string, body any) (int, []byte) {
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

// seedSpecimenDirect inserts a specimen with the given visibility
// authored by ownerID, bypassing the HTTP create endpoint (which
// would require auth). Returns the new id.
func seedSpecimenDirect(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID, vis domain.Visibility, name string) uuid.UUID {
	t.Helper()
	repo := db.NewSpecimenPostgres(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := domain.Specimen{
		ID:         domain.NewID(),
		Type:       domain.SpecimenMineral,
		Name:       name,
		Visibility: vis,
		TypeData:   []byte(`{}`),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	ctx := auth.WithUser(context.Background(), auth.User{ID: ownerID, Roles: []string{"user"}})
	if err := repo.Create(ctx, nil, s); err != nil {
		t.Fatalf("seed specimen %q: %v", name, err)
	}
	return s.ID
}

// TestIntegration_AnonymousBrowse_TopLevelList covers the bead
// acceptance criterion: GET /api/v1/specimens with no Authorization
// header returns 200 with public rows only.
func TestIntegration_AnonymousBrowse_TopLevelList_Specimens(t *testing.T) {
	srv, pool, ownerID, _ := anonBrowseTestSrv(t)

	pubID := seedSpecimenDirect(t, pool, ownerID, domain.VisibilityPublic, "public-1")
	_ = seedSpecimenDirect(t, pool, ownerID, domain.VisibilityPrivate, "private-1")

	status, data := doAuthJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens", "", nil)
	if status != http.StatusOK {
		t.Fatalf("anonymous list: status %d body=%s, want 200", status, data)
	}
	var body struct {
		Items []struct {
			ID         uuid.UUID `json:"id"`
			Visibility string    `json:"visibility"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode list: %v body=%s", err, data)
	}
	// Anonymous must see public-1 and only public-1 — private-1
	// must be filtered by the DB scoping layer.
	if len(body.Items) != 1 {
		t.Fatalf("anonymous list size = %d, want 1; body=%s", len(body.Items), data)
	}
	if body.Items[0].ID != pubID {
		t.Errorf("anonymous list item = %s, want public id %s", body.Items[0].ID, pubID)
	}
	if body.Items[0].Visibility != "public" {
		t.Errorf("anonymous list visibility = %q, want public", body.Items[0].Visibility)
	}
}

// TestIntegration_AnonymousBrowse_TopLevelList_Collectors covers the
// bead acceptance criterion: GET /api/v1/collectors with no
// Authorization header returns 200. Collectors are owned per-user
// with no public tier, so an anonymous caller sees an empty list.
func TestIntegration_AnonymousBrowse_TopLevelList_Collectors(t *testing.T) {
	srv, pool, ownerID, _ := anonBrowseTestSrv(t)

	// Seed a collector so the list isn't trivially empty for the
	// authenticated caller — the anonymous list MUST still come back
	// empty because anonymous owns nothing.
	now := time.Now().UTC().Truncate(time.Microsecond)
	c := domain.Collector{
		ID:        domain.NewID(),
		Name:      "owner-collector",
		AuthorID:  ownerID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	ctx := auth.WithUser(context.Background(), auth.User{ID: ownerID, Roles: []string{"user"}})
	if err := db.NewCollectorPostgres(pool).Create(ctx, nil, c); err != nil {
		t.Fatalf("seed collector: %v", err)
	}

	status, data := doAuthJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/collectors", "", nil)
	if status != http.StatusOK {
		t.Fatalf("anonymous list: status %d body=%s, want 200", status, data)
	}
	var body struct {
		Items []struct {
			ID uuid.UUID `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode list: %v body=%s", err, data)
	}
	if len(body.Items) != 0 {
		t.Errorf("anonymous collector list size = %d, want 0; body=%s", len(body.Items), data)
	}
}

// TestIntegration_AnonymousBrowse_TopLevelDetail covers the bead
// acceptance: GET /api/v1/specimens/<public_id> → 200, GET on a
// private specimen the caller can't see → 404 (not 403/401).
func TestIntegration_AnonymousBrowse_TopLevelDetail(t *testing.T) {
	srv, pool, ownerID, _ := anonBrowseTestSrv(t)

	pubID := seedSpecimenDirect(t, pool, ownerID, domain.VisibilityPublic, "public-detail")
	privID := seedSpecimenDirect(t, pool, ownerID, domain.VisibilityPrivate, "private-detail")

	// Public: anonymous can see it.
	if status, data := doAuthJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+pubID.String(), "", nil); status != http.StatusOK {
		t.Errorf("anonymous GET public: status %d, want 200; body=%s", status, data)
	}
	// Private: anonymous gets 404, not 403/401 — don't leak existence.
	if status, data := doAuthJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+privID.String(), "", nil); status != http.StatusNotFound {
		t.Errorf("anonymous GET private: status %d, want 404; body=%s", status, data)
	}
}

// TestIntegration_AnonymousBrowse_SubResourceList covers the bead
// acceptance: GET /api/v1/specimens/<forbidden_id>/photos → 404
// before the sub-list is ever queried (parent visibility gates).
// We exercise the collectors sub-resource because it's wired in the
// default test server; the photos handler follows the identical
// gate.
func TestIntegration_AnonymousBrowse_SubResourceList_ParentGate(t *testing.T) {
	srv, pool, ownerID, _ := anonBrowseTestSrv(t)

	privID := seedSpecimenDirect(t, pool, ownerID, domain.VisibilityPrivate, "private-sub")

	// Anonymous → 404 (parent unviewable, sub-list never queried).
	if status, data := doAuthJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+privID.String()+"/collectors", "", nil); status != http.StatusNotFound {
		t.Errorf("anonymous GET collectors-of-private: status %d, want 404; body=%s",
			status, data)
	}

	// Sanity: a public parent admits the sub-list (empty in this case).
	pubID := seedSpecimenDirect(t, pool, ownerID, domain.VisibilityPublic, "public-sub")
	if status, data := doAuthJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+pubID.String()+"/collectors", "", nil); status != http.StatusOK {
		t.Errorf("anonymous GET collectors-of-public: status %d, want 200; body=%s",
			status, data)
	}
}

// TestIntegration_AnonymousBrowse_WritesStill401 confirms write
// endpoints still reject an anonymous caller with 401 — only reads
// switched to the optional-auth chain.
func TestIntegration_AnonymousBrowse_WritesStill401(t *testing.T) {
	srv, _, _, _ := anonBrowseTestSrv(t)

	status, data := doAuthJSON(t, srv.Client(), http.MethodPost,
		srv.URL+"/api/v1/specimens", "", map[string]any{
			"type": "mineral",
			"name": "anonymous-create-attempt",
		})
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous POST: status %d body=%s, want 401", status, data)
	}
}
