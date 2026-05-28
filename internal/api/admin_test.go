package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	return adminConsoleTestServerWith(t, nil)
}

// adminConsoleTestServerWith is adminConsoleTestServer with an optional
// AdminRepo wired — the seam the users + published-content surfaces
// (mi-n5av / mi-gtkp) need. A nil admin reproduces the foundation-only
// server (those routes unregistered, sections "planned").
func adminConsoleTestServerWith(t *testing.T, admin domain.AdminRepo) http.Handler {
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

	return New(Deps{Users: repo, Verifier: verifier, Enforcer: enf, Admin: admin})
}

// fakeAdminRepo is the unit-test double for domain.AdminRepo. It returns
// canned rows (and an empty next-cursor); err, when set, exercises the
// 500 path.
type fakeAdminRepo struct {
	users   []domain.AdminUser
	content []domain.AdminContent
}

func (f *fakeAdminRepo) ListUsers(_ context.Context, _ domain.Page) ([]domain.AdminUser, domain.Cursor, error) {
	return f.users, "", nil
}

func (f *fakeAdminRepo) ListPublishedContent(_ context.Context, _ domain.Page) ([]domain.AdminContent, domain.Cursor, error) {
	return f.content, "", nil
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

// TestAdminOverview_SectionsFlipWhenAdminWired confirms that wiring the
// AdminRepo flips the users + published-content sections to "available"
// in the overview manifest (mi-n5av / mi-gtkp) while the still-unbuilt
// sections stay "planned".
func TestAdminOverview_SectionsFlipWhenAdminWired(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServerWith(t, &fakeAdminRepo{})

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
	want := map[string]string{
		"users":             "available",
		"published-content": "available",
		"moderation":        "planned",
		"site-management":   "planned",
	}
	got := map[string]string{}
	for _, s := range body.Sections {
		got[s.Key] = s.Status
	}
	for key, status := range want {
		if got[key] != status {
			t.Errorf("section %q status = %q, want %q", key, got[key], status)
		}
	}
}

// TestAdminUsers_RoleGate is the load-bearing access-control test for the
// non-personal user list: anonymous → 401, non-admin user → 403, and the
// devops/admin roles → 200.
func TestAdminUsers_RoleGate(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServerWith(t, &fakeAdminRepo{})

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
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestAdminUsers_BodyShapeNoPII confirms the user list returns the
// non-personal fields AND, critically, that NO email or PII leaks onto
// the wire — the load-bearing PII-boundary assertion for mi-n5av.
func TestAdminUsers_BodyShapeNoPII(t *testing.T) {
	t.Parallel()

	name := "Rocky"
	repo := &fakeAdminRepo{users: []domain.AdminUser{{
		ID:            uuid.MustParse("00000000-0000-0000-0000-0000000000c1"),
		DisplayName:   &name,
		Status:        domain.UserStatusActive,
		SpecimenCount: 3,
		PhotoCount:    7,
		JournalCount:  2,
		CreatedAt:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}}}
	h := adminConsoleTestServerWith(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	// The hard PII gate: nothing email-shaped may appear on the wire.
	if strings.Contains(raw, "@") || strings.Contains(strings.ToLower(raw), "email") {
		t.Fatalf("user list leaked email/PII: %s", raw)
	}

	var body adminUserListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw = %s", err, raw)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	got := body.Items[0]
	if got.DisplayName == nil || *got.DisplayName != "Rocky" {
		t.Errorf("display_name = %v, want Rocky", got.DisplayName)
	}
	if got.SpecimenCount != 3 || got.PhotoCount != 7 || got.JournalCount != 2 {
		t.Errorf("counts = (%d,%d,%d), want (3,7,2)", got.SpecimenCount, got.PhotoCount, got.JournalCount)
	}
	if got.Status != "active" {
		t.Errorf("status = %q, want active", got.Status)
	}
}

// TestAdminUsers_AuditLogged confirms every list access emits the
// audit-trail event the bead requires (admin role + what was viewed),
// and that the event carries no email/PII.
func TestAdminUsers_AuditLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	h := adminConsoleTestServerWith(t, &fakeAdminRepo{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	logged := buf.String()
	if !strings.Contains(logged, `"event":"admin.view"`) || !strings.Contains(logged, `"surface":"users"`) {
		t.Fatalf("audit event missing from log: %s", logged)
	}
	if strings.Contains(logged, "@") {
		t.Fatalf("audit log leaked email/PII: %s", logged)
	}
}

// TestAdminPublishedContent_RoleGate mirrors the users gate for the
// published-content review feed (mi-gtkp).
func TestAdminPublishedContent_RoleGate(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServerWith(t, &fakeAdminRepo{})

	cases := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{"anonymous is 401", "", http.StatusUnauthorized},
		{"plain user is 403", "user-tok", http.StatusForbidden},
		{"admin is 200", "admin-tok", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/published-content", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestAdminPublishedContent_BodyShape confirms the feed returns its
// owner-attributed rows (display name + opaque id, no email).
func TestAdminPublishedContent_BodyShape(t *testing.T) {
	t.Parallel()

	owner := "Specimen Owner"
	repo := &fakeAdminRepo{content: []domain.AdminContent{{
		Kind:             domain.AdminContentSpecimen,
		ID:               uuid.MustParse("00000000-0000-0000-0000-0000000000d1"),
		SpecimenID:       uuid.MustParse("00000000-0000-0000-0000-0000000000d1"),
		Title:            "Public Quartz",
		Visibility:       domain.VisibilityPublic,
		OwnerID:          uuid.MustParse("00000000-0000-0000-0000-0000000000c1"),
		OwnerDisplayName: &owner,
		CreatedAt:        time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	}}}
	h := adminConsoleTestServerWith(t, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/published-content", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "@") {
		t.Fatalf("published-content leaked email/PII: %s", rec.Body.String())
	}
	var body publishedContentListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; raw = %s", err, rec.Body.String())
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(body.Items))
	}
	got := body.Items[0]
	if got.Kind != "specimen" || got.Title != "Public Quartz" || got.Visibility != "public" {
		t.Errorf("unexpected row: %+v", got)
	}
	if got.OwnerDisplayName == nil || *got.OwnerDisplayName != "Specimen Owner" {
		t.Errorf("owner_display_name = %v, want Specimen Owner", got.OwnerDisplayName)
	}
}

// TestAdminDataSurfacesUnregisteredWithoutRepo confirms that a server
// built WITHOUT an AdminRepo leaves the two data routes unregistered —
// they fall through to the §10 404 envelope rather than 200/500.
func TestAdminDataSurfacesUnregisteredWithoutRepo(t *testing.T) {
	t.Parallel()
	h := adminConsoleTestServer(t) // nil admin

	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/published-content"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer admin-tok")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404 (route unregistered)", path, rec.Code)
		}
	}
}
