//go:build integration

package api_test

import (
	"context"
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
)

// Authorization integration suite (mi-aw3b / CONTRACT.md §13 v2
// layer 2). Stands up api.New() with a real Casbin enforcer in front
// of a real Postgres pool and drives the per-resource enforcement
// through the full HTTP stack.
//
// The httptest server runs with a nil Verifier, so every request is
// the seeded stub user (migration 0008). To exercise a denial we seed
// a row owned by a *different* user directly through the repo, then
// hit the handler as the stub user — the enforcer must reject access
// to another user's private resource and permit a public one.

func authzTestSrv(t *testing.T) (*httptest.Server, *pgxpool.Pool, *db.SpecimenPostgres) {
	t.Helper()
	pool := scopedDB(t)
	enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enforcer); err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	specimens := db.NewSpecimenPostgres(pool)
	h := api.New(api.Deps{Specimens: specimens, Enforcer: enforcer})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, pool, specimens
}

func TestIntegration_Authz_SpecimenEnforcement(t *testing.T) {
	srv, pool, specimens := authzTestSrv(t)

	// Seed a foreign user so the specimen's author_id FK resolves; the
	// stub user driving the HTTP requests is deliberately NOT the
	// author.
	other := domain.NewID()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, keycloak_sub, email, status)
		VALUES ($1, $2, $3, 'active')`,
		other, "authz-"+other.String(), other.String()+"@example.invalid",
	); err != nil {
		t.Fatalf("seed foreign user: %v", err)
	}

	otherCtx := auth.WithUser(context.Background(), auth.User{ID: other, Roles: []string{"user"}})
	mk := func(vis domain.Visibility) uuid.UUID {
		t.Helper()
		now := time.Now().UTC().Truncate(time.Microsecond)
		s := domain.Specimen{
			ID: domain.NewID(), Type: domain.SpecimenMineral, Name: "foreign-" + string(vis),
			Visibility: vis, TypeData: []byte(`{}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := specimens.Create(otherCtx, nil, s); err != nil {
			t.Fatalf("create foreign specimen: %v", err)
		}
		return s.ID
	}
	privID := mk(domain.VisibilityPrivate)
	pubID := mk(domain.VisibilityPublic)

	// The stub user is not the author: a private specimen is 404
	// (CONTRACT.md §13 v2 don't-leak-existence rule for detail
	// endpoints), a public one is permitted by the §13 v2 view
	// shortcut.
	if status, body := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+privID.String(), nil); status != http.StatusNotFound {
		t.Errorf("GET foreign private: status %d, want 404; body=%s", status, body)
	}
	if status, body := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+pubID.String(), nil); status != http.StatusOK {
		t.Errorf("GET foreign public: status %d, want 200; body=%s", status, body)
	}

	// Writes always go through Casbin — even on a public resource the
	// stub user may not edit or delete another user's specimen.
	if status, body := doJSON(t, srv.Client(), http.MethodPatch,
		srv.URL+"/api/v1/specimens/"+pubID.String(),
		map[string]any{"name": "hijacked"}); status != http.StatusForbidden {
		t.Errorf("PATCH foreign public: status %d, want 403; body=%s", status, body)
	}
	if status, body := doJSON(t, srv.Client(), http.MethodDelete,
		srv.URL+"/api/v1/specimens/"+privID.String(), nil); status != http.StatusForbidden {
		t.Errorf("DELETE foreign private: status %d, want 403; body=%s", status, body)
	}

	// The stub user's own specimen round-trips: create, then GET.
	created := createMineral(t, srv, "stub-owned")
	if status, body := doJSON(t, srv.Client(), http.MethodGet,
		srv.URL+"/api/v1/specimens/"+created.ID.String(), nil); status != http.StatusOK {
		t.Errorf("GET own specimen: status %d, want 200; body=%s", status, body)
	}
}

// TestIntegration_Authz_ChainCollectorEnforcement is the regression
// guard for mi-6863: PUT /specimens/{id}/collectors must authorize
// every collector_id in the body, not just the specimen. Collectors
// are owned-only and GET embeds the full collector (name, notes); a
// caller who could link another user's private collector to their own
// specimen would read it straight back. The foreign collector must be
// rejected as collector_not_found (a forbidden view rewritten to 404,
// so the response doesn't leak that the id exists), while the caller's
// own collector links normally.
func TestIntegration_Authz_ChainCollectorEnforcement(t *testing.T) {
	pool := scopedDB(t)
	enforcer, err := authz.NewEnforcer(nil, db.NewSharesLookup(pool))
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enforcer); err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	h := api.New(api.Deps{
		Specimens:          db.NewSpecimenPostgres(pool),
		Collectors:         db.NewCollectorPostgres(pool),
		SpecimenCollectors: db.NewSpecimenCollectorPostgres(pool),
		Enforcer:           enforcer,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Seed a foreign user and a private collector they own. The stub
	// user driving the HTTP requests is deliberately NOT the author.
	other := domain.NewID()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, keycloak_sub, email, status)
		VALUES ($1, $2, $3, 'active')`,
		other, "authz-chain-"+other.String(), other.String()+"@example.invalid",
	); err != nil {
		t.Fatalf("seed foreign user: %v", err)
	}
	otherCtx := auth.WithUser(context.Background(), auth.User{ID: other, Roles: []string{"user"}})
	now := time.Now().UTC().Truncate(time.Microsecond)
	foreignColl := domain.Collector{
		ID: domain.NewID(), Name: "foreign-collector", AuthorID: other,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.NewCollectorPostgres(pool).Create(otherCtx, nil, foreignColl); err != nil {
		t.Fatalf("create foreign collector: %v", err)
	}

	// The stub user owns the specimen they're editing.
	specID := createMineral(t, srv, "stub-chain-owner").ID

	// Linking the foreign collector must be rejected as a 404 — the
	// stub user can't view it, and that's indistinguishable on the wire
	// from a missing id.
	if status, body := doJSON(t, srv.Client(), http.MethodPut,
		srv.URL+"/api/v1/specimens/"+specID.String()+"/collectors",
		map[string]any{"collector_ids": []string{foreignColl.ID.String()}}); status != http.StatusNotFound {
		t.Errorf("PUT foreign collector: status %d, want 404; body=%s", status, body)
	}

	// The stub user's own collector links normally.
	ownColl := createCollector(t, srv, "stub-own-collector").ID
	if status, body := doJSON(t, srv.Client(), http.MethodPut,
		srv.URL+"/api/v1/specimens/"+specID.String()+"/collectors",
		map[string]any{"collector_ids": []string{ownColl.String()}}); status != http.StatusOK {
		t.Errorf("PUT own collector: status %d, want 200; body=%s", status, body)
	}
}
