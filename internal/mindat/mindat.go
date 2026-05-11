// Package mindat is the HTTP client for the Mindat REST API
// (https://api.mindat.org/v1/), used by the F-1 mineral-species
// lookup pipeline.
//
// The package exposes a single domain operation — LookupByName — and
// is deliberately thin. The service layer combines this with the
// mineral_species DB store (the canonical record), so this package
// owns only HTTP transport, error mapping, and the JSON shape
// translation into domain.MineralData.
//
// Authentication: per CONTRACT.md §15 / CONFIG.md, the API key is
// read from the MINDAT_API_KEY env var and threaded in via the
// Config struct. The key is optional in prod — when unset, callers
// receive ErrNoAPIKey and the service falls through to DB-only
// behavior (per the F-1 acceptance criteria).
//
// The package MUST NOT log or surface the API key. Mindat sends the
// token in the Authorization header (Token <key>); we never echo
// the request URL or headers.
package mindat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
)

// DefaultBaseURL is the canonical Mindat API root. Tests inject an
// httptest.Server URL via Options.BaseURL.
const DefaultBaseURL = "https://api.mindat.org/v1/"

// DefaultTimeout caps any single Mindat request. Mindat is an
// external dependency; we never let a single lookup wedge the
// search endpoint.
const DefaultTimeout = 10 * time.Second

// Sentinel errors. Service-layer code branches on these via errors.Is.
var (
	// ErrNoAPIKey is returned when MINDAT_API_KEY is unset. The
	// service maps this to "DB-only mode" — not an error to the
	// HTTP caller.
	ErrNoAPIKey = errors.New("mindat: no api key configured")
	// ErrNotFound is returned when the lookup found no record.
	ErrNotFound = errors.New("mindat: no record")
	// ErrRateLimited is returned when Mindat answers HTTP 429. The
	// service maps this to "no result" (graceful degradation per
	// the F-1 acceptance criteria — never crash the search).
	ErrRateLimited = errors.New("mindat: rate limited")
)

// Options configures a Client. Empty values fall back to the
// defaults documented above.
type Options struct {
	APIKey     string
	BaseURL    string        // default: DefaultBaseURL
	HTTPClient *http.Client  // default: http.Client with DefaultTimeout
	Timeout    time.Duration // default: DefaultTimeout (only used when HTTPClient is nil)
}

// Client is the Mindat HTTP wrapper. Construct via NewClient. The
// zero value is not usable.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewClient returns a Client. Returns nil and ErrNoAPIKey when no
// key is configured — callers that handle the no-key path
// gracefully should branch on errors.Is(err, ErrNoAPIKey).
func NewClient(opts Options) (*Client, error) {
	key := strings.TrimSpace(opts.APIKey)
	if key == "" {
		return nil, ErrNoAPIKey
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	hc := opts.HTTPClient
	if hc == nil {
		t := opts.Timeout
		if t <= 0 {
			t = DefaultTimeout
		}
		hc = &http.Client{Timeout: t}
	}
	return &Client{apiKey: key, baseURL: base, http: hc}, nil
}

// MineralRecord is the lookup result shape, ready to drop into a
// domain.MineralSpecies row. The Data field is already serialized
// JSON (matching domain.MineralSpecies.Data).
type MineralRecord struct {
	Name        string
	MindatID    string
	Data        domain.MineralData
	Attribution string
}

// geomaterialResponse mirrors the Mindat /geomaterials/ search
// envelope. We only depend on the small subset of fields that map
// into domain.MineralData; unknown fields pass through harmlessly.
type geomaterialResponse struct {
	Results []geomaterial `json:"results"`
}

type geomaterial struct {
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	IMAFormula    string  `json:"ima_formula"`
	MindatFormula string  `json:"mindat_formula"`
	HardnessMin   float64 `json:"hardness_min"`
	HardnessMax   float64 `json:"hardness_max"`
	CrystalSystem string  `json:"csystem"`
	Colour        string  `json:"colour"`
	Lustretype    string  `json:"lustretype"`
}

// LookupByName performs an exact-name lookup against Mindat's
// /geomaterials/?name= endpoint. Returns ErrNotFound when no result
// matches and ErrRateLimited on HTTP 429. Network and parse errors
// surface as wrapped errors.
//
// Mindat uses POSIX-style case-sensitive name matching; we trim and
// pass the input through verbatim. The caller is expected to
// normalize.
func (c *Client) LookupByName(ctx context.Context, name string) (*MineralRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNotFound
	}

	q := url.Values{}
	q.Set("name", name)
	// Note: Mindat's free-text 'fluorescence' field is intentionally
	// not requested. MineralData stores UV fluorescence as three
	// structured per-wavelength color lists (mi-qas); Mindat's
	// prose answer can't be safely mapped into that enum.
	q.Set("fields", "id,name,ima_formula,mindat_formula,hardness_min,hardness_max,csystem,colour,lustretype")
	endpoint := c.baseURL + "geomaterials/?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("mindat: build request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mindat: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case http.StatusUnauthorized, http.StatusForbidden:
		// Don't leak whether the key was wrong vs missing — either
		// way the service falls back to DB-only mode.
		return nil, fmt.Errorf("mindat: unauthorized (status %d)", resp.StatusCode)
	default:
		// Drain a small error preamble for log-friendly diagnostics
		// without dumping huge bodies into our logs.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("mindat: unexpected status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(preview)))
	}

	var body geomaterialResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("mindat: decode response: %w", err)
	}

	// Pick the first exact case-insensitive name match. Mindat's
	// `?name=` is normally exact already, but we double-check so a
	// fuzzy/server-side change can't silently mis-resolve.
	for _, g := range body.Results {
		if strings.EqualFold(g.Name, name) {
			return toMineralRecord(g), nil
		}
	}
	return nil, ErrNotFound
}

// toMineralRecord converts a Mindat geomaterial into a MineralRecord
// with the Mindat attribution string filled in. Empty Mindat fields
// stay nil in MineralData (Mindat returns "" for unset values).
func toMineralRecord(g geomaterial) *MineralRecord {
	rec := &MineralRecord{
		Name:        g.Name,
		MindatID:    fmt.Sprintf("%d", g.ID),
		Attribution: "data via Mindat (CC-BY-NC-SA 4.0)",
	}
	formula := strings.TrimSpace(firstNonEmpty(g.IMAFormula, g.MindatFormula))
	if formula != "" {
		rec.Data.ChemicalFormula = &formula
	}
	if cs := strings.TrimSpace(g.CrystalSystem); cs != "" {
		rec.Data.CrystalSystem = &cs
	}
	if g.HardnessMin > 0 || g.HardnessMax > 0 {
		// Mindat reports a range; the domain model has a single
		// scalar. Use the midpoint when both ends are present,
		// otherwise the populated end.
		var h float64
		switch {
		case g.HardnessMin > 0 && g.HardnessMax > 0:
			h = (g.HardnessMin + g.HardnessMax) / 2
		case g.HardnessMin > 0:
			h = g.HardnessMin
		default:
			h = g.HardnessMax
		}
		rec.Data.MohsHardness = &h
	}
	if c := strings.TrimSpace(g.Colour); c != "" {
		rec.Data.Color = &c
	}
	if l := strings.TrimSpace(g.Lustretype); l != "" {
		rec.Data.Luster = &l
	}
	rec.Data.MindatID = &rec.MindatID
	return rec
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
