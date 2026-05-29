package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWebHandlerNil_NoSPAFallback locks the API-only contract (mi-zomq):
// when Deps.WebHandler is nil (WEB_SERVE_MODE=disabled), the backend does
// NOT register the "/" catch-all, so an unknown non-API path is a plain
// 404 rather than the SPA shell. This is what lets the SPA be served from
// a single shared source (MinIO/CDN) without the backend in the path.
func TestWebHandlerNil_NoSPAFallback(t *testing.T) {
	h := New(Deps{}) // WebHandler nil
	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no SPA fallback when WebHandler is nil)", rec.Code)
	}
}

// TestWebHandlerSet_SPAFallback is the converse: when a WebHandler is
// supplied (WEB_SERVE_MODE=embedded), unmatched paths route to it.
func TestWebHandlerSet_SPAFallback(t *testing.T) {
	const marker = "spa-shell"
	spa := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(marker))
	})
	h := New(Deps{WebHandler: spa})
	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if rec.Body.String() != marker {
		t.Errorf("body = %q, want %q (request should route to the SPA handler)", rec.Body.String(), marker)
	}
}

// TestAPINotFoundUnaffectedByWebMode verifies the /api/v1/* 404 envelope
// is independent of the SPA catch-all: an unknown API path returns the
// structured 404 whether or not a WebHandler is registered.
func TestAPINotFoundUnaffectedByWebMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		deps Deps
	}{
		{"web-disabled", Deps{}},
		{"web-embedded", Deps{WebHandler: http.NotFoundHandler()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := New(tc.deps)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}
