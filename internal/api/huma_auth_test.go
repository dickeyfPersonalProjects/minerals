package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// fakeVerifier maps known token strings to claims. Unknown tokens
// return an error, standing in for an invalid/expired/forged JWT.
// Real signature verification is covered by internal/oidc's tests.
type fakeVerifier struct {
	tokens map[string]*oidc.Claims
}

func (f fakeVerifier) Verify(_ context.Context, raw string) (*oidc.Claims, error) {
	c, ok := f.tokens[raw]
	if !ok {
		return nil, errors.New("oidc: token not recognized")
	}
	return c, nil
}

// fakeUserRepo is an in-memory domain.UserRepo for middleware tests.
// It is goroutine-safe so it can stand in for the race detector's
// scheduling of the resolver's GetBySub/Create double-tap.
type fakeUserRepo struct {
	mu          sync.Mutex
	bySub       map[string]domain.User
	createCalls int32
	createErr   error
	getErr      error
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{bySub: map[string]domain.User{}}
}

func (r *fakeUserRepo) seed(u domain.User) {
	r.mu.Lock()
	r.bySub[u.KeycloakSub] = u
	r.mu.Unlock()
}

func (r *fakeUserRepo) GetBySub(_ context.Context, sub string) (domain.User, error) {
	if r.getErr != nil {
		return domain.User{}, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.bySub[sub]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) Create(_ context.Context, _ domain.Tx, u domain.User) error {
	atomic.AddInt32(&r.createCalls, 1)
	if r.createErr != nil {
		return r.createErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.bySub[u.KeycloakSub]; exists {
		return domain.ErrUserConflict
	}
	r.bySub[u.KeycloakSub] = u
	return nil
}

func (r *fakeUserRepo) MarkActive(
	_ context.Context, _ domain.Tx, id uuid.UUID, displayName string, updatedAt time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for sub, u := range r.bySub {
		if u.ID == id {
			u.DisplayName = &displayName
			u.Status = domain.UserStatusActive
			u.UpdatedAt = updatedAt
			r.bySub[sub] = u
			return nil
		}
	}
	return domain.ErrUserNotFound
}

func (r *fakeUserRepo) UpdateFieldDefaults(
	_ context.Context, _ domain.Tx, id uuid.UUID, defaults *domain.FieldDefaults, updatedAt time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for sub, u := range r.bySub {
		if u.ID == id {
			u.FieldDefaults = defaults
			u.UpdatedAt = updatedAt
			r.bySub[sub] = u
			return nil
		}
	}
	return domain.ErrUserNotFound
}

// seedActiveStubUser plants the migration-0008 overseer row that the
// stub auth path resolves to. Test fixtures share this baseline so
// the gate doesn't fire spuriously on routes the test isn't gating.
func seedActiveStubUser(repo *fakeUserRepo) {
	repo.seed(domain.User{
		ID:          auth.StubUser.ID,
		KeycloakSub: auth.StubUserSub,
		Email:       auth.StubUser.Email,
		Status:      domain.UserStatusActive,
	})
}

func TestAuthMiddlewares_NilRepoCollapsesToHumaAuth(t *testing.T) {
	t.Parallel()
	mw := newAuthMiddlewares(nil, nil)
	if got := len(mw.Protected()); got != 1 {
		t.Fatalf("Protected len = %d, want 1 when repo is nil", got)
	}
	if got := len(mw.SetupAllowed()); got != 1 {
		t.Fatalf("SetupAllowed len = %d, want 1 when repo is nil", got)
	}
}

func TestAuthMiddlewares_WithRepoIncludesResolverAndGate(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	mw := newAuthMiddlewares(repo, nil)
	if got := len(mw.Protected()); got != 3 {
		t.Fatalf("Protected len = %d, want auth+resolve+gate", got)
	}
	if got := len(mw.SetupAllowed()); got != 2 {
		t.Fatalf("SetupAllowed len = %d, want auth+resolve only", got)
	}
}

// realAuthSub is a UUID-shaped Keycloak subject used by the
// verifier-backed tests below.
const realAuthSub = "00000000-0000-0000-0000-0000000000fe"

func newFakeVerifier() fakeVerifier {
	return fakeVerifier{tokens: map[string]*oidc.Claims{
		"valid": {Subject: realAuthSub, Email: "fury@minerals.local", Roles: []string{"user"}},
	}}
}

func TestHumaAuth_ValidTokenResolvesSeededUser(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	repo.seed(domain.User{
		ID:          uuid.MustParse(realAuthSub),
		KeycloakSub: realAuthSub,
		Email:       "fury@minerals.local",
		Status:      domain.UserStatusActive,
	})
	h := New(Deps{Users: repo, Verifier: newFakeVerifier(), Collectors: newStubCollectorRepo()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	req.Header.Set("Authorization", "Bearer valid")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHumaAuth_ValidTokenFirstLoginGatesOnRealSub(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo() // not seeded — simulates first-login
	h := New(Deps{Users: repo, Verifier: newFakeVerifier(), Collectors: newStubCollectorRepo()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	req.Header.Set("Authorization", "Bearer valid")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	// The resolver must have created the pending row keyed by the
	// JWT subject — proof the real sub (not the stub) drove resolution.
	stored, err := repo.GetBySub(context.Background(), realAuthSub)
	if err != nil {
		t.Fatalf("GetBySub(realAuthSub): %v", err)
	}
	if stored.Status != domain.UserStatusPending {
		t.Errorf("status = %q, want pending", stored.Status)
	}
	if stored.Email != "fury@minerals.local" {
		t.Errorf("email = %q, want fury@minerals.local", stored.Email)
	}
}

// TestHumaAuth_RejectsMissingAndInvalidTokens drives the write-side
// chain (POST /api/v1/collectors), which stays on Protected() — the
// read-side now uses Optional() and admits anonymous callers (see
// TestHumaOptionalAuth_AnonymousReadIs200). Per CONTRACT.md §13 v2
// only writes 401 on missing-or-invalid credentials.
func TestHumaAuth_RejectsMissingAndInvalidTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"unrecognized token", "Bearer forged"},
		{"wrong scheme", "Basic Zm9vOmJhcg=="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeUserRepo()
			h := New(Deps{Users: repo, Verifier: newFakeVerifier(), Collectors: newStubCollectorRepo()})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/collectors",
				strings.NewReader(`{"name":"new"}`))
			req.Header.Set("Content-Type", "application/json")
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			got := decodeError(t, rec.Body)
			if got.Error.Code != "unauthorized" {
				t.Errorf("code = %q, want unauthorized", got.Error.Code)
			}
			// A rejected token must never reach the resolver.
			if atomic.LoadInt32(&repo.createCalls) != 0 {
				t.Errorf("resolver ran on rejected auth (createCalls = %d)", repo.createCalls)
			}
		})
	}
}

// TestHumaOptionalAuth_AnonymousReadIs200 confirms read-side
// endpoints on visibility-scoped resources admit anonymous callers
// (CONTRACT.md §13 v2). The DB-level scoping is what filters; an
// anonymous list MUST return 200 with whatever the caller may see
// (here: empty, because collectors are owned per-user with no
// public tier).
func TestHumaOptionalAuth_AnonymousReadIs200(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong scheme", "Basic Zm9vOmJhcg=="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeUserRepo()
			h := New(Deps{Users: repo, Verifier: newFakeVerifier(), Collectors: newStubCollectorRepo()})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			// Anonymous never triggers the resolver — it has no Sub.
			if atomic.LoadInt32(&repo.createCalls) != 0 {
				t.Errorf("resolver ran on anonymous read (createCalls = %d)", repo.createCalls)
			}
		})
	}
}

// TestHumaOptionalAuth_InvalidTokenStill401 verifies the
// optional-auth chain still rejects a deliberately-presented but
// invalid bearer token. Per CONTRACT.md §13 v2 only a *missing*
// credential is treated as anonymous; an invalid one fails closed.
func TestHumaOptionalAuth_InvalidTokenStill401(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	h := New(Deps{Users: repo, Verifier: newFakeVerifier(), Collectors: newStubCollectorRepo()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	req.Header.Set("Authorization", "Bearer forged")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", got.Error.Code)
	}
	if atomic.LoadInt32(&repo.createCalls) != 0 {
		t.Errorf("resolver ran on rejected token (createCalls = %d)", repo.createCalls)
	}
}

func TestResolver_StubUserPassesThroughGate(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	seedActiveStubUser(repo)

	h := New(Deps{Users: repo, Collectors: newStubCollectorRepo()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestResolver_FirstLoginCreatesPendingAndGates(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	// Do NOT seed the stub user — simulate first-login.
	h := New(Deps{Users: repo, Collectors: newStubCollectorRepo()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "profile_setup_required" {
		t.Errorf("code = %q, want profile_setup_required", got.Error.Code)
	}
	redirect, _ := got.Error.Details["redirect"].(string)
	if redirect != ProfileSetupPath {
		t.Errorf("details.redirect = %q, want %q", redirect, ProfileSetupPath)
	}
	if atomic.LoadInt32(&repo.createCalls) != 1 {
		t.Errorf("Create called %d times, want 1", repo.createCalls)
	}
	stored, err := repo.GetBySub(context.Background(), auth.StubUserSub)
	if err != nil {
		t.Fatalf("GetBySub after first-login: %v", err)
	}
	if stored.Status != domain.UserStatusPending {
		t.Errorf("status = %q, want pending", stored.Status)
	}
	if stored.Email != auth.StubUser.Email {
		t.Errorf("email = %q, want %q", stored.Email, auth.StubUser.Email)
	}
}

func TestResolver_RaceWinnerWins(t *testing.T) {
	t.Parallel()
	// Two requests race; both find no row and try to Create. The
	// second hits ErrUserConflict and re-reads the winner's row.
	repo := newFakeUserRepo()
	// Inject a winner directly to simulate the race.
	repo.seed(domain.User{
		ID:          domain.NewID(),
		KeycloakSub: "race-winner",
		Email:       "winner@example.com",
		Status:      domain.UserStatusActive,
	})
	user := auth.User{Sub: "race-winner", Email: "loser@example.com"}
	resolved, err := resolveOrCreateUser(context.Background(), repo, user)
	if err != nil {
		t.Fatalf("resolveOrCreateUser: %v", err)
	}
	if resolved.Email != "winner@example.com" {
		t.Errorf("email = %q, want winner@example.com (existing row wins)", resolved.Email)
	}
	if resolved.Status != domain.UserStatusActive {
		t.Errorf("status = %q, want active", resolved.Status)
	}
}

func TestResolver_GetErrorSurfacesAs500(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	repo.getErr = errors.New("db down")
	h := New(Deps{Users: repo, Collectors: newStubCollectorRepo()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/collectors", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "internal_error" {
		t.Errorf("code = %q, want internal_error", got.Error.Code)
	}
}

// stubCollectorRepo is a minimal domain.CollectorRepo just for
// authentication-chain tests. It returns an empty list on List and
// is enough to register the /api/v1/collectors operations so the
// middleware chain runs.
type stubCollectorRepo struct{}

func newStubCollectorRepo() *stubCollectorRepo { return &stubCollectorRepo{} }

func (r *stubCollectorRepo) Create(context.Context, domain.Tx, domain.Collector) error {
	return nil
}
func (r *stubCollectorRepo) GetByID(context.Context, uuid.UUID) (domain.Collector, error) {
	return domain.Collector{}, domain.ErrCollectorNotFound
}
func (r *stubCollectorRepo) Update(context.Context, domain.Tx, domain.Collector) error {
	return nil
}
func (r *stubCollectorRepo) Delete(context.Context, domain.Tx, uuid.UUID) error { return nil }
func (r *stubCollectorRepo) List(
	context.Context, domain.CollectorFilter, domain.Page,
) ([]domain.Collector, domain.Cursor, error) {
	return nil, "", nil
}

type decodedError struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeError(t *testing.T, body io.Reader) decodedError {
	t.Helper()
	var out decodedError
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return out
}

// TestProfileEndpoint_ReachableWhilePending verifies the setup
// endpoint is exempt from the gate: a pending user can POST to it
// and walk the resolver chain without being redirected.
func TestProfileEndpoint_ReachableWhilePending(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	// Pre-seed the row as pending so the test exercises the
	// existing-pending path (not the auto-create path).
	repo.seed(domain.User{
		ID:          domain.NewID(),
		KeycloakSub: auth.StubUserSub,
		Email:       auth.StubUser.Email,
		Status:      domain.UserStatusPending,
	})

	h := New(Deps{Users: repo})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile",
		strings.NewReader(`{"display_name":"Test User"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var got profileBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.DisplayName != "Test User" {
		t.Errorf("display_name = %q", got.DisplayName)
	}
	if got.Pending {
		t.Errorf("pending = true, want false after successful setup")
	}

	stored, err := repo.GetBySub(context.Background(), auth.StubUserSub)
	if err != nil {
		t.Fatalf("GetBySub: %v", err)
	}
	if stored.Status != domain.UserStatusActive {
		t.Errorf("stored status = %q, want active", stored.Status)
	}
	if stored.DisplayName == nil || *stored.DisplayName != "Test User" {
		t.Errorf("stored display_name = %v, want Test User", stored.DisplayName)
	}
}

func TestProfileEndpoint_RejectsEmptyDisplayName(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	repo.seed(domain.User{
		ID: domain.NewID(), KeycloakSub: auth.StubUserSub,
		Email: auth.StubUser.Email, Status: domain.UserStatusPending,
	})
	h := New(Deps{Users: repo})

	for _, body := range []string{
		`{"display_name":""}`,
		`{"display_name":"   "}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/profile",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%q → status = %d", body, rec.Code)
		}
		got := decodeError(t, rec.Body)
		if got.Error.Code != "invalid_display_name" {
			t.Errorf("body=%q → code = %q", body, got.Error.Code)
		}
	}
}

func TestProfileEndpoint_RejectsTooLongDisplayName(t *testing.T) {
	t.Parallel()
	repo := newFakeUserRepo()
	repo.seed(domain.User{
		ID: domain.NewID(), KeycloakSub: auth.StubUserSub,
		Email: auth.StubUser.Email, Status: domain.UserStatusPending,
	})
	h := New(Deps{Users: repo})

	tooLong := strings.Repeat("x", MaxDisplayNameLen+1)
	body, _ := json.Marshal(map[string]string{"display_name": tooLong})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/profile",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	got := decodeError(t, rec.Body)
	if got.Error.Code != "invalid_display_name" {
		t.Errorf("code = %q", got.Error.Code)
	}
}
