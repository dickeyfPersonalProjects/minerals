package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// adminConsoleTestServer wires api.New() with a real (in-memory)
// Casbin enforcer seeded with the §13 v2 default policies, a fake
// verifier that maps tokens to roles, and a user repo pre-seeded with
// an active row per token subject. This is the unit-level analogue of
// the integration authz suite — no Postgres required, because the
// devops gate the console uses resolves entirely from the policy set
// and the JWT roles (the `devops` resource carries no shares/instance
// lookup).
func adminConsoleTestServer(t *testing.T) http.Handler {
	t.Helper()

	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	const (
		userSub   = "00000000-0000-0000-0000-0000000000a1"
		viewerSub = "00000000-0000-0000-0000-0000000000a2"
		adminSub  = "00000000-0000-0000-0000-0000000000a3"
	)
	verifier := fakeVerifier{tokens: map[string]*oidc.Claims{
		"user-tok":   {Subject: userSub, Email: "user@minerals.local", Roles: []string{"user"}},
		"viewer-tok": {Subject: viewerSub, Email: "viewer@minerals.local", Roles: []string{"devops-viewer"}},
		"admin-tok":  {Subject: adminSub, Email: "admin@minerals.local", Roles: []string{"admin"}},
	}}

	repo := newFakeUserRepo()
	for _, sub := range []string{userSub, viewerSub, adminSub} {
		repo.seed(domain.User{
			ID:          uuid.MustParse(sub),
			KeycloakSub: sub,
			Email:       sub + "@minerals.local",
			Status:      domain.UserStatusActive,
		})
	}

	return New(Deps{Users: repo, Verifier: verifier, Enforcer: enf})
}

// TestAdminOverview_RoleGate is the load-bearing test for the mi-agff
// foundation: the console landing must be reachable only to the
// admin/devops roles. Anonymous → 401, an authenticated non-admin
// user → 403, devops-viewer and admin → 200.
func TestAdminOverview_RoleGate(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServer(t)

	cases := []struct {
		name       string
		token      string // empty = no Authorization header
		wantStatus int
	}{
		{"anonymous is 401", "", http.StatusUnauthorized},
		{"plain user is 403", "user-tok", http.StatusForbidden},
		{"devops-viewer is 200", "viewer-tok", http.StatusOK},
		{"admin is 200", "admin-tok", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s",
					rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestAdminOverview_BodyShape confirms a permitted caller receives the
// placeholder manifest the SPA shell renders. Every advertised section
// is "planned" in the foundation pass — none of the data-bearing
// surfaces exist yet.
func TestAdminOverview_BodyShape(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body adminOverviewBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw = %s", err, rec.Body.String())
	}
	if body.Console != "admin" {
		t.Errorf("console = %q, want %q", body.Console, "admin")
	}
	if len(body.Sections) == 0 {
		t.Fatal("sections is empty; want the planned-surface manifest")
	}
	for _, s := range body.Sections {
		if s.Status != "planned" {
			t.Errorf("section %q status = %q, want planned", s.Key, s.Status)
		}
	}
}
