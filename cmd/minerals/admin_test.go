package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminHandler_Metrics(t *testing.T) {
	t.Parallel()
	h := newAdminHandler(adminProbes{})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// promhttp registers the default registry which ships go_* and
	// process_* runtime collectors. Spot-check one of each.
	for _, want := range []string{"go_goroutines", "process_start_time_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q metric", want)
		}
	}
}

func TestAdminHandler_Healthz(t *testing.T) {
	t.Parallel()
	h := newAdminHandler(adminProbes{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "ok" {
		t.Errorf("body = %q, want \"ok\"", got)
	}
}

func TestAdminHandler_ReadyzDelegates(t *testing.T) {
	t.Parallel()
	called := false
	h := newAdminHandler(adminProbes{
		readyz: func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"ready":false}`))
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatal("readyz delegate was not invoked")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestAdminHandler_UnknownPath_404(t *testing.T) {
	t.Parallel()
	h := newAdminHandler(adminProbes{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/specimens", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (admin port serves only operator paths)", rec.Code)
	}
}
