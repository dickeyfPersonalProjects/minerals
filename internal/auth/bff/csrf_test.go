package bff

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeCSRFSession returns a Session with a fixed CSRFToken and its
// base64url(no-padding) encoding. The fixed bytes let tests compare
// the wire token to a known string; production code generates the
// token at session creation via crypto/rand.
func makeCSRFSession() (Session, string) {
	var token [32]byte
	for i := range token {
		token[i] = byte(i + 1)
	}
	sess := Session{CSRFToken: token}
	return sess, base64.RawURLEncoding.EncodeToString(token[:])
}

// chainSession is the SessionMiddleware substitute used in CSRF
// middleware tests: it attaches the supplied Session (or nothing, for
// the anonymous case) to the request context before handing off to
// CSRFMiddleware. Using a real Session value rather than the full
// SessionMiddleware keeps these tests scoped to the CSRF check —
// SessionMiddleware has its own coverage in middleware_test.go.
func chainSession(sess *Session, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess != nil {
			r = r.WithContext(WithSession(r.Context(), *sess))
		}
		h.ServeHTTP(w, r)
	})
}

func nextOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func decodeErrorEnvelope(t *testing.T, body io.Reader) (code, msg string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return env.Error.Code, env.Error.Message
}

// --- CSRFMiddleware ----------------------------------------------------

func TestCSRFMiddleware_SafeMethodsBypassWhenAuthenticated(t *testing.T) {
	sess, _ := makeCSRFSession()
	handler := chainSession(&sess, CSRFMiddleware(nextOK()))

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/api/v1/specimens", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Errorf("method %s: got status %d, want %d (safe-method bypass)",
				method, rr.Code, http.StatusNoContent)
		}
	}
}

func TestCSRFMiddleware_AnonymousBypassesOnUnsafeMethod(t *testing.T) {
	// No session attached: middleware must fall through and let the
	// handler decide. POST with no header succeeds because the CSRF
	// check has nothing to compare against.
	handler := chainSession(nil, CSRFMiddleware(nextOK()))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("anonymous POST: got %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestCSRFMiddleware_MissingHeaderRejected(t *testing.T) {
	sess, _ := makeCSRFSession()
	handler := chainSession(&sess, CSRFMiddleware(nextOK()))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", rr.Code)
	}
	code, _ := decodeErrorEnvelope(t, rr.Body)
	if code != "csrf_missing" {
		t.Errorf("got error code %q, want csrf_missing", code)
	}
}

func TestCSRFMiddleware_WrongTokenRejected(t *testing.T) {
	sess, _ := makeCSRFSession()
	handler := chainSession(&sess, CSRFMiddleware(nextOK()))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", nil)
	req.Header.Set(CSRFHeaderName, "definitely-not-the-token")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", rr.Code)
	}
	code, _ := decodeErrorEnvelope(t, rr.Body)
	if code != "csrf_mismatch" {
		t.Errorf("got error code %q, want csrf_mismatch", code)
	}
}

func TestCSRFMiddleware_CorrectTokenPasses(t *testing.T) {
	sess, token := makeCSRFSession()
	handler := chainSession(&sess, CSRFMiddleware(nextOK()))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/specimens", strings.NewReader("{}"))
		req.Header.Set(CSRFHeaderName, token)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Errorf("method %s: got status %d, want %d (passed CSRF check)",
				method, rr.Code, http.StatusNoContent)
		}
	}
}

func TestCSRFMiddleware_NearMissTokenRejected(t *testing.T) {
	// A token that matches every byte except the last is the
	// classic timing-side-channel probe. The constant-time compare
	// must still reject — the test asserts correctness (rejection),
	// not the timing property itself (Go's compiler/scheduler make
	// microbenchmark timing assertions unreliable per the design
	// note).
	sess, token := makeCSRFSession()
	handler := chainSession(&sess, CSRFMiddleware(nextOK()))

	// Flip the final byte's encoding. base64url of [32]byte is
	// always 43 chars; mutating index 42 changes exactly one byte
	// in the encoded form.
	bad := []byte(token)
	if bad[len(bad)-1] == 'A' {
		bad[len(bad)-1] = 'B'
	} else {
		bad[len(bad)-1] = 'A'
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/specimens", nil)
	req.Header.Set(CSRFHeaderName, string(bad))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403 on one-byte-off token", rr.Code)
	}
	code, _ := decodeErrorEnvelope(t, rr.Body)
	if code != "csrf_mismatch" {
		t.Errorf("got code %q, want csrf_mismatch", code)
	}
}

// TestCSRFMiddleware_LogoutLikePathStillChecked covers the design's
// "logout requires CSRF" rule. The /auth/logout handler is built in
// mi-bm5b (#3); this test asserts the middleware behavior the logout
// route will rely on — an unsafe method against an authenticated
// session is checked regardless of URL path.
func TestCSRFMiddleware_LogoutLikePathStillChecked(t *testing.T) {
	sess, token := makeCSRFSession()
	handler := chainSession(&sess, CSRFMiddleware(nextOK()))

	// No header → 403 csrf_missing
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("logout without header: got %d, want 403", rr.Code)
	}
	if code, _ := decodeErrorEnvelope(t, rr.Body); code != "csrf_missing" {
		t.Errorf("logout without header: code %q, want csrf_missing", code)
	}

	// Correct header → passes through to handler
	req2 := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req2.Header.Set(CSRFHeaderName, token)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNoContent {
		t.Errorf("logout with correct header: got %d, want %d (passed CSRF check)",
			rr2.Code, http.StatusNoContent)
	}
}

// --- CSRFHandler -------------------------------------------------------

func TestCSRFHandler_AnonymousReturns401(t *testing.T) {
	// Behind SessionMiddleware in production; here we model the
	// no-session branch directly. The endpoint MUST refuse —
	// minting a token cross-site is the design's explicit threat
	// model (§csrf §subtle-choices).
	handler := chainSession(nil, http.HandlerFunc(CSRFHandler))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/csrf", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rr.Code)
	}
	code, _ := decodeErrorEnvelope(t, rr.Body)
	if code != "unauthorized" {
		t.Errorf("got code %q, want unauthorized", code)
	}
}

func TestCSRFHandler_AuthenticatedReturnsToken(t *testing.T) {
	sess, want := makeCSRFSession()
	handler := chainSession(&sess, http.HandlerFunc(CSRFHandler))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/csrf", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json...", ct)
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Token != want {
		t.Errorf("token = %q, want %q", body.Token, want)
	}
}

// TestCSRFHandler_ReachableThroughCSRFMiddleware verifies the
// composition invariant: the GET /api/v1/csrf endpoint must be
// reachable when CSRFMiddleware is wrapping it (the safe-method
// bypass keeps it so), and the returned token must round-trip
// through a follow-up POST.
func TestCSRFHandler_ReachableThroughCSRFMiddleware(t *testing.T) {
	sess, _ := makeCSRFSession()

	// Two routes behind the same session-bearing chain:
	//   GET /api/v1/csrf → CSRFHandler (safe-method bypass)
	//   POST /api/v1/x   → nextOK (CSRF check enforced)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/csrf", CSRFHandler)
	mux.Handle("/api/v1/x", nextOK())
	chained := chainSession(&sess, CSRFMiddleware(mux))

	// 1. SPA fetches the token.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/csrf", nil)
	getRR := httptest.NewRecorder()
	chained.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/csrf: got %d, want 200", getRR.Code)
	}
	var got struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(getRR.Body).Decode(&got); err != nil {
		t.Fatalf("decode token: %v", err)
	}

	// 2. SPA replays it on a POST.
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/x", nil)
	postReq.Header.Set(CSRFHeaderName, got.Token)
	postRR := httptest.NewRecorder()
	chained.ServeHTTP(postRR, postReq)
	if postRR.Code != http.StatusNoContent {
		t.Errorf("POST with fetched token: got %d, want %d", postRR.Code, http.StatusNoContent)
	}
}
