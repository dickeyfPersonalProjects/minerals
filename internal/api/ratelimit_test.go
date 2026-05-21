package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/auth/bff"
)

// rlClock is a controllable clock for the middleware tests. No
// time.Sleep — tests advance explicitly.
type rlClock struct {
	mu sync.Mutex
	t  time.Time
}

func newRLClock() *rlClock {
	return &rlClock{t: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)}
}

func (c *rlClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *rlClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// okHandler counts how many requests reach it and returns 200.
type okHandler struct{ hits int }

func (h *okHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.hits++
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// testTiers gives every tier a small, distinct budget so exhaustion is
// quick and cross-tier independence is observable.
func testTiers(clk *rlClock) RateLimitOptions {
	return RateLimitOptions{
		Auth:  RateLimitTier{Requests: 2, Window: time.Minute},
		Read:  RateLimitTier{Requests: 3, Window: time.Minute},
		Write: RateLimitTier{Requests: 2, Window: time.Minute},
		File:  RateLimitTier{Requests: 2, Window: time.Minute},
		Now:   clk.now,
	}
}

// send runs one request through the limiter and returns the recorder.
// user, when non-nil, is attached to the context the way SessionMW
// would. cfIP, when non-empty, sets CF-Connecting-IP.
func send(mw func(http.Handler) http.Handler, h http.Handler, method, path, cfIP, remoteAddr string, user *auth.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cfIP != "" {
		req.Header.Set("CF-Connecting-IP", cfIP)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if user != nil {
		req = req.WithContext(auth.WithUser(req.Context(), *user))
	}
	rec := httptest.NewRecorder()
	mw(h).ServeHTTP(rec, req)
	return rec
}

func TestRateLimit_AnonymousKeysOffCFConnectingIP(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	// Read tier budget is 3. Same CF-Connecting-IP shares one bucket.
	for i := 0; i < 3; i++ {
		if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "1.2.3.4", "10.0.0.1:1111", nil); rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i+1, rec.Code)
		}
	}
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "1.2.3.4", "10.0.0.1:1111", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th same-IP request: want 429, got %d", rec.Code)
	}
	// A different CF-Connecting-IP has its own bucket (still allowed),
	// even sharing the same socket RemoteAddr.
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "9.9.9.9", "10.0.0.1:1111", nil); rec.Code != http.StatusOK {
		t.Fatalf("different CF-IP: want 200, got %d", rec.Code)
	}
}

func TestRateLimit_CFConnectingIPAbsentFallsBackToRemoteAddr(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	// No CF-Connecting-IP — the socket RemoteAddr (host part) is the key.
	for i := 0; i < 3; i++ {
		if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "", "203.0.113.7:5555", nil); rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i+1, rec.Code)
		}
	}
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "", "203.0.113.7:5555", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request from same RemoteAddr: want 429, got %d", rec.Code)
	}
	// A different source port is the same host → same bucket (already
	// exhausted). A different host is independent.
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "", "198.51.100.2:5555", nil); rec.Code != http.StatusOK {
		t.Fatalf("different RemoteAddr host: want 200, got %d", rec.Code)
	}
}

func TestRateLimit_AuthenticatedSharedAcrossSessions(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	uid := uuid.New()
	user := &auth.User{ID: uid, Sub: "kc-sub"}

	// THE operator requirement: two distinct sessions/devices (modeled
	// here as different CF IPs) of the SAME user share ONE account
	// bucket. Read budget 3 → the 4th is throttled regardless of which
	// "session" sends it.
	send(mw, h, http.MethodGet, "/api/v1/specimens", "1.1.1.1", "", user)
	send(mw, h, http.MethodGet, "/api/v1/specimens", "2.2.2.2", "", user)
	send(mw, h, http.MethodGet, "/api/v1/specimens", "3.3.3.3", "", user)
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "4.4.4.4", "", user); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request from a different session of the same user: want 429, got %d", rec.Code)
	}
}

func TestRateLimit_AuthWinsOverIP(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	uid := uuid.New()
	user := &auth.User{ID: uid}

	// Drain the authenticated user's read budget (3).
	for i := 0; i < 3; i++ {
		send(mw, h, http.MethodGet, "/api/v1/specimens", "5.5.5.5", "", user)
	}
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "5.5.5.5", "", user); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("authed user over budget: want 429, got %d", rec.Code)
	}
	// An anonymous caller from the SAME IP is keyed by IP, not the
	// drained account bucket — it still passes (auth keying did not
	// leak into the IP bucket).
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "5.5.5.5", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("anonymous same-IP after account drain: want 200, got %d", rec.Code)
	}
}

func TestRateLimit_DifferentUsersIndependent(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	a := &auth.User{ID: uuid.New()}
	b := &auth.User{ID: uuid.New()}

	for i := 0; i < 3; i++ {
		send(mw, h, http.MethodGet, "/api/v1/specimens", "", "", a)
	}
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "", "", a); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("user a over budget: want 429, got %d", rec.Code)
	}
	// User b is untouched.
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "", "", b); rec.Code != http.StatusOK {
		t.Fatalf("user b independent: want 200, got %d", rec.Code)
	}
}

func TestRateLimit_AuthTierStricterAndIndependentFromReads(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	// Auth tier budget is 2 (stricter than the read tier's 3). Login
	// is keyed per-IP even though no user is attached.
	send(mw, h, http.MethodPost, "/auth/login", "7.7.7.7", "", nil)
	send(mw, h, http.MethodPost, "/auth/login", "7.7.7.7", "", nil)
	if rec := send(mw, h, http.MethodPost, "/auth/login", "7.7.7.7", "", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd login from same IP: want 429 (auth budget 2), got %d", rec.Code)
	}
	// Exhausting auth does NOT drain the read tier — reads from the
	// same IP still pass (separate limiter per tier).
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "7.7.7.7", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("read after auth-tier exhaustion: want 200, got %d", rec.Code)
	}
}

func TestRateLimit_AuthTierIPKeyedEvenWhenAuthenticated(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	// /auth/logout carries a session, but the auth tier is brute-force
	// defense and keys per-IP regardless of the attached user. Two
	// different users on the same IP share the auth bucket.
	send(mw, h, http.MethodPost, "/auth/logout", "8.8.8.8", "", &auth.User{ID: uuid.New()})
	send(mw, h, http.MethodPost, "/auth/logout", "8.8.8.8", "", &auth.User{ID: uuid.New()})
	if rec := send(mw, h, http.MethodPost, "/auth/logout", "8.8.8.8", "", &auth.User{ID: uuid.New()}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd logout on same IP (different users): want 429, got %d", rec.Code)
	}
}

func TestRateLimit_ReadsDoNotDrainWrites(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	user := &auth.User{ID: uuid.New()}
	// Exhaust the read tier (3) for this user.
	for i := 0; i < 4; i++ {
		send(mw, h, http.MethodGet, "/api/v1/specimens", "", "", user)
	}
	// Writes (budget 2) are a separate bucket — still available.
	if rec := send(mw, h, http.MethodPost, "/api/v1/specimens", "", "", user); rec.Code != http.StatusOK {
		t.Fatalf("write after read exhaustion: want 200, got %d", rec.Code)
	}
	if rec := send(mw, h, http.MethodPatch, "/api/v1/specimens/x", "", "", user); rec.Code != http.StatusOK {
		t.Fatalf("2nd write: want 200, got %d", rec.Code)
	}
	if rec := send(mw, h, http.MethodDelete, "/api/v1/specimens/x", "", "", user); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd write over budget: want 429, got %d", rec.Code)
	}
}

func TestRateLimit_FileTierSeparate(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	// File serving (GET /api/v1/photos/*) uses the file tier (budget 2),
	// not the read tier.
	send(mw, h, http.MethodGet, "/api/v1/photos/abc", "6.6.6.6", "", nil)
	send(mw, h, http.MethodGet, "/api/v1/photos/abc/display", "6.6.6.6", "", nil)
	if rec := send(mw, h, http.MethodGet, "/api/v1/photos/abc/thumb", "6.6.6.6", "", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd photo fetch over file budget: want 429, got %d", rec.Code)
	}
	// A plain read from the same IP is the read tier — still allowed.
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "6.6.6.6", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("read after file exhaustion: want 200, got %d", rec.Code)
	}
}

func TestRateLimit_UnmatchedPathsPassThrough(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	// SPA + system endpoints are not limited here (edge handles flood).
	for _, p := range []string{"/", "/healthz", "/readyz", "/docs", "/index.html"} {
		for i := 0; i < 10; i++ {
			if rec := send(mw, h, http.MethodGet, p, "1.2.3.4", "", nil); rec.Code != http.StatusOK {
				t.Fatalf("%s request %d: want 200 (unlimited), got %d", p, i+1, rec.Code)
			}
		}
	}
}

func TestRateLimit_ResponseShape(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	// Under-limit response passes through unchanged (handler's body).
	rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "4.3.2.1", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("under-limit: want 200/ok, got %d/%q", rec.Code, rec.Body.String())
	}

	// Exhaust, then inspect the 429 envelope.
	send(mw, h, http.MethodGet, "/api/v1/specimens", "4.3.2.1", "", nil)
	send(mw, h, http.MethodGet, "/api/v1/specimens", "4.3.2.1", "", nil)
	over := send(mw, h, http.MethodGet, "/api/v1/specimens", "4.3.2.1", "", nil)

	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", over.Code)
	}
	ra := over.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("Retry-After header missing")
	}
	secs, err := strconv.Atoi(ra)
	if err != nil || secs < 1 {
		t.Fatalf("Retry-After must be a sane positive integer, got %q", ra)
	}
	if ct := over.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("unexpected Content-Type %q", ct)
	}
	var env errorEnvelope
	if err := json.Unmarshal(over.Body.Bytes(), &env); err != nil {
		t.Fatalf("body is not a §10 envelope: %v", err)
	}
	if env.Error.Code != "rate_limited" {
		t.Fatalf("want code rate_limited, got %q", env.Error.Code)
	}
}

func TestRateLimit_RefillAfterWindow(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	mw := NewRateLimitMiddleware(testTiers(clk))
	h := &okHandler{}

	for i := 0; i < 3; i++ {
		send(mw, h, http.MethodGet, "/api/v1/specimens", "1.0.0.1", "", nil)
	}
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "1.0.0.1", "", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 before refill, got %d", rec.Code)
	}
	// Advance a full window — the bucket refills and requests flow again.
	clk.advance(time.Minute)
	if rec := send(mw, h, http.MethodGet, "/api/v1/specimens", "1.0.0.1", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("want 200 after window refill, got %d", rec.Code)
	}
}

// stubSessionMW mimics SessionMiddleware for the integration test: it
// attaches an auth.User (account keying input) AND a bff.Session (so
// the real CSRF middleware sees a session) to the request context.
func stubSessionMW(user auth.User, sess bff.Session) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithUser(r.Context(), user)
			ctx = bff.WithSession(ctx, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func TestRateLimit_ChainOrder_SessionRateLimitCSRF(t *testing.T) {
	t.Parallel()
	clk := newRLClock()
	rlMW := NewRateLimitMiddleware(testTiers(clk))

	user := auth.User{ID: uuid.New()}
	sessMW := stubSessionMW(user, bff.Session{})
	h := &okHandler{}

	// Faithful chain order: Session → RateLimit → CSRF → handler.
	// CSRF bypasses safe methods, so GETs exercise the full chain
	// without needing a CSRF token.
	chain := sessMW(rlMW(bff.CSRFMiddleware(h)))

	do := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens", nil)
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req.WithContext(context.Background()))
		return rec.Code
	}

	// Read budget 3: first three pass, fourth is throttled — proving
	// the limiter keyed off the user the session middleware attached
	// upstream (no IP/CF header set on these requests).
	for i := 0; i < 3; i++ {
		if code := do(); code != http.StatusOK {
			t.Fatalf("request %d through chain: want 200, got %d", i+1, code)
		}
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("4th request through chain: want 429, got %d", code)
	}
	if h.hits != 3 {
		t.Fatalf("handler should have been reached exactly 3 times, got %d", h.hits)
	}
}
