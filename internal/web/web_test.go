package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func newTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>spa</title>"), ModTime: time.Unix(0, 0)},
		"assets/foo.js": &fstest.MapFile{Data: []byte("console.log('foo');"), ModTime: time.Unix(0, 0)},
	}
}

func TestFS_HappyPath(t *testing.T) {
	sub, err := FS()
	if err != nil {
		t.Fatalf("FS() returned error: %v", err)
	}
	if sub == nil {
		t.Fatal("FS() returned nil filesystem")
	}
	// dist/ contains at least .gitkeep — verify we can list the root.
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("ReadDir on FS(): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry in embedded dist/")
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := handlerFor(newTestFS())
	req := httptest.NewRequest(http.MethodPost, "/anything", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: got %d, want 405", rr.Code)
	}
}

func TestHandler_MethodAllowed_HEAD(t *testing.T) {
	h := handlerFor(newTestFS())
	req := httptest.NewRequest(http.MethodHead, "/assets/foo.js", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("HEAD: got %d, want 200", rr.Code)
	}
}

func TestHandler_ExistingFile(t *testing.T) {
	h := handlerFor(newTestFS())
	req := httptest.NewRequest(http.MethodGet, "/assets/foo.js", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /assets/foo.js: got %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "console.log") {
		t.Fatalf("body did not contain expected asset content: %q", rr.Body.String())
	}
}

func TestHandler_RootServesIndex(t *testing.T) {
	h := handlerFor(newTestFS())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /: got %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<title>spa</title>") {
		t.Fatalf("root did not serve index.html, got: %q", rr.Body.String())
	}
}

func TestHandler_SPAFallback_DeepLink(t *testing.T) {
	h := handlerFor(newTestFS())
	req := httptest.NewRequest(http.MethodGet, "/specimens/123", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /specimens/123: got %d, want 200 (SPA fallback)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "<title>spa</title>") {
		t.Fatalf("SPA fallback did not serve index.html, got: %q", rr.Body.String())
	}
}

func TestHandler_SPAFallback_NoIndex_Returns404(t *testing.T) {
	// Filesystem with no index.html: SPA fallback fails → 404.
	h := handlerFor(fstest.MapFS{})
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing index.html: got %d, want 404", rr.Code)
	}
}

// errFS wraps an fs.FS to return a non-ErrNotExist error on Open, exercising
// the generic-read-error branch of the handler.
type errFS struct{}

func (errFS) Open(_ string) (fs.File, error) {
	return nil, fs.ErrPermission
}

func TestHandler_GenericReadError(t *testing.T) {
	h := handlerFor(errFS{})
	req := httptest.NewRequest(http.MethodGet, "/something", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("ErrPermission: got %d, want 500", rr.Code)
	}
}

func TestHandler_RealEmbeddedFS(t *testing.T) {
	// Smoke test: the production Handler() (using the real embed.FS) must
	// at least return a response for any method and not panic.
	h := Handler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("real Handler POST: got %d, want 405", rr.Code)
	}
}
