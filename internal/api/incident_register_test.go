package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/incidentregister"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// fakeIncidentRegister is an in-memory IncidentRegister for the handler
// tests. It records the last NewIncident passed to Create so the
// recorded_by wiring (caller identity, not body) can be asserted.
type fakeIncidentRegister struct {
	entries  []incidentregister.Incident
	lastIn   incidentregister.NewIncident
	createN  int
	seq      int64
	notFound bool // when true, GetByID returns ErrNotFound
}

func (f *fakeIncidentRegister) Create(_ context.Context, in incidentregister.NewIncident) (incidentregister.Incident, error) {
	f.createN++
	f.lastIn = in
	f.seq++
	e := incidentregister.Incident{
		ID:                   uuid.New(),
		Seq:                  f.seq,
		PersonalInfoInvolved: in.PersonalInfoInvolved,
		BecameAwareDate:      in.BecameAwareDate.UTC(),
		RecordedBy:           in.RecordedBy,
		RetainUntil:          in.BecameAwareDate.UTC().AddDate(5, 0, 0),
	}
	f.entries = append(f.entries, e)
	return e, nil
}

func (f *fakeIncidentRegister) GetByID(_ context.Context, id uuid.UUID) (incidentregister.Incident, error) {
	if f.notFound {
		return incidentregister.Incident{}, incidentregister.ErrNotFound
	}
	for _, e := range f.entries {
		if e.ID == id {
			return e, nil
		}
	}
	return incidentregister.Incident{}, incidentregister.ErrNotFound
}

func (f *fakeIncidentRegister) List(_ context.Context) ([]incidentregister.Incident, error) {
	return f.entries, nil
}

func (f *fakeIncidentRegister) Export(_ context.Context) (incidentregister.Export, error) {
	return incidentregister.Export{
		Incidents: f.entries,
		Integrity: incidentregister.VerifyResult{OK: true, Count: len(f.entries)},
	}, nil
}

// incidentRegisterTestServer wires api.New with a real (in-memory)
// Casbin enforcer seeded with the §13 v2 defaults, the standard role
// tokens, and the supplied fake register. Mirrors adminConsoleTestServer.
func incidentRegisterTestServer(t *testing.T, reg IncidentRegister) http.Handler {
	t.Helper()

	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	const (
		userSub   = "00000000-0000-0000-0000-0000000000b1"
		viewerSub = "00000000-0000-0000-0000-0000000000b2"
		opsSub    = "00000000-0000-0000-0000-0000000000b3"
		adminSub  = "00000000-0000-0000-0000-0000000000b4"
	)
	verifier := fakeVerifier{tokens: map[string]*oidc.Claims{
		"user-tok":   {Subject: userSub, Email: "user@minerals.local", Roles: []string{"user"}},
		"viewer-tok": {Subject: viewerSub, Email: "viewer@minerals.local", Roles: []string{"devops-viewer"}},
		"ops-tok":    {Subject: opsSub, Email: "ops@minerals.local", Roles: []string{"devops-admin"}},
		"admin-tok":  {Subject: adminSub, Email: "admin@minerals.local", Roles: []string{"admin"}},
	}}

	repo := newFakeUserRepo()
	for _, sub := range []string{userSub, viewerSub, opsSub, adminSub} {
		repo.seed(domain.User{
			ID:          uuid.MustParse(sub),
			KeycloakSub: sub,
			Email:       sub + "@minerals.local",
			Status:      domain.UserStatusActive,
		})
	}

	return New(Deps{Users: repo, Verifier: verifier, Enforcer: enf, IncidentRegister: reg})
}

const opsSubForAssert = "00000000-0000-0000-0000-0000000000b3"

// TestIncidentRegister_RecordedByIsCaller confirms recorded_by is taken
// from the authenticated caller's id, NOT from the request body — the
// register must attribute an entry to who actually filed it.
func TestIncidentRegister_RecordedByIsCaller(t *testing.T) {
	t.Parallel()
	reg := &fakeIncidentRegister{}
	h := incidentRegisterTestServer(t, reg)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/admin/incident-register/incidents", strings.NewReader(validFileBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ops-tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if reg.createN != 1 {
		t.Fatalf("Create called %d times, want 1", reg.createN)
	}
	if reg.lastIn.RecordedBy != opsSubForAssert {
		t.Errorf("recorded_by = %q, want caller id %q", reg.lastIn.RecordedBy, opsSubForAssert)
	}
	// The 8 fields must round-trip from body to store input.
	if reg.lastIn.PersonalInfoInvolved != "email addresses" {
		t.Errorf("personal_info_involved = %q, want from body", reg.lastIn.PersonalInfoInvolved)
	}
	if reg.lastIn.BecameAwareDate.Format("2006-01-02") != "2026-05-02" {
		t.Errorf("became_aware_date = %v, want 2026-05-02", reg.lastIn.BecameAwareDate)
	}
}

// TestIncidentRegister_GetNotFound maps the store's ErrNotFound to a 404
// envelope for a viewer-permitted caller.
func TestIncidentRegister_GetNotFound(t *testing.T) {
	t.Parallel()
	reg := &fakeIncidentRegister{notFound: true}
	h := incidentRegisterTestServer(t, reg)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/incident-register/incidents/"+uuid.New().String(), nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestIncidentRegister_FileBadRequest rejects a missing required field
// (huma schema validation) before reaching the store.
func TestIncidentRegister_FileBadRequest(t *testing.T) {
	t.Parallel()
	reg := &fakeIncidentRegister{}
	h := incidentRegisterTestServer(t, reg)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/admin/incident-register/incidents", strings.NewReader(`{"circumstances":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer admin-tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 422/400; body = %s", rec.Code, rec.Body.String())
	}
	if reg.createN != 0 {
		t.Errorf("Create called %d times on invalid body, want 0", reg.createN)
	}
}

// TestIncidentRegister_NotWired confirms that when no register store is
// configured the endpoints are not registered (404) — the single-DB
// dev/test path.
func TestIncidentRegister_NotWired(t *testing.T) {
	t.Parallel()
	h := incidentRegisterTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/incident-register/incidents", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route unregistered); body = %s", rec.Code, rec.Body.String())
	}
}

// TestAdminOverview_IncidentSectionStatus asserts the overview manifest
// flips the incident-register section to "available" when the store is
// wired, and reports "planned" when it is not (mi-2p6i).
func TestAdminOverview_IncidentSectionStatus(t *testing.T) {
	t.Parallel()

	sectionStatus := func(t *testing.T, reg IncidentRegister) string {
		t.Helper()
		h := incidentRegisterTestServer(t, reg)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil)
		req.Header.Set("Authorization", "Bearer admin-tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("overview status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body adminOverviewBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, s := range body.Sections {
			if s.Key == "incident-register" {
				return s.Status
			}
		}
		t.Fatal("incident-register section missing from overview")
		return ""
	}

	if got := sectionStatus(t, &fakeIncidentRegister{}); got != "available" {
		t.Errorf("wired: section status = %q, want available", got)
	}
	if got := sectionStatus(t, nil); got != "planned" {
		t.Errorf("unwired: section status = %q, want planned", got)
	}
}

func validFileBody() string {
	return `{
		"personal_info_involved": "email addresses",
		"circumstances": "a laptop was lost",
		"incident_occurred_at": "2026-05-01",
		"became_aware_date": "2026-05-02",
		"people_affected": "approximately 40",
		"risk_assessment": "No - the disk was encrypted",
		"cai_notified": false,
		"individuals_notified": false,
		"measures_taken": "rotated credentials, enabled remote wipe"
	}`
}

// TestIncidentRegister_RoleGate is the load-bearing authz test across the
// endpoints. View endpoints (list/get/export) require devops:view; the
// file (POST) endpoint requires devops:edit. The matrix carries an
// explicit expected status per (role, endpoint) so the edit-vs-view split
// is asserted unambiguously: a devops-viewer reads (200) but is 403 on
// POST, while devops-admin and admin both read and file (201).
func TestIncidentRegister_RoleGate(t *testing.T) {
	t.Parallel()

	const (
		fileEP   = "/api/v1/admin/incident-register/incidents"
		listEP   = "/api/v1/admin/incident-register/incidents"
		exportEP = "/api/v1/admin/incident-register/export"
	)

	cases := []struct {
		name                                    string
		token                                   string
		wantFile, wantList, wantGet, wantExport int
	}{
		{"anonymous", "", http.StatusUnauthorized, http.StatusUnauthorized, http.StatusUnauthorized, http.StatusUnauthorized},
		{"plain user", "user-tok", http.StatusForbidden, http.StatusForbidden, http.StatusForbidden, http.StatusForbidden},
		{"devops-viewer", "viewer-tok", http.StatusForbidden, http.StatusOK, http.StatusOK, http.StatusOK},
		{"devops-admin", "ops-tok", http.StatusCreated, http.StatusOK, http.StatusOK, http.StatusOK},
		{"admin", "admin-tok", http.StatusCreated, http.StatusOK, http.StatusOK, http.StatusOK},
	}

	do := func(t *testing.T, h http.Handler, method, path, token, body string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Seed one entry so the GET-by-id path has a target to read
			// for the roles that can view it (the gate fires before the
			// lookup, so non-viewers get 401/403 regardless of id).
			reg := &fakeIncidentRegister{}
			seeded, _ := reg.Create(context.Background(), incidentregister.NewIncident{
				BecameAwareDate: time.Now().UTC(),
			})
			h := incidentRegisterTestServer(t, reg)

			if got := do(t, h, http.MethodPost, fileEP, tc.token, validFileBody()); got != tc.wantFile {
				t.Errorf("POST file: status = %d, want %d", got, tc.wantFile)
			}
			if got := do(t, h, http.MethodGet, listEP, tc.token, ""); got != tc.wantList {
				t.Errorf("GET list: status = %d, want %d", got, tc.wantList)
			}
			getEP := "/api/v1/admin/incident-register/incidents/" + seeded.ID.String()
			if got := do(t, h, http.MethodGet, getEP, tc.token, ""); got != tc.wantGet {
				t.Errorf("GET by id: status = %d, want %d", got, tc.wantGet)
			}
			if got := do(t, h, http.MethodGet, exportEP, tc.token, ""); got != tc.wantExport {
				t.Errorf("GET export: status = %d, want %d", got, tc.wantExport)
			}
		})
	}
}
