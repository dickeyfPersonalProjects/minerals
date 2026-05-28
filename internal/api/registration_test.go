package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// fakeSettings is the unit-test double for domain.SettingsRepo. stored
// is nil until SetRegistrationEnabled writes (mirroring an unset row);
// getErr / setErr exercise the error paths.
type fakeSettings struct {
	mu     sync.Mutex
	stored *bool
	actor  uuid.UUID
	getErr error
	setErr error
}

func (f *fakeSettings) RegistrationEnabled(context.Context) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return false, false, f.getErr
	}
	if f.stored == nil {
		return false, false, nil
	}
	return *f.stored, true, nil
}

func (f *fakeSettings) SetRegistrationEnabled(_ context.Context, enabled bool, actor uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.stored = &enabled
	f.actor = actor
	return nil
}

// fakeRealmSyncer records the last value it was asked to sync and can be
// made to fail.
type fakeRealmSyncer struct {
	mu    sync.Mutex
	calls int
	last  bool
	err   error
}

func (f *fakeRealmSyncer) SetRegistrationAllowed(_ context.Context, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.last = enabled
	return nil
}

// registrationTestServer wires api.New() with the registration toggle
// surface: a seeded enforcer + verifier (same fixtures the admin console
// tests use), the supplied settings store and realm syncer, and the
// given deploy-time default. A nil settings store reproduces the
// route-unregistered path.
func registrationTestServer(t *testing.T, settings domain.SettingsRepo, sync RegistrationRealmSyncer, def bool) http.Handler {
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
		editorSub = "00000000-0000-0000-0000-0000000000a4"
	)
	verifier := fakeVerifier{tokens: map[string]*oidc.Claims{
		"user-tok":   {Subject: userSub, Email: "user@minerals.local", Roles: []string{"user"}},
		"viewer-tok": {Subject: viewerSub, Email: "viewer@minerals.local", Roles: []string{"devops-viewer"}},
		"admin-tok":  {Subject: adminSub, Email: "admin@minerals.local", Roles: []string{"admin"}},
		"editor-tok": {Subject: editorSub, Email: "editor@minerals.local", Roles: []string{"devops-admin"}},
	}}

	repo := newFakeUserRepo()
	for _, sub := range []string{userSub, viewerSub, adminSub, editorSub} {
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
		Settings:            settings,
		RegistrationSync:    sync,
		RegistrationDefault: def,
	})
}

func putRegistration(t *testing.T, h http.Handler, token string, enabled bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(setRegistrationBody{Enabled: enabled})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/registration", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// TestRegistrationGet_RoleGate: read is on devops:view — anonymous 401,
// plain user 403, devops-viewer/admin 200.
func TestRegistrationGet_RoleGate(t *testing.T) {
	t.Parallel()
	h := registrationTestServer(t, &fakeSettings{}, nil, true)

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
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/registration", nil)
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

// TestRegistrationPut_RoleGate: write is on devops:edit — a view-only
// devops-viewer is 403, devops-admin and admin succeed.
func TestRegistrationPut_RoleGate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{"anonymous is 401", "", http.StatusUnauthorized},
		{"plain user is 403", "user-tok", http.StatusForbidden},
		{"devops-viewer is 403 (view-only)", "viewer-tok", http.StatusForbidden},
		{"devops-admin is 200", "editor-tok", http.StatusOK},
		{"admin is 200", "admin-tok", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := registrationTestServer(t, &fakeSettings{}, &fakeRealmSyncer{}, true)
			rec := putRegistration(t, h, tc.token, false)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestRegistrationGet_DefaultWhenUnset: with no stored row, GET reports
// the deploy-time default and source="default".
func TestRegistrationGet_DefaultWhenUnset(t *testing.T) {
	t.Parallel()
	h := registrationTestServer(t, &fakeSettings{}, nil, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/registration", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body registrationStateBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Source != "default" || body.Enabled != true {
		t.Fatalf("got %+v, want {enabled:true source:default}", body)
	}
}

// TestRegistrationGet_StoredValue: once a row exists, GET reports it with
// source="stored", overriding the default.
func TestRegistrationGet_StoredValue(t *testing.T) {
	t.Parallel()
	stored := false
	h := registrationTestServer(t, &fakeSettings{stored: &stored}, nil, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/registration", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body registrationStateBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Source != "stored" || body.Enabled != false {
		t.Fatalf("got %+v, want {enabled:false source:stored}", body)
	}
}

// TestRegistrationPut_PersistsAndSyncs: a successful flip writes the
// store, syncs the realm, stamps the actor, and reports realm_synced.
func TestRegistrationPut_PersistsAndSyncs(t *testing.T) {
	t.Parallel()
	settings := &fakeSettings{}
	syncer := &fakeRealmSyncer{}
	h := registrationTestServer(t, settings, syncer, true)

	rec := putRegistration(t, h, "admin-tok", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body setRegistrationResultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Enabled != false || !body.RealmSynced {
		t.Fatalf("got %+v, want {enabled:false realm_synced:true}", body)
	}
	if settings.stored == nil || *settings.stored != false {
		t.Fatalf("settings not persisted: %v", settings.stored)
	}
	if settings.actor != uuid.MustParse("00000000-0000-0000-0000-0000000000a3") {
		t.Fatalf("actor = %v, want admin sub", settings.actor)
	}
	if syncer.calls != 1 || syncer.last != false {
		t.Fatalf("syncer calls=%d last=%v, want 1/false", syncer.calls, syncer.last)
	}
}

// TestRegistrationPut_RealmSyncFailsKeepsConsistency: when the Keycloak
// sync errors, the endpoint returns 502 and does NOT persist the new
// value — app and realm stay at their previous (consistent) state.
func TestRegistrationPut_RealmSyncFailsKeepsConsistency(t *testing.T) {
	t.Parallel()
	settings := &fakeSettings{}
	syncer := &fakeRealmSyncer{err: errors.New("keycloak down")}
	h := registrationTestServer(t, settings, syncer, true)

	rec := putRegistration(t, h, "admin-tok", false)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if settings.stored != nil {
		t.Fatalf("settings persisted despite realm sync failure: %v", *settings.stored)
	}
}

// TestRegistrationPut_NoSyncerIsAppOnly: with no Keycloak admin client
// wired, the flip still persists but reports realm_synced=false.
func TestRegistrationPut_NoSyncerIsAppOnly(t *testing.T) {
	t.Parallel()
	settings := &fakeSettings{}
	h := registrationTestServer(t, settings, nil, true)

	rec := putRegistration(t, h, "admin-tok", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body setRegistrationResultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.RealmSynced {
		t.Fatalf("realm_synced = true, want false (no syncer wired)")
	}
	if settings.stored == nil || *settings.stored != false {
		t.Fatalf("settings not persisted: %v", settings.stored)
	}
}

// TestRegistrationPut_AuditLogged: a flip emits the structured audit
// event with the actor and target state, and leaks no email/PII.
func TestRegistrationPut_AuditLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	h := registrationTestServer(t, &fakeSettings{}, &fakeRealmSyncer{}, true)
	rec := putRegistration(t, h, "admin-tok", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, `"event":"admin.registration.changed"`) {
		t.Fatalf("audit event missing: %s", logged)
	}
	if strings.Contains(logged, "@") {
		t.Fatalf("audit log leaked email/PII: %s", logged)
	}
}

// TestRegistrationOverviewFlipsSiteManagement: wiring the settings store
// flips the admin overview's site-management section to "available".
func TestRegistrationOverviewFlipsSiteManagement(t *testing.T) {
	t.Parallel()
	h := registrationTestServer(t, &fakeSettings{}, nil, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body adminOverviewBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range body.Sections {
		if s.Key == "site-management" && s.Status != "available" {
			t.Fatalf("site-management status = %q, want available", s.Status)
		}
	}
}

// TestRegistrationRoutesUnregisteredWithoutStore: with no settings store
// wired, both routes fall through to the §10 404 envelope.
func TestRegistrationRoutesUnregisteredWithoutStore(t *testing.T) {
	t.Parallel()
	h := registrationTestServer(t, nil, nil, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/registration", nil)
	req.Header.Set("Authorization", "Bearer admin-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404 (route unregistered)", rec.Code)
	}

	rec = putRegistration(t, h, "admin-tok", false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PUT status = %d, want 404 (route unregistered)", rec.Code)
	}
}
