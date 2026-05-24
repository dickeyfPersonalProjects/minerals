package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// legalTestServer builds the full handler with no auth/DB deps; the
// legal endpoint is public and self-contained, so this exercises the
// real registration + render path.
func legalTestServer(t *testing.T) http.Handler {
	t.Helper()
	return New(Deps{})
}

func getJSON(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s body: %v (raw: %s)", path, err, rec.Body.String())
		}
	}
	return rec, body
}

func TestLegalEndpointServesPrivacy(t *testing.T) {
	h := legalTestServer(t)
	rec, body := getJSON(t, h, "/api/v1/legal/privacy")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := body["slug"]; got != "privacy" {
		t.Errorf("slug = %v, want privacy", got)
	}
	if got := body["title"]; got != "Privacy Policy" {
		t.Errorf("title = %v, want Privacy Policy", got)
	}
	html, _ := body["html"].(string)
	if !strings.Contains(html, "<h1") {
		t.Errorf("html missing rendered heading: %q", html)
	}
	// §17 pipeline renders the markdown table; confirm it survived
	// sanitization (tables are on the allowlist).
	if !strings.Contains(html, "<table") {
		t.Errorf("html missing rendered table: %q", html)
	}
}

func TestLegalEndpointServesTerms(t *testing.T) {
	h := legalTestServer(t)
	rec, body := getJSON(t, h, "/api/v1/legal/terms")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := body["title"]; got != "Terms of Service" {
		t.Errorf("title = %v, want Terms of Service", got)
	}
}

func TestLegalEndpointUnknownSlug404(t *testing.T) {
	h := legalTestServer(t)
	rec, body := getJSON(t, h, "/api/v1/legal/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "legal_document_not_found" {
		t.Errorf("error.code = %v, want legal_document_not_found", errObj["code"])
	}
}

// The endpoint must be reachable without authentication — it backs the
// pre-login registration consent links.
func TestLegalEndpointIsPublic(t *testing.T) {
	h := legalTestServer(t)
	rec, _ := getJSON(t, h, "/api/v1/legal/privacy")
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("legal endpoint required auth (status %d); must be public", rec.Code)
	}
}
