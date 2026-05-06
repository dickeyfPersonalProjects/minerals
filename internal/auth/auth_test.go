package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestStubUserConstant(t *testing.T) {
	want := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if StubUser.ID != want {
		t.Fatalf("StubUser.ID = %s, want %s", StubUser.ID, want)
	}
	if StubUser.Email != "overseer@minerals.local" {
		t.Fatalf("StubUser.Email = %q", StubUser.Email)
	}
}

func TestAuthMiddlewarePopulatesStubUser(t *testing.T) {
	var got User
	h := Auth(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if got.ID != StubUser.ID || got.Email != StubUser.Email {
		t.Fatalf("got %+v, want StubUser %+v", got, StubUser)
	}
}

func TestRequireUser_AllowsPopulatedContext(t *testing.T) {
	called := false
	h := RequireUser(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(WithUser(httptest.NewRequest(http.MethodGet, "/", nil).Context(), StubUser))
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("downstream handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRequireUser_Returns401WhenNoUser(t *testing.T) {
	h := RequireUser(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("downstream handler should not have been called")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.Error.Code != "unauthorized" {
		t.Fatalf("error.code = %q, want unauthorized", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Fatal("error.message is empty")
	}
}

func TestFromContext_ReturnsZeroWhenAbsent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	u := FromContext(req.Context())
	if u.ID != uuid.Nil || u.Email != "" {
		t.Fatalf("FromContext on bare context = %+v, want zero User", u)
	}
}

func TestRequestID_RoundTrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithRequestID(req.Context(), "01HFOO")
	if got := RequestID(ctx); got != "01HFOO" {
		t.Fatalf("RequestID = %q, want 01HFOO", got)
	}
	if got := RequestID(req.Context()); got != "" {
		t.Fatalf("RequestID on bare context = %q, want empty", got)
	}
}
