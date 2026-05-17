package mindat_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// TestLookupByName_NormalizesHTMLFormula proves the Mindat-ingest path
// applies NormalizeChemicalFormula. Mindat is the dominant source of
// HTML-flavored markup; this guard catches a future refactor that
// accidentally drops the normalization call.
func TestLookupByName_NormalizesHTMLFormula(t *testing.T) {
	const body = `{"results":[{
		"id": 1,
		"name": "Curite",
		"ima_formula": "Pb(UO<sub>2</sub>)<sub>3</sub>O<sub>3</sub>(OH)<sub>2</sub> &middot; 3H<sub>2</sub>O"
	}]}`
	_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	rec, err := c.LookupByName(context.Background(), "Curite")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if rec.Data.ChemicalFormula == nil {
		t.Fatal("formula was nil")
	}
	const want = "Pb(UO₂)₃O₃(OH)₂ · 3H₂O"
	if got := *rec.Data.ChemicalFormula; got != want {
		t.Errorf("formula = %q, want %q", got, want)
	}
	if strings.ContainsAny(*rec.Data.ChemicalFormula, "<&") {
		t.Errorf("formula still contains markup: %q", *rec.Data.ChemicalFormula)
	}
}

// TestLookupByName_MagnetismMapping exercises the heuristic that
// turns Mindat's free-text `magnetism` field into MineralData.Magnetic.
// Anything non-empty and non-"diamagnetic" maps to true; empty and
// diamagnetic stay nil (the UI checkbox default is unchecked, which
// matches "unknown / not magnetic to the naked magnet").
func TestLookupByName_MagnetismMapping(t *testing.T) {
	cases := []struct {
		name      string
		magnetism string
		want      *bool
	}{
		{"magnetite ferromagnetic", "ferromagnetic", ptrBool(true)},
		{"paramagnetic", "Paramagnetic", ptrBool(true)},
		{"antiferromagnetic", "antiferromagnetic", ptrBool(true)},
		{"diamagnetic case-insensitive", "Diamagnetic", nil},
		{"empty magnetism", "", nil},
		{"whitespace only", "   ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"results":[{"id":1,"name":"Sample","magnetism":` +
				strconv.Quote(tc.magnetism) + `}]}`
			_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			rec, err := c.LookupByName(context.Background(), "Sample")
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			switch {
			case tc.want == nil && rec.Data.Magnetic != nil:
				t.Errorf("Magnetic = %v, want nil", *rec.Data.Magnetic)
			case tc.want != nil && rec.Data.Magnetic == nil:
				t.Errorf("Magnetic = nil, want *%v", *tc.want)
			case tc.want != nil && rec.Data.Magnetic != nil && *rec.Data.Magnetic != *tc.want:
				t.Errorf("Magnetic = *%v, want *%v", *rec.Data.Magnetic, *tc.want)
			}
		})
	}
}

// TestLookupByName_MagnetismFieldRequested confirms the magnetism
// column is in the `fields=` query — a regression here would silently
// blank the magnetic checkbox for every lookup.
func TestLookupByName_MagnetismFieldRequested(t *testing.T) {
	var gotFields string
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotFields = r.URL.Query().Get("fields")
		_, _ = w.Write([]byte(`{"results":[{"id":1,"name":"Quartz"}]}`))
	})
	if _, err := c.LookupByName(context.Background(), "Quartz"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !strings.Contains(gotFields, "magnetism") {
		t.Errorf("fields query missing magnetism: %q", gotFields)
	}
}

func ptrBool(b bool) *bool { return &b }

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
