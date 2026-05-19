package bff

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// fakeResolver is the in-memory SessionResolver the middleware tests
// drive against. Every method is recorded so individual tests assert
// on call counts (stampede, debounce) without instrumenting the
// production code with hooks.
type fakeResolver struct {
	mu sync.Mutex

	// store is the keyed session table. nil sessions are returned
	// as ErrSessionNotFound to exercise the cookie-clear branch.
	store map[[32]byte]*Session

	// blockGetByID, if non-nil, is closed-on by GetByID before
	// returning. Lets the stampede test pin both refreshers inside
	// the lookup so they both see "stale" before either runs the
	// refresh path.
	blockGetByID chan struct{}

	calls struct {
		getByID      atomic.Int32
		updateTokens atomic.Int32
		touch        atomic.Int32
		revoke       atomic.Int32
	}

	// nextLookupErr forces GetByID to return this error once. Set
	// to a non-ErrSessionNotFound err to exercise the 500 branch.
	nextLookupErr error
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{store: map[[32]byte]*Session{}}
}

func (r *fakeResolver) GetByID(_ context.Context, id [32]byte) (Session, error) {
	r.calls.getByID.Add(1)
	if r.blockGetByID != nil {
		<-r.blockGetByID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nextLookupErr != nil {
		err := r.nextLookupErr
		r.nextLookupErr = nil
		return Session{}, err
	}
	s, ok := r.store[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return *s, nil
}

func (r *fakeResolver) Create(_ context.Context, _ CreateParams) (Session, error) {
	panic("not used by middleware tests")
}

func (r *fakeResolver) UpdateTokens(_ context.Context, id [32]byte, t TokenSet) (Session, error) {
	r.calls.updateTokens.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.store[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	s.AccessToken = t.AccessToken
	s.RefreshToken = t.RefreshToken
	s.IDToken = t.IDToken
	s.AccessTokenExpiresAt = t.AccessTokenExpiresAt
	s.RefreshTokenExpiresAt = t.RefreshTokenExpiresAt
	return *s, nil
}

func (r *fakeResolver) Touch(_ context.Context, id [32]byte, at time.Time) error {
	r.calls.touch.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.store[id]; ok {
		s.LastUsedAt = at
	}
	return nil
}

func (r *fakeResolver) Revoke(_ context.Context, id [32]byte) error {
	r.calls.revoke.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.store[id]; ok {
		now := time.Now()
		s.RevokedAt = &now
	}
	return nil
}

func (r *fakeResolver) RevokeAllForUser(_ context.Context, _ uuid.UUID) error {
	panic("not used by middleware tests")
}

// fakeOAuth is the in-memory OAuthClient the refresh-path tests
// drive. The Refresh hook is per-call so a test can fail the first
// invocation or count concurrent invocations.
type fakeOAuth struct {
	refreshCalls atomic.Int32
	// refreshHook overrides the default ok response. nil → return a
	// fresh Tokens shifted 5 minutes into the future.
	refreshHook func(refreshToken string) (Tokens, error)
}

func (o *fakeOAuth) AuthCodeURL(_, _ string) string { panic("not used") }

func (o *fakeOAuth) RegisterURL(_, _ string) string { panic("not used") }

func (o *fakeOAuth) Exchange(_ context.Context, _, _ string) (Tokens, error) {
	panic("not used")
}

func (o *fakeOAuth) EndSessionURL(_, _ string) string { panic("not used") }

func (o *fakeOAuth) Refresh(_ context.Context, refreshToken string) (Tokens, error) {
	o.refreshCalls.Add(1)
	if o.refreshHook != nil {
		return o.refreshHook(refreshToken)
	}
	return Tokens{
		AccessToken:           "new-access-" + refreshToken,
		RefreshToken:          "new-refresh-" + refreshToken,
		IDToken:               "new-id-" + refreshToken,
		AccessTokenExpiresAt:  time.Now().Add(5 * time.Minute),
		RefreshTokenExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}, nil
}

// fakeUsers is the in-memory UserLookup the tests inject. Loaded with
// a single user matching the session's UserID; missing user returns
// an error so the 500 path can be exercised.
type fakeUsers struct {
	store map[uuid.UUID]domain.User
	err   error
}

func (u *fakeUsers) GetByID(_ context.Context, id uuid.UUID) (domain.User, error) {
	if u.err != nil {
		return domain.User{}, u.err
	}
	if user, ok := u.store[id]; ok {
		return user, nil
	}
	return domain.User{}, errors.New("not found")
}

// fixedClock builds a Now func() that returns the same instant every
// call. Tests that need to advance time wrap a *time.Time pointer.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

type testFixture struct {
	resolver *fakeResolver
	oauth    *fakeOAuth
	users    *fakeUsers
	cookie   CookieConfig
	now      time.Time

	sessionID [32]byte
	userID    uuid.UUID
	session   Session
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	var id [32]byte
	for i := range id {
		id[i] = byte(i + 1)
	}
	userID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	displayName := "Ada"

	sess := Session{
		ID:                    id,
		UserSub:               "sub-abc",
		UserID:                userID,
		AccessToken:           "access-1",
		RefreshToken:          "refresh-1",
		IDToken:               "id-1",
		AccessTokenExpiresAt:  now.Add(5 * time.Minute),
		RefreshTokenExpiresAt: now.Add(30 * 24 * time.Hour),
		CreatedAt:             now.Add(-1 * time.Hour),
		LastUsedAt:            now.Add(-5 * time.Minute),
		AbsoluteExpiresAt:     now.Add(7 * 24 * time.Hour),
	}

	fr := newFakeResolver()
	fr.store[id] = &sess
	return &testFixture{
		resolver: fr,
		oauth:    &fakeOAuth{},
		users: &fakeUsers{store: map[uuid.UUID]domain.User{
			userID: {
				ID:          userID,
				KeycloakSub: "sub-abc",
				Email:       "ada@example.com",
				DisplayName: &displayName,
				Status:      domain.UserStatusActive,
			},
		}},
		cookie: CookieConfig{
			Path:     "/",
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   14 * 24 * time.Hour,
		},
		now:       now,
		sessionID: id,
		userID:    userID,
		session:   sess,
	}
}

func (f *testFixture) deps() MiddlewareDeps {
	return MiddlewareDeps{
		Sessions:     f.resolver,
		OAuth:        f.oauth,
		Users:        f.users,
		CookieConfig: f.cookie,
		IdleTimeout:  24 * time.Hour,
		Now:          fixedClock(f.now),
	}
}

// sinkHandler is the "downstream" handler in tests. It records the
// auth.User and Session the chain delivered so the test can assert
// on attachment vs anonymous.
type sinkHandler struct {
	called    bool
	gotUser   auth.User
	gotSess   Session
	sessOK    bool
	gotReqCtx context.Context
}

func (s *sinkHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	s.called = true
	s.gotUser = auth.FromContext(r.Context())
	s.gotSess, s.sessOK = SessionFromContext(r.Context())
	s.gotReqCtx = r.Context()
}

func cookieValueFor(id [32]byte) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// testSessionCookie returns a cookie that the middleware can decode.
// HttpOnly/Secure/SameSite are set so gosec G124 is satisfied — those
// attributes are server→browser hints, the values are irrelevant on
// the request side, but the linter scans Cookie literals uniformly.
func testSessionCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// hasClearCookie asserts the response carries a Set-Cookie that
// clears the session cookie (MaxAge=-1 / Max-Age=0).
func hasClearCookie(w *httptest.ResponseRecorder) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			return true
		}
	}
	return false
}

func TestSessionMiddleware_NoCookie_AnonymousFallthrough(t *testing.T) {
	f := newTestFixture(t)
	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("downstream handler must run anonymously when no cookie present")
	}
	if sink.sessOK {
		t.Fatal("session must NOT be in context for anonymous request")
	}
	if sink.gotUser.Sub != "" || sink.gotUser.ID != uuid.Nil {
		t.Fatalf("user must be zero-valued, got %+v", sink.gotUser)
	}
	if hasClearCookie(w) {
		t.Fatal("must NOT emit clear-cookie when there was no cookie to clear")
	}
	if f.resolver.calls.getByID.Load() != 0 {
		t.Fatal("must not touch SessionResolver when no cookie present")
	}
}

func TestSessionMiddleware_BadCookie_ClearAndAnonymous(t *testing.T) {
	f := newTestFixture(t)
	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie("not-base64-or-wrong-length"))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("must fall through to handler after clearing bad cookie")
	}
	if sink.sessOK {
		t.Fatal("no session in context for bad cookie")
	}
	if !hasClearCookie(w) {
		t.Fatal("expected clear-cookie response for bad cookie")
	}
	if f.resolver.calls.getByID.Load() != 0 {
		t.Fatal("must not hit the resolver on a cookie that can't decode to 32 bytes")
	}
}

func TestSessionMiddleware_SessionNotFound_ClearAndAnonymous(t *testing.T) {
	f := newTestFixture(t)
	// Remove the session so GetByID returns ErrSessionNotFound.
	delete(f.resolver.store, f.sessionID)
	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("must fall through after ErrSessionNotFound")
	}
	if sink.sessOK {
		t.Fatal("no session in context for ErrSessionNotFound")
	}
	if !hasClearCookie(w) {
		t.Fatal("expected clear-cookie response for missing session")
	}
}

func TestSessionMiddleware_LookupError_500(t *testing.T) {
	f := newTestFixture(t)
	f.resolver.nextLookupErr = errors.New("pgx: pool exhausted")
	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if sink.called {
		t.Fatal("must short-circuit to 500 on lookup error, not fall through")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "internal_error") {
		t.Fatalf("expected internal_error envelope, got %s", w.Body.String())
	}
}

func TestSessionMiddleware_Valid_NotExpiring_NoRefresh(t *testing.T) {
	f := newTestFixture(t)
	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("downstream handler did not run")
	}
	if !sink.sessOK {
		t.Fatal("session must be in context for valid session")
	}
	if sink.gotUser.Sub != "sub-abc" {
		t.Fatalf("user sub = %q, want sub-abc", sink.gotUser.Sub)
	}
	if sink.gotUser.ID != f.userID {
		t.Fatalf("user id = %v, want %v", sink.gotUser.ID, f.userID)
	}
	if sink.gotUser.Email != "ada@example.com" {
		t.Fatalf("user email = %q, want ada@example.com", sink.gotUser.Email)
	}
	if sink.gotUser.Pending {
		t.Fatal("active user must not be Pending")
	}
	if f.oauth.refreshCalls.Load() != 0 {
		t.Fatalf("Refresh must not be called when token is not expiring; got %d", f.oauth.refreshCalls.Load())
	}
	if f.resolver.calls.updateTokens.Load() != 0 {
		t.Fatal("UpdateTokens must not be called when no refresh happened")
	}
}

func TestSessionMiddleware_Expiring_RefreshSucceeds(t *testing.T) {
	f := newTestFixture(t)
	// Place AccessTokenExpiresAt 10s in the future — inside the
	// 30s refresh leeway → refresh required.
	f.session.AccessTokenExpiresAt = f.now.Add(10 * time.Second)
	f.resolver.store[f.sessionID] = &f.session

	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("downstream handler did not run after successful refresh")
	}
	if f.oauth.refreshCalls.Load() != 1 {
		t.Fatalf("Refresh call count = %d, want 1", f.oauth.refreshCalls.Load())
	}
	if f.resolver.calls.updateTokens.Load() != 1 {
		t.Fatalf("UpdateTokens call count = %d, want 1", f.resolver.calls.updateTokens.Load())
	}
	// Session attached to ctx must be the post-refresh row.
	if sink.gotSess.AccessToken != "new-access-refresh-1" {
		t.Fatalf("post-refresh AccessToken = %q, want new-access-refresh-1", sink.gotSess.AccessToken)
	}
}

func TestSessionMiddleware_RefreshFailure_RevokeAndAnonymous(t *testing.T) {
	f := newTestFixture(t)
	f.session.AccessTokenExpiresAt = f.now.Add(10 * time.Second)
	f.resolver.store[f.sessionID] = &f.session
	f.oauth.refreshHook = func(_ string) (Tokens, error) {
		return Tokens{}, errors.New("keycloak: invalid_grant")
	}

	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if sink.called {
		t.Fatal("downstream handler must not run after refresh failure")
	}
	if f.resolver.calls.revoke.Load() != 1 {
		t.Fatalf("Revoke must be called once after refresh failure; got %d", f.resolver.calls.revoke.Load())
	}
	if !hasClearCookie(w) {
		t.Fatal("expected clear-cookie after refresh failure")
	}
}

func TestSessionMiddleware_Revoked_ClearAndAnonymous(t *testing.T) {
	f := newTestFixture(t)
	revokedAt := f.now.Add(-1 * time.Minute)
	f.session.RevokedAt = &revokedAt
	f.resolver.store[f.sessionID] = &f.session

	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("revoked session falls through anonymously; downstream must still run")
	}
	if sink.sessOK {
		t.Fatal("session must not be in context for revoked session")
	}
	if !hasClearCookie(w) {
		t.Fatal("expected clear-cookie for revoked session")
	}
	if f.resolver.calls.revoke.Load() != 1 {
		t.Fatalf("Revoke must be called once even though row was already revoked; got %d", f.resolver.calls.revoke.Load())
	}
}

func TestSessionMiddleware_AbsoluteExpired_RevokeAndAnonymous(t *testing.T) {
	f := newTestFixture(t)
	f.session.AbsoluteExpiresAt = f.now.Add(-1 * time.Minute)
	f.resolver.store[f.sessionID] = &f.session

	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("absolute-expired session falls through anonymously")
	}
	if sink.sessOK {
		t.Fatal("no session in context for absolute-expired session")
	}
	if f.resolver.calls.revoke.Load() != 1 {
		t.Fatalf("Revoke must be called once; got %d", f.resolver.calls.revoke.Load())
	}
	if !hasClearCookie(w) {
		t.Fatal("expected clear-cookie for absolute-expired session")
	}
}

func TestSessionMiddleware_IdleTimeout_RevokeAndAnonymous(t *testing.T) {
	f := newTestFixture(t)
	// LastUsedAt > IdleTimeout ago
	f.session.LastUsedAt = f.now.Add(-25 * time.Hour)
	f.resolver.store[f.sessionID] = &f.session

	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)

	if !sink.called {
		t.Fatal("idle-expired session falls through anonymously")
	}
	if sink.sessOK {
		t.Fatal("no session in context for idle-expired session")
	}
	if f.resolver.calls.revoke.Load() != 1 {
		t.Fatalf("Revoke must be called once; got %d", f.resolver.calls.revoke.Load())
	}
}

func TestSessionMiddleware_TouchDebounce(t *testing.T) {
	f := newTestFixture(t)
	// LastUsedAt 5 minutes ago → > 30s default → must Touch.
	f.session.LastUsedAt = f.now.Add(-5 * time.Minute)
	f.resolver.store[f.sessionID] = &f.session

	sink := &sinkHandler{}
	mw := SessionMiddleware(f.deps())(sink)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	if f.resolver.calls.touch.Load() != 1 {
		t.Fatalf("first request must call Touch; got %d", f.resolver.calls.touch.Load())
	}

	// Second request: LastUsedAt was just updated to `now` (fake
	// clock), so the debounce window is open → must NOT Touch.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req2.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	mw.ServeHTTP(w2, req2)
	if f.resolver.calls.touch.Load() != 1 {
		t.Fatalf("second request inside debounce window must NOT call Touch again; total Touch = %d", f.resolver.calls.touch.Load())
	}
}

func TestSessionMiddleware_StampedeOnRefresh_SingleOAuthCall(t *testing.T) {
	f := newTestFixture(t)
	// Force the refresh branch.
	f.session.AccessTokenExpiresAt = f.now.Add(10 * time.Second)
	f.resolver.store[f.sessionID] = &f.session

	// Block both requests inside GetByID so both reach the refresh
	// branch concurrently. After releasing, the per-session mutex
	// must serialise them so only one OAuth.Refresh fires.
	gate := make(chan struct{})
	f.resolver.blockGetByID = gate

	// Synchronise Refresh so the second waiter has a definite window
	// to re-read and observe the rotated tokens before it would
	// otherwise call Refresh itself.
	refreshStarted := make(chan struct{}, 1)
	refreshRelease := make(chan struct{})
	f.oauth.refreshHook = func(_ string) (Tokens, error) {
		refreshStarted <- struct{}{}
		<-refreshRelease
		return Tokens{
			AccessToken:           "new-access",
			RefreshToken:          "new-refresh",
			IDToken:               "new-id",
			AccessTokenExpiresAt:  f.now.Add(5 * time.Minute),
			RefreshTokenExpiresAt: f.now.Add(30 * 24 * time.Hour),
		}, nil
	}

	mw := SessionMiddleware(f.deps())(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
			req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)
		}()
	}

	// Release both initial GetByID calls so both requests reach the
	// refresh branch. Subsequent GetByID re-reads (the in-critical-
	// section re-read by the second waiter) read from the same now-
	// closed channel — receiving from a closed channel returns
	// immediately, so the gate goes from "block all" to "pass all"
	// atomically without touching the field again.
	close(gate)

	// Wait for the one (and only one) Refresh call to start.
	<-refreshStarted
	// Allow it to complete; the second waiter then enters the lock,
	// re-reads the now-fresh row, and skips its own Refresh.
	close(refreshRelease)
	wg.Wait()

	if f.oauth.refreshCalls.Load() != 1 {
		t.Fatalf("stampede defense failed: Refresh called %d times, want 1", f.oauth.refreshCalls.Load())
	}
	if f.resolver.calls.updateTokens.Load() != 1 {
		t.Fatalf("UpdateTokens called %d times, want 1", f.resolver.calls.updateTokens.Load())
	}
}

func TestSessionMiddleware_PromMetrics_RegisterAndRecord(t *testing.T) {
	// Two assertions: the metrics are registered with the default
	// gatherer (visible via Gather), and an observation against
	// them survives a round trip through the middleware.
	f := newTestFixture(t)
	// Force refresh so both histograms record.
	f.session.AccessTokenExpiresAt = f.now.Add(10 * time.Second)
	f.resolver.store[f.sessionID] = &f.session

	mw := SessionMiddleware(f.deps())(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.AddCookie(testSessionCookie(cookieValueFor(f.sessionID)))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	seen := map[string]bool{}
	for _, mf := range mfs {
		seen[mf.GetName()] = true
	}
	if !seen["minerals_session_lookup_duration_seconds"] {
		t.Fatal("minerals_session_lookup_duration_seconds not registered")
	}
	if !seen["minerals_session_refresh_duration_seconds"] {
		t.Fatal("minerals_session_refresh_duration_seconds not registered")
	}

	// The "miss" label must have at least one observation after our
	// successful lookup; "ok" must have one after the refresh.
	lookupCount := histogramCount(t, mfs, "minerals_session_lookup_duration_seconds", "result", "miss")
	if lookupCount < 1 {
		t.Fatalf("lookup histogram (result=miss) count = %d, want >= 1", lookupCount)
	}
	refreshCount := histogramCount(t, mfs, "minerals_session_refresh_duration_seconds", "outcome", "ok")
	if refreshCount < 1 {
		t.Fatalf("refresh histogram (outcome=ok) count = %d, want >= 1", refreshCount)
	}
}

// histogramCount returns the SampleCount of the {label=value} series
// of the named histogram metric family, or fatals when nothing matches.
func histogramCount(t *testing.T, mfs []*dto.MetricFamily, name, label, value string) uint64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			match := false
			for _, lp := range m.GetLabel() {
				if lp.GetName() == label && lp.GetValue() == value {
					match = true
					break
				}
			}
			if !match {
				continue
			}
			if h := m.GetHistogram(); h != nil {
				return h.GetSampleCount()
			}
		}
	}
	t.Fatalf("no %s{%s=%q} sample observed", name, label, value)
	return 0
}
