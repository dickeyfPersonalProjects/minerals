// Mineral-species HTTP surface (mi-dtg / F-1). Implements three §10
// endpoints on top of the domain.MineralSpeciesRepo + a Mindat HTTP
// client. The DB is the canonical store; Mindat is consulted only as
// a populate-on-miss shoulder when configured.
//
//	GET    /api/v1/mineral-species?q=<name>
//	POST   /api/v1/mineral-species
//	GET    /api/v1/mineral-species/{id}
//
// The autocomplete UX in the specimen create/edit form is driven by
// GET /api/v1/mineral-species?q=. POST is called when the user enters
// a mineral that didn't match anything in the DB or in Mindat.
//
// Mindat is a third-party dependency. The package falls back to
// DB-only mode when the API key is unset (per the F-1 acceptance
// criteria) and treats Mindat 429 / unauthorized as "no result" so
// the search endpoint never crashes.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
	"github.com/dickeyfPersonalProjects/minerals/internal/domain"
	"github.com/dickeyfPersonalProjects/minerals/internal/mindat"
)

// MindatLookup is the subset of *mindat.Client the service depends
// on. Tests inject a fake; production wires *mindat.Client. A nil
// MindatLookup means "DB-only mode" (no API key configured).
type MindatLookup interface {
	LookupByName(ctx context.Context, name string) (*mindat.MineralRecord, error)
}

// MineralSpeciesServiceDeps gathers the repo and the optional Mindat
// client into one struct so api.Deps stays narrow. A nil
// MineralSpeciesServiceDeps leaves the routes unregistered.
type MineralSpeciesServiceDeps struct {
	Repo   domain.MineralSpeciesRepo
	Mindat MindatLookup // nil → DB-only mode
}

// MineralSpeciesView is the wire shape of a mineral_species row.
// Field names are snake_case and match the database column names.
type MineralSpeciesView struct {
	ID          uuid.UUID          `json:"id" doc:"UUIDv7 primary key."`
	Name        string             `json:"name" doc:"Canonical mineral name; unique across all rows."`
	Source      string             `json:"source" doc:"Provenance: 'mindat' or 'user' (CONTRACT-style enum)."`
	MindatID    *string            `json:"mindat_id" doc:"Mindat geomaterial id; null for source='user'."`
	Data        domain.MineralData `json:"data" doc:"Pre-fill payload for the specimen form (design §2 MineralData)."`
	Attribution *string            `json:"attribution" doc:"Required when source='mindat' per Mindat's CC-BY-NC-SA 4.0 terms; null otherwise."`
	AuthorID    uuid.UUID          `json:"author_id" doc:"UUID of the user who created the row (CONTRACT.md §13)."`
	CreatedAt   time.Time          `json:"created_at" doc:"RFC 3339 creation timestamp."`
	UpdatedAt   time.Time          `json:"updated_at" doc:"RFC 3339 last-update timestamp."`
}

func toMineralSpeciesView(s domain.MineralSpecies) MineralSpeciesView {
	view := MineralSpeciesView{
		ID:          s.ID,
		Name:        s.Name,
		Source:      string(s.Source),
		MindatID:    s.MindatID,
		Attribution: s.Attribution,
		AuthorID:    s.AuthorID,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
	if len(s.Data) > 0 {
		// A malformed JSON in the DB column is a server-side bug,
		// not a client-visible error. Log and emit an empty Data
		// object rather than failing the whole list.
		if err := json.Unmarshal(s.Data, &view.Data); err != nil {
			slog.Error("mineral_species: data decode",
				"id", s.ID, "err", err)
		}
	}
	return view
}

type listMineralSpeciesInput struct {
	Q string `query:"q" doc:"Free-form name filter (case-insensitive substring match). Empty returns the most-recent rows up to the limit."`
}

type listMineralSpeciesOutput struct {
	Body mineralSpeciesListBody
}

type mineralSpeciesListBody struct {
	Items []MineralSpeciesView `json:"items" doc:"Matched mineral species."`
}

type getMineralSpeciesInput struct {
	ID string `path:"id" doc:"Mineral species UUID."`
}

type mineralSpeciesResponseOutput struct {
	Body MineralSpeciesView
}

type createMineralSpeciesInput struct {
	Body createMineralSpeciesBody
}

type createMineralSpeciesBody struct {
	Name string             `json:"name" minLength:"1" maxLength:"200" doc:"Canonical mineral name; must be unique across all sources."`
	Data domain.MineralData `json:"data" doc:"Pre-fill payload (design §2 MineralData)."`
}

type createMineralSpeciesOutput struct {
	Location string `header:"Location" doc:"URL of the newly created mineral species."`
	Body     MineralSpeciesView
}

// MineralSpeciesService wires huma operations against the repo + Mindat.
type MineralSpeciesService struct {
	repo   domain.MineralSpeciesRepo
	mindat MindatLookup
}

func registerMineralSpeciesOperations(api huma.API, deps *MineralSpeciesServiceDeps) {
	if deps == nil || deps.Repo == nil {
		return
	}
	s := &MineralSpeciesService{repo: deps.Repo, mindat: deps.Mindat}
	mws := huma.Middlewares{humaAuth}

	huma.Register(api, huma.Operation{
		OperationID: "list-mineral-species",
		Method:      http.MethodGet,
		Path:        "/api/v1/mineral-species",
		Summary:     "Search mineral species",
		Description: "Returns mineral species matching `q` (case-insensitive substring on `name`). " +
			"DB is consulted first; if no match and a Mindat API key is configured, the server " +
			"falls through to the Mindat API and stores any successful result before returning. " +
			"Without a key, the DB-only result is returned (possibly empty).",
		Tags:        []string{"mineral-species"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized},
		Middlewares: mws,
	}, s.list)

	huma.Register(api, huma.Operation{
		OperationID:   "create-mineral-species",
		Method:        http.MethodPost,
		Path:          "/api/v1/mineral-species",
		Summary:       "Create a user-entered mineral species",
		Description:   "Used when the user enters a mineral that didn't match any DB or Mindat record. The server stamps source='user' and author_id from auth context. Returns 409 if `name` collides with an existing row.",
		Tags:          []string{"mineral-species"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusConflict},
		Middlewares:   mws,
	}, s.create)

	huma.Register(api, huma.Operation{
		OperationID: "get-mineral-species",
		Method:      http.MethodGet,
		Path:        "/api/v1/mineral-species/{id}",
		Summary:     "Get a mineral species by id",
		Tags:        []string{"mineral-species"},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound},
		Middlewares: mws,
	}, s.get)
}

func (s *MineralSpeciesService) list(ctx context.Context, in *listMineralSpeciesInput) (*listMineralSpeciesOutput, error) {
	q := strings.TrimSpace(in.Q)
	rows, err := s.repo.FindByName(ctx, q)
	if err != nil {
		return nil, mapMineralSpeciesError(err)
	}

	// DB miss + Mindat configured → consult Mindat. We only fall
	// through when q is non-empty (an empty query is "show me what
	// exists locally", not a Mindat lookup).
	if len(rows) == 0 && q != "" && s.mindat != nil {
		if mindatRow, ok := s.fetchAndStoreFromMindat(ctx, q); ok {
			rows = []domain.MineralSpecies{mindatRow}
		}
	}

	items := make([]MineralSpeciesView, 0, len(rows))
	for _, r := range rows {
		items = append(items, toMineralSpeciesView(r))
	}
	return &listMineralSpeciesOutput{Body: mineralSpeciesListBody{Items: items}}, nil
}

// fetchAndStoreFromMindat performs the Mindat lookup and persists a
// successful hit. Returns the stored row on success. All Mindat
// errors (rate-limit, unauthorized, network) degrade to "no result"
// — never bubble up — so the search endpoint stays available even
// when Mindat is unhealthy.
func (s *MineralSpeciesService) fetchAndStoreFromMindat(ctx context.Context, q string) (domain.MineralSpecies, bool) {
	rec, err := s.mindat.LookupByName(ctx, q)
	if err != nil {
		switch {
		case errors.Is(err, mindat.ErrNotFound),
			errors.Is(err, mindat.ErrRateLimited),
			errors.Is(err, mindat.ErrNoAPIKey):
			// Soft failures: the user just doesn't get a Mindat hit.
		default:
			slog.Warn("mineral_species: mindat lookup failed",
				"q", q, "err", err)
		}
		return domain.MineralSpecies{}, false
	}

	// If a row with this mindat_id already exists (race or earlier
	// import under a different name), reuse it rather than tripping
	// the unique constraint.
	if existing, err := s.repo.FindByMindatID(ctx, rec.MindatID); err == nil {
		return existing, true
	}

	dataBytes, err := json.Marshal(rec.Data)
	if err != nil {
		slog.Error("mineral_species: marshal mindat data", "err", err)
		return domain.MineralSpecies{}, false
	}
	now := time.Now().UTC()
	mindatID := rec.MindatID
	attribution := rec.Attribution
	row := domain.MineralSpecies{
		ID:          domain.NewID(),
		Name:        rec.Name,
		Source:      domain.MineralSpeciesSourceMindat,
		MindatID:    &mindatID,
		Data:        dataBytes,
		Attribution: &attribution,
		AuthorID:    auth.FromContext(ctx).ID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.Create(ctx, nil, row); err != nil {
		// On a unique-name collision, prefer the existing row.
		if errors.Is(err, domain.ErrMineralSpeciesConflict) {
			if existing, ferr := s.repo.FindByMindatID(ctx, rec.MindatID); ferr == nil {
				return existing, true
			}
		}
		slog.Warn("mineral_species: persist mindat hit", "err", err)
		return domain.MineralSpecies{}, false
	}
	return row, true
}

func (s *MineralSpeciesService) get(ctx context.Context, in *getMineralSpeciesInput) (*mineralSpeciesResponseOutput, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	row, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapMineralSpeciesError(err)
	}
	return &mineralSpeciesResponseOutput{Body: toMineralSpeciesView(row)}, nil
}

func (s *MineralSpeciesService) create(ctx context.Context, in *createMineralSpeciesInput) (*createMineralSpeciesOutput, error) {
	name := strings.TrimSpace(in.Body.Name)
	if name == "" {
		return nil, newAPIError(http.StatusBadRequest, "invalid_name",
			"name is required", nil)
	}
	// Defensive normalization on the user-write boundary, mirroring
	// the Mindat-ingest path so the column stays uniformly Unicode at
	// rest (mi-c8v).
	if in.Body.Data.ChemicalFormula != nil {
		normalized := mindat.NormalizeChemicalFormula(*in.Body.Data.ChemicalFormula)
		in.Body.Data.ChemicalFormula = &normalized
	}
	if err := in.Body.Data.Validate(); err != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid_data",
			err.Error(), nil)
	}

	dataBytes, err := json.Marshal(in.Body.Data)
	if err != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid_data",
			"data could not be encoded", nil)
	}
	now := time.Now().UTC()
	row := domain.MineralSpecies{
		ID:        domain.NewID(),
		Name:      name,
		Source:    domain.MineralSpeciesSourceUser,
		Data:      dataBytes,
		AuthorID:  auth.FromContext(ctx).ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, nil, row); err != nil {
		return nil, mapMineralSpeciesError(err)
	}
	return &createMineralSpeciesOutput{
		Location: "/api/v1/mineral-species/" + row.ID.String(),
		Body:     toMineralSpeciesView(row),
	}, nil
}

func mapMineralSpeciesError(err error) error {
	switch {
	case errors.Is(err, domain.ErrMineralSpeciesNotFound):
		return newAPIError(http.StatusNotFound, "mineral_species_not_found",
			"no such mineral species", nil)
	case errors.Is(err, domain.ErrMineralSpeciesConflict):
		return newAPIError(http.StatusConflict, "mineral_species_conflict",
			"a mineral species with that name already exists",
			map[string]any{"field": "name", "constraint": "unique"})
	}
	return newAPIError(http.StatusInternalServerError, "internal_error",
		"internal server error", nil)
}
