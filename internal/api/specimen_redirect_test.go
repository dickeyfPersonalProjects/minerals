package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSpecimenRedirect_RedirectsToHashRoute covers the mi-1rg contract:
// scanning a QR sticker (which encodes /specimens/{id}) must 302 the
// browser to the SPA's hash route so the detail page renders instead
// of the app root.
func TestSpecimenRedirect_RedirectsToHashRoute(t *testing.T) {
	h := New(Deps{})

	const id = "11111111-2222-3333-4444-555555555555"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/specimens/"+id, nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	want := "/#/specimens/" + id
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// Non-GET methods MUST NOT trigger the redirect — the SPA fallback
// would otherwise be bypassed and the user would see a confusing 302
// on a POST. The Go 1.22+ method-aware pattern constrains the route.
func TestSpecimenRedirect_NonGETFallsThrough(t *testing.T) {
	h := New(Deps{})

	const id = "11111111-2222-3333-4444-555555555555"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/specimens/"+id, nil)
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusFound {
		t.Fatalf("POST returned 302; redirect should be GET-only")
	}
}

// Sub-segment paths under /specimens/ are not part of the QR contract
// and must fall through to the SPA fallback rather than producing a
// broken redirect like /#/specimens/abc/extra.
func TestSpecimenRedirect_SubSegmentFallsThrough(t *testing.T) {
	h := New(Deps{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/specimens/abc/extra", nil)
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusFound {
		if loc := rec.Header().Get("Location"); loc == "/#/specimens/abc/extra" || loc == "/#/specimens/abc" {
			t.Fatalf("sub-segment redirected to %q; should fall through to SPA", loc)
		}
	}
}
