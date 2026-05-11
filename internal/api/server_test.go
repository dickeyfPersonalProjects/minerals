package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options missing")
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("CSP header missing")
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id header missing")
	}
}

// TestOpenAPISpecHasExpectedPaths verifies the type-derived OpenAPI
// spec served at /api/v1/openapi.json includes every system endpoint
// registered through huma. Required by mi-cy4 acceptance.
func TestOpenAPISpecHasExpectedPaths(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	var body struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(body.OpenAPI, "3.") {
		t.Errorf("openapi version = %q, want 3.x", body.OpenAPI)
	}
	if body.Info.Title == "" || body.Info.Version == "" {
		t.Errorf("info = %+v, want non-empty title/version", body.Info)
	}
	for _, want := range []string{"/healthz", "/readyz", "/docs", "/api/v1/openapi.json"} {
		if _, ok := body.Paths[want]; !ok {
			t.Errorf("spec missing path %q (have %v)", want, keysOf(body.Paths))
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDocsPlaceholder(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestRequestIDInboundHonored(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	// Valid ULID — round-trips unchanged.
	const ulid = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	req.Header.Set("X-Request-Id", ulid)
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != ulid {
		t.Fatalf("X-Request-Id = %q, want inbound %q", got, ulid)
	}
}

func TestRequestIDInboundReplacedWhenMalformed(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "not-a-ulid")
	h.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-Id")
	if got == "" || got == "not-a-ulid" {
		t.Fatalf("X-Request-Id = %q, want freshly generated ULID", got)
	}
}

func TestHSTSOnlyOverHTTPS(t *testing.T) {
	h := New(Deps{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS set on plain HTTP request")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Header().Get("Strict-Transport-Security"), "max-age=") {
		t.Error("HSTS missing on https request")
	}
}

func TestApiV1NotFoundReturnsEnvelope(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens/abc", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code == "" {
		t.Error("error.code missing")
	}
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(_ context.Context) error { return f.err }

type fakeBucket struct{ err error }

func (f fakeBucket) HeadBucket(_ context.Context) error { return f.err }

func TestReadyzAllChecksOK(t *testing.T) {
	h := New(Deps{
		DB:      fakePinger{},
		Storage: fakeBucket{},
		SchemaVersion: func(_ context.Context) (uint, bool, error) {
			return 1, false, nil
		},
		ExpectedVersion: 1,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Ready  bool `json:"ready"`
		Checks map[string]struct {
			OK bool `json:"ok"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Ready {
		t.Errorf("ready=false")
	}
	for name, c := range body.Checks {
		if !c.OK {
			t.Errorf("check %s not ok", name)
		}
	}
}

func TestReadyzReports503OnDBFailure(t *testing.T) {
	h := New(Deps{
		DB:      fakePinger{err: context.DeadlineExceeded},
		Storage: fakeBucket{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
