package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/authz"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// fakeSuspender is the unit-test double for domain.IdentitySuspender.
// It records every call and can be made to fail to exercise the
// IdP-unreachable abort path.
type fakeSuspender struct {
	mu    sync.Mutex
	calls []struct {
		sub     string
		enabled bool
	}
	err error
}

func (f *fakeSuspender) SetIdentityEnabled(_ context.Context, sub string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, struct {
		sub     string
		enabled bool
	}{sub, enabled})
	return nil
}

func (f *fakeSuspender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeSessionRevoker is the unit-test double for accountSessionRevoker.
type fakeSessionRevoker struct {
	mu      sync.Mutex
	revoked []uuid.UUID
	err     error
}

func (f *fakeSessionRevoker) RevokeAllForUser(_ context.Context, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.revoked = append(f.revoked, userID)
	return nil
}

func (f *fakeSessionRevoker) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.revoked)
}

// suspend-test fixtures. The operator tokens mirror the registration
// suite; targetSub is a separate user the operator acts on.
const (
	suspOperatorSub = "00000000-0000-0000-0000-0000000000b3" // admin
	suspViewerSub   = "00000000-0000-0000-0000-0000000000b2" // devops-viewer
	suspEditorSub   = "00000000-0000-0000-0000-0000000000b4" // devops-admin
	suspPlainSub    = "00000000-0000-0000-0000-0000000000b1" // plain user
	suspTargetSub   = "00000000-0000-0000-0000-0000000000c1" // the account being suspended
)

// suspendTestServer wires api.New() with the account-suspension surface:
// a seeded enforcer + verifier, the shared fakeUserRepo (seeded with the
// operator roles + a target user in the given status), and the supplied
// identity suspender / session revoker. A nil identity reproduces the
// no-admin-client (application-only) path.
func suspendTestServer(
	t *testing.T, repo *fakeUserRepo, identity domain.IdentitySuspender, sessions accountSessionRevoker,
) http.Handler {
	t.Helper()

	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}

	verifier := fakeVerifier{tokens: map[string]*oidc.Claims{
		"user-tok":   {Subject: suspPlainSub, Email: "user@minerals.local", Roles: []string{"user"}},
		"viewer-tok": {Subject: suspViewerSub, Email: "viewer@minerals.local", Roles: []string{"devops-viewer"}},
		"admin-tok":  {Subject: suspOperatorSub, Email: "admin@minerals.local", Roles: []string{"admin"}},
		"editor-tok": {Subject: suspEditorSub, Email: "editor@minerals.local", Roles: []string{"devops-admin"}},
		"target-tok": {Subject: suspTargetSub, Email: "target@minerals.local", Roles: []string{"user"}},
	}}

	for _, sub := range []string{suspPlainSub, suspViewerSub, suspOperatorSub, suspEditorSub} {
		repo.seed(domain.User{
			ID:          uuid.MustParse(sub),
			KeycloakSub: sub,
			Email:       sub + "@minerals.local",
			Status:      domain.UserStatusActive,
		})
	}

	return New(Deps{
		Users:    repo,
		Verifier: verifier,
		Enforcer: enf,
		AdminSuspend: &AdminSuspendDeps{
			Users:    repo,
			Identity: identity,
			Sessions: sessions,
		},
	})
}

// seedTarget plants the account the suspend action targets, in the
// requested status.
func seedTarget(repo *fakeUserRepo, status domain.UserStatus) {
	repo.seed(domain.User{
		ID:          uuid.MustParse(suspTargetSub),
		KeycloakSub: suspTargetSub,
		Email:       "target@minerals.local",
		Status:      status,
	})
}

// postSuspend POSTs to the suspend endpoint with an empty JSON body
// (the reason is optional but huma still requires the body envelope).
func postSuspend(t *testing.T, h http.Handler, id, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, suspendPath(id), strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h.ServeHTTP(rec, req)
	return rec
}

// postUnsuspend POSTs to the unsuspend endpoint (no request body).
func postUnsuspend(t *testing.T, h http.Handler, id, token string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, unsuspendPath(id), nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func suspendPath(id string) string   { return "/api/v1/admin/users/" + id + "/suspend" }
func unsuspendPath(id string) string { return "/api/v1/admin/users/" + id + "/unsuspend" }

// TestSuspend_RoleGate: suspend is on devops:edit — anonymous 401, plain
// user 403, view-only devops-viewer 403, devops-admin/admin succeed.
func TestSuspend_RoleGate(t *testing.T) {
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
			repo := newFakeUserRepo()
			h := suspendTestServer(t, repo, &fakeSuspender{}, &fakeSessionRevoker{})
			seedTarget(repo, domain.UserStatusActive)
			rec := postSuspend(t, h, suspTargetSub, tc.token)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestSuspend_ActiveDisablesIdPRevokesAndFlips: the happy path —
// Keycloak disabled, sessions revoked, app status flipped to suspended,
// identity_synced reported true.
func TestSuspend_ActiveDisablesIdPRevokesAndFlips(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	idp := &fakeSuspender{}
	sessions := &fakeSessionRevoker{}
	h := suspendTestServer(t, repo, idp, sessions)
	seedTarget(repo, domain.UserStatusActive)

	rec := postSuspend(t, h, suspTargetSub, "admin-tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body accountStatusResultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != string(domain.UserStatusSuspended) || !body.IdentitySynced {
		t.Fatalf("got %+v, want {status:suspended identity_synced:true}", body)
	}
	// IdP disabled with enabled=false for the target sub.
	if idp.count() != 1 || idp.calls[0].sub != suspTargetSub || idp.calls[0].enabled {
		t.Fatalf("idp calls = %+v, want one disable(%s)", idp.calls, suspTargetSub)
	}
	// Sessions revoked for immediate logout.
	if sessions.count() != 1 || sessions.revoked[0] != uuid.MustParse(suspTargetSub) {
		t.Fatalf("revoked = %v, want [%s]", sessions.revoked, suspTargetSub)
	}
	// App status persisted.
	got, _ := repo.GetByID(context.Background(), uuid.MustParse(suspTargetSub))
	if got.Status != domain.UserStatusSuspended {
		t.Fatalf("persisted status = %s, want suspended", got.Status)
	}
}

// TestSuspend_AppOnlyWhenNoIdP: with no admin client (nil Identity), the
// status still flips and sessions are revoked, but identity_synced is
// false and no IdP call is attempted.
func TestSuspend_AppOnlyWhenNoIdP(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	sessions := &fakeSessionRevoker{}
	h := suspendTestServer(t, repo, nil, sessions)
	seedTarget(repo, domain.UserStatusActive)

	rec := postSuspend(t, h, suspTargetSub, "admin-tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body accountStatusResultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.IdentitySynced {
		t.Fatalf("identity_synced = true, want false (no admin client)")
	}
	got, _ := repo.GetByID(context.Background(), uuid.MustParse(suspTargetSub))
	if got.Status != domain.UserStatusSuspended {
		t.Fatalf("persisted status = %s, want suspended", got.Status)
	}
	if sessions.count() != 1 {
		t.Fatalf("revoked count = %d, want 1", sessions.count())
	}
}

// TestSuspend_IdPFailureAbortsConsistently: a Keycloak failure aborts
// with 502 — the app status is NOT flipped and no session is revoked, so
// both sides stay at the prior (active/enabled) state.
func TestSuspend_IdPFailureAbortsConsistently(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	idp := &fakeSuspender{err: errors.New("keycloak down")}
	sessions := &fakeSessionRevoker{}
	h := suspendTestServer(t, repo, idp, sessions)
	seedTarget(repo, domain.UserStatusActive)

	rec := postSuspend(t, h, suspTargetSub, "admin-tok")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrCode(t, rec); code != "identity_sync_failed" {
		t.Fatalf("error code = %q, want identity_sync_failed", code)
	}
	got, _ := repo.GetByID(context.Background(), uuid.MustParse(suspTargetSub))
	if got.Status != domain.UserStatusActive {
		t.Fatalf("status = %s, want active (unchanged after IdP failure)", got.Status)
	}
	if sessions.count() != 0 {
		t.Fatalf("revoked count = %d, want 0 (abort before revoke)", sessions.count())
	}
}

// TestSuspend_Idempotent: suspending an already-suspended account is a
// no-op 200 — no IdP call, no status churn.
func TestSuspend_Idempotent(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	idp := &fakeSuspender{}
	h := suspendTestServer(t, repo, idp, &fakeSessionRevoker{})
	seedTarget(repo, domain.UserStatusSuspended)

	rec := postSuspend(t, h, suspTargetSub, "admin-tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if idp.count() != 0 {
		t.Fatalf("idp calls = %d, want 0 on idempotent re-suspend", idp.count())
	}
}

// TestSuspend_StatusGuards: pending and deleted accounts are rejected
// with 409; an unknown id is 404.
func TestSuspend_StatusGuards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		status     domain.UserStatus
		seed       bool
		wantStatus int
		wantCode   string
	}{
		{"pending is 409", domain.UserStatusPending, true, http.StatusConflict, "account_not_active"},
		{"deleted is 409", domain.UserStatusDeleted, true, http.StatusConflict, "account_deleted"},
		{"unknown id is 404", "", false, http.StatusNotFound, "user_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeUserRepo()
			h := suspendTestServer(t, repo, &fakeSuspender{}, &fakeSessionRevoker{})
			if tc.seed {
				seedTarget(repo, tc.status)
			}
			rec := postSuspend(t, h, suspTargetSub, "admin-tok")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if code := decodeErrCode(t, rec); code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

// TestSuspend_CannotSuspendSelf: an operator suspending their own id is
// rejected with 400 so they can't lock themselves out.
func TestSuspend_CannotSuspendSelf(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	h := suspendTestServer(t, repo, &fakeSuspender{}, &fakeSessionRevoker{})

	rec := postSuspend(t, h, suspOperatorSub, "admin-tok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrCode(t, rec); code != "cannot_suspend_self" {
		t.Fatalf("error code = %q, want cannot_suspend_self", code)
	}
}

// TestSuspend_CannotSuspendSystem: the migration-0008 stub/system
// account is not suspendable (it underpins legacy author_id FKs).
func TestSuspend_CannotSuspendSystem(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	h := suspendTestServer(t, repo, &fakeSuspender{}, &fakeSessionRevoker{})
	repo.seed(domain.User{
		ID:          auth.StubUser.ID,
		KeycloakSub: auth.StubUserSub,
		Email:       "overseer@minerals.local",
		Status:      domain.UserStatusActive,
	})

	rec := postSuspend(t, h, auth.StubUser.ID.String(), "admin-tok")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrCode(t, rec); code != "cannot_suspend_system" {
		t.Fatalf("error code = %q, want cannot_suspend_system", code)
	}
}

// TestSuspend_BlocksSubsequentRequests: once suspended, the account is
// fail-closed on every authenticated request by the auth resolver —
// the error code distinguishes it from a plain authz 403.
func TestSuspend_BlocksSubsequentRequests(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	h := suspendTestServer(t, repo, &fakeSuspender{}, &fakeSessionRevoker{})
	seedTarget(repo, domain.UserStatusActive)

	if rec := postSuspend(t, h, suspTargetSub, "admin-tok"); rec.Code != http.StatusOK {
		t.Fatalf("suspend failed: %d %s", rec.Code, rec.Body.String())
	}

	// The suspended user now hits a protected endpoint. The resolver
	// gate fires before authz, so the code is account_suspended (403).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil)
	req.Header.Set("Authorization", "Bearer target-tok")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	if code := decodeErrCode(t, rec); code != "account_suspended" {
		t.Fatalf("error code = %q, want account_suspended", code)
	}
}

// TestUnsuspend_ReenablesIdPAndFlips: lifting a suspension re-enables the
// Keycloak identity and flips the status back to active.
func TestUnsuspend_ReenablesIdPAndFlips(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	idp := &fakeSuspender{}
	h := suspendTestServer(t, repo, idp, &fakeSessionRevoker{})
	seedTarget(repo, domain.UserStatusSuspended)

	rec := postUnsuspend(t, h, suspTargetSub, "admin-tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body accountStatusResultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != string(domain.UserStatusActive) {
		t.Fatalf("status = %s, want active", body.Status)
	}
	if idp.count() != 1 || !idp.calls[0].enabled {
		t.Fatalf("idp calls = %+v, want one enable(true)", idp.calls)
	}
	got, _ := repo.GetByID(context.Background(), uuid.MustParse(suspTargetSub))
	if got.Status != domain.UserStatusActive {
		t.Fatalf("persisted status = %s, want active", got.Status)
	}
}

// TestUnsuspend_IdempotentOnActive: unsuspending an account that is not
// suspended returns its status unchanged with no IdP call.
func TestUnsuspend_IdempotentOnActive(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	idp := &fakeSuspender{}
	h := suspendTestServer(t, repo, idp, &fakeSessionRevoker{})
	seedTarget(repo, domain.UserStatusActive)

	rec := postUnsuspend(t, h, suspTargetSub, "admin-tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body accountStatusResultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != string(domain.UserStatusActive) {
		t.Fatalf("status = %s, want active (unchanged)", body.Status)
	}
	if idp.count() != 0 {
		t.Fatalf("idp calls = %d, want 0 on idempotent unsuspend", idp.count())
	}
}

// TestSuspend_RoutesUnregisteredWithoutDeps: with no AdminSuspend deps
// the endpoints are absent (404), not a half-wired 500.
func TestSuspend_RoutesUnregisteredWithoutDeps(t *testing.T) {
	t.Parallel()
	enf, err := authz.NewEnforcer(nil, nil)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	if err := authz.SeedDefaultPolicies(enf); err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	repo := newFakeUserRepo()
	repo.seed(domain.User{
		ID: uuid.MustParse(suspOperatorSub), KeycloakSub: suspOperatorSub,
		Email: "admin@minerals.local", Status: domain.UserStatusActive,
	})
	h := New(Deps{
		Users:    repo,
		Verifier: fakeVerifier{tokens: map[string]*oidc.Claims{"admin-tok": {Subject: suspOperatorSub, Roles: []string{"admin"}}}},
		Enforcer: enf,
		// AdminSuspend deliberately nil.
	})
	rec := postSuspend(t, h, suspTargetSub, "admin-tok")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route unregistered); body = %s", rec.Code, rec.Body.String())
	}
}
