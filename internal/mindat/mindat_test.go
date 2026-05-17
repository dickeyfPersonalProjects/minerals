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

// TestLookupByName_DerivesRadioactiveAndAcidReactive proves the end-
// to-end path: Mindat geomaterial → MineralData with Radioactive /
// ReactsToAcid set from the derivation rules (mi-8pcs). Unit-level
// tests for the helpers themselves live in derive_test.go; this is
// the integration guard against a future refactor that drops the
// wiring or forgets to request `elements` / `strunz10ed1`.
func TestLookupByName_DerivesRadioactiveAndAcidReactive(t *testing.T) {
	cases := []struct {
		name             string
		body             string
		wantRadioactive  *bool
		wantReactsToAcid *bool
	}{
		{
			name:             "uraninite — U in elements ticks radioactive",
			body:             `{"results":[{"id":1,"name":"Uraninite","elements":"U O","strunz10ed1":"4.DL.05"}]}`,
			wantRadioactive:  boolPtr(true),
			wantReactsToAcid: nil,
		},
		{
			name:             "calcite — Strunz class 5 ticks reacts-to-acid",
			body:             `{"results":[{"id":2,"name":"Calcite","elements":"Ca C O","strunz10ed1":"5.AB.05"}]}`,
			wantRadioactive:  nil,
			wantReactsToAcid: boolPtr(true),
		},
		{
			name:             "quartz — neither",
			body:             `{"results":[{"id":3,"name":"Quartz","elements":"Si O","strunz10ed1":"4.DA.05"}]}`,
			wantRadioactive:  nil,
			wantReactsToAcid: nil,
		},
		{
			name:             "microcline — K-40 deliberately excluded",
			body:             `{"results":[{"id":4,"name":"Microcline","elements":"K Al Si O","strunz10ed1":"9.FA.30"}]}`,
			wantRadioactive:  nil,
			wantReactsToAcid: nil,
		},
		{
			name:             "malachite — both U absent, Strunz 5 present",
			body:             `{"results":[{"id":5,"name":"Malachite","elements":"Cu C O H","strunz10ed1":"5.BA.10"}]}`,
			wantRadioactive:  nil,
			wantReactsToAcid: boolPtr(true),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			})
			// Extract the name from the body for the lookup arg —
			// each fixture is single-result so the inner name is
			// the lookup query.
			lookup := lookupNameFromBody(t, tc.body)
			rec, err := c.LookupByName(context.Background(), lookup)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if !boolPtrEqual(rec.Data.Radioactive, tc.wantRadioactive) {
				t.Errorf("Radioactive = %v, want %v", fmtBoolPtr(rec.Data.Radioactive), fmtBoolPtr(tc.wantRadioactive))
			}
			if !boolPtrEqual(rec.Data.ReactsToAcid, tc.wantReactsToAcid) {
				t.Errorf("ReactsToAcid = %v, want %v", fmtBoolPtr(rec.Data.ReactsToAcid), fmtBoolPtr(tc.wantReactsToAcid))
			}
		})
	}
}

// TestLookupByName_RequestsDerivationFields guards the fields= query
// — the derivation is pointless if we don't ask Mindat for the
// inputs.
func TestLookupByName_RequestsDerivationFields(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("fields")
		for _, f := range []string{"elements", "strunz10ed1"} {
			if !strings.Contains(got, f) {
				t.Errorf("fields=%q is missing %q", got, f)
			}
		}
		_, _ = w.Write([]byte(`{"results":[{"id":1,"name":"Quartz"}]}`))
	})
	if _, err := c.LookupByName(context.Background(), "Quartz"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
}

func boolPtr(b bool) *bool { return &b }

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fmtBoolPtr(p *bool) string {
	if p == nil {
		return "<nil>"
	}
	if *p {
		return "*true"
	}
	return "*false"
}

// lookupNameFromBody yanks the first "name" value out of a fixture
// JSON body so each table entry stays self-describing.
func lookupNameFromBody(t *testing.T, body string) string {
	t.Helper()
	const tag = `"name":"`
	i := strings.Index(body, tag)
	if i < 0 {
		t.Fatalf("fixture has no name: %s", body)
	}
	rest := body[i+len(tag):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("fixture name not terminated: %s", body)
	}
	return rest[:j]
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
