package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/oidc"
)

// fakeVerifier is a TokenVerifier that maps known token strings to
// claims. An unknown token returns an error, standing in for an
// invalid/expired/forged JWT without the crypto machinery — real
// signature verification is covered by internal/oidc's own tests.
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

func TestStubUserConstant(t *testing.T) {
	t.Parallel()
	want := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if StubUser.ID != want {
		t.Fatalf("StubUser.ID = %s, want %s", StubUser.ID, want)
	}
	if StubUser.Email != "overseer@minerals.local" {
		t.Fatalf("StubUser.Email = %q", StubUser.Email)
	}
}

func TestAuth_NilVerifierPopulatesStubUser(t *testing.T) {
	t.Parallel()
	var got User
	h := Auth(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if got.ID != StubUser.ID || got.Email != StubUser.Email {
		t.Fatalf("got %+v, want StubUser %+v", got, StubUser)
	}
}

func TestAuth_ValidTokenPopulatesUserFromClaims(t *testing.T) {
	t.Parallel()
	const sub = "00000000-0000-0000-0000-000000000abc"
	v := fakeVerifier{tokens: map[string]*oidc.Claims{
		"good": {Subject: sub, Email: "fury@minerals.local", Roles: []string{"user", "devops-viewer"}},
	}}
	var got User
	h := Auth(v)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer good")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got.Sub != sub {
		t.Errorf("Sub = %q, want %q", got.Sub, sub)
	}
	if got.ID != uuid.MustParse(sub) {
		t.Errorf("ID = %s, want %s (parsed from sub)", got.ID, sub)
	}
	if got.Email != "fury@minerals.local" {
		t.Errorf("Email = %q", got.Email)
	}
	if len(got.Roles) != 2 || got.Roles[0] != "user" || got.Roles[1] != "devops-viewer" {
		t.Errorf("Roles = %v, want [user devops-viewer]", got.Roles)
	}
}

func TestAuth_RejectsMissingAndInvalidTokens(t *testing.T) {
	t.Parallel()
	v := fakeVerifier{tokens: map[string]*oidc.Claims{
		"good": {Subject: "00000000-0000-0000-0000-000000000abc"},
	}}
	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong scheme", "Basic Zm9vOmJhcg=="},
		{"bearer no token", "Bearer "},
		{"unrecognized token", "Bearer forged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := Auth(v)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatal("downstream handler should not run on rejected auth")
			}))
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		header  string
		wantTok string
		wantOK  bool
	}{
		{"Bearer abc123", "abc123", true},
		{"bearer abc123", "abc123", true},     // case-insensitive scheme
		{"BEARER abc123", "abc123", true},     // case-insensitive scheme
		{"Bearer   abc123  ", "abc123", true}, // trimmed
		{"", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Basic abc123", "", false},
		{"abc123", "", false},
	}
	for _, tc := range cases {
		gotTok, gotOK := BearerToken(tc.header)
		if gotTok != tc.wantTok || gotOK != tc.wantOK {
			t.Errorf("BearerToken(%q) = (%q, %t), want (%q, %t)",
				tc.header, gotTok, gotOK, tc.wantTok, tc.wantOK)
		}
	}
}

func TestUserFromClaims(t *testing.T) {
	t.Parallel()
	const sub = "11111111-1111-1111-1111-111111111111"
	u := UserFromClaims(&oidc.Claims{Subject: sub, Email: "x@y.z", Roles: []string{"user"}})
	if u.Sub != sub || u.ID != uuid.MustParse(sub) || u.Email != "x@y.z" {
		t.Errorf("UserFromClaims UUID sub = %+v", u)
	}
	// Non-UUID sub leaves ID nil but still carries Sub.
	u2 := UserFromClaims(&oidc.Claims{Subject: "not-a-uuid", Email: "x@y.z"})
	if u2.ID != uuid.Nil || u2.Sub != "not-a-uuid" {
		t.Errorf("UserFromClaims non-UUID sub = %+v, want nil ID with Sub set", u2)
	}
}

func TestRequireUser_AllowsPopulatedContext(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	u := FromContext(req.Context())
	if u.ID != uuid.Nil || u.Email != "" {
		t.Fatalf("FromContext on bare context = %+v, want zero User", u)
	}
}

func TestRequestID_RoundTrip(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithRequestID(req.Context(), "01HFOO")
	if got := RequestID(ctx); got != "01HFOO" {
		t.Fatalf("RequestID = %q, want 01HFOO", got)
	}
	if got := RequestID(req.Context()); got != "" {
		t.Fatalf("RequestID on bare context = %q, want empty", got)
	}
}
