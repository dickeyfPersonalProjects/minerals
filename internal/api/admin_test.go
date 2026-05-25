package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// ── fakeAdminStats ────────────────────────────────────────────────────────────

// fakeAdminStats implements domain.AdminStatsProvider for unit tests.
type fakeAdminStats struct {
	users          int64
	specimens      int64
	photos         int64
	journalEntries int64
	err            error // if non-nil, all Count calls return this error
}

func (f *fakeAdminStats) CountUsers(ctx context.Context) (int64, error) {
	return f.users, f.err
}
func (f *fakeAdminStats) CountSpecimens(ctx context.Context) (int64, error) {
	return f.specimens, f.err
}
func (f *fakeAdminStats) CountPhotos(ctx context.Context) (int64, error) {
	return f.photos, f.err
}
func (f *fakeAdminStats) CountJournalEntries(ctx context.Context) (int64, error) {
	return f.journalEntries, f.err
}

// ── test server helpers ───────────────────────────────────────────────────────

// adminConsoleTestServer wires api.New() with a real (in-memory)
// Casbin enforcer seeded with the §13 v2 default policies, a fake
// verifier that maps tokens to roles, and a user repo pre-seeded with
// an active row per token subject. This is the unit-level analogue of
// the integration authz suite — no Postgres required, because the
// devops gate the console uses resolves entirely from the policy set
// and the JWT roles.
func adminConsoleTestServer(t *testing.T) http.Handler {
	t.Helper()
	return adminConsoleTestServerWithDeps(t, nil, false)
}

// adminConsoleTestServerWithDeps is the general form used by tests that
// need to control AdminStats or RegistrationEnabled.
func adminConsoleTestServerWithDeps(t *testing.T, stats domain.AdminStatsProvider, regEnabled bool) http.Handler {
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

	return New(Deps{
		Users:               repo,
		Verifier:            verifier,
		Enforcer:            enf,
		AdminStats:          stats,
		RegistrationEnabled: regEnabled,
	})
}

// ── Overview tests ────────────────────────────────────────────────────────────

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
// manifest. Verifies that site-management is now "available" and every
// other section is "planned".
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
		t.Fatal("sections is empty; want the manifest")
	}

	siteManagementFound := false
	for _, s := range body.Sections {
		if s.Key == "site-management" {
			siteManagementFound = true
			if s.Status != "available" {
				t.Errorf("site-management status = %q, want available (mi-ilvt)", s.Status)
			}
		} else {
			if s.Status != "planned" {
				t.Errorf("section %q status = %q, want planned", s.Key, s.Status)
			}
		}
	}
	if !siteManagementFound {
		t.Error("site-management section missing from manifest")
	}
}

// ── Health endpoint tests ─────────────────────────────────────────────────────

// TestAdminHealth_RoleGate mirrors the mi-agff role-gate matrix for the
// new /admin/health endpoint (mi-ilvt). Same auth policy: anonymous→401,
// non-admin user→403. Authorized callers (devops-viewer/admin) reach the
// handler; in the unit-test environment no DB or Storage are wired so the
// handler returns 503 (not-ready). A 503 proves the auth gate was cleared —
// 401/403 would have come from the auth layer before the handler ran.
func TestAdminHealth_RoleGate(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServer(t)

	cases := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{"anonymous is 401", "", http.StatusUnauthorized},
		{"plain user is 403", "user-tok", http.StatusForbidden},
		// 503 = authorized but no DB/Storage wired in the unit-test seam.
		{"devops-viewer clears gate (503 not-ready)", "viewer-tok", http.StatusServiceUnavailable},
		{"admin clears gate (503 not-ready)", "admin-tok", http.StatusServiceUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
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

// TestAdminHealth_BodyShape verifies the health response includes the
// required fields: ready, registration_enabled, and a non-empty checks map.
func TestAdminHealth_BodyShape(t *testing.T) {
	t.Parallel()

	for _, regEnabled := range []bool{false, true} {
		regEnabled := regEnabled
		t.Run(fmt.Sprintf("registration_enabled=%v", regEnabled), func(t *testing.T) {
			t.Parallel()
			h := adminConsoleTestServerWithDeps(t, nil, regEnabled)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
			req.Header.Set("Authorization", "Bearer admin-tok")
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 200 or 503; body = %s",
					rec.Code, rec.Body.String())
			}

			var body adminHealthBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v; raw = %s", err, rec.Body.String())
			}

			if body.RegistrationEnabled != regEnabled {
				t.Errorf("registration_enabled = %v, want %v", body.RegistrationEnabled, regEnabled)
			}
			if len(body.Checks) == 0 {
				t.Error("checks map is empty; want at least one check entry")
			}
			// Verify shape of each check.
			for k, c := range body.Checks {
				_ = k
				_ = c.OK // must be decodable
			}
		})
	}
}

// ── Stats endpoint tests ──────────────────────────────────────────────────────

// TestAdminStats_RoleGate mirrors the role-gate matrix for /admin/stats.
func TestAdminStats_RoleGate(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServer(t)

	cases := []struct {
		name       string
		token      string
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
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/stats", nil)
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

// TestAdminStats_NilProvider verifies that when no AdminStats provider
// is wired (the unit-test seam), all counts are zero and the endpoint
// still returns 200.
func TestAdminStats_NilProvider(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServerWithDeps(t, nil, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body adminStatsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw = %s", err, rec.Body.String())
	}
	if body.Users != 0 || body.Specimens != 0 || body.Photos != 0 || body.JournalEntries != 0 {
		t.Errorf("nil provider should return all-zero counts; got %+v", body)
	}
}

// TestAdminStats_WithProvider verifies that counts from the provider are
// faithfully forwarded to the response.
func TestAdminStats_WithProvider(t *testing.T) {
	t.Parallel()

	fake := &fakeAdminStats{
		users:          10,
		specimens:      25,
		photos:         100,
		journalEntries: 5,
	}
	h := adminConsoleTestServerWithDeps(t, fake, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body adminStatsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw = %s", err, rec.Body.String())
	}
	if body.Users != 10 {
		t.Errorf("users = %d, want 10", body.Users)
	}
	if body.Specimens != 25 {
		t.Errorf("specimens = %d, want 25", body.Specimens)
	}
	if body.Photos != 100 {
		t.Errorf("photos = %d, want 100", body.Photos)
	}
	if body.JournalEntries != 5 {
		t.Errorf("journal_entries = %d, want 5", body.JournalEntries)
	}
}
