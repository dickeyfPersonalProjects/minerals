package mindat_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dickeyfPersonalProjects/minerals/internal/mindat"
)

func TestNewClient_NoAPIKey(t *testing.T) {
	_, err := mindat.NewClient(mindat.Options{APIKey: ""})
	if !errors.Is(err, mindat.ErrNoAPIKey) {
		t.Fatalf("got %v, want ErrNoAPIKey", err)
	}
}

func TestNewClient_TrimsKey(t *testing.T) {
	_, err := mindat.NewClient(mindat.Options{APIKey: "   "})
	if !errors.Is(err, mindat.ErrNoAPIKey) {
		t.Fatalf("whitespace-only key: got %v, want ErrNoAPIKey", err)
	}
}

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *mindat.Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := mindat.NewClient(mindat.Options{APIKey: "secret", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return srv, c
}

func TestLookupByName_Success(t *testing.T) {
	const body = `{"results":[{
		"id": 12345,
		"name": "Quartz",
		"ima_formula": "SiO2",
		"hardness_min": 7,
		"hardness_max": 7,
		"csystem": "Trigonal",
		"colour": "colorless to white",
		"lustretype": "vitreous",
		"fluorescence": ""
	}]}`
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Token secret" {
			t.Errorf("Authorization = %q, want \"Token secret\"", got)
		}
		if got := r.URL.Query().Get("name"); got != "Quartz" {
			t.Errorf("name = %q", got)
		}
		if r.URL.Path != "/geomaterials/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})

	rec, err := c.LookupByName(context.Background(), "Quartz")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if rec.Name != "Quartz" {
		t.Errorf("name = %q", rec.Name)
	}
	if rec.MindatID != "12345" {
		t.Errorf("mindat_id = %q", rec.MindatID)
	}
	if rec.Data.ChemicalFormula == nil || *rec.Data.ChemicalFormula != "SiO2" {
		t.Errorf("formula = %v", rec.Data.ChemicalFormula)
	}
	if rec.Data.MohsHardness == nil || *rec.Data.MohsHardness != 7 {
		t.Errorf("mohs = %v", rec.Data.MohsHardness)
	}
	if rec.Data.CrystalSystem == nil || *rec.Data.CrystalSystem != "Trigonal" {
		t.Errorf("crystal_system = %v", rec.Data.CrystalSystem)
	}
	if rec.Data.MindatID == nil || *rec.Data.MindatID != "12345" {
		t.Errorf("data.mindat_id = %v", rec.Data.MindatID)
	}
	if !strings.Contains(rec.Attribution, "Mindat") {
		t.Errorf("attribution = %q", rec.Attribution)
	}
}

func TestLookupByName_NoResults(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	_, err := c.LookupByName(context.Background(), "Unobtanium")
	if !errors.Is(err, mindat.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestLookupByName_RateLimited(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.LookupByName(context.Background(), "Quartz")
	if !errors.Is(err, mindat.ErrRateLimited) {
		t.Fatalf("got %v, want ErrRateLimited", err)
	}
}

func TestLookupByName_Unauthorized(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.LookupByName(context.Background(), "Quartz")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, mindat.ErrNotFound) || errors.Is(err, mindat.ErrRateLimited) {
		t.Errorf("should be a generic error, got sentinel: %v", err)
	}
}

func TestLookupByName_NameMismatchFiltered(t *testing.T) {
	const body = `{"results":[{
		"id": 99,
		"name": "Quartzite",
		"ima_formula": "SiO2"
	}]}`
	_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	_, err := c.LookupByName(context.Background(), "Quartz")
	if !errors.Is(err, mindat.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound (results name didn't match)", err)
	}
}

func TestLookupByName_EmptyName(t *testing.T) {
	_, c := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called for empty name")
	})
	_, err := c.LookupByName(context.Background(), "   ")
	if !errors.Is(err, mindat.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestLookupByName_HardnessRangeMidpoint(t *testing.T) {
	const body = `{"results":[{"id":1,"name":"Talc","hardness_min":1,"hardness_max":2}]}`
	_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	rec, err := c.LookupByName(context.Background(), "Talc")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if rec.Data.MohsHardness == nil || *rec.Data.MohsHardness != 1.5 {
		t.Errorf("mohs midpoint = %v, want 1.5", rec.Data.MohsHardness)
	}
}
